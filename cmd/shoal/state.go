package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Active is one background download's live state — one JSON file per download,
// written only by that download's worker (no locking).
type Active struct {
	ID        string    `json:"id"`
	InfoHash  string    `json:"info_hash"`
	Name      string    `json:"name"`
	Total     int64     `json:"total"`
	Completed int64     `json:"completed"`
	Peers     int       `json:"peers"`
	Seeding   bool      `json:"seeding"`
	Done      bool      `json:"done"`
	Error     string    `json:"error,omitempty"`
	Path      string    `json:"path,omitempty"`
	Out       string    `json:"out"`
	Pid       int       `json:"pid"`
	UpdatedAt time.Time `json:"updated_at"`
}

// cacheEntry is what `download <short-id>` needs to start a prior search hit.
type cacheEntry struct {
	Magnet     string `json:"magnet"`
	TorrentURL string `json:"torrent_url"`
	Title      string `json:"title"`
}

func activeDir(base string) string { return filepath.Join(base, "active") }
func cachePath(base string) string { return filepath.Join(base, "last-search.json") }

// configDir is the shoal config dir (…/shoal), base for all state files.
func configDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "shoal")
}

func writeActive(base string, a Active) error {
	dir := activeDir(base)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, a.ID+".json"), b, 0o600)
}

func readActive(base, id string) (Active, error) {
	var a Active
	b, err := os.ReadFile(filepath.Join(activeDir(base), id+".json"))
	if err != nil {
		return a, err
	}
	err = json.Unmarshal(b, &a)
	return a, err
}

func listActive(base string) ([]Active, error) {
	ents, err := os.ReadDir(activeDir(base))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Active
	for _, e := range ents {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(activeDir(base), e.Name()))
		if err != nil {
			continue
		}
		var a Active
		if json.Unmarshal(b, &a) == nil {
			out = append(out, a)
		}
	}
	return out, nil
}

// clearFinished deletes done/errored entries; returns how many were removed.
func clearFinished(base string) (int, error) {
	items, err := listActive(base)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, a := range items {
		if a.Done || a.Error != "" {
			if os.Remove(filepath.Join(activeDir(base), a.ID+".json")) == nil {
				n++
			}
		}
	}
	return n, nil
}

func writeCache(base string, m map[string]cacheEntry) error {
	if err := os.MkdirAll(base, 0o700); err != nil {
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
