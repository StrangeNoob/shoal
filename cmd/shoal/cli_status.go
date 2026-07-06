package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/StrangeNoob/shoal/internal/daemon"
	"github.com/StrangeNoob/shoal/internal/engine"
)

// statusPollInterval is how often `status --follow` redraws. A var so tests can
// shorten it.
var statusPollInterval = time.Second

// filterStatuses keeps only the torrents whose infohash starts with prefix
// (case-insensitive). An empty prefix keeps everything.
func filterStatuses(ss []engine.Status, prefix string) []engine.Status {
	if prefix == "" {
		return ss
	}
	prefix = strings.ToLower(prefix)
	out := ss[:0:0]
	for _, s := range ss {
		if strings.HasPrefix(strings.ToLower(s.InfoHash), prefix) {
			out = append(out, s)
		}
	}
	return out
}

// followStatus redraws the status table every statusPollInterval until ctx is
// canceled (Ctrl+C), clearing the screen between frames. Returns 0.
//
// fetch runs in a goroutine so a blocked daemon RPC can't keep Ctrl+C from
// exiting: both the fetch and the inter-frame wait are raced against ctx.Done().
// The channel is buffered so a late-returning fetch doesn't leak on the send.
// (fetch is also given a per-poll deadline by the caller, so it does return.)
func followStatus(ctx context.Context, fetch func() []statusRow, out io.Writer) int {
	for {
		ch := make(chan []statusRow, 1)
		go func() { ch <- fetch() }()
		select {
		case <-ctx.Done():
			return 0
		case rows := <-ch:
			fmt.Fprint(out, "\033[H\033[2J") // cursor home + clear screen
			printStatus(out, rows, false)
		}
		select {
		case <-ctx.Done():
			return 0
		case <-time.After(statusPollInterval):
		}
	}
}

// statusState classifies a torrent for display.
func statusState(s engine.Status) string {
	switch {
	case s.Done && s.Seeding:
		return "seeding"
	case s.Done:
		return "done"
	case s.Paused:
		return "paused"
	case s.Queued:
		return "queued"
	default:
		return "downloading"
	}
}

type statusRow struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	InfoHash  string  `json:"info_hash"`
	Percent   float64 `json:"percent"`
	Completed int64   `json:"completed"`
	Total     int64   `json:"total"`
	Peers     int     `json:"peers"`
	State     string  `json:"state"`
	Seeding   bool    `json:"seeding"`
	Done      bool    `json:"done"`
	Path      string  `json:"path,omitempty"`
}

func toStatusRows(ss []engine.Status) []statusRow {
	rows := make([]statusRow, 0, len(ss))
	for _, s := range ss {
		id := s.InfoHash
		if len(id) > 8 {
			id = id[:8]
		}
		rows = append(rows, statusRow{
			ID: id, Name: s.Name, InfoHash: s.InfoHash, Percent: s.Percent(),
			Completed: s.CompletedBytes, Total: s.TotalBytes, Peers: s.Peers,
			State: statusState(s), Seeding: s.Seeding, Done: s.Done, Path: s.Path,
		})
	}
	return rows
}

func printStatus(out io.Writer, rows []statusRow, asJSON bool) {
	if asJSON {
		b, _ := json.MarshalIndent(rows, "", "  ")
		fmt.Fprintln(out, string(b))
		return
	}
	if len(rows) == 0 {
		fmt.Fprintln(out, "no downloads")
		return
	}
	table := make([][]string, 0, len(rows))
	for _, r := range rows {
		table = append(table, []string{
			r.ID,
			truncate(r.Name, 50),
			fmt.Sprintf("%.1f%%", r.Percent*100),
			humanBytes(r.Completed) + "/" + humanBytes(r.Total),
			fmt.Sprintf("%d", r.Peers),
			r.State,
		})
	}
	printTable(out, []string{"ID", "NAME", "PROGRESS", "SIZE", "PEERS", "STATE"}, table)
}

func runStatus(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit JSON")
	clear := fs.Bool("clear", false, "remove finished torrents (keeps files)")
	follow := fs.Bool("follow", false, "redraw live until interrupted (Ctrl+C)")
	positionals, err := parseArgs(fs, args)
	if err != nil {
		return 2
	}
	prefix := ""
	if len(positionals) > 0 {
		prefix = positionals[0]
	}

	c, err := daemon.Dial(daemon.SocketPath())
	if err != nil {
		printStatus(out, toStatusRows(nil), *jsonOut) // no daemon running → nothing to show
		return 0
	}
	defer c.Close()

	// Bound daemon reads so a one-shot `shoal status` (e.g. what shell completion
	// shells out to) can't hang on a wedged daemon. --follow clears this below,
	// since it polls indefinitely by design.
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))

	if *clear {
		for _, s := range c.Statuses() {
			if s.Done && !s.Seeding {
				if err := c.Remove(s.InfoHash, false); err != nil {
					fmt.Fprintf(os.Stderr, "warning: failed to clear %s: %v\n", s.InfoHash, err)
				}
			}
		}
	}

	if *follow && *jsonOut {
		fmt.Fprintln(os.Stderr, "warning: --follow is ignored when --json is set")
	}
	if *follow && !*jsonOut {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		return followStatus(ctx, func() []statusRow {
			// Bound each poll so a wedged daemon can't block a frame forever;
			// followStatus still races this against ctx for a prompt Ctrl+C.
			_ = c.SetDeadline(time.Now().Add(3 * time.Second))
			return toStatusRows(filterStatuses(c.Statuses(), prefix))
		}, out)
	}

	printStatus(out, toStatusRows(filterStatuses(c.Statuses(), prefix)), *jsonOut)
	return 0
}
