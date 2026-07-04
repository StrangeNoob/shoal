package main

import (
	"fmt"
	"sync"
	"time"

	"github.com/StrangeNoob/shoal/internal/daemon"
	"github.com/StrangeNoob/shoal/internal/engine"
)

// daemonPoller is a self-healing engine.Engine for the TUI: it holds a daemon
// client, (re)connecting via ensureDaemon (which auto-starts a down daemon) on
// demand, and exposes Poll() so the TUI can distinguish a dead daemon from an
// empty engine and show a reconnecting state.
type daemonPoller struct {
	mu      sync.Mutex
	connMu  sync.Mutex     // serializes reconnect attempts; never held with mu across ensureDaemon
	c       *daemon.Client // nil when disconnected
	timeout time.Duration
}

func newDaemonPoller() *daemonPoller { return &daemonPoller{timeout: 2 * time.Second} }

var _ engine.Engine = (*daemonPoller)(nil)

// client returns a live client, reconnecting via ensureDaemon (auto-start) when
// none is cached. mu (guarding c) is never held across ensureDaemon; connMu
// serializes reconnect attempts so concurrent callers don't each spawn a daemon.
func (p *daemonPoller) client() (*daemon.Client, error) {
	p.mu.Lock()
	c := p.c
	p.mu.Unlock()
	if c != nil {
		return c, nil
	}

	p.connMu.Lock()
	defer p.connMu.Unlock()
	p.mu.Lock()
	c = p.c
	p.mu.Unlock()
	if c != nil { // another goroutine reconnected while we waited on connMu
		return c, nil
	}
	nc, err := ensureDaemon()
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.c = nc
	p.mu.Unlock()
	return nc, nil
}

// dropIf closes and forgets the client only if it's still the one that failed,
// so a slow straggler can't close a client another goroutine just reconnected.
func (p *daemonPoller) dropIf(bad *daemon.Client) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.c == bad {
		p.c.Close()
		p.c = nil
	}
}

// callWithTimeout runs fn against a live client, bounding it so a hung daemon
// can't block the caller forever. On error or timeout it drops that client
// (closing it, which unblocks the in-flight goroutine) to force a reconnect.
//
// We don't use daemon.Client.SetDeadline for this: it's a persistent net/rpc
// client whose background reader would then time out between polls and tear
// the client down even when the daemon is healthy.
func (p *daemonPoller) callWithTimeout(fn func(*daemon.Client) error) error {
	c, err := p.client()
	if err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- fn(c) }()
	select {
	case err := <-done:
		if err != nil {
			p.dropIf(c)
		}
		return err
	case <-time.After(p.timeout):
		p.dropIf(c) // closing c unblocks the goroutine's call, which then exits
		return fmt.Errorf("daemon request timed out")
	}
}

// Poll returns the daemon's statuses and any connection error, dropping the
// client on error to force a reconnect next time.
//
// This doesn't go through callWithTimeout: that helper only carries an error
// across its done channel, but Poll also needs the statuses value. Capturing
// it into a variable outside the per-call goroutine (as an outer closure
// write) would race with this function reading it after a timeout, since the
// abandoned goroutine can still be writing when we return. Instead the
// result is carried through the channel itself, so it's only ever touched by
// one goroutine at a time.
func (p *daemonPoller) Poll() ([]engine.Status, error) {
	c, err := p.client()
	if err != nil {
		return nil, err
	}
	type result struct {
		ss  []engine.Status
		err error
	}
	done := make(chan result, 1)
	go func() {
		ss, err := c.StatusesErr()
		done <- result{ss, err}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			p.dropIf(c)
		}
		return r.ss, r.err
	case <-time.After(p.timeout):
		p.dropIf(c) // closing c unblocks the goroutine's call, which then exits
		return nil, fmt.Errorf("daemon request timed out")
	}
}

func (p *daemonPoller) Statuses() []engine.Status { ss, _ := p.Poll(); return ss }

func (p *daemonPoller) AddMagnet(m string) error {
	return p.callWithTimeout(func(c *daemon.Client) error { return c.AddMagnet(m) })
}
func (p *daemonPoller) AddTorrentURL(u, n string) error {
	return p.callWithTimeout(func(c *daemon.Client) error { return c.AddTorrentURL(u, n) })
}
func (p *daemonPoller) Remove(h string, del bool) error {
	return p.callWithTimeout(func(c *daemon.Client) error { return c.Remove(h, del) })
}
func (p *daemonPoller) Pause(h string) error {
	return p.callWithTimeout(func(c *daemon.Client) error { return c.Pause(h) })
}
func (p *daemonPoller) Resume(h string) error {
	return p.callWithTimeout(func(c *daemon.Client) error { return c.Resume(h) })
}

// Close unconditionally drops the current client, regardless of identity.
func (p *daemonPoller) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.c != nil {
		p.c.Close()
		p.c = nil
	}
	return nil
}
