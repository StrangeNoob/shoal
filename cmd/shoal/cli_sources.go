package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/StrangeNoob/shoal/internal/config"
	"github.com/StrangeNoob/shoal/internal/source"
)

// matchSourceName resolves a case-insensitive name to the canonical provider Name().
func matchSourceName(name string) (string, bool) {
	for _, n := range sourceNames(source.DefaultSources()) {
		if strings.EqualFold(n, name) {
			return n, true
		}
	}
	return "", false
}

// runSources lists the search providers with their on/off state, and toggles them:
//
//	shoal sources                  # list with state
//	shoal sources enable  <name>   # turn a provider on
//	shoal sources disable <name>   # turn a provider off
func runSources(args []string, out io.Writer) int {
	action := ""
	if len(args) > 0 && (args[0] == "enable" || args[0] == "disable") {
		action, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("sources", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit JSON")
	positionals, err := parseArgs(fs, args)
	if err != nil {
		return 2
	}
	cfg := config.Load()

	if action != "" {
		name := strings.Join(positionals, " ")
		if name == "" {
			fmt.Fprintf(os.Stderr, "usage: shoal sources %s <name>\n", action)
			return 2
		}
		canonical, ok := matchSourceName(name)
		if !ok {
			fmt.Fprintf(os.Stderr, "unknown source %q; valid: %s\n",
				name, strings.Join(sourceNames(source.DefaultSources()), ", "))
			return 1
		}
		enable := action == "enable"
		cfg.SetSourceEnabled(canonical, enable)
		if err := cfg.Save(); err != nil {
			fmt.Fprintln(os.Stderr, "shoal: sources:", err)
			return 1
		}
		state := "disabled"
		if enable {
			state = "enabled"
		}
		fmt.Fprintf(out, "%s %s\n", canonical, state)
		return 0
	}

	names := sourceNames(source.DefaultSources())
	if *jsonOut {
		type row struct {
			Name    string `json:"name"`
			Enabled bool   `json:"enabled"`
		}
		rows := make([]row, len(names))
		for i, n := range names {
			rows[i] = row{Name: n, Enabled: cfg.SourceEnabled(n)}
		}
		b, _ := json.MarshalIndent(rows, "", "  ")
		fmt.Fprintln(out, string(b))
		return 0
	}
	table := make([][]string, 0, len(names))
	for _, n := range names {
		state := "off"
		if cfg.SourceEnabled(n) {
			state = "on"
		}
		table = append(table, []string{state, n})
	}
	printTable(out, []string{"STATE", "SOURCE"}, table)
	return 0
}
