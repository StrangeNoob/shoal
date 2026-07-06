// Package glob matches file paths against simple shell-style patterns for the
// --files selector. Case-insensitive; each pattern is tried against the whole
// path and the basename (so "*.mkv" catches nested files). No "**" — stdlib
// path.Match only.
package glob

import (
	"path"
	"strings"
)

// Match reports whether filePath matches any non-empty pattern.
func Match(patterns []string, filePath string) bool {
	lp := strings.ToLower(filePath)
	base := path.Base(lp)
	for _, p := range patterns {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		if ok, _ := path.Match(p, lp); ok {
			return true
		}
		if ok, _ := path.Match(p, base); ok {
			return true
		}
	}
	return false
}
