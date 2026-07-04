// internal/daemon/client.go
package daemon

import (
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"time"

	"github.com/StrangeNoob/shoal/internal/engine"
)

// Client talks to a daemon over a unix socket and implements engine.Engine.
type Client struct {
	conn net.Conn
	rpc  *rpc.Client
}

// Client is a drop-in engine.Engine, checked at compile time.
var _ engine.Engine = (*Client)(nil)

// Dial connects to the daemon listening at the unix socket path.
func Dial(path string) (*Client, error) {
	conn, err := net.Dial("unix", path)
	if err != nil {
		return nil, err
	}
	return &Client{conn: conn, rpc: jsonrpc.NewClient(conn)}, nil
}

// SetDeadline bounds subsequent RPC calls (zero value = no deadline). CLI control
// commands set it so they fail fast against a stuck daemon instead of hanging.
func (c *Client) SetDeadline(t time.Time) error { return c.conn.SetDeadline(t) }

func (c *Client) AddMagnet(m string) error {
	return c.rpc.Call("Engine.AddMagnet", AddMagnetArgs{Magnet: m}, &Empty{})
}
func (c *Client) AddTorrentURL(url, name string) error {
	return c.rpc.Call("Engine.AddTorrentURL", AddTorrentURLArgs{URL: url, Name: name}, &Empty{})
}
func (c *Client) Statuses() []engine.Status {
	var r StatusesReply
	if err := c.rpc.Call("Engine.Statuses", Empty{}, &r); err != nil {
		return nil
	}
	return r.Statuses
}

// StatusesErr is Statuses with the RPC error exposed, for callers (the TUI poll)
// that must distinguish a dead daemon from an empty engine.
func (c *Client) StatusesErr() ([]engine.Status, error) {
	var r StatusesReply
	err := c.rpc.Call("Engine.Statuses", Empty{}, &r)
	return r.Statuses, err
}
func (c *Client) Remove(hash string, deleteData bool) error {
	return c.rpc.Call("Engine.Remove", RemoveArgs{InfoHash: hash, DeleteData: deleteData}, &Empty{})
}
func (c *Client) Pause(hash string) error {
	return c.rpc.Call("Engine.Pause", HashArgs{InfoHash: hash}, &Empty{})
}
func (c *Client) Resume(hash string) error {
	return c.rpc.Call("Engine.Resume", HashArgs{InfoHash: hash}, &Empty{})
}

// Close closes the RPC connection only — it does not stop the shared engine.
func (c *Client) Close() error { return c.rpc.Close() }

// Shutdown asks the daemon to stop gracefully.
func (c *Client) Shutdown() error {
	return c.rpc.Call("Control.Shutdown", Empty{}, &Empty{})
}

// Status reports the daemon's uptime and torrent counts.
func (c *Client) Status() (StatusReply, error) {
	var r StatusReply
	err := c.rpc.Call("Control.Status", Empty{}, &r)
	return r, err
}
