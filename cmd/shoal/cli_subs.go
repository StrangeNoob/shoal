// cmd/shoal/cli_subs.go
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/StrangeNoob/shoal/internal/config"
	"github.com/StrangeNoob/shoal/internal/daemon"
	"github.com/StrangeNoob/shoal/internal/engine"
	"github.com/StrangeNoob/shoal/internal/glob"
	"github.com/StrangeNoob/shoal/internal/subtitles"
)

// subsFetch is a seam over subtitles.Fetch so CLI tests use a recorder, not HTTP.
var subsFetch = func(apiKey, videoPath, lang string) (string, error) {
	return subtitles.Fetch(subtitles.NewDefaultClient(apiKey), videoPath, lang)
}

// runSubs implements `shoal subs <id> [--lang <code>] [--files <glob>]`:
// fetches subtitles for a downloaded torrent's qualifying files.
func runSubs(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("subs", flag.ContinueOnError)
	lang := fs.String("lang", "", "subtitle language code (defaults to the configured subs_lang)")
	files := fs.String("files", "", "select files by glob (comma-separated); default: video files ≥100 MiB")
	positionals, err := parseArgs(fs, args)
	if err != nil {
		return 2
	}
	if len(positionals) == 0 || positionals[0] == "" {
		fmt.Fprintln(os.Stderr, "usage: shoal subs <id> [--lang <code>] [--files <glob>]")
		return 2
	}

	cfg := config.Load()
	if cfg.OpenSubsAPIKey == "" {
		fmt.Fprintln(os.Stderr, "shoal subs: no OpenSubtitles API key configured — set one in Settings → OS API key (config field opensubs_api_key)")
		return 1
	}
	useLang := cfg.SubsLang
	if *lang != "" {
		useLang = *lang
	}
	globs := splitGlobs(*files)

	return withDaemon(positionals[0], out, func(c *daemon.Client, s engine.Status) error {
		det, err := c.Detail(s.InfoHash)
		if err != nil {
			return err
		}
		targets := subsTargetFiles(det.Files, globs)
		if len(targets) == 0 {
			return fmt.Errorf("no matching files") // nothing qualified — not a fetch failure
		}
		ok := 0
		for _, f := range targets {
			path := engine.AbsFilePath(s.Path, det.Files, f)
			if path == "" {
				// FileDetail.Path is raw torrent metadata (DisplayPath does no
				// sanitizing) — a crafted "../" entry must never be fetched.
				fmt.Fprintf(os.Stderr, "shoal subs: %s: unsafe path (escapes torrent directory)\n", f.Path)
				continue
			}
			srt, err := subsFetch(cfg.OpenSubsAPIKey, path, useLang)
			if err != nil {
				fmt.Fprintf(os.Stderr, "shoal subs: %s: %v\n", f.Path, err)
				// A rate limit or a rejected key will fail every remaining
				// target the same way — stop instead of burning through
				// the rest of the list one failed request at a time.
				if errors.Is(err, subtitles.ErrRateLimited) || errors.Is(err, subtitles.ErrBadKey) {
					break
				}
				continue
			}
			fmt.Fprintln(out, srt)
			ok++
		}
		if ok == 0 {
			return fmt.Errorf("no subtitles fetched")
		}
		return nil
	})
}

// subsTargetFiles picks the files to fetch subtitles for: glob matches when
// globs is non-empty, else the default video-extension + size rule (shared
// with the engine's auto-fetch hook via engine.IsSubsCandidate). Deselected
// files are skipped in both branches — they were never downloaded, so
// fetching a subtitle for one would write an orphaned .srt next to nothing.
func subsTargetFiles(files []engine.FileDetail, globs []string) []engine.FileDetail {
	var out []engine.FileDetail
	if len(globs) > 0 {
		for _, f := range files {
			if f.Selected && glob.Match(globs, f.Path) {
				out = append(out, f)
			}
		}
		return out
	}
	for _, f := range files {
		if f.Selected && engine.IsSubsCandidate(f.Path, f.Length) {
			out = append(out, f)
		}
	}
	return out
}
