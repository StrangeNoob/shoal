// Command shoal is the terminal UI: a calm, fullscreen torrent finder and
// downloader. It searches multiple torrent sources and downloads with a full
// BitTorrent engine (anacrolix/torrent).
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"runtime/debug"
	"strings"
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

// version is set at build time via -ldflags "-X main.version=..." (GoReleaser
// release builds). A plain `go install`/`go build` doesn't set it, so it stays
// "dev" and resolveVersion falls back to Go's build metadata.
var version = "dev"

// pseudoVersion matches Go's local-build pseudo-versions
// (e.g. v0.4.1-0.20260702143006-aa296a01f28f), which embed a 14-digit build
// timestamp and a 12-char commit hash.
var pseudoVersion = regexp.MustCompile(`\d{14}-[0-9a-f]{12}`)

// pickVersion resolves the running version. A release build's ldflags value
// wins; otherwise fall back to the module version Go records for
// `go install pkg@vX` (a clean tag). Local builds record "(devel)", a
// pseudo-version, or a "+dirty" marker — those resolve to "dev" so a dev build
// never self-updates.
func pickVersion(ldflags, buildInfo string) string {
	if ldflags != "" && ldflags != "dev" {
		return ldflags
	}
	if buildInfo != "" && buildInfo != "(devel)" &&
		!strings.Contains(buildInfo, "+") && !pseudoVersion.MatchString(buildInfo) {
		return buildInfo
	}
	return "dev"
}

// resolveVersion is pickVersion wired to the real build info.
func resolveVersion() string {
	var bi string
	if info, ok := debug.ReadBuildInfo(); ok {
		bi = info.Main.Version
	}
	return pickVersion(version, bi)
}

const usage = `shoal — a calm BitTorrent client for your terminal

Usage:
  shoal                         launch the fullscreen TUI
  shoal search "<query>"        search torrent sources (add --json for scripts)
  shoal download <id|magnet>    download in the background (add --out <dir>)
  shoal status [id]             show download progress (add --json, --clear)
  shoal update                  update shoal to the latest release
  shoal version                 print the version
  shoal help                    show this help
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
	case "search":
		return true, runSearch(args[2:], out)
	case "download":
		return true, runDownload(args[2:], out)
	case "status":
		return true, runStatus(args[2:], out)
	default:
		return false, 0
	}
}

func runUpdate(out io.Writer, version string) int {
	fmt.Fprintln(out, "Checking for updates…")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	rel, err := update.CheckLatest(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "shoal: update failed:", err)
		return 1
	}
	if !update.Newer(version, rel.Version) {
		fmt.Fprintf(out, "Already on the latest version (%s).\n", update.DisplayVersion(rel.Version))
		return 0
	}
	fmt.Fprintf(out, "Updating %s → %s…\n", update.DisplayVersion(version), update.DisplayVersion(rel.Version))
	to, _, err := update.Apply(ctx, version, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "shoal: update failed:", err)
		return 1
	}
	fmt.Fprintf(out, "Updated to %s — restart shoal to use it.\n", update.DisplayVersion(to))
	return 0
}

func main() {
	v := resolveVersion()
	if handled, code := cli(os.Args, v, os.Stdout); handled {
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
		ui.NewWithConfig(src, eng, cfg).WithHistory(history.Load()).WithVersion(v),
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
