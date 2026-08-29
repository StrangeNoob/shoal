package engine

import (
	"path/filepath"
	"strings"

	"github.com/StrangeNoob/shoal/internal/glob"
)

// AbsFilePath returns the on-disk absolute path of f within a torrent whose
// top-level path is statusPath (Status.Path). Status.Path comes from
// anacrolix's File.DisplayPath(), documented as "the relative file path for
// a multi-file torrent, and the torrent name for a single-file torrent" — so
// for a true single-file torrent, f.Path duplicates the torrent name already
// baked into statusPath (they're the same string), and the file's absolute
// path is statusPath itself. A directory-mode torrent that happens to
// contain exactly one file looks similar (len(files)==1) but its file's
// DisplayPath is relative to the directory, not equal to the directory's own
// name — so the single-file shortcut only applies when that equality
// actually holds; otherwise this falls through to the multi-file join.
// f.Path is raw torrent metadata (DisplayPath does no sanitizing), so the
// join is confined: a crafted "../" chain or dot path must never resolve
// outside the torrent's own directory — such paths return "".
func AbsFilePath(statusPath string, files []FileDetail, f FileDetail) string {
	if len(files) == 1 && f.Path == filepath.Base(statusPath) {
		return statusPath
	}
	joined := filepath.Join(statusPath, filepath.FromSlash(f.Path)) // Join cleans ".." et al.
	if !strings.HasPrefix(joined, statusPath+string(filepath.Separator)) {
		return ""
	}
	return joined
}

// resolveDeselected returns the file paths to deselect for the given --files
// globs: every path that matches none of the globs. Empty globs → nil (keep all).
func resolveDeselected(paths, globs []string) []string {
	if len(globs) == 0 {
		return nil
	}
	var out []string
	for _, p := range paths {
		if !glob.Match(globs, p) {
			out = append(out, p)
		}
	}
	return out
}
