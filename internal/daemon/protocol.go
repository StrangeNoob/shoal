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
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		// Fallback when the config dir is unavailable (e.g. $HOME unset). Namespace
		// by uid in a subdir rather than dropping a predictable name in a possibly
		// world-writable temp root (/tmp is 1777 on Linux) — the daemon creates that
		// subdir 0700 (runDaemon), so another user can't squat the socket path.
		// ponytail: a pre-existing attacker-owned /tmp/shoal-<uid> would defeat this;
		// full hardening (ownership check) belongs in Phase 4 if the fallback ever matters.
		dir = filepath.Join(os.TempDir(), "shoal-"+strconv.Itoa(os.Getuid()))
		return filepath.Join(dir, "daemon.sock")
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

type StatusReply struct {
	Uptime      time.Duration
	Torrents    int
	Downloading int
	Seeding     int
	Pid         int
}
