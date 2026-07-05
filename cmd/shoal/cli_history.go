package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/StrangeNoob/shoal/internal/config"
	"github.com/StrangeNoob/shoal/internal/daemon"
	"github.com/StrangeNoob/shoal/internal/engine"
	"github.com/StrangeNoob/shoal/internal/history"
)

type historyRow struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	InfoHash    string `json:"info_hash"`
	Size        int64  `json:"size"`
	CompletedAt string `json:"completed_at"`
	Path        string `json:"path,omitempty"`
}

// runHistory dispatches `history` (list), `history rm <id>`, and `history clear`.
func runHistory(args []string, out io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "rm":
			return runHistoryRm(args[1:], out)
		case "clear":
			return runHistoryClear(args[1:], out)
		}
	}
	return runHistoryList(args, out)
}

func runHistoryList(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("history", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit JSON")
	if _, err := parseArgs(fs, args); err != nil {
		return 2
	}
	entries := history.Load().Entries
	if *jsonOut {
		rows := make([]historyRow, 0, len(entries))
		for _, e := range entries {
			rows = append(rows, toHistoryRow(e))
		}
		b, _ := json.MarshalIndent(rows, "", "  ")
		fmt.Fprintln(out, string(b))
		return 0
	}
	if len(entries) == 0 {
		fmt.Fprintln(out, "no history")
		return 0
	}
	for _, e := range entries {
		r := toHistoryRow(e)
		fmt.Fprintf(out, "%-8s  %-40.40s  %10s  %s\n", r.ID, r.Name, humanBytes(r.Size), r.CompletedAt)
	}
	return 0
}

func toHistoryRow(e history.Entry) historyRow {
	id := e.InfoHash
	if len(id) > 8 {
		id = id[:8]
	}
	return historyRow{ID: id, Name: e.Name, InfoHash: e.InfoHash, Size: e.Size,
		CompletedAt: e.CompletedAt.Format("2006-01-02 15:04"), Path: e.Path}
}

func runHistoryRm(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("history rm", flag.ContinueOnError)
	del := fs.Bool("delete-files", false, "also delete the downloaded files")
	positionals, err := parseArgs(fs, args)
	if err != nil {
		return 2
	}
	if len(positionals) == 0 || positionals[0] == "" {
		fmt.Fprintln(os.Stderr, "usage: shoal history rm <id> [--delete-files]")
		return 2
	}
	s := history.Load()
	e, err := findHistoryEntry(s.Entries, positionals[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if *del {
		if err := deleteEntryFiles(e); err != nil {
			fmt.Fprintln(os.Stderr, "warning: could not delete files:", err)
		}
	}
	s.Remove(e.InfoHash)
	fmt.Fprintf(out, "removed: %s (%s)\n", e.Name, e.InfoHash[:min(8, len(e.InfoHash))])
	return 0
}

func runHistoryClear(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("history clear", flag.ContinueOnError)
	del := fs.Bool("delete-files", false, "also delete the downloaded files")
	if _, err := parseArgs(fs, args); err != nil {
		return 2
	}
	s := history.Load()
	n := len(s.Entries)
	if *del {
		for _, e := range s.Entries {
			if err := deleteEntryFiles(e); err != nil {
				fmt.Fprintln(os.Stderr, "warning:", err)
			}
		}
	}
	s.Clear()
	fmt.Fprintf(out, "cleared %d history entr%s\n", n, plural(n, "y", "ies"))
	return 0
}

// findHistoryEntry returns the unique entry whose infohash starts with prefix,
// or an error distinguishing "no match" from "ambiguous".
func findHistoryEntry(entries []history.Entry, prefix string) (history.Entry, error) {
	prefix = strings.ToLower(prefix)
	var found history.Entry
	n := 0
	for _, e := range entries {
		if strings.HasPrefix(strings.ToLower(e.InfoHash), prefix) {
			found = e
			n++
		}
	}
	switch n {
	case 0:
		return history.Entry{}, fmt.Errorf("no history entry: %s", prefix)
	case 1:
		return found, nil
	default:
		return history.Entry{}, fmt.Errorf("ambiguous id %q matches %d history entries", prefix, n)
	}
}

// deleteEntryFiles removes e's files: via the daemon if the torrent is live
// (clean stop + delete), else directly under the data dir (containment-safe).
func deleteEntryFiles(e history.Entry) error {
	if c, err := daemon.Dial(daemon.SocketPath()); err == nil {
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(5 * time.Second))
		for _, s := range c.Statuses() {
			if s.InfoHash == e.InfoHash {
				return c.Remove(e.InfoHash, true)
			}
		}
	}
	if e.Name == "" {
		return nil
	}
	return engine.RemoveUnderDir(config.Load().DataDir, e.Name)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
