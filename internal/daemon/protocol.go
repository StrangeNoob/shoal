// internal/daemon/protocol.go
package daemon

import (
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/StrangeNoob/shoal/internal/engine"
)

// SocketPath is the unix socket the daemon listens on and clients dial. The
// SHOAL_DAEMON_SOCK env var overrides it (useful for tests and running more than
// one daemon).
func SocketPath() string {
	if p := os.Getenv("SHOAL_DAEMON_SOCK"); p != "" {
		return p
	}
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "shoal", "daemon.sock")
	}
	// Fallback when the config dir is unavailable (e.g. $HOME unset). Prefer the
	// user-private runtime dir (systemd guarantees $XDG_RUNTIME_DIR is 0700 and
	// user-owned) over a per-uid subdir of a world-writable temp root. The daemon
	// verifies/creates whichever dir 0700 & user-owned before binding
	// (SecureSocketDir), so another user can't pre-squat the socket path.
	if rd := os.Getenv("XDG_RUNTIME_DIR"); rd != "" {
		return filepath.Join(rd, "shoal", "daemon.sock")
	}
	return filepath.Join(os.TempDir(), "shoal-"+strconv.Itoa(os.Getuid()), "daemon.sock")
}

type AddMagnetArgs struct{ Magnet string }
type AddTorrentURLArgs struct{ URL, Name string }
type RemoveArgs struct {
	InfoHash   string
	DeleteData bool
}
type HashArgs struct{ InfoHash string }
type ReorderArgs struct {
	InfoHash string
	Delta    int
}
type Empty struct{}
type StatusesReply struct{ Statuses []engine.Status }
type DetailReply struct{ Detail engine.Detail }

type StatusReply struct {
	Uptime      time.Duration
	Torrents    int
	Downloading int
	Seeding     int
	Pid         int
}
