package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/StrangeNoob/shoal/internal/daemon"
	"github.com/StrangeNoob/shoal/internal/engine"
)

// statusState classifies a torrent for display.
func statusState(s engine.Status) string {
	switch {
	case s.Done && s.Seeding:
		return "seeding"
	case s.Done:
		return "done"
	case s.Paused:
		return "paused"
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
			fmt.Sprintf("%.1f%%", r.Percent*100),
			humanBytes(r.Completed) + "/" + humanBytes(r.Total),
			fmt.Sprintf("%d", r.Peers),
			r.State,
			truncate(r.Name, 50),
		})
	}
	printTable(out, []string{"ID", "PROGRESS", "SIZE", "PEERS", "STATE", "NAME"}, table)
}

func runStatus(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit JSON")
	clear := fs.Bool("clear", false, "remove finished torrents (keeps files)")
	positionals, err := parseArgs(fs, args)
	if err != nil {
		return 2
	}

	c, err := daemon.Dial(daemon.SocketPath())
	if err != nil {
		printStatus(out, toStatusRows(nil), *jsonOut) // no daemon running → nothing to show
		return 0
	}
	defer c.Close()

	statuses := c.Statuses()
	if *clear {
		for _, s := range statuses {
			if s.Done && !s.Seeding {
				if err := c.Remove(s.InfoHash, false); err != nil {
					fmt.Fprintf(os.Stderr, "warning: failed to clear %s: %v\n", s.InfoHash, err)
				}
			}
		}
		statuses = c.Statuses() // refresh after removals
	}
	if len(positionals) > 0 && positionals[0] != "" {
		prefix := strings.ToLower(positionals[0])
		var filtered []engine.Status
		for _, s := range statuses {
			if strings.HasPrefix(strings.ToLower(s.InfoHash), prefix) {
				filtered = append(filtered, s)
			}
		}
		statuses = filtered
	}
	printStatus(out, toStatusRows(statuses), *jsonOut)
	return 0
}
