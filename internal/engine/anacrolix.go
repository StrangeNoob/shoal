package engine

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
)

// Anacrolix implements Engine on top of anacrolix/torrent — a mature, full
// BitTorrent stack (DHT, magnet/BEP-9 metadata, UDP trackers, web seeds,
// seeding).
type Anacrolix struct {
	client  *torrent.Client
	http    *http.Client
	dataDir string

	seedRatio float64
	done      chan struct{}
	closeOnce sync.Once

	mu       sync.Mutex
	addedAt  map[metainfo.Hash]time.Time
	names    map[metainfo.Hash]string
	paused   map[metainfo.Hash]bool
	maxConns int
}

// maxConnsFor is the per-torrent connection cap to restore on resume: the
// configured value, or anacrolix's default of 50 when unset.
func maxConnsFor(configured int) int {
	if configured > 0 {
		return configured
	}
	return 50
}

// NewAnacrolix starts a torrent client configured from the user's settings.
func NewAnacrolix(c Config) (*Anacrolix, error) {
	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = c.DataDir
	cfg.Seed = c.Seed

	// Verified against anacrolix/torrent v1.61.0: SetListenAddr and
	// EstablishedConnsPerTorrent both exist on ClientConfig.
	if c.ListenPort > 0 {
		cfg.SetListenAddr(fmt.Sprintf(":%d", c.ListenPort))
	}
	if c.MaxPeers > 0 {
		cfg.EstablishedConnsPerTorrent = c.MaxPeers
	}

	client, err := torrent.NewClient(cfg)
	if err != nil {
		return nil, err
	}
	a := &Anacrolix{
		client:    client,
		http:      &http.Client{Timeout: 30 * time.Second},
		dataDir:   c.DataDir,
		seedRatio: c.SeedRatio,
		done:      make(chan struct{}),
		addedAt:   map[metainfo.Hash]time.Time{},
		names:     map[metainfo.Hash]string{},
		paused:    map[metainfo.Hash]bool{},
		maxConns:  maxConnsFor(c.MaxPeers),
	}
	if c.Seed && c.SeedRatio > 0 {
		go a.seedRatioLoop(10 * time.Second)
	}
	return a, nil
}

// seedRatioLoop periodically stops seeding torrents that have hit the configured
// share ratio, until Close signals done. anacrolix has no built-in ratio limiter,
// so we enforce it by dropping a finished torrent's peer connections.
func (a *Anacrolix) seedRatioLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-a.done:
			return
		case <-ticker.C:
			a.enforceSeedRatio()
		}
	}
}

func (a *Anacrolix) enforceSeedRatio() {
	for _, t := range a.client.Torrents() {
		info := t.Info()
		if info == nil {
			continue // metadata not in yet
		}
		stats := t.Stats()
		uploaded := stats.BytesWrittenData.Int64()
		if reachedRatio(uploaded, info.TotalLength(), a.seedRatio) {
			// Drop peers so it stops uploading; idempotent to call repeatedly.
			t.SetMaxEstablishedConns(0)
		}
	}
}

// reachedRatio reports whether a torrent's share ratio (uploaded/total) has met
// or exceeded target. A non-positive target or unknown total means "never stop".
func reachedRatio(uploaded, total int64, target float64) bool {
	if target <= 0 || total <= 0 {
		return false
	}
	return float64(uploaded)/float64(total) >= target
}

func (a *Anacrolix) AddTorrentURL(url, name string) error {
	resp, err := a.http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch .torrent: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return err
	}
	mi, err := metainfo.Load(bytes.NewReader(body))
	if err != nil {
		return err
	}
	t, err := a.client.AddTorrent(mi)
	if err != nil {
		return err
	}
	a.track(t, name)
	return nil
}

func (a *Anacrolix) AddMagnet(magnet string) error {
	t, err := a.client.AddMagnet(magnet)
	if err != nil {
		return err
	}
	a.track(t, "")
	return nil
}

// track records when a torrent was added and, once its metadata arrives, starts
// downloading every file.
func (a *Anacrolix) track(t *torrent.Torrent, name string) {
	h := t.InfoHash()
	a.mu.Lock()
	if _, seen := a.addedAt[h]; !seen {
		a.addedAt[h] = time.Now()
	}
	if name != "" {
		a.names[h] = name
	}
	a.mu.Unlock()

	go func() {
		<-t.GotInfo() // blocks until we have the metadata (instant for a .torrent)
		a.mu.Lock()
		if a.names[h] == "" {
			a.names[h] = t.Name()
		}
		a.mu.Unlock()
		t.DownloadAll()
	}()
}

func (a *Anacrolix) Statuses() []Status {
	a.mu.Lock()
	defer a.mu.Unlock()

	torrents := a.client.Torrents()
	out := make([]Status, 0, len(torrents))
	for _, t := range torrents {
		h := t.InfoHash()
		name := a.names[h]

		var total int64
		if info := t.Info(); info != nil {
			total = info.TotalLength()
			if name == "" {
				name = t.Name()
			}
		}
		if name == "" {
			name = h.HexString()[:12] // still fetching metadata
		}

		completed := t.BytesCompleted()
		stats := t.Stats()
		out = append(out, Status{
			Name:           name,
			InfoHash:       h.HexString(),
			TotalBytes:     total,
			CompletedBytes: completed,
			// BytesWrittenData is the uploaded-payload counter (verified for v1.61.0).
			Uploaded: stats.BytesWrittenData.Int64(),
			Peers:    stats.ActivePeers,
			Done:     total > 0 && completed >= total,
			Paused:   a.paused[h],
			AddedAt:  a.addedAt[h],
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AddedAt.After(out[j].AddedAt) })
	return out
}

// torrentByHash returns the tracked torrent for a hex infohash. Caller holds mu.
func (a *Anacrolix) torrentByHash(hex string) (*torrent.Torrent, metainfo.Hash, bool) {
	for _, t := range a.client.Torrents() {
		if h := t.InfoHash(); h.HexString() == hex {
			return t, h, true
		}
	}
	return nil, metainfo.Hash{}, false
}

func (a *Anacrolix) Remove(infoHash string, deleteData bool) error {
	a.mu.Lock()
	found, hash, ok := a.torrentByHash(infoHash)
	if !ok {
		a.mu.Unlock()
		return nil // already gone
	}
	delete(a.paused, hash)
	diskName := found.Name() // the info name anacrolix wrote files under (attacker-influenced)
	found.Drop()
	delete(a.names, hash)
	delete(a.addedAt, hash)
	a.mu.Unlock()

	if deleteData && diskName != "" {
		return removeUnderDir(a.dataDir, diskName)
	}
	return nil
}

func (a *Anacrolix) Pause(infoHash string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	t, h, ok := a.torrentByHash(infoHash)
	if !ok {
		return nil
	}
	t.DisallowDataDownload()
	t.SetMaxEstablishedConns(0)
	a.paused[h] = true
	return nil
}

func (a *Anacrolix) Resume(infoHash string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	t, h, ok := a.torrentByHash(infoHash)
	if !ok {
		return nil
	}
	t.AllowDataDownload()
	t.SetMaxEstablishedConns(a.maxConns)
	a.paused[h] = false
	return nil
}

// removeUnderDir deletes name within dir, refusing any path that escapes dir or
// targets the dir root. Torrent names are attacker-influenced, so a name like
// "../../x" must never let os.RemoveAll reach outside the download folder.
func removeUnderDir(dir, name string) error {
	base := filepath.Clean(dir)
	target := filepath.Clean(filepath.Join(base, name))
	rel, err := filepath.Rel(base, target)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("refusing to delete %q: escapes data dir", name)
	}
	return os.RemoveAll(target)
}

func (a *Anacrolix) Close() error {
	a.closeOnce.Do(func() { close(a.done) }) // stop the seed-ratio loop; safe if called twice
	a.client.Close()
	return nil
}
