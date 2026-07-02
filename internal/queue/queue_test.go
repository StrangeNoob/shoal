package queue

import (
	"os"
	"path/filepath"
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
