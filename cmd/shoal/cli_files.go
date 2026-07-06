// cmd/shoal/cli_files.go
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
	"github.com/StrangeNoob/shoal/internal/glob"
)

// runFiles implements `shoal files <id> [--only <glob>] [--json]`: lists a
// torrent's files, optionally selecting only those matching --only (a
// comma-separated glob list) and deselecting the rest.
func runFiles(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("files", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit JSON")
	only := fs.String("only", "", "select only files matching this glob (comma-separated, deselects the rest)")
	positionals, err := parseArgs(fs, args)
	if err != nil {
		return 2
	}
	if len(positionals) == 0 || positionals[0] == "" {
		fmt.Fprintln(os.Stderr, "usage: shoal files <id> [--only <glob>] [--json]")
		return 2
	}
	return withDaemon(positionals[0], out, func(c *daemon.Client, s engine.Status) error {
		if *only != "" {
			if err := applyOnlyGlob(c, s.InfoHash, *only); err != nil {
				return err
			}
		}
		det, err := c.Detail(s.InfoHash)
		if err != nil {
			return err
		}
		if *jsonOut {
			files := det.Files
			if files == nil {
				files = []engine.FileDetail{}
			}
			b, err := json.MarshalIndent(files, "", "  ")
			if err != nil {
				return err
			}
			fmt.Fprintln(out, string(b))
			return nil
		}
		printFileTable(out, det.Files)
		return nil
	})
}

// applyOnlyGlob selects the files matching globs and deselects the rest.
func applyOnlyGlob(c *daemon.Client, infoHash, globsCSV string) error {
	det, err := c.Detail(infoHash)
	if err != nil {
		return err
	}
	globs := splitGlobs(globsCSV)
	if len(globs) == 0 {
		return fmt.Errorf("--only pattern is empty; selection unchanged")
	}
	var sel, deselect []string
	for _, f := range det.Files {
		if glob.Match(globs, f.Path) {
			sel = append(sel, f.Path)
		} else {
			deselect = append(deselect, f.Path)
		}
	}
	if len(sel) == 0 {
		return fmt.Errorf("no files matched %q; selection unchanged", globsCSV)
	}
	if err := c.SetFiles(infoHash, sel, true); err != nil {
		return err
	}
	if len(deselect) > 0 {
		if err := c.SetFiles(infoHash, deselect, false); err != nil {
			return err
		}
	}
	return nil
}

func printFileTable(out io.Writer, files []engine.FileDetail) {
	rows := make([][]string, 0, len(files))
	for i, f := range files {
		mark := "✓"
		if !f.Selected {
			mark = "✗"
		}
		rows = append(rows, []string{fmt.Sprintf("%d", i+1), mark, humanBytes(f.Length), f.Path})
	}
	printTable(out, []string{"#", "SEL", "SIZE", "PATH"}, rows)
}

// splitGlobs splits a comma-separated --only/--files value into trimmed,
// non-empty glob patterns.
func splitGlobs(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
