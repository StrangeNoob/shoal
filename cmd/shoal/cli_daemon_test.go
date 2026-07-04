package main

import (
	"bytes"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/StrangeNoob/shoal/internal/engine"
	"github.com/StrangeNoob/shoal/internal/history"
)

func TestDaemonRunningGuard(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "d.sock")
	if daemonRunning(sock) {
		t.Fatal("no socket yet — should be false")
	}
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if !daemonRunning(sock) {
		t.Fatal("a listening socket — should be true")
	}
}

func TestCLIRoutesDaemonGuarded(t *testing.T) {
	// Pretend a daemon is already running so runDaemon hits its guard and returns
	// immediately (no engine, no blocking Serve). SHOAL_DAEMON_SOCK keeps the path
	// short (macOS sun_path limit).
	sock := filepath.Join(t.TempDir(), "d.sock")
	t.Setenv("SHOAL_DAEMON_SOCK", sock)
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	var buf bytes.Buffer
	handled, code := cli([]string{"shoal", "daemon"}, "1.0.0", &buf)
	if !handled {
		t.Error("cli did not handle daemon")
	}
	if code != 1 {
		t.Errorf("already-running guard should exit 1, got %d", code)
	}
}

func TestEnsureDaemonUsesRunning(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "d.sock")
	t.Setenv("SHOAL_DAEMON_SOCK", sock)
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	c, err := ensureDaemon() // must connect to the already-running socket, not spawn
	if err != nil {
		t.Fatalf("ensureDaemon should connect to the running daemon: %v", err)
	}
	c.Close()
}

func TestRecordCompletions(t *testing.T) {
	fake := &fakeEngine{statuses: []engine.Status{
		{InfoHash: "abcdef", Name: "Movie", TotalBytes: 100, Done: true, Path: "/d/Movie"},
	}}
	hist := history.Store{Path: filepath.Join(t.TempDir(), "history.json")}
	stop := make(chan struct{})
	go recordCompletions(fake, &hist, time.Millisecond, stop)
	deadline := time.After(2 * time.Second)
	for {
		if got := history.LoadFrom(hist.Path); len(got.Entries) == 1 {
			if got.Entries[0].Name != "Movie" || got.Entries[0].Path != "/d/Movie" {
				t.Fatalf("bad entry: %+v", got.Entries[0])
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("completion was not recorded to history")
		case <-time.After(10 * time.Millisecond):
		}
	}
	close(stop)
}
