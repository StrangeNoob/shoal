package subtitles

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// ErrNotFound is returned when a search yields no results.
var ErrNotFound = errors.New("subtitles: no matching subtitles found")

// queryReplacer turns filename separators into spaces for the search query.
var queryReplacer = strings.NewReplacer(".", " ", "_", " ")

// SrtPath is the path Fetch writes a subtitle to: the video path with its
// extension replaced by ".<lang>.srt". Exported because that written file is
// the record of a completed fetch — the daemon's auto-fetch checks for it
// instead of keeping in-memory state that a restart would lose.
func SrtPath(videoPath, lang string) string {
	if lang == "" {
		lang = defaultLang
	}
	return strings.TrimSuffix(videoPath, filepath.Ext(videoPath)) + "." + lang + ".srt"
}

// defaultLang is used when no language is configured, so a fetch never writes
// a "movie..srt" or searches without a language filter.
const defaultLang = "en"

// Fetch finds and writes the subtitle for videoPath, returning the .srt path.
// It hashes the video for a moviehash search, falling back to a query-only
// search on any hash error (e.g. the file is under the 128 KiB minimum).
// Among results, a moviehash match is preferred; otherwise the first result
// is used.
func Fetch(c *Client, videoPath, lang string) (string, error) {
	if lang == "" {
		lang = defaultLang
	}
	ext := filepath.Ext(videoPath)
	// Lowercased: the API's gateway 301s non-canonical requests (sorted params,
	// lowercase query — it answers with x-os-rule: canonical), and skipping that
	// redirect hop avoids a flaky edge. Search is case-insensitive server-side.
	query := strings.ToLower(strings.TrimSpace(queryReplacer.Replace(strings.TrimSuffix(filepath.Base(videoPath), ext))))

	// Only a too-small file falls back to a query-only search. Any other Hash
	// error (missing/unreadable file, ...) must not be swallowed: a silent
	// fallback here could write an orphan .srt beside a video that isn't
	// actually there, which would then poison the auto-fetch existence guard.
	hash, err := Hash(videoPath)
	if errors.Is(err, ErrTooSmall) {
		hash = ""
	} else if err != nil {
		return "", err
	}

	results, err := c.Search(hash, query, lang)
	if err != nil {
		return "", err
	}
	if len(results) == 0 {
		return "", ErrNotFound
	}
	best := results[0]
	for _, r := range results {
		if r.HashMatch {
			best = r
			break
		}
	}

	data, err := c.Download(best.FileID)
	if err != nil {
		return "", err
	}

	srtPath := SrtPath(videoPath, lang)
	if err := writeAtomic(srtPath, data); err != nil {
		return "", err
	}
	return srtPath, nil
}

// writeAtomic writes data to path via a temp file in the same directory and a
// rename, so nobody — least of all the auto-fetch existence guard — can see a
// truncated .srt and take it for a finished one.
func writeAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Chmod(tmp, 0o644) // CreateTemp makes 0600
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
