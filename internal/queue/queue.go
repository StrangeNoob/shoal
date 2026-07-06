// Package queue persists the set of torrents the user has added (downloads and
// seeding) as JSON in the OS user-config dir (queue.json), so they can be
// re-added on the next launch. It is a persisted set, not a scheduler.
package queue

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
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
// without a temp file, and when persistence is turned off). Safe for
// concurrent use: mu guards Entries (unexported, so json.Marshal ignores it).
type Store struct {
	Path    string  `json:"-"`
	Entries []Entry `json:"entries"`

	mu sync.Mutex
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
	s.mu.Lock()
	found := false
	for i := range s.Entries {
		if s.Entries[i].InfoHash == e.InfoHash {
			s.Entries[i] = e
			found = true
			break
		}
	}
	if !found {
		s.Entries = append(s.Entries, e)
	}
	s.mu.Unlock()
	_ = s.Save()
}

// Remove drops the entry with infoHash (if present) and persists.
func (s *Store) Remove(infoHash string) {
	s.mu.Lock()
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
	}
	s.mu.Unlock()
	if changed {
		_ = s.Save()
	}
}

// SetPaused updates the paused flag for infoHash (if present) and persists.
func (s *Store) SetPaused(infoHash string, paused bool) {
	s.mu.Lock()
	found := false
	for i := range s.Entries {
		if s.Entries[i].InfoHash == infoHash {
			s.Entries[i].Paused = paused
			found = true
			break
		}
	}
	s.mu.Unlock()
	if found {
		_ = s.Save()
	}
}

// SetFileGlobs records add-time --files patterns for infoHash (if present) and persists.
func (s *Store) SetFileGlobs(infoHash string, globs []string) {
	s.mu.Lock()
	found := false
	for i := range s.Entries {
		if s.Entries[i].InfoHash == infoHash {
			s.Entries[i].FileGlobs = append([]string(nil), globs...)
			found = true
			break
		}
	}
	s.mu.Unlock()
	if found {
		_ = s.Save()
	}
}

// SetDeselected records the deselected file paths for infoHash (if present) and persists.
func (s *Store) SetDeselected(infoHash string, paths []string) {
	s.mu.Lock()
	found := false
	for i := range s.Entries {
		if s.Entries[i].InfoHash == infoHash {
			s.Entries[i].Deselected = append([]string(nil), paths...)
			found = true
			break
		}
	}
	s.mu.Unlock()
	if found {
		_ = s.Save()
	}
}

// Snapshot returns a shallow copy of Entries (a new slice header) for callers
// that need to range over the queue without racing Save's marshal or other
// mutators. Entry values are copied by value; their inner slices are not
// deep-cloned since callers only read them.
func (s *Store) Snapshot() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Entry(nil), s.Entries...)
}

// Get returns a copy of the entry for infoHash (false if not found). The
// returned Entry's slice fields are cloned so callers can't mutate the store.
func (s *Store) Get(infoHash string) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	s.mu.Lock()
	changed := false
	for i := range s.Entries {
		if s.Entries[i].InfoHash == infoHash {
			if s.Entries[i].Name != name {
				s.Entries[i].Name = name
				changed = true
			}
			break
		}
	}
	s.mu.Unlock()
	if changed {
		_ = s.Save()
	}
}

// Save writes the store to Path (creating the dir). No-op when Path is empty.
func (s *Store) Save() error {
	if s.Path == "" {
		return nil
	}
	s.mu.Lock()
	b, err := json.MarshalIndent(s, "", "  ") // marshal a consistent snapshot of Entries
	s.mu.Unlock()
	if err != nil {
		return err
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
	return os.WriteFile(s.Path, b, 0o600)
}
