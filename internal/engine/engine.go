// Package engine is the download backend behind the UI. The TUI depends only on
// the Engine interface and the Status snapshot, so the concrete backend (here,
// anacrolix/torrent) can be swapped without touching the interface.
package engine

import "time"

// Status is a point-in-time snapshot of one torrent, shaped for display.
type Status struct {
	Name       string
	InfoHash   string // lowercase hex infohash; known immediately (from the .torrent/magnet)
	TotalBytes int64
	// CompletedBytes is fully downloaded, hash-verified bytes (whole pieces) —
	// the amount that actually survives a restart. Partial in-piece data is not
	// counted because BitTorrent/anacrolix does not persist it.
	CompletedBytes int64
	// Downloaded is the live count of useful payload bytes received this session
	// (monotonic); used only to sample download speed, which stays smooth even
	// though CompletedBytes advances a whole piece at a time.
	Downloaded int64
	// Uploaded is total bytes shared with peers; used by the Seeding pane to
	// show an upload total and a ratio. Zero when unknown.
	Uploaded int64
	Peers    int
	// TotalPeers is all peers discovered via announce (trackers/DHT), including
	// those not currently connected — nonzero proves announce is working even
	// when Peers (active downloaders) is 0.
	TotalPeers int
	Done       bool
	Paused     bool
	// Queued reports that the scheduler is holding this (incomplete, un-paused)
	// torrent because the max-concurrent-downloads limit is reached. It resumes
	// automatically when a slot frees.
	Queued bool
	// Seeding reports whether the client is actively seeding this torrent
	// (Seed enabled, upload allowed, not paused) — independent of peer count.
	Seeding bool
	// Path is the torrent's top-level on-disk location (<data dir>/<name>), or
	// "" before metadata is known. Used by the UI to open the download folder.
	Path    string
	AddedAt time.Time
}

// Percent is download progress in the range [0, 1].
func (s Status) Percent() float64 {
	if s.TotalBytes <= 0 {
		return 0
	}
	p := float64(s.CompletedBytes) / float64(s.TotalBytes)
	if p > 1 {
		p = 1
	}
	return p
}

// Ratio is uploaded / total (share ratio). Zero when total is unknown.
func (s Status) Ratio() float64 {
	if s.TotalBytes <= 0 {
		return 0
	}
	return float64(s.Uploaded) / float64(s.TotalBytes)
}

// FileDetail is one file within a torrent, with its download progress.
type FileDetail struct {
	Path      string
	Length    int64
	Completed int64
}

// Detail is the on-demand extra information for one torrent (the Downloads
// details screen): its files' progress and its tracker list. Fetched separately
// from Status so the per-tick Statuses() snapshot stays cheap.
type Detail struct {
	Files    []FileDetail
	Trackers []string
}

// Config tunes the engine at construction. main.go fills this from the user's
// persisted config (internal/config); the UI never touches it directly.
type Config struct {
	DataDir    string  // where downloaded files land
	ListenPort int     // BitTorrent listen port (0 = library default, <0 = OS-assigned ephemeral)
	MaxPeers   int     // max established connections per torrent (0 = default)
	Seed       bool    // keep seeding finished torrents
	SeedRatio  float64 // stop seeding a torrent once uploaded/size reaches this (0 = seed forever)
	QueuePath  string  // where to persist the set of added torrents ("" = disabled)

	// Rate limits in bytes/sec; 0 = unlimited (leaves anacrolix's defaults).
	DownloadRate int
	UploadRate   int

	// MaxActive caps concurrently-downloading torrents; extras are queued and
	// promoted (FIFO by add time) as slots free. 0 = unlimited.
	MaxActive int
}

// Engine adds torrents and reports their live status.
type Engine interface {
	// AddTorrentURL fetches a .torrent at url and starts downloading it. name
	// is a display hint used until the real torrent name is known.
	AddTorrentURL(url, name string) error
	// AddMagnet starts a download from a magnet link.
	AddMagnet(magnet string) error
	// Statuses returns a snapshot of every torrent, newest first.
	Statuses() []Status
	// Remove stops the torrent with the given hex infohash and forgets it. When
	// deleteData is true, its downloaded file/dir under the data dir is also
	// removed. An unknown hash is a no-op (nil error).
	Remove(infoHash string, deleteData bool) error
	// Pause halts the torrent with the given hex infohash (stops downloading
	// and uploading). An unknown hash is a no-op (nil error).
	Pause(infoHash string) error
	// Resume restarts a paused torrent. An unknown hash is a no-op (nil error).
	Resume(infoHash string) error
	// Close tears the engine down.
	Close() error
}
