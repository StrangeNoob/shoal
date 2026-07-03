package main

import (
	"testing"
	"time"

	"github.com/StrangeNoob/shoal/internal/engine"
	"github.com/StrangeNoob/shoal/internal/history"
)

func TestResolveTarget(t *testing.T) {
	lookup := func(id string) (string, bool) {
		if id == "a1b2c3d4" {
			return "magnet:?xt=urn:btih:a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4&dn=Cached", true
		}
		return "", false
	}
	const ih = "0123456789abcdef0123456789abcdef01234567"
	cases := []struct {
		name       string
		arg        string
		wantMagnet string
		wantURL    string
		wantHandle string
		wantErr    bool
	}{
		{"magnet", "magnet:?xt=urn:btih:" + ih + "&dn=X", "magnet:?xt=urn:btih:" + ih + "&dn=X", "", "01234567", false},
		{"http url", "https://example.com/x.torrent", "", "https://example.com/x.torrent", "", false},
		{"infohash40", ih, "magnet:?xt=urn:btih:" + ih, "", "01234567", false},
		{"short id hit", "a1b2c3d4", "magnet:?xt=urn:btih:a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4&dn=Cached", "", "a1b2c3d4", false},
		{"short id miss", "deadbeef", "", "", "", true},
		{"garbage", "not a torrent", "", "", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := resolveTarget(c.arg, lookup)
			if c.wantErr {
				if err == nil {
					t.Fatalf("want error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Magnet != c.wantMagnet || got.URL != c.wantURL {
				t.Errorf("target = %+v, want magnet=%q url=%q", got, c.wantMagnet, c.wantURL)
			}
			if c.name == "http url" {
				if len(got.Handle) != 8 {
					t.Errorf("url handle = %q, want 8 hex chars", got.Handle)
				}
			} else if got.Handle != c.wantHandle {
				t.Errorf("handle = %q, want %q", got.Handle, c.wantHandle)
			}
		})
	}
}

func TestResolveTargetNilLookup(t *testing.T) {
	got, err := resolveTarget("deadbeef", nil)
	if err == nil {
		t.Fatalf("nil lookup should error, got %+v", got)
	}
}

type fakeEngine struct {
	frames [][]engine.Status
	i      int
}

func (f *fakeEngine) AddTorrentURL(string, string) error { return nil }
func (f *fakeEngine) AddMagnet(string) error             { return nil }
func (f *fakeEngine) Remove(string, bool) error          { return nil }
func (f *fakeEngine) Pause(string) error                 { return nil }
func (f *fakeEngine) Resume(string) error                { return nil }
func (f *fakeEngine) Close() error                       { return nil }
func (f *fakeEngine) Statuses() []engine.Status {
	if f.i < len(f.frames) {
		s := f.frames[f.i]
		f.i++
		return s
	}
	if len(f.frames) == 0 {
		return nil
	}
	return f.frames[len(f.frames)-1]
}

func TestStepWorkerProgressAndDone(t *testing.T) {
	base := t.TempDir()
	eng := &fakeEngine{frames: [][]engine.Status{
		{{InfoHash: "ffff0000", Name: "Movie", TotalBytes: 100, CompletedBytes: 40, Peers: 3}},
		{{InfoHash: "ffff0000", Name: "Movie", TotalBytes: 100, CompletedBytes: 100, Done: true, Path: "/data/Movie"}},
	}}
	a := Active{ID: "ffff0000"}
	if done := stepWorker(eng, base, &a); done {
		t.Fatal("frame 1 should not be done")
	}
	got, _ := readActive(base, "ffff0000")
	if got.Completed != 40 || got.Name != "Movie" || got.Peers != 3 {
		t.Fatalf("mid-progress not written: %+v", got)
	}
	if done := stepWorker(eng, base, &a); !done {
		t.Fatal("frame 2 should be done")
	}
	got, _ = readActive(base, "ffff0000")
	if !got.Done || got.Path != "/data/Movie" {
		t.Fatalf("done state not written: %+v", got)
	}
}

func TestRunWorkerRecordsHistory(t *testing.T) {
	base := t.TempDir()
	eng := &fakeEngine{frames: [][]engine.Status{
		{{InfoHash: "eeee1111", Name: "Done", TotalBytes: 10, CompletedBytes: 10, Done: true, Path: "/d/Done"}},
	}}
	hist := history.Store{Path: base + "/history.json"}
	runWorker(eng, base, Active{ID: "eeee1111"}, &hist, time.Millisecond)
	got := history.LoadFrom(hist.Path)
	if len(got.Entries) != 1 || got.Entries[0].Name != "Done" || got.Entries[0].Path != "/d/Done" {
		t.Fatalf("history not recorded: %+v", got.Entries)
	}
}
