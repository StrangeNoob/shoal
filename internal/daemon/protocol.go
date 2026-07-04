// internal/daemon/protocol.go
package daemon

import (
	"os"
	"path/filepath"

	"github.com/StrangeNoob/shoal/internal/engine"
)

// SocketPath is the unix socket the daemon listens on and clients dial. The
// SHOAL_DAEMON_SOCK env var overrides it (useful for tests and running more than
// one daemon).
func SocketPath() string {
	if p := os.Getenv("SHOAL_DAEMON_SOCK"); p != "" {
		return p
	}
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		return filepath.Join(".", "shoal-daemon.sock")
	}
	return filepath.Join(dir, "shoal", "daemon.sock")
}

type AddMagnetArgs struct{ Magnet string }
type AddTorrentURLArgs struct{ URL, Name string }
type RemoveArgs struct {
	InfoHash   string
	DeleteData bool
}
type HashArgs struct{ InfoHash string }
type Empty struct{}
type StatusesReply struct{ Statuses []engine.Status }
