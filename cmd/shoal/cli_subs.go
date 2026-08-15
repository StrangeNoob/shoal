// cmd/shoal/cli_subs.go
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/StrangeNoob/shoal/internal/config"
	"github.com/StrangeNoob/shoal/internal/daemon"
	"github.com/StrangeNoob/shoal/internal/engine"
	"github.com/StrangeNoob/shoal/internal/glob"
	"github.com/StrangeNoob/shoal/internal/subtitles"
)

// subsVideoExts/subsMinVideoBytes mirror the default video-file rule in
// internal/engine/anacrolix.go (auto-fetch on download completion). They're
// unexported there, so this CLI package — the only other caller — duplicates
// the two small values rather than exporting new API surface for one client.
var subsVideoExts = map[string]bool{
	".mkv": true, ".mp4": true, ".avi": true, ".webm": true, ".mov": true, ".m4v": true,
}

var subsMinVideoBytes = int64(100) << 20

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
// globs is non-empty, else the default video-extension + size rule.
func subsTargetFiles(files []engine.FileDetail, globs []string) []engine.FileDetail {
	var out []engine.FileDetail
	if len(globs) > 0 {
		for _, f := range files {
			if glob.Match(globs, f.Path) {
				out = append(out, f)
			}
		}
		return out
	}
	for _, f := range files {
		ext := strings.ToLower(filepath.Ext(f.Path))
		if subsVideoExts[ext] && f.Length >= subsMinVideoBytes {
			out = append(out, f)
		}
	}
	return out
}

// subsFilePath resolves a file's absolute on-disk path from Status.Path
// (<data dir>/<torrent name>) and a FileDetail.Path from Detail(), which
// comes from anacrolix's File.DisplayPath(). DisplayPath is documented as
// "the relative file path for a multi-file torrent, and the torrent name for
// a single-file torrent" — so for a single-file torrent (exactly one file)
// FileDetail.Path duplicates the torrent name already baked into Status.Path,
// and the file's absolute path is Status.Path itself; for a multi-file
// torrent, Status.Path is the shared top-level directory and FileDetail.Path
// is relative to it.
func subsFilePath(statusPath string, allFiles []engine.FileDetail, f engine.FileDetail) string {
	if len(allFiles) == 1 {
		return statusPath
	}
	return filepath.Join(statusPath, filepath.FromSlash(f.Path))
}
