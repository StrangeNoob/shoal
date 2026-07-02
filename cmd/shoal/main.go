// Command shoal is the terminal UI: a calm, fullscreen torrent finder and
// downloader. It searches multiple torrent sources and downloads with a full
// BitTorrent engine (anacrolix/torrent).
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/StrangeNoob/shoal/internal/config"
	"github.com/StrangeNoob/shoal/internal/engine"
	"github.com/StrangeNoob/shoal/internal/history"
	"github.com/StrangeNoob/shoal/internal/queue"
	"github.com/StrangeNoob/shoal/internal/source"
	"github.com/StrangeNoob/shoal/internal/ui"
	"github.com/StrangeNoob/shoal/internal/update"
)

// version is set at build time via -ldflags "-X main.version=...". "dev" locally.
var version = "dev"

const usage = `shoal — a calm BitTorrent client for your terminal

Usage:
  shoal            launch the fullscreen TUI
  shoal update     update shoal to the latest release
  shoal version    print the version
  shoal help       show this help
`

// cli handles subcommands. Returns handled=true (with an exit code) when it
// consumed the invocation; handled=false means "launch the TUI".
func cli(args []string, version string, out io.Writer) (handled bool, code int) {
	if len(args) < 2 {
		return false, 0
	}
	switch args[1] {
	case "version", "--version", "-v":
		fmt.Fprintln(out, "shoal", update.DisplayVersion(version))
		return true, 0
	case "help", "--help", "-h":
		fmt.Fprint(out, usage)
		return true, 0
	case "update":
		return true, runUpdate(out, version)
	default:
		return false, 0
	}
}

func runUpdate(out io.Writer, version string) int {
	fmt.Fprintln(out, "Checking for updates…")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	to, upToDate, err := update.Apply(ctx, version, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "shoal: update failed:", err)
		return 1
	}
	if upToDate {
		fmt.Fprintf(out, "Already on the latest version (%s).\n", update.DisplayVersion(to))
		return 0
	}
	fmt.Fprintf(out, "Updated to %s — restart shoal to use it.\n", update.DisplayVersion(to))
	return 0
}

func main() {
	if handled, code := cli(os.Args, version, os.Stdout); handled {
		os.Exit(code)
	}

	cfg := config.Load()

	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		fatal(err)
	}

	eng, err := engine.NewAnacrolix(engine.Config{
		DataDir:    cfg.DataDir,
		ListenPort: cfg.ListenPort,
		MaxPeers:   cfg.MaxPeers,
		Seed:       cfg.Seed,
		SeedRatio:  cfg.SeedRatio,
		QueuePath:  queue.DefaultPath(),
	})
	if err != nil {
		fatal(fmt.Errorf("starting torrent engine: %w", err))
	}
	defer eng.Close()

	src := source.NewDefault()

	p := tea.NewProgram(
		ui.NewWithConfig(src, eng, cfg).WithHistory(history.Load()).WithVersion(version),
		tea.WithAltScreen(),       // fullscreen
		tea.WithMouseCellMotion(), // allow scroll wheel
	)
	if _, err := p.Run(); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "shoal:", err)
	os.Exit(1)
}
