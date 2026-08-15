// cmd/shoal/cli_subs.go
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/StrangeNoob/shoal/internal/config"
	"github.com/StrangeNoob/shoal/internal/daemon"
	"github.com/StrangeNoob/shoal/internal/engine"
	"github.com/StrangeNoob/shoal/internal/glob"
	"github.com/StrangeNoob/shoal/internal/subtitles"
)

// subsFetch is a seam over subtitles.Fetch so CLI tests use a recorder, not HTTP.
var subsFetch = func(apiKey, videoPath, lang string) (string, error) {
	c := subtitles.NewClient("https://api.opensubtitles.com/api/v1", apiKey, "shoal")
	return subtitles.Fetch(c, videoPath, lang)
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
		ok := 0
		for _, f := range targets {
			path := subsFilePath(s.Path, det.Files, f)
			srt, err := subsFetch(cfg.OpenSubsAPIKey, path, useLang)
			if err != nil {
				fmt.Fprintf(os.Stderr, "shoal subs: %s: %v\n", f.Path, err)
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

// subsFilePath resolves a file's absolute on-disk path from Status.Path
// (<data dir>/<torrent name>) and a FileDetail.Path from Detail(), which
// comes from anacrolix's File.DisplayPath(). DisplayPath is documented as
// "the relative file path for a multi-file torrent, and the torrent name for
// a single-file torrent" — so for a true single-file torrent, FileDetail.Path
// duplicates the torrent name already baked into Status.Path (they're the
// same string), and the file's absolute path is Status.Path itself. A
// directory-mode torrent that happens to contain exactly one file looks
// similar (len(allFiles)==1) but its one file's DisplayPath is relative to
// the directory, not equal to the directory's own name — so the single-file
// shortcut only applies when that equality actually holds; otherwise this
// falls through to the multi-file join.
func subsFilePath(statusPath string, allFiles []engine.FileDetail, f engine.FileDetail) string {
	if len(allFiles) == 1 && f.Path == filepath.Base(statusPath) {
		return statusPath
	}
	return filepath.Join(statusPath, filepath.FromSlash(f.Path))
}
