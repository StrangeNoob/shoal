package queue

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func tmpStore(t *testing.T) *Store {
	t.Helper()
	return LoadFrom(filepath.Join(t.TempDir(), "queue.json"))
}

func TestUpsertRoundTrip(t *testing.T) {
	s := tmpStore(t)
	s.Upsert(Entry{InfoHash: "aaa", Magnet: "magnet:?a", Name: "A"})
	s.Upsert(Entry{InfoHash: "bbb", TorrentURL: "http://x/b.torrent", Name: "B", Paused: true})

	got := LoadFrom(s.Path)
	if len(got.Entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(got.Entries))
	}
	if got.Entries[0].InfoHash != "aaa" || got.Entries[0].Magnet != "magnet:?a" {
		t.Errorf("entry 0 = %+v", got.Entries[0])
	}
	if got.Entries[1].TorrentURL != "http://x/b.torrent" || !got.Entries[1].Paused {
		t.Errorf("entry 1 = %+v", got.Entries[1])
	}
}

func TestUpsertReplacesByInfoHash(t *testing.T) {
	s := tmpStore(t)
	s.Upsert(Entry{InfoHash: "aaa", Name: "old"})
	s.Upsert(Entry{InfoHash: "aaa", Name: "new"})
	if len(s.Entries) != 1 || s.Entries[0].Name != "new" {
		t.Fatalf("upsert should replace by hash: %+v", s.Entries)
	}
}

func TestRemove(t *testing.T) {
	s := tmpStore(t)
	s.Upsert(Entry{InfoHash: "aaa"})
	s.Upsert(Entry{InfoHash: "bbb"})
	s.Remove("aaa")
	if len(s.Entries) != 1 || s.Entries[0].InfoHash != "bbb" {
		t.Fatalf("remove failed: %+v", s.Entries)
	}
	if len(LoadFrom(s.Path).Entries) != 1 {
		t.Fatal("remove not persisted")
	}
}

func TestSetName(t *testing.T) {
	s := tmpStore(t)
	s.Upsert(Entry{InfoHash: "aaa", Name: ""})
	s.SetName("aaa", "Cool Movie")
	if got := LoadFrom(s.Path).Entries[0].Name; got != "Cool Movie" {
		t.Fatalf("SetName not persisted: %q", got)
	}
	s.SetName("nope", "x") // unknown hash is a no-op
	if len(s.Entries) != 1 {
		t.Fatalf("SetName on unknown hash changed the store: %+v", s.Entries)
	}
}

func TestSetPaused(t *testing.T) {
	s := tmpStore(t)
	s.Upsert(Entry{InfoHash: "aaa"})
	s.SetPaused("aaa", true)
	if !LoadFrom(s.Path).Entries[0].Paused {
		t.Fatal("SetPaused not persisted")
	}
}

func TestSelectionPersists(t *testing.T) {
	s := tmpStore(t)
	s.Upsert(Entry{InfoHash: "abc", Magnet: "magnet:?xt=urn:btih:abc"})

	s.SetFileGlobs("abc", []string{"*.mkv"})
	s.SetDeselected("abc", []string{"extras/sample.mkv"})

	got := LoadFrom(s.Path)
	e := got.Entries[0]
	if len(e.FileGlobs) != 1 || e.FileGlobs[0] != "*.mkv" {
		t.Fatalf("FileGlobs = %v", e.FileGlobs)
	}
	if len(e.Deselected) != 1 || e.Deselected[0] != "extras/sample.mkv" {
		t.Fatalf("Deselected = %v", e.Deselected)
	}
}

func TestStoreGetReturnsCopy(t *testing.T) {
	s := tmpStore(t)
	s.Upsert(Entry{InfoHash: "aaa", FileGlobs: []string{"*.mkv"}, Deselected: []string{"a"}})

	e, ok := s.Get("aaa")
	if !ok {
		t.Fatal("Get: not found")
	}
	e.FileGlobs[0] = "mutated"
	e.Deselected[0] = "mutated"
	if s.Entries[0].FileGlobs[0] == "mutated" || s.Entries[0].Deselected[0] == "mutated" {
		t.Fatal("Get returned aliased slices, not a copy")
	}

	if _, ok := s.Get("nope"); ok {
		t.Fatal("Get: found unknown hash")
	}
}

func TestStoreSnapshot(t *testing.T) {
	s := tmpStore(t)
	s.Upsert(Entry{InfoHash: "aaa"})
	s.Upsert(Entry{InfoHash: "bbb"})

	snap := s.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("Snapshot: got %d entries, want 2", len(snap))
	}

	snap = append(snap, Entry{InfoHash: "ccc"})
	if len(snap) != 3 {
		t.Fatalf("append to the snapshot should grow it to 3, got %d", len(snap))
	}
	if len(s.Entries) != 2 {
		t.Fatal("mutating the returned slice header affected the store")
	}
}

func TestSaveUsesOwnerOnlyPerms(t *testing.T) {
	s := tmpStore(t)
	s.Upsert(Entry{InfoHash: "aaa"})
	fi, err := os.Stat(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %o, want 600", fi.Mode().Perm())
	}
	di, err := os.Stat(filepath.Dir(s.Path))
	if err != nil {
		t.Fatal(err)
	}
	if di.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %o, want 700", di.Mode().Perm())
	}
}

// TestConcurrentAccess mimics engine.SetFiles's background goroutine (SetDeselected,
// no engine lock held) racing RPC-driven calls (SetPaused, SetName, Get, Save) on the
// same Store, as happens with a.store touched both under and without a.mu. Run with
// -race: it must not report a data race on Entries.
func TestConcurrentAccess(t *testing.T) {
	s := tmpStore(t)
	s.Upsert(Entry{InfoHash: "aaa"})

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(4)
		go func() { defer wg.Done(); s.SetDeselected("aaa", []string{"a", "b"}) }()
		go func() { defer wg.Done(); s.SetPaused("aaa", true) }()
		go func() { defer wg.Done(); s.SetName("aaa", "concurrent") }()
		go func() { defer wg.Done(); s.Get("aaa") }()
	}
	wg.Wait()
}
