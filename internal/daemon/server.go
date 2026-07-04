// internal/daemon/server.go
package daemon

import (
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"

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

// Serve answers Engine.* RPC calls against eng for every connection accepted on
// l, until l is closed. The wrapped engine is already concurrency-safe.
func Serve(l net.Listener, eng engine.Engine) error {
	srv := rpc.NewServer()
	if err := srv.RegisterName("Engine", &EngineService{eng: eng}); err != nil {
		return err
	}
	for {
		conn, err := l.Accept()
		if err != nil {
			return err // listener closed
		}
		go srv.ServeCodec(jsonrpc.NewServerCodec(conn))
	}
}
