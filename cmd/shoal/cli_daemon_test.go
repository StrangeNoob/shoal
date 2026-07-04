package main

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/StrangeNoob/shoal/internal/daemon"
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
	// os.MkdirTemp (not t.TempDir()) keeps the socket path short — t.TempDir()
	// embeds the test name and can overflow macOS's ~104-byte unix sun_path limit.
	dir, err := os.MkdirTemp("", "shoal-d")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "d.sock")
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

func TestListenDaemonReclaimsStaleSocket(t *testing.T) {
	dir, err := os.MkdirTemp("", "shoal-d")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "d.sock")
	// A stale socket *file* with nothing listening (create then close a listener).
	l0, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	l0.Close() // leaves (or removes) the file; either way no daemon answers
	if err := os.WriteFile(sock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := listenDaemon(sock)
	if err != nil {
		t.Fatalf("listenDaemon should reclaim a stale socket: %v", err)
	}
	l.Close()
}

func TestDaemonStopStopsRunning(t *testing.T) {
	serveFakeDaemon(t, &fakeEngine{}) // serves via daemon.Serve (registers Control)
	var buf bytes.Buffer
	if code := runDaemonStop(&buf); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(buf.String(), "daemon stopped") {
		t.Fatalf("output = %q, want 'daemon stopped'", buf.String())
	}
}

func TestDaemonStopNoDaemon(t *testing.T) {
	t.Setenv("SHOAL_DAEMON_SOCK", filepath.Join(t.TempDir(), "absent.sock"))
	var buf bytes.Buffer
	if code := runDaemonStop(&buf); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(buf.String(), "daemon not running") {
		t.Fatalf("output = %q, want 'daemon not running'", buf.String())
	}
}

func TestDaemonStatusReportsCounts(t *testing.T) {
	serveFakeDaemon(t, &fakeEngine{statuses: []engine.Status{
		{InfoHash: "a", Done: false},
		{InfoHash: "b", Done: true},
	}})
	var buf bytes.Buffer
	if code := runDaemonStatus(&buf); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	out := buf.String()
	if !strings.Contains(out, "running") || !strings.Contains(out, "2 torrents") ||
		!strings.Contains(out, "1 downloading") || !strings.Contains(out, "1 seeding") {
		t.Fatalf("status output = %q", out)
	}
}

func TestDaemonStatusNoDaemon(t *testing.T) {
	t.Setenv("SHOAL_DAEMON_SOCK", filepath.Join(t.TempDir(), "absent.sock"))
	var buf bytes.Buffer
	if code := runDaemonStatus(&buf); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(buf.String(), "daemon not running") {
		t.Fatalf("output = %q, want 'daemon not running'", buf.String())
	}
}

func TestListenDaemonRefusesLiveDaemon(t *testing.T) {
	dir, err := os.MkdirTemp("", "shoal-d")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "d.sock")
	fake := &fakeEngine{}
	l0, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	go daemon.Serve(l0, fake) // a live daemon is answering
	t.Cleanup(func() { l0.Close() })
	if _, err := listenDaemon(sock); err == nil {
		t.Fatal("listenDaemon should refuse when a live daemon already answers")
	}
}
