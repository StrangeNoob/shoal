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
	mu            sync.Mutex
	magnets       []string
	urls          [][2]string
	removed       []string
	removedDelete []bool
	paused        []string
	resumed       []string
	statuses      []engine.Status
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
	return append([]engine.Status(nil), f.statuses...)
}
func (f *fakeEngine) Remove(h string, deleteData bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, h)
	f.removedDelete = append(f.removedDelete, deleteData)
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
func (f *fakeEngine) gotRemovedDeleteFlags() []bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]bool(nil), f.removedDelete...)
}
func (f *fakeEngine) gotPaused() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.paused...)
}
func (f *fakeEngine) gotResumed() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.resumed...)
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
