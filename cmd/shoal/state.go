package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// cacheEntry is what `download <short-id>` needs to start a prior search hit.
type cacheEntry struct {
	Magnet     string `json:"magnet"`
	TorrentURL string `json:"torrent_url"`
	Title      string `json:"title"`
}

func cachePath(base string) string { return filepath.Join(base, "last-search.json") }

// configDir is the shoal config dir (…/shoal), base for the search cache.
func configDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "shoal")
}

func writeCache(base string, m map[string]cacheEntry) error {
	if err := os.MkdirAll(base, 0o700); err != nil {
		return err
	}
	// MkdirAll won't chmod an existing dir; tighten the shared shoal dir explicitly.
	if err := os.Chmod(base, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(cachePath(base), b, 0o600)
}

func lookupCache(base, id string) (cacheEntry, bool) {
	b, err := os.ReadFile(cachePath(base))
	if err != nil {
		return cacheEntry{}, false
	}
	var m map[string]cacheEntry
	if json.Unmarshal(b, &m) != nil {
		return cacheEntry{}, false
	}
	e, ok := m[id]
	return e, ok
}
