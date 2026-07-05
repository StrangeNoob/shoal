package main

import (
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

// parseArgs parses fs but tolerates flags placed after positional arguments.
// Stdlib flag stops at the first non-flag token, so `download <id> --out <dir>`
// would otherwise silently drop --out; this re-parses the tail after each
// positional. Returns the positionals in order.
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var positionals []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return positionals, nil
		}
		positionals = append(positionals, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// printTable writes an aligned table with a header row. The last column is not
// padded (tabwriter leaves trailing cells flush), so callers put the widest
// free-form field (name/title) last.
func printTable(out io.Writer, headers []string, rows [][]string) {
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(headers, "\t"))
	for _, r := range rows {
		fmt.Fprintln(tw, strings.Join(r, "\t"))
	}
	tw.Flush()
}

// truncate shortens s to n runes, appending "…" when it had to cut.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n < 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
}

// humanBytes renders a byte count like "723.0 MiB".
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
