package history

import (
	"path/filepath"
	"testing"
	"time"
)

func TestAppendSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	s := LoadFrom(path) // missing file → empty
	if len(s.Entries) != 0 {
		t.Fatalf("fresh store = %d entries, want 0", len(s.Entries))
	}

	s.Append(Entry{InfoHash: "a", Name: "First", Size: 100, CompletedAt: time.Unix(1000, 0)})
	s.Append(Entry{InfoHash: "b", Name: "Second", Size: 200, CompletedAt: time.Unix(2000, 0)})
	s.Append(Entry{InfoHash: "a", Name: "First-dup", Size: 999, CompletedAt: time.Unix(3000, 0)}) // dup infohash: ignored

	got := LoadFrom(path)
	if len(got.Entries) != 2 {
		t.Fatalf("loaded %d entries, want 2 (dup ignored): %+v", len(got.Entries), got.Entries)
	}
	if got.Entries[0].InfoHash != "b" {
		t.Fatalf("newest-first expected b first, got %q", got.Entries[0].InfoHash)
	}
}

func TestSaveNoopWithoutPath(t *testing.T) {
	s := Store{} // Path == ""
	s.Append(Entry{InfoHash: "x", Name: "X"})
	if err := s.Save(); err != nil {
		t.Fatalf("Save with empty Path should be a no-op nil, got %v", err)
	}
	if len(s.Entries) != 1 {
		t.Fatalf("Append should still update in-memory entries, got %d", len(s.Entries))
	}
}
