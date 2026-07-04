// internal/daemon/server.go
package daemon

import (
	"errors"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"os"
	"sync"
	"time"

	"github.com/StrangeNoob/shoal/internal/engine"
)

// EngineService adapts an engine.Engine to net/rpc method shapes (Engine.*).
type EngineService struct{ eng engine.Engine }

func (s *EngineService) AddMagnet(a AddMagnetArgs, _ *Empty) error {
	return s.eng.AddMagnet(a.Magnet)
}
func (s *EngineService) AddTorrentURL(a AddTorrentURLArgs, _ *Empty) error {
	return s.eng.AddTorrentURL(a.URL, a.Name)
}
func (s *EngineService) Statuses(_ Empty, r *StatusesReply) error {
	r.Statuses = s.eng.Statuses()
	return nil
}
func (s *EngineService) Remove(a RemoveArgs, _ *Empty) error {
	return s.eng.Remove(a.InfoHash, a.DeleteData)
}
func (s *EngineService) Pause(a HashArgs, _ *Empty) error {
	return s.eng.Pause(a.InfoHash)
}
func (s *EngineService) Resume(a HashArgs, _ *Empty) error {
	return s.eng.Resume(a.InfoHash)
}

// Server owns a daemon's lifecycle: it serves Engine.* and Control.* RPC over a
// listener, tracks open connections, and shuts itself down when idle.
type Server struct {
	eng         engine.Engine
	started     time.Time
	idleTimeout time.Duration // 0 = no idle shutdown
	mu          sync.Mutex
	conns       int
	shutdown    chan struct{}
	once        sync.Once
}

func NewServer(eng engine.Engine, started time.Time, idleTimeout time.Duration) *Server {
	return &Server{eng: eng, started: started, idleTimeout: idleTimeout, shutdown: make(chan struct{})}
}

// Shutdown triggers a graceful stop (idempotent): the listener closes and Serve
// returns. Called by the Control.Shutdown RPC, the idle monitor, and the daemon's
// signal handler.
func (s *Server) Shutdown() { s.once.Do(func() { close(s.shutdown) }) }

// Serve registers Engine.* and Control.*, runs the idle monitor, and accepts
// connections (tracking the open count) until the listener closes.
func (s *Server) Serve(l net.Listener) error {
	defer s.Shutdown() // reap the shutdown-watcher and idle monitor on any return
	srv := rpc.NewServer()
	if err := srv.RegisterName("Engine", &EngineService{eng: s.eng}); err != nil {
		return err
	}
	if err := srv.RegisterName("Control", &controlService{s: s}); err != nil {
		return err
	}
	go func() { <-s.shutdown; l.Close() }()
	go s.monitorIdle()
	for {
		conn, err := l.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil // graceful shutdown: the listener was closed
			}
			return err
		}
		s.mu.Lock()
		s.conns++
		s.mu.Unlock()
		go func() {
			defer func() { s.mu.Lock(); s.conns--; s.mu.Unlock() }()
			srv.ServeCodec(jsonrpc.NewServerCodec(conn))
		}()
	}
}

func (s *Server) idle() bool {
	s.mu.Lock()
	c := s.conns
	s.mu.Unlock()
	return c == 0 && len(s.eng.Statuses()) == 0
}

// monitorIdle triggers Shutdown once the daemon has been idle for idleTimeout.
func (s *Server) monitorIdle() {
	if s.idleTimeout <= 0 {
		return
	}
	check := s.idleTimeout / 4
	if check < 10*time.Millisecond {
		check = 10 * time.Millisecond
	}
	if check > time.Minute {
		check = time.Minute
	}
	t := time.NewTicker(check)
	defer t.Stop()
	var idleSince time.Time
	for {
		select {
		case <-s.shutdown:
			return
		case now := <-t.C:
			if s.idle() {
				if idleSince.IsZero() {
					idleSince = now
				} else if now.Sub(idleSince) >= s.idleTimeout {
					s.Shutdown()
					return
				}
			} else {
				idleSince = time.Time{}
			}
		}
	}
}

// controlService exposes daemon lifecycle over RPC (Control.*).
type controlService struct{ s *Server }

func (c *controlService) Shutdown(_ Empty, _ *Empty) error {
	c.s.Shutdown()
	return nil
}

func (c *controlService) Status(_ Empty, r *StatusReply) error {
	ss := c.s.eng.Statuses()
	r.Uptime = time.Since(c.s.started)
	r.Torrents = len(ss)
	for _, st := range ss {
		if st.Done {
			r.Seeding++
		} else {
			r.Downloading++
		}
	}
	r.Pid = os.Getpid()
	return nil
}

// Serve answers Engine.* RPC against eng until l closes (no idle shutdown). Kept
// for callers that don't need lifecycle control (tests, the CLI's fake daemon).
func Serve(l net.Listener, eng engine.Engine) error {
	return NewServer(eng, time.Now(), 0).Serve(l)
}
