package engine

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	g "github.com/anacrolix/generics"
	alog "github.com/anacrolix/log"
	"golang.org/x/time/rate"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"

	"github.com/StrangeNoob/shoal/internal/queue"
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
	wg        sync.WaitGroup // tracks the background URL-restore goroutine

	mu        sync.Mutex
	addedAt   map[metainfo.Hash]time.Time
	names     map[metainfo.Hash]string
	paused    map[metainfo.Hash]bool
	queued    map[metainfo.Hash]bool // held by the scheduler (max-concurrent)
	maxConns  int
	maxActive int
	store     *queue.Store
}

// rateLimiter builds a token-bucket limiter for bytesPerSec (> 0). The burst is
// at least 256 KiB so a single large piece request still fits and throughput
// isn't throttled below the target.
func rateLimiter(bytesPerSec int) *rate.Limiter {
	burst := max(bytesPerSec, 1<<18)
	return rate.NewLimiter(rate.Limit(bytesPerSec), burst)
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

	// anacrolix logs to stderr by default, which scribbles over the fullscreen
	// TUI (Bubble Tea owns the alternate screen). Silence every log sink: the
	// analog client logger, the client's slog logger, and the file-storage logger.
	discard := slog.New(slog.DiscardHandler)
	cfg.Logger = alog.NewLogger().WithFilterLevel(alog.Disabled)
	cfg.Slogger = discard

	// Disable part files. anacrolix's default part-file storage re-derives piece
	// completion from whether each file is fully renamed to its final name on
	// every open, which wipes per-piece progress for any still-incomplete file on
	// restart — a paused, partly-downloaded torrent would resume from 0. With part
	// files off, the persistent piece-completion DB stays authoritative and
	// piece-level progress survives a restart.
	cfg.DefaultStorage = storage.NewFileOpts(storage.NewFileClientOpts{
		ClientBaseDir: c.DataDir,
		UsePartFiles:  g.Some(false),
		Logger:        discard,
	})

	// Verified against anacrolix/torrent v1.61.0: SetListenAddr and
	// EstablishedConnsPerTorrent both exist on ClientConfig.
	if c.ListenPort > 0 {
		cfg.SetListenAddr(fmt.Sprintf(":%d", c.ListenPort))
	} else if c.ListenPort < 0 {
		// A negative port requests an OS-assigned ephemeral port, so background
		// CLI download workers never collide with the TUI (6881) or each other.
		cfg.SetListenAddr(":0")
	}
	if c.MaxPeers > 0 {
		cfg.EstablishedConnsPerTorrent = c.MaxPeers
	}
	if c.DownloadRate > 0 {
		cfg.DownloadRateLimiter = rateLimiter(c.DownloadRate)
	}
	if c.UploadRate > 0 {
		cfg.UploadRateLimiter = rateLimiter(c.UploadRate)
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
		queued:    map[metainfo.Hash]bool{},
		maxConns:  maxConnsFor(c.MaxPeers),
		maxActive: c.MaxActive,
	}
	if c.QueuePath != "" {
		a.store = queue.LoadFrom(c.QueuePath)
		a.restore()
	}
	if c.Seed && c.SeedRatio > 0 {
		go a.seedRatioLoop(10 * time.Second)
	}
	if c.MaxActive > 0 {
		go a.queueLoop(3 * time.Second)
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

// queueLoop periodically enforces the max-concurrent-downloads limit until Close
// signals done.
func (a *Anacrolix) queueLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-a.done:
			return
		case <-ticker.C:
			a.reconcileQueue()
		}
	}
}

// reconcileQueue holds/releases torrents so at most maxActive download at once
// (oldest first). The decision is made by the pure planQueue; this only gathers
// state and applies the result under the lock.
func (a *Anacrolix) reconcileQueue() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.maxActive <= 0 {
		return
	}
	var items []queueItem
	for _, t := range a.client.Torrents() {
		h := t.InfoHash()
		items = append(items, queueItem{
			Hash:       h,
			AddedAt:    a.addedAt[h],
			Done:       t.Complete().Bool(),
			UserPaused: a.paused[h],
			Queued:     a.queued[h],
		})
	}
	release, hold := planQueue(items, a.maxActive)
	for _, h := range hold {
		if t, ok := a.client.Torrent(h); ok {
			t.DisallowDataDownload()
			t.SetMaxEstablishedConns(0)
			a.queued[h] = true
		}
	}
	for _, h := range release {
		// Releasing clears the scheduler hold. Never resume a user-paused torrent
		// — that would undo an explicit Pause; just drop the queued bookkeeping.
		if !a.paused[h] {
			if t, ok := a.client.Torrent(h); ok {
				t.AllowDataDownload()
				t.SetMaxEstablishedConns(a.maxConns)
			}
		}
		delete(a.queued, h)
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
	err := a.addTorrentURL(url, name, true)
	a.reconcileQueue() // queue the new torrent immediately if over the limit
	return err
}

func (a *Anacrolix) addTorrentURL(url, name string, persist bool) error {
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
	if persist {
		a.persist(t.InfoHash(), queue.Entry{TorrentURL: url, Name: name})
	}
	return nil
}

func (a *Anacrolix) AddMagnet(magnet string) error {
	err := a.addMagnet(magnet, "", true)
	a.reconcileQueue() // queue the new torrent immediately if over the limit
	return err
}

func (a *Anacrolix) addMagnet(magnet, name string, persist bool) error {
	t, err := a.client.AddMagnet(magnet)
	if err != nil {
		return err
	}
	if name == "" {
		name = magnetDisplayName(magnet) // show the dn until metadata arrives
	}
	a.track(t, name)
	if persist {
		a.persist(t.InfoHash(), queue.Entry{Magnet: magnet})
	}
	return nil
}

// magnetDisplayName returns a magnet URI's "dn" (display name), or "" if absent.
func magnetDisplayName(magnet string) string {
	u, err := url.Parse(magnet)
	if err != nil {
		return ""
	}
	return u.Query().Get("dn")
}

// persist records an added torrent in the queue store (no-op when disabled).
// Name falls back to the tracked name so the entry is human-readable.
func (a *Anacrolix) persist(h metainfo.Hash, e queue.Entry) {
	if a.store == nil {
		return
	}
	e.InfoHash = h.HexString()
	if e.Name == "" {
		a.mu.Lock()
		e.Name = a.names[h]
		a.mu.Unlock()
	}
	a.mu.Lock()
	a.store.Upsert(e)
	a.mu.Unlock()
}

// restore re-adds every persisted torrent on startup (best-effort) and applies
// paused state. A failed .torrent-URL re-fetch is skipped, leaving the entry.
func (a *Anacrolix) restore() {
	var urls []queue.Entry
	for _, e := range a.store.Entries {
		switch {
		case e.Magnet != "":
			// Magnets re-add instantly (no network), so do them synchronously.
			if err := a.addMagnet(e.Magnet, e.Name, false); err == nil && e.Paused {
				_ = a.Pause(e.InfoHash)
			}
		case e.TorrentURL != "":
			urls = append(urls, e) // may be slow/dead: fetch off the startup path
		}
	}
	if len(urls) > 0 {
		a.wg.Add(1)
		go a.restoreURLs(urls)
	}
}

// restoreURLs re-adds .torrent-URL entries in the background so a slow or dead
// URL never stalls startup. Close waits on a.wg before closing the client, so
// no add ever races a torn-down client.
func (a *Anacrolix) restoreURLs(entries []queue.Entry) {
	defer a.wg.Done()
	for _, e := range entries {
		select {
		case <-a.done:
			return // shutting down
		default:
		}
		if err := a.addTorrentURL(e.TorrentURL, e.Name, false); err == nil && e.Paused {
			_ = a.Pause(e.InfoHash)
		}
	}
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
		// Persist the resolved display name so a restored torrent shows it even
		// while paused with no peers (when metadata can't be re-fetched).
		if a.store != nil {
			a.store.SetName(h.HexString(), a.names[h])
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

		var total, pieceLen int64
		var diskPath string
		if info := t.Info(); info != nil {
			total = info.TotalLength()
			pieceLen = info.PieceLength
			diskPath = filepath.Join(a.dataDir, t.Name())
			if name == "" {
				name = t.Name()
			}
		}
		if name == "" {
			name = h.HexString()[:12] // still fetching metadata
		}

		// Count only whole, hash-verified pieces — that's what persists across a
		// restart. t.BytesCompleted() also includes in-memory partial-piece data
		// that is lost on exit, which made progress appear to drop on resume.
		var completePieces int
		for _, r := range t.PieceStateRuns() {
			if r.Complete {
				completePieces += r.Length
			}
		}
		verified := verifiedBytes(completePieces, pieceLen, total)
		stats := t.Stats()
		out = append(out, Status{
			Name:           name,
			InfoHash:       h.HexString(),
			TotalBytes:     total,
			CompletedBytes: verified,
			// BytesReadUsefulData is the live payload-received counter — smooth,
			// so download speed stays fluid even though CompletedBytes steps up a
			// whole piece at a time. BytesWrittenData is the uploaded counter.
			Downloaded: stats.BytesReadUsefulData.Int64(),
			Uploaded:   stats.BytesWrittenData.Int64(),
			Peers:      stats.ActivePeers,
			TotalPeers: stats.TotalPeers,
			Done:       total > 0 && verified >= total,
			Paused:     a.paused[h],
			Queued:     a.queued[h],
			Seeding:    !a.paused[h] && t.Seeding(),
			Path:       diskPath,
			AddedAt:    a.addedAt[h],
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AddedAt.After(out[j].AddedAt) })
	return out
}

// verifiedBytes is the number of fully downloaded, hash-verified bytes given a
// count of complete pieces — the amount that survives a restart. It caps at
// total because the final piece is usually shorter than pieceLen.
func verifiedBytes(completePieces int, pieceLen, total int64) int64 {
	b := int64(completePieces) * pieceLen
	if total > 0 && b > total {
		b = total
	}
	return b
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
	if a.store != nil {
		a.store.Remove(infoHash)
	}
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
	if a.store != nil {
		a.store.SetPaused(infoHash, true)
	}
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
	if a.store != nil {
		a.store.SetPaused(infoHash, false)
	}
	return nil
}

// RemoveUnderDir deletes name within dir, refusing any path that escapes dir.
// Exported so the CLI/TUI can safely delete a finished download's files when the
// torrent is no longer in the daemon.
func RemoveUnderDir(dir, name string) error { return removeUnderDir(dir, name) }

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
	a.closeOnce.Do(func() { close(a.done) }) // stop the seed-ratio loop + signal restore; safe if called twice
	a.wg.Wait()                              // let the background URL-restore finish before tearing down the client
	a.client.Close()
	return nil
}
