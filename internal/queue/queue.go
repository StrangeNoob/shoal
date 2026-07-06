// Package queue persists the set of torrents the user has added (downloads and
// seeding) as JSON in the OS user-config dir (queue.json), so they can be
// re-added on the next launch. It is a persisted set, not a scheduler.
package queue

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Entry is one persisted torrent. Exactly one of Magnet / TorrentURL is set.
type Entry struct {
	InfoHash   string   `json:"info_hash"`
	Magnet     string   `json:"magnet,omitempty"`
	TorrentURL string   `json:"torrent_url,omitempty"`
	Name       string   `json:"name"`
	Paused     bool     `json:"paused"`
	FileGlobs  []string `json:"file_globs,omitempty"` // add-time --files patterns, until resolved
	Deselected []string `json:"deselected,omitempty"` // file paths not being downloaded
}

// Store is the persisted queue. An empty Path disables Save (used by tests
// without a temp file, and when persistence is turned off).
type Store struct {
	Path    string  `json:"-"`
	Entries []Entry `json:"entries"`
}

// DefaultPath is <config dir>/shoal/queue.json ("" if the config dir is unknown).
func DefaultPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "shoal", "queue.json")
}

// Load reads the queue from the default config-dir path.
func Load() *Store { return LoadFrom(DefaultPath()) }

// LoadFrom reads the queue from path; a missing or corrupt file yields an empty
// (but writable) store.
func LoadFrom(path string) *Store {
	s := &Store{Path: path}
	b, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	_ = json.Unmarshal(b, s)
	s.Path = path // Unmarshal can't set the json:"-" field
	return s
}

// Upsert replaces the entry with the same InfoHash, or appends it, then persists.
func (s *Store) Upsert(e Entry) {
	for i := range s.Entries {
		if s.Entries[i].InfoHash == e.InfoHash {
			s.Entries[i] = e
			_ = s.Save()
			return
		}
	}
	s.Entries = append(s.Entries, e)
	_ = s.Save()
}

// Remove drops the entry with infoHash (if present) and persists.
func (s *Store) Remove(infoHash string) {
	kept := make([]Entry, 0, len(s.Entries))
	changed := false
	for _, e := range s.Entries {
		if e.InfoHash == infoHash {
			changed = true
			continue
		}
		kept = append(kept, e)
	}
	if changed {
		s.Entries = kept
		_ = s.Save()
	}
}

// SetPaused updates the paused flag for infoHash (if present) and persists.
func (s *Store) SetPaused(infoHash string, paused bool) {
	for i := range s.Entries {
		if s.Entries[i].InfoHash == infoHash {
			s.Entries[i].Paused = paused
			_ = s.Save()
			return
		}
	}
}

// SetFileGlobs records add-time --files patterns for infoHash (if present) and persists.
func (s *Store) SetFileGlobs(infoHash string, globs []string) {
	for i := range s.Entries {
		if s.Entries[i].InfoHash == infoHash {
			s.Entries[i].FileGlobs = globs
			_ = s.Save()
			return
		}
	}
}

// SetDeselected records the deselected file paths for infoHash (if present) and persists.
func (s *Store) SetDeselected(infoHash string, paths []string) {
	for i := range s.Entries {
		if s.Entries[i].InfoHash == infoHash {
			s.Entries[i].Deselected = paths
			_ = s.Save()
			return
		}
	}
}

// Get returns a copy of the entry for infoHash (false if not found). The
// returned Entry's slice fields are cloned so callers can't mutate the store.
func (s *Store) Get(infoHash string) (Entry, bool) {
	for i := range s.Entries {
		if s.Entries[i].InfoHash == infoHash {
			e := s.Entries[i]
			e.FileGlobs = append([]string(nil), e.FileGlobs...)
			e.Deselected = append([]string(nil), e.Deselected...)
			return e, true
		}
	}
	return Entry{}, false
}

// SetName updates the display name for infoHash (if present and changed) and
// persists — so a restored torrent shows its real name before metadata loads.
func (s *Store) SetName(infoHash, name string) {
	for i := range s.Entries {
		if s.Entries[i].InfoHash == infoHash {
			if s.Entries[i].Name != name {
				s.Entries[i].Name = name
				_ = s.Save()
			}
			return
		}
	}
}

// Save writes the store to Path (creating the dir). No-op when Path is empty.
func (s *Store) Save() error {
	if s.Path == "" {
		return nil
	}
	dir := filepath.Dir(s.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// MkdirAll is a no-op (no chmod) when dir already exists, so tighten it
	// explicitly — keep this even if it looks redundant.
	// ponytail: Chmod needs dir ownership; on a shared/multi-user or
	// UID-remapped dir this can fail where a plain write would've succeeded.
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.Path, b, 0o600)
}
