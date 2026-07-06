package engine

import "github.com/StrangeNoob/shoal/internal/glob"

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
