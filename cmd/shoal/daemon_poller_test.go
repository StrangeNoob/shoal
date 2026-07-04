package main

import (
	"strings"
	"testing"

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
