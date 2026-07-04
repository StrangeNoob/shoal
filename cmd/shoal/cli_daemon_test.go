package main

import (
	"bytes"
	"net"
	"path/filepath"
	"testing"
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
