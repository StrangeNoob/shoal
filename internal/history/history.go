// Package history persists a record of completed downloads as JSON in the OS
// user-config dir (e.g. ~/.config/shoal/history.json), newest first.
package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Entry is one completed download.
type Entry struct {
	InfoHash    string    `json:"info_hash"`
	Name        string    `json:"name"`
	Size        int64     `json:"size"`
	CompletedAt time.Time `json:"completed_at"`
}

// Store is the persisted history. Path is where it loads/saves; an empty Path
// disables Save (used by tests).
type Store struct {
	Path    string  `json:"-"`
	Entries []Entry `json:"entries"`
}

func defaultPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "shoal", "history.json")
}

// Load reads history from the default config-dir path.
func Load() Store { return LoadFrom(defaultPath()) }

// LoadFrom reads history from path; a missing or corrupt file yields an empty
// (but writable) store.
func LoadFrom(path string) Store {
	s := Store{Path: path}
	b, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	_ = json.Unmarshal(b, &s)
	s.Path = path // Unmarshal can't set the json:"-" field
	return s
}

// Append prepends e (newest first) unless an entry with the same InfoHash is
// already recorded, then persists. Dedup makes re-recording harmless.
func (s *Store) Append(e Entry) {
	for _, existing := range s.Entries {
		if existing.InfoHash == e.InfoHash {
			return
		}
	}
	s.Entries = append([]Entry{e}, s.Entries...)
	_ = s.Save()
}

// Save writes the store to Path (creating the dir). No-op when Path is empty.
func (s Store) Save() error {
	if s.Path == "" {
		return nil
	}
	dir := filepath.Dir(s.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// MkdirAll won't chmod an existing dir; tighten it (shared with config/queue).
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.Path, b, 0o600)
}
