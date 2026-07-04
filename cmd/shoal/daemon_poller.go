package main

import (
	"sync"

	"github.com/StrangeNoob/shoal/internal/daemon"
	"github.com/StrangeNoob/shoal/internal/engine"
)

// daemonPoller is a self-healing engine.Engine for the TUI: it holds a daemon
// client, (re)connecting via ensureDaemon (which auto-starts a down daemon) on
// demand, and exposes Poll() so the TUI can distinguish a dead daemon from an
// empty engine and show a reconnecting state.
type daemonPoller struct {
	mu sync.Mutex
	c  *daemon.Client // nil when disconnected
}

func newDaemonPoller() *daemonPoller { return &daemonPoller{} }

var _ engine.Engine = (*daemonPoller)(nil)

// client returns a live client, reconnecting via ensureDaemon (auto-start) if none.
func (p *daemonPoller) client() (*daemon.Client, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.c != nil {
		return p.c, nil
	}
	c, err := ensureDaemon()
	if err != nil {
		return nil, err
	}
	p.c = c
	return c, nil
}

// drop closes and forgets the client so the next call reconnects.
func (p *daemonPoller) drop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.c != nil {
		p.c.Close()
		p.c = nil
	}
}

// Poll returns the daemon's statuses and any connection error, dropping the
// client on error to force a reconnect next time.
func (p *daemonPoller) Poll() ([]engine.Status, error) {
	c, err := p.client()
	if err != nil {
		return nil, err
	}
	ss, err := c.StatusesErr()
	if err != nil {
		p.drop()
	}
	return ss, err
}

func (p *daemonPoller) Statuses() []engine.Status { ss, _ := p.Poll(); return ss }

// do runs an engine op through a live client, dropping the client on error.
func (p *daemonPoller) do(fn func(*daemon.Client) error) error {
	c, err := p.client()
	if err != nil {
		return err
	}
	if err := fn(c); err != nil {
		p.drop()
		return err
	}
	return nil
}

func (p *daemonPoller) AddMagnet(m string) error {
	return p.do(func(c *daemon.Client) error { return c.AddMagnet(m) })
}
func (p *daemonPoller) AddTorrentURL(u, n string) error {
	return p.do(func(c *daemon.Client) error { return c.AddTorrentURL(u, n) })
}
func (p *daemonPoller) Remove(h string, del bool) error {
	return p.do(func(c *daemon.Client) error { return c.Remove(h, del) })
}
func (p *daemonPoller) Pause(h string) error {
	return p.do(func(c *daemon.Client) error { return c.Pause(h) })
}
func (p *daemonPoller) Resume(h string) error {
	return p.do(func(c *daemon.Client) error { return c.Resume(h) })
}
func (p *daemonPoller) Close() error { p.drop(); return nil }
