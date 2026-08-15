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

// Fetch finds and writes the subtitle for videoPath, returning the .srt path.
// It hashes the video for a moviehash search, falling back to a query-only
// search on any hash error (e.g. the file is under the 128 KiB minimum).
// Among results, a moviehash match is preferred; otherwise the first result
// is used.
func Fetch(c *Client, videoPath, lang string) (string, error) {
	ext := filepath.Ext(videoPath)
	query := queryReplacer.Replace(strings.TrimSuffix(filepath.Base(videoPath), ext))

	hash, err := Hash(videoPath)
	if err != nil {
		hash = ""
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

	srtPath := strings.TrimSuffix(videoPath, ext) + "." + lang + ".srt"
	if err := os.WriteFile(srtPath, data, 0o644); err != nil {
		return "", err
	}
	return srtPath, nil
}
