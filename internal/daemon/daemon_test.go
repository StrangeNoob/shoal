// internal/daemon/daemon_test.go
package daemon

import (
	"errors"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/StrangeNoob/shoal/internal/engine"
)

// fakeEngine records calls (mutex-guarded so the -race detector is happy across
// the server goroutine and the test goroutine).
type fakeEngine struct {
	mu       sync.Mutex
	magnets  []string
	urls     [][2]string
	removed  []string
	paused   []string
	resumed  []string
	statuses []engine.Status
	addErr   error
}

func (f *fakeEngine) AddMagnet(m string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.magnets = append(f.magnets, m)
	return f.addErr
}
func (f *fakeEngine) AddTorrentURL(u, n string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.urls = append(f.urls, [2]string{u, n})
	return nil
}
func (f *fakeEngine) Statuses() []engine.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.statuses
}
func (f *fakeEngine) Remove(h string, _ bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, h)
	return nil
}
func (f *fakeEngine) Pause(h string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paused = append(f.paused, h)
	return nil
}
func (f *fakeEngine) Resume(h string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resumed = append(f.resumed, h)
	return nil
}
func (f *fakeEngine) Close() error { return nil }

func (f *fakeEngine) snap() ([]string, [][2]string, []string, []string, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.magnets, f.urls, f.removed, f.paused, f.resumed
}

func serveTest(t *testing.T, eng engine.Engine) *Client {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "d.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	go Serve(l, eng)
	t.Cleanup(func() { l.Close() })
	c, err := Dial(sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestRoundTrip(t *testing.T) {
	wantAdded := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	fake := &fakeEngine{statuses: []engine.Status{
		{Name: "Movie", InfoHash: "abc", TotalBytes: 100, CompletedBytes: 40, Peers: 3, AddedAt: wantAdded},
	}}
	c := serveTest(t, fake)

	for _, err := range []error{
		c.AddMagnet("magnet:x"),
		c.AddTorrentURL("http://x/y.torrent", "Y"),
		c.Remove("h1", true),
		c.Pause("h2"),
		c.Resume("h3"),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	st := c.Statuses()

	magnets, urls, removed, paused, resumed := fake.snap()
	if len(magnets) != 1 || magnets[0] != "magnet:x" {
		t.Errorf("magnets=%v", magnets)
	}
	if len(urls) != 1 || urls[0] != [2]string{"http://x/y.torrent", "Y"} {
		t.Errorf("urls=%v", urls)
	}
	if len(removed) != 1 || removed[0] != "h1" {
		t.Errorf("removed=%v", removed)
	}
	if len(paused) != 1 || paused[0] != "h2" || len(resumed) != 1 || resumed[0] != "h3" {
		t.Errorf("paused=%v resumed=%v", paused, resumed)
	}
	if len(st) != 1 || st[0].Name != "Movie" || st[0].CompletedBytes != 40 || st[0].Peers != 3 || !st[0].AddedAt.Equal(wantAdded) {
		t.Errorf("statuses=%+v", st)
	}
}

func TestErrorPropagation(t *testing.T) {
	c := serveTest(t, &fakeEngine{addErr: errors.New("boom")})
	if err := c.AddMagnet("m"); err == nil || err.Error() != "boom" {
		t.Fatalf("want boom error, got %v", err)
	}
}

func TestStatusesOnClosedConn(t *testing.T) {
	c := serveTest(t, &fakeEngine{})
	c.Close()
	if st := c.Statuses(); st != nil {
		t.Fatalf("closed client should return nil statuses, got %v", st)
	}
}

func TestSocketPathEnvOverride(t *testing.T) {
	t.Setenv("SHOAL_DAEMON_SOCK", "/tmp/custom.sock")
	if SocketPath() != "/tmp/custom.sock" {
		t.Fatalf("env override ignored: %s", SocketPath())
	}
}
