// cmd/shoal/cli_sequential.go
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/StrangeNoob/shoal/internal/daemon"
	"github.com/StrangeNoob/shoal/internal/engine"
)

const sequentialUsage = "usage: shoal sequential <id> on|off"

// runSequential implements `shoal sequential <id> on|off`: toggles sequential
// (streaming) piece-priority mode for an existing download.
func runSequential(args []string, out io.Writer) int {
	positionals, err := parseArgs(flag.NewFlagSet("sequential", flag.ContinueOnError), args)
	if err != nil {
		return 2
	}
	if len(positionals) != 2 || positionals[0] == "" {
		fmt.Fprintln(os.Stderr, sequentialUsage)
		return 2
	}
	var on bool
	switch positionals[1] {
	case "on":
		on = true
	case "off":
		on = false
	default:
		fmt.Fprintln(os.Stderr, sequentialUsage)
		return 2
	}
	return withDaemon(positionals[0], out, func(c *daemon.Client, s engine.Status) error {
		if err := c.SetSequential(s.InfoHash, on); err != nil {
			return err
		}
		verb := "disabled"
		if on {
			verb = "enabled"
		}
		fmt.Fprintf(out, "sequential %s: %s\n", verb, s.Name)
		return nil
	})
}
