package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/StrangeNoob/shoal/internal/engine"
)

// writeTestTorrent writes a minimal valid single-file .torrent to path.
func writeTestTorrent(t *testing.T, path string) {
	t.Helper()
	info := metainfo.Info{Name: "hello.txt", Length: 5, PieceLength: 32768, Pieces: make([]byte, 20)}
	b, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	mi := metainfo.MetaInfo{InfoBytes: b, Announce: "http://tracker.example/announce"}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := mi.Write(f); err != nil {
		t.Fatal(err)
	}
}

func TestResolveTargetLocalTorrentFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.torrent")
	writeTestTorrent(t, path)
	got, err := resolveTarget(path, nil)
	if err != nil {
		t.Fatalf("resolveTarget(local .torrent): %v", err)
	}
	if !strings.HasPrefix(got.Magnet, "magnet:?") {
		t.Errorf("magnet = %q, want a magnet URI", got.Magnet)
	}
	if !strings.Contains(got.Magnet, "tracker.example") {
		t.Errorf("magnet should carry the .torrent's tracker: %q", got.Magnet)
	}
	if len(got.Handle) != 8 {
		t.Errorf("handle = %q, want 8 hex chars", got.Handle)
	}
}

func TestResolveTargetInvalidTorrentFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.torrent")
	if err := os.WriteFile(path, []byte("not bencode"), 0o644); err != nil {
		t.Fatal(err)
	}
	// An existing-but-unparseable file must report a parse error, not the
	// generic "unrecognized target" (which would hide the real problem).
	_, err := resolveTarget(path, nil)
	if err == nil || !strings.Contains(err.Error(), "bad.torrent") {
		t.Fatalf("want a parse error naming the file, got %v", err)
	}
}

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

func TestDownloadForwardsMagnetToDaemon(t *testing.T) {
	fake := &fakeEngine{}
	serveFakeDaemon(t, fake)
	const ih = "0123456789abcdef0123456789abcdef01234567"
	var buf bytes.Buffer
	if code := runDownload([]string{"magnet:?xt=urn:btih:" + ih}, &buf); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if m := fake.gotMagnets(); len(m) != 1 || !strings.Contains(m[0], ih) {
		t.Fatalf("daemon did not receive the magnet: %v", m)
	}
	if !strings.Contains(buf.String(), "started:") {
		t.Fatalf("expected a 'started:' line, got %q", buf.String())
	}
}

func TestDownloadFilesForwardsGlobs(t *testing.T) {
	fake := &fakeEngine{}
	serveFakeDaemon(t, fake)
	const ih = "0123456789abcdef0123456789abcdef01234567"
	var buf bytes.Buffer
	if code := runDownload([]string{"--files", "*.mkv, *.srt", "magnet:?xt=urn:btih:" + ih}, &buf); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if g := fake.gotFileGlobs(); len(g) != 2 || g[0] != "*.mkv" || g[1] != "*.srt" {
		t.Fatalf("daemon did not receive the globs: %v", g)
	}
}

func TestDownloadFilesUnsupportedForURL(t *testing.T) {
	fake := &fakeEngine{}
	serveFakeDaemon(t, fake)
	var buf bytes.Buffer
	if code := runDownload([]string{"--files", "*.mkv", "https://example.com/x.torrent"}, &buf); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if g := fake.gotFileGlobs(); g != nil {
		t.Fatalf("expected no SetFileGlobs call for a URL target, got %v", g)
	}
}

func TestDownloadForwardsURLToDaemon(t *testing.T) {
	fake := &fakeEngine{}
	serveFakeDaemon(t, fake)
	var buf bytes.Buffer
	if code := runDownload([]string{"https://example.com/x.torrent"}, &buf); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if u := fake.gotURLs(); len(u) != 1 || u[0][0] != "https://example.com/x.torrent" {
		t.Fatalf("daemon did not receive the URL: %v", u)
	}
}

func TestDownloadWaitBlocksUntilDone(t *testing.T) {
	const ih = "0123456789abcdef0123456789abcdef01234567"
	fake := &fakeEngine{statuses: []engine.Status{
		{Name: "Movie", InfoHash: ih, TotalBytes: 100, CompletedBytes: 100, Done: true},
	}}
	serveFakeDaemon(t, fake)
	var buf bytes.Buffer
	if code := runDownload([]string{"--wait", "magnet:?xt=urn:btih:" + ih}, &buf); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(buf.String(), "done:") {
		t.Fatalf("--wait should print a 'done:' line, got %q", buf.String())
	}
}

func TestDownloadWaitURLBailsInsteadOfHanging(t *testing.T) {
	// A URL --wait where no new torrent ever appears (already present, or the
	// fake never adds one) must give up after waitResolveTries, not hang.
	old := waitPollInterval
	waitPollInterval = time.Millisecond
	t.Cleanup(func() { waitPollInterval = old })

	fake := &fakeEngine{statuses: []engine.Status{
		{Name: "Old", InfoHash: "1111111111111111111111111111111111111111"},
	}}
	serveFakeDaemon(t, fake)
	var buf bytes.Buffer
	done := make(chan int, 1)
	go func() { done <- runDownload([]string{"--wait", "https://example.com/x.torrent"}, &buf) }()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit = %d, want 0", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("download --wait hung instead of bailing")
	}
}

func TestDownloadURLPrintsNoHandle(t *testing.T) {
	fake := &fakeEngine{}
	serveFakeDaemon(t, fake)
	var buf bytes.Buffer
	if code := runDownload([]string{"https://example.com/x.torrent"}, &buf); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	// a URL download's sha1 handle is not queryable via status, so it must not be printed
	if strings.Contains(buf.String(), "(") {
		t.Fatalf("URL download should not print a handle, got %q", buf.String())
	}
}
