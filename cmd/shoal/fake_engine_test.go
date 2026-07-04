package main

import (
	"net"
	"os"
	"sync"
	"testing"

	"github.com/StrangeNoob/shoal/internal/daemon"
	"github.com/StrangeNoob/shoal/internal/engine"
)

type fakeEngine struct {
	mu       sync.Mutex
	magnets  []string
	urls     [][2]string
	removed  []string
	statuses []engine.Status
}

func (f *fakeEngine) AddMagnet(m string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.magnets = append(f.magnets, m)
	return nil
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
func (f *fakeEngine) Pause(string) error  { return nil }
func (f *fakeEngine) Resume(string) error { return nil }
func (f *fakeEngine) Close() error        { return nil }

func (f *fakeEngine) gotMagnets() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.magnets...)
}
func (f *fakeEngine) gotURLs() [][2]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][2]string(nil), f.urls...)
}
func (f *fakeEngine) gotRemoved() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.removed...)
}

// serveFakeDaemon starts a daemon backed by fake on a temp socket and points
// SHOAL_DAEMON_SOCK at it so the CLI connects to it.
func serveFakeDaemon(t *testing.T, fake engine.Engine) {
	t.Helper()
	// ponytail: os.MkdirTemp (not t.TempDir()) — t.TempDir() embeds the full test
	// name in the path, which overflows macOS's ~103-byte unix sun_path limit for
	// longer test names; a short fixed prefix keeps every caller under it.
	dir, err := os.MkdirTemp("", "shoal-d")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := dir + "/d.sock"
	t.Setenv("SHOAL_DAEMON_SOCK", sock)
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	go daemon.Serve(l, fake)
	t.Cleanup(func() { l.Close() })
}
