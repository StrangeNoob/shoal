// Command shoal is the terminal UI: a calm, fullscreen torrent finder and
// downloader. It searches multiple torrent sources and downloads with a full
// BitTorrent engine (anacrolix/torrent).
package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/StrangeNoob/shoal/internal/config"
	"github.com/StrangeNoob/shoal/internal/engine"
	"github.com/StrangeNoob/shoal/internal/history"
	"github.com/StrangeNoob/shoal/internal/source"
	"github.com/StrangeNoob/shoal/internal/ui"
)

// version is set at build time via -ldflags "-X main.version=...". "dev" locally.
var version = "dev"

func main() {
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
