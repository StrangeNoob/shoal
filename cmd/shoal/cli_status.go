package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
)

// stateOf classifies an active entry for display.
func stateOf(a Active, alive func(pid int) bool) string {
	switch {
	case a.Error != "":
		return "error"
	case a.Done:
		return "done"
	case a.Pid > 0 && alive != nil && !alive(a.Pid):
		return "stalled"
	default:
		return "downloading"
	}
}

type statusRow struct {
	Active
	State   string  `json:"state"`
	Percent float64 `json:"percent"`
}

func printStatus(out io.Writer, items []Active, asJSON bool, alive func(int) bool) {
	rows := make([]statusRow, 0, len(items))
	for _, a := range items {
		pct := 0.0
		if a.Total > 0 {
			pct = float64(a.Completed) / float64(a.Total)
		}
		rows = append(rows, statusRow{Active: a, State: stateOf(a, alive), Percent: pct})
	}
	if asJSON {
		b, _ := json.MarshalIndent(rows, "", "  ")
		fmt.Fprintln(out, string(b))
		return
	}
	if len(rows) == 0 {
		fmt.Fprintln(out, "no active downloads")
		return
	}
	for _, r := range rows {
		fmt.Fprintf(out, "%-8s  %-40.40s  %5.1f%%  %10s/%-10s  %d peers  %s\n",
			r.ID, r.Name, r.Percent*100, humanBytes(r.Completed), humanBytes(r.Total), r.Peers, r.State)
	}
}

func runStatus(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit JSON")
	clear := fs.Bool("clear", false, "remove finished/errored entries after printing")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	base := configDir()
	var items []Active
	if id := fs.Arg(0); id != "" {
		a, err := readActive(base, id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "no such download: %s\n", id)
			return 1
		}
		items = []Active{a}
	} else {
		items, _ = listActive(base)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].UpdatedAt.After(items[j].UpdatedAt) })
	printStatus(out, items, *jsonOut, pidAlive)
	if *clear {
		_, _ = clearFinished(base)
	}
	return 0
}
