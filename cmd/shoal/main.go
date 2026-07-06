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
	"github.com/StrangeNoob/shoal/internal/history"
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
  shoal sources                 list the searchable torrent sources (add --json)
  shoal search "<query>"        search torrent sources (add --json for scripts)
  shoal download <magnet|url|infohash|id|file.torrent>  background download (add --wait)
  shoal status [id]             show download progress (--json, --clear, --follow)
  shoal history [--json]        list completed downloads
  shoal history rm <id>         remove a history entry (add --delete-files)
  shoal history clear           clear the history log (add --delete-files)
  shoal pause <id>              pause a download
  shoal resume <id>             resume a paused download
  shoal remove <id>             cancel/remove a download (add --delete-files)
  shoal files <id>              list a download's files (add --only <glob>, --json)
  shoal open <id>               reveal download folder
  shoal daemon                  run the shared background engine (experimental)
  shoal daemon stop             stop the shared daemon
  shoal daemon status           show the daemon's status
  shoal completion <shell>      print a bash/zsh/fish completion script
  shoal skill install           install the Claude Code skill (~/.claude/skills)
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
	case "skill":
		return true, runSkill(args[2:], out)
	case "sources":
		return true, runSources(args[2:], out)
	case "search":
		return true, runSearch(args[2:], out)
	case "download":
		return true, runDownload(args[2:], out)
	case "status":
		return true, runStatus(args[2:], out)
	case "daemon":
		return true, runDaemonCmd(args[2:], out)
	case "history":
		return true, runHistory(args[2:], out)
	case "pause":
		return true, runPause(args[2:], out)
	case "resume":
		return true, runResume(args[2:], out)
	case "remove":
		return true, runRemove(args[2:], out)
	case "files":
		return true, runFiles(args[2:], out)
	case "open":
		return true, runOpen(args[2:], out)
	case "completion":
		return true, runCompletion(args[2:], out)
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

	// The TUI drives the shared daemon (auto-started), so it stays in sync with the
	// CLI. The transport is a unix-domain socket on all platforms — including
	// Windows 10 1803+, where Go supports AF_UNIX.
	eng := newDaemonPoller()
	if _, err := eng.client(); err != nil { // startup connect (auto-starts); fatal if it can't start
		fatal(err)
	}
	defer eng.Close() // closes the connection only; the daemon and its downloads persist

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
