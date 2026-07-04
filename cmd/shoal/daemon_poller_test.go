package main

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/StrangeNoob/shoal/internal/engine"
)

func TestDaemonPollerPollsAndReconnects(t *testing.T) {
	fake := &fakeEngine{statuses: []engine.Status{{InfoHash: "a"}}}
	serveFakeDaemon(t, fake) // sets SHOAL_DAEMON_SOCK to a served socket

	p := newDaemonPoller()
	ss, err := p.Poll()
	if err != nil || len(ss) != 1 || ss[0].InfoHash != "a" {
		t.Fatalf("Poll on a live daemon = (%v, %v), want ([a], nil)", ss, err)
	}
	// A mutation goes through the same connection.
	if err := p.AddMagnet("magnet:?xt=urn:btih:" + strings.Repeat("0", 40)); err != nil {
		t.Fatalf("AddMagnet: %v", err)
	}
	if got := fake.gotMagnets(); len(got) != 1 {
		t.Fatalf("daemon did not receive the magnet: %v", got)
	}
	p.Close()
}

func TestDaemonPollerPollTimesOut(t *testing.T) {
	dir, err := os.MkdirTemp("", "shoal-d")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "d.sock")
	t.Setenv("SHOAL_DAEMON_SOCK", sock)
	l, err := net.Listen("unix", sock) // accepts connections but never serves RPC → calls hang
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	p := newDaemonPoller()
	p.timeout = 100 * time.Millisecond
	errc := make(chan error, 1)
	go func() { _, err := p.Poll(); errc <- err }()
	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("Poll should time out against an unresponsive daemon")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Poll hung despite the timeout")
	}
}
