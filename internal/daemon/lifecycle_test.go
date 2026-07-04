package daemon

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/StrangeNoob/shoal/internal/engine"
)

// serveServer runs srv on a short-path temp unix socket and returns a connected
// Client plus a channel carrying Serve's return value.
func serveServer(t *testing.T, srv *Server) (*Client, <-chan error) {
	t.Helper()
	dir, err := os.MkdirTemp("", "shoal-d") // short path — macOS sun_path limit
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "d.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(l) }()
	t.Cleanup(func() { l.Close() })
	c, err := Dial(sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c, errc
}

func TestControlShutdownStopsServe(t *testing.T) {
	srv := NewServer(&fakeEngine{}, time.Now(), 0)
	c, errc := serveServer(t, srv)
	if err := c.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	select {
	case err := <-errc:
		if err != nil {
			t.Fatalf("Serve returned %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after Control.Shutdown")
	}
}

func TestControlStatusCounts(t *testing.T) {
	fake := &fakeEngine{statuses: []engine.Status{
		{InfoHash: "a", Done: false},
		{InfoHash: "b", Done: true},
	}}
	srv := NewServer(fake, time.Now(), 0)
	c, _ := serveServer(t, srv)
	st, err := c.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Torrents != 2 || st.Downloading != 1 || st.Seeding != 1 {
		t.Fatalf("counts = %+v, want 2/1/1", st)
	}
	if st.Pid != os.Getpid() {
		t.Fatalf("Pid = %d, want %d", st.Pid, os.Getpid())
	}
	if st.Uptime <= 0 {
		t.Fatalf("Uptime = %v, want > 0", st.Uptime)
	}
}

func TestIdleShutdownFires(t *testing.T) {
	// No client connected, no torrents → idle → Serve returns within the timeout.
	dir, err := os.MkdirTemp("", "shoal-d")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	l, err := net.Listen("unix", filepath.Join(dir, "d.sock"))
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(&fakeEngine{}, time.Now(), 60*time.Millisecond)
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(l) }()
	select {
	case <-errc: // Serve returned = idle shutdown fired
	case <-time.After(2 * time.Second):
		l.Close()
		t.Fatal("idle shutdown did not fire")
	}
}

func TestIdleHeldOffByTorrent(t *testing.T) {
	dir, err := os.MkdirTemp("", "shoal-d")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	l, err := net.Listen("unix", filepath.Join(dir, "d.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	srv := NewServer(&fakeEngine{statuses: []engine.Status{{InfoHash: "a"}}}, time.Now(), 60*time.Millisecond)
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(l) }()
	select {
	case <-errc:
		t.Fatal("Serve returned; a torrent should keep the daemon alive")
	case <-time.After(300 * time.Millisecond): // still running — good
	}
}

func TestIdleHeldOffByConnection(t *testing.T) {
	dir, err := os.MkdirTemp("", "shoal-d")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "d.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	srv := NewServer(&fakeEngine{}, time.Now(), 60*time.Millisecond)
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(l) }()
	var d net.Dialer
	conn, err := d.DialContext(context.Background(), "unix", sock) // hold a connection open
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-errc:
		conn.Close()
		t.Fatal("Serve returned; an open connection should keep the daemon alive")
	case <-time.After(300 * time.Millisecond): // still running — good
	}
	conn.Close() // now idle → should shut down
	select {
	case <-errc:
	case <-time.After(2 * time.Second):
		t.Fatal("idle shutdown did not fire after the connection closed")
	}
}

func TestClientSetDeadlineBoundsCalls(t *testing.T) {
	dir, err := os.MkdirTemp("", "shoal-d")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "d.sock")
	l, err := net.Listen("unix", sock) // listen but never Accept/serve → RPC would hang
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	c, err := Dial(sock)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.SetDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := c.Status(); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Status should fail on the deadline against an unresponsive server")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Status hung despite the deadline")
	}
}
