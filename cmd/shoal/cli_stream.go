// cmd/shoal/cli_stream.go
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/StrangeNoob/shoal/internal/daemon"
	"github.com/StrangeNoob/shoal/internal/engine"
	"github.com/StrangeNoob/shoal/internal/glob"
	"github.com/StrangeNoob/shoal/internal/source"
)

// streamHeadBytes is how much of a file's start must be contiguously complete
// (plus a complete last piece — mp4 moov atoms often live at the tail) before
// `shoal stream` considers it playable.
const streamHeadBytes = int64(8) << 20

// videoExts are the extensions preferred as the stream target when no --files
// glob is given.
var videoExts = map[string]bool{
	".mkv": true, ".mp4": true, ".avi": true, ".webm": true, ".mov": true, ".m4v": true,
}

const streamUsage = "usage: shoal stream <id|magnet> [--files <glob>]"

// runStream implements `shoal stream <id|magnet> [--files <glob>]`: resolves
// the target torrent (existing id/prefix, or adds a magnet), enables
// sequential mode, picks a target file, waits until it's playable, then
// prints its absolute path to stdout and exits. Only the final path ever goes
// to stdout — every other message (progress, errors) goes to stderr — so the
// canonical `mpv "$(shoal stream <id>)"` never sees anything else.
func runStream(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("stream", flag.ContinueOnError)
	filesGlob := fs.String("files", "", "target file glob (comma-separated)")
	positionals, err := parseArgs(fs, args)
	if err != nil {
		return 2
	}
	if len(positionals) == 0 || positionals[0] == "" {
		fmt.Fprintln(os.Stderr, streamUsage)
		return 2
	}

	c, err := ensureDaemon()
	if err != nil {
		fmt.Fprintln(os.Stderr, "shoal stream:", err)
		return 1
	}
	defer c.Close()

	infoHash, err := resolveStreamTarget(c, positionals[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "shoal stream:", err)
		return 1
	}

	if err := c.SetSequential(infoHash, true); err != nil {
		fmt.Fprintln(os.Stderr, "shoal stream: enable sequential mode:", err)
		return 1
	}

	det, err := c.Detail(infoHash)
	if err != nil {
		fmt.Fprintln(os.Stderr, "shoal stream:", err)
		return 1
	}
	// A freshly-added magnet has no files until metadata arrives (the engine
	// fetches it in the background) — wait for it the same way the playability
	// loop below waits, instead of hard-erroring "no files" on the first poll.
	for len(det.Files) == 0 {
		fmt.Fprint(os.Stderr, "\r\033[Kwaiting for metadata…")
		time.Sleep(waitPollInterval)
		det, err = c.Detail(infoHash)
		if err != nil {
			fmt.Fprintln(os.Stderr, "\nshoal stream:", err)
			return 1
		}
	}

	target, err := pickStreamTarget(det.Files, *filesGlob)
	if err != nil {
		fmt.Fprintln(os.Stderr, "shoal stream:", err)
		return 1
	}

	var st engine.Status
	for {
		// Status first: a paused or queued torrent never advances, so waiting on
		// it would hang forever. This also gives the path printed on success.
		statuses, err := c.StatusesErr()
		if err != nil {
			fmt.Fprintln(os.Stderr, "\nshoal stream:", err)
			return 1
		}
		var ok bool
		st, ok = findStreamStatus(statuses, infoHash)
		if !ok {
			fmt.Fprintln(os.Stderr, "\nshoal stream: torrent disappeared")
			return 1
		}
		if reason := streamStuckReason(st, infoHash[:8]); reason != "" {
			fmt.Fprintln(os.Stderr, "\nshoal stream:", reason)
			return 1
		}
		f, ok := findStreamFile(det.Files, target.Path)
		if !ok {
			fmt.Fprintln(os.Stderr, "\nshoal stream: target file disappeared:", target.Path)
			return 1
		}
		if !f.Selected {
			// Recheck every iteration, not just once up front: a concurrent
			// `shoal files --only` can deselect the target mid-wait, which would
			// otherwise freeze HeadBytes/TailDone and spin forever.
			fmt.Fprintf(os.Stderr, "\nshoal stream: %s is deselected; run `shoal files %s --only %q` to select it\n",
				f.Path, infoHash[:8], f.Path)
			return 1
		}
		need := streamHeadBytes
		if f.Length < need {
			need = f.Length
		}
		if f.HeadBytes >= need && f.TailDone {
			break
		}
		fmt.Fprintf(os.Stderr, "\r\033[Kbuffering %s: %s/%s", f.Path, humanBytes(f.HeadBytes), humanBytes(need))
		time.Sleep(waitPollInterval)
		det, err = c.Detail(infoHash)
		if err != nil {
			fmt.Fprintln(os.Stderr, "\nshoal stream: daemon unreachable:", err)
			return 1
		}
	}
	fmt.Fprint(os.Stderr, "\r\033[K")
	fmt.Fprintln(out, filePathFor(st, det.Files, target))
	return 0
}

// streamStuckReason explains why st can't make progress toward playability, or
// "" when it can. id is the short torrent id to put in the suggested command.
func streamStuckReason(st engine.Status, id string) string {
	switch {
	case st.Paused:
		return fmt.Sprintf("%s is paused — run `shoal resume %s`", st.Name, id)
	case st.Queued:
		return fmt.Sprintf("%s is queued behind max-active downloads — finish or pause another download to free a slot (id %s)", st.Name, id)
	}
	return ""
}

// resolveStreamTarget resolves arg to an infohash: a magnet is added to the
// daemon and its infohash returned; anything else is matched as an id/prefix
// against currently-known torrents (mirrors `shoal sequential`'s resolution).
func resolveStreamTarget(c *daemon.Client, arg string) (string, error) {
	s := strings.TrimSpace(arg)
	if strings.HasPrefix(strings.ToLower(s), "magnet:") {
		ih := source.ParseMagnetInfoHash(s)
		if ih == "" {
			return "", fmt.Errorf("magnet has no infohash: %s", s)
		}
		if err := c.AddMagnet(s); err != nil {
			return "", err
		}
		return strings.ToLower(ih), nil
	}
	st, err := resolveOne(c, s)
	if err != nil {
		return "", err
	}
	return st.InfoHash, nil
}

// pickStreamTarget chooses the file to stream: the --files glob match if
// given (error if nothing matches), else the largest file with a video
// extension, else the largest file overall. Among the candidates, files being
// downloaded win over deselected ones (a deselected file never becomes
// playable); if none are selected the largest still wins, so the wait loop can
// report the deselection with an actionable hint.
func pickStreamTarget(files []engine.FileDetail, globsCSV string) (engine.FileDetail, error) {
	if len(files) == 0 {
		return engine.FileDetail{}, fmt.Errorf("torrent has no files")
	}
	candidates := files
	if globs := splitGlobs(globsCSV); len(globs) > 0 {
		var matched []engine.FileDetail
		for _, f := range files {
			if glob.Match(globs, f.Path) {
				matched = append(matched, f)
			}
		}
		if len(matched) == 0 {
			return engine.FileDetail{}, fmt.Errorf("no files matched %q", globsCSV)
		}
		candidates = matched
	} else if video := filterVideo(files); len(video) > 0 {
		candidates = video
	}
	if selected := filterSelected(candidates); len(selected) > 0 {
		candidates = selected
	}
	best := candidates[0]
	for _, f := range candidates[1:] {
		if f.Length > best.Length {
			best = f
		}
	}
	if best.Length == 0 {
		// Playability is "first N bytes + last piece"; a 0-byte file can never
		// satisfy it, so waiting on it would spin forever.
		return engine.FileDetail{}, fmt.Errorf("target file %q is empty (0 bytes)", best.Path)
	}
	return best, nil
}

func filterSelected(files []engine.FileDetail) []engine.FileDetail {
	var out []engine.FileDetail
	for _, f := range files {
		if f.Selected {
			out = append(out, f)
		}
	}
	return out
}

func filterVideo(files []engine.FileDetail) []engine.FileDetail {
	var out []engine.FileDetail
	for _, f := range files {
		if videoExts[strings.ToLower(filepath.Ext(f.Path))] {
			out = append(out, f)
		}
	}
	return out
}

func findStreamFile(files []engine.FileDetail, path string) (engine.FileDetail, bool) {
	for _, f := range files {
		if f.Path == path {
			return f, true
		}
	}
	return engine.FileDetail{}, false
}

func findStreamStatus(statuses []engine.Status, infoHash string) (engine.Status, bool) {
	for _, s := range statuses {
		if strings.EqualFold(s.InfoHash, infoHash) {
			return s, true
		}
	}
	return engine.Status{}, false
}

// filePathFor resolves the absolute on-disk path of file f within the torrent
// described by st.
//
// Ground truth (anacrolix/torrent storage/file-client.go, NewFileOpts'
// default FilePathMaker): every file's real path is
// filepath.Join(dataDir, info.BestName(), file.BestPath()...). For a
// multi-file torrent, file.BestPath() is the file's path *within* the
// torrent, and info.BestName() is the torrent's directory — exactly
// Status.Path (built the same way in anacrolix.go) — so joining Status.Path
// with FileDetail.Path (multi-file DisplayPath == strings.Join(BestPath, "/"))
// reproduces it. For a single-file torrent there is no subdirectory:
// file.BestPath() is empty, so the real path is just dataDir/info.BestName()
// == Status.Path — but FileDetail.Path there is DisplayPath's single-file
// case, which returns info.BestName() itself (the same string, not empty).
// Joining Status.Path with that would duplicate the trailing component, so
// detect single-file torrents (exactly one file, whose path equals the
// basename of Status.Path — an identity guaranteed by both being
// info.BestName(), not a coincidence) and use Status.Path as-is.
func filePathFor(st engine.Status, files []engine.FileDetail, f engine.FileDetail) string {
	if len(files) == 1 && filepath.FromSlash(f.Path) == filepath.Base(st.Path) {
		return st.Path
	}
	return filepath.Join(st.Path, filepath.FromSlash(f.Path))
}
