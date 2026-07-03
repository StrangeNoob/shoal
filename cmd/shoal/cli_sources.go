package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/StrangeNoob/shoal/internal/source"
)

// runSources lists the search providers `shoal search` queries, so a user or
// script can discover valid --source names without triggering an error path.
func runSources(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("sources", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit a JSON array of provider names")
	if _, err := parseArgs(fs, args); err != nil {
		return 2
	}
	names := sourceNames(source.DefaultSources())
	if *jsonOut {
		b, _ := json.MarshalIndent(names, "", "  ")
		fmt.Fprintln(out, string(b))
		return 0
	}
	for _, n := range names {
		fmt.Fprintln(out, n)
	}
	return 0
}
