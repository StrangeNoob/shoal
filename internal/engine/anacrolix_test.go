package engine

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	atbencode "github.com/anacrolix/torrent/bencode"
	atmetainfo "github.com/anacrolix/torrent/metainfo"

	"github.com/StrangeNoob/shoal/internal/queue"
)

// buildTorrentBytes builds a real, self-contained .torrent (no trackers) for a
// temp file, entirely offline.
func buildTorrentBytes(t *testing.T, content []byte) []byte {
	t.Helper()
	return buildTorrentBytesNamed(t, "blob.bin", content)
}

// buildTorrentBytesNamed is buildTorrentBytes for a caller-chosen single file
// name (so the resulting torrent's file has a specific extension).
func buildTorrentBytesNamed(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, content, 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	info := atmetainfo.Info{PieceLength: 16384}
	if err := info.BuildFromFilePath(p); err != nil {
		t.Fatalf("BuildFromFilePath: %v", err)
	}
	ib, err := atbencode.Marshal(info)
	if err != nil {
		t.Fatalf("marshal info: %v", err)
	}
	var buf bytes.Buffer
	if err := (&atmetainfo.MetaInfo{InfoBytes: ib}).Write(&buf); err != nil {
		t.Fatalf("write metainfo: %v", err)
	}
	return buf.Bytes()
}

func newEngine(t *testing.T) *Anacrolix {
	t.Helper()
	eng, err := NewAnacrolix(Config{DataDir: t.TempDir(), Seed: true})
	if err != nil {
		t.Skipf("cannot start torrent client in this environment: %v", err)
	}
	t.Cleanup(func() { eng.Close() })
	return eng
}

func TestAnacrolixStartsEmpty(t *testing.T) {
	eng := newEngine(t)
	if got := eng.Statuses(); len(got) != 0 {
		t.Errorf("fresh engine Statuses() = %d entries, want 0", len(got))
	}
}

func TestAnacrolixAddTorrentURLErrors(t *testing.T) {
	eng := newEngine(t)

	notFound := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(notFound.Close)
	if err := eng.AddTorrentURL(notFound.URL, "x"); err == nil {
		t.Error("AddTorrentURL expected error on 404")
	}

	notTorrent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("definitely not a torrent"))
	}))
	t.Cleanup(notTorrent.Close)
	if err := eng.AddTorrentURL(notTorrent.URL, "x"); err == nil {
		t.Error("AddTorrentURL expected error on non-torrent body")
	}
}

func TestEnforceSeedRatioLeavesUnderRatioTorrent(t *testing.T) {
	eng, err := NewAnacrolix(Config{DataDir: t.TempDir(), Seed: true, SeedRatio: 2.0})
	if err != nil {
		t.Skipf("cannot start torrent client in this environment: %v", err)
	}
	t.Cleanup(func() { eng.Close() })

	torrent := buildTorrentBytes(t, bytes.Repeat([]byte("shoal"), 8000))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(torrent)
	}))
	t.Cleanup(srv.Close)
	if err := eng.AddTorrentURL(srv.URL, "ratio-test"); err != nil {
		t.Fatalf("AddTorrentURL: %v", err)
	}

	// Wait for metadata, then run one enforcement pass. Nothing has been uploaded
	// (no peers), so a 2.0 ratio is not met and the torrent must survive untouched.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && (len(eng.Statuses()) == 0 || eng.Statuses()[0].TotalBytes == 0) {
		time.Sleep(50 * time.Millisecond)
	}
	eng.enforceSeedRatio() // must not panic and must not drop the torrent
	if len(eng.Statuses()) != 1 {
		t.Errorf("after enforcement, Statuses() = %d, want 1", len(eng.Statuses()))
	}
}

func TestAnacrolixSeedRatioLoopShutsDown(t *testing.T) {
	// With a ratio set, NewAnacrolix starts the enforcement goroutine; Close must
	// stop it cleanly (no panic, no double-close, returns promptly).
	eng, err := NewAnacrolix(Config{DataDir: t.TempDir(), Seed: true, SeedRatio: 2.0})
	if err != nil {
		t.Skipf("cannot start torrent client in this environment: %v", err)
	}
	if err := eng.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	eng.Close() // second Close must not panic (closeOnce guards the done channel)
}

func TestAnacrolixAddMagnetInvalid(t *testing.T) {
	eng := newEngine(t)
	if err := eng.AddMagnet("not-a-magnet-link"); err == nil {
		t.Error("AddMagnet expected error on invalid magnet")
	}
}

func TestAnacrolixAddTorrentURLTracksStatus(t *testing.T) {
	eng := newEngine(t)
	content := bytes.Repeat([]byte("shoal"), 8000) // 40000 bytes
	torrent := buildTorrentBytes(t, content)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(torrent)
	}))
	t.Cleanup(srv.Close)

	if err := eng.AddTorrentURL(srv.URL, "My Display Name"); err != nil {
		t.Fatalf("AddTorrentURL: %v", err)
	}

	// Metadata is embedded, so the status should resolve quickly. Poll briefly.
	var st Status
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		all := eng.Statuses()
		if len(all) == 1 && all[0].TotalBytes > 0 {
			st = all[0]
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if st.TotalBytes != int64(len(content)) {
		t.Fatalf("TotalBytes = %d, want %d", st.TotalBytes, len(content))
	}
	if st.Name != "My Display Name" {
		t.Errorf("Name = %q, want My Display Name", st.Name)
	}
	if st.Done {
		t.Error("Done = true, want false (no peers, nothing downloaded)")
	}
	if st.Percent() != 0 {
		t.Errorf("Percent() = %v, want 0", st.Percent())
	}
	if st.AddedAt.IsZero() {
		t.Error("AddedAt not set")
	}
}

func TestAnacrolixRemoveDropsTorrent(t *testing.T) {
	eng := newEngine(t)
	torrentBytes := buildTorrentBytes(t, bytes.Repeat([]byte("shoal"), 8000))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(torrentBytes)
	}))
	t.Cleanup(srv.Close)
	if err := eng.AddTorrentURL(srv.URL, "to-remove"); err != nil {
		t.Fatalf("AddTorrentURL: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	var hash string
	for time.Now().Before(deadline) {
		if all := eng.Statuses(); len(all) == 1 && all[0].InfoHash != "" {
			hash = all[0].InfoHash
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if hash == "" {
		t.Fatal("torrent never appeared with an InfoHash")
	}

	if err := eng.Remove(hash, false); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if got := eng.Statuses(); len(got) != 0 {
		t.Fatalf("after Remove, Statuses() = %d, want 0", len(got))
	}
	// removing an unknown hash is a no-op
	if err := eng.Remove("deadbeef", false); err != nil {
		t.Fatalf("Remove(unknown) = %v, want nil", err)
	}
}

func TestAnacrolixPauseResume(t *testing.T) {
	eng := newEngine(t)
	defer eng.Close()

	data := buildTorrentBytes(t, bytes.Repeat([]byte("shoal"), 8000))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(data)
	}))
	defer srv.Close()

	if err := eng.AddTorrentURL(srv.URL, "pause-test"); err != nil {
		t.Fatalf("AddTorrentURL: %v", err)
	}
	ss := eng.Statuses()
	if len(ss) != 1 {
		t.Fatalf("want 1 status, got %d", len(ss))
	}
	h := ss[0].InfoHash
	if ss[0].Paused {
		t.Fatal("a new torrent should not be paused")
	}

	if err := eng.Pause(h); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if !eng.Statuses()[0].Paused {
		t.Fatal("Pause did not set Status.Paused")
	}

	if err := eng.Resume(h); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if eng.Statuses()[0].Paused {
		t.Fatal("Resume did not clear Status.Paused")
	}

	if err := eng.Pause("deadbeef00000000000000000000000000000000"); err != nil {
		t.Fatalf("Pause of unknown hash should be nil, got %v", err)
	}
}

// Close waits for the per-torrent metadata goroutine (so no store writes
// outlive Close), but a magnet whose metadata never arrives must not make
// Close hang — the goroutine bails out on the engine's done signal.
func TestCloseReturnsWithPendingMagnet(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewAnacrolix(Config{DataDir: dir, QueuePath: filepath.Join(dir, "queue.json")})
	if err != nil {
		t.Skipf("cannot start torrent client: %v", err)
	}
	magnet := "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=never-resolves"
	if err := eng.AddMagnet(magnet); err != nil {
		t.Fatalf("AddMagnet: %v", err)
	}
	closed := make(chan struct{})
	go func() {
		eng.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("Close hung waiting for a magnet that never resolves metadata")
	}
}

func TestAnacrolixPersistsAndRestores(t *testing.T) {
	dir := t.TempDir()
	qpath := filepath.Join(dir, "queue.json")
	data := buildTorrentBytes(t, bytes.Repeat([]byte("shoal"), 8000))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(data)
	}))
	defer srv.Close()

	eng1, err := NewAnacrolix(Config{DataDir: dir, QueuePath: qpath})
	if err != nil {
		t.Skipf("cannot start torrent client: %v", err)
	}
	if err := eng1.AddTorrentURL(srv.URL, "persist-test"); err != nil {
		t.Fatalf("AddTorrentURL: %v", err)
	}
	h := eng1.Statuses()[0].InfoHash
	if err := eng1.Pause(h); err != nil {
		t.Fatal(err)
	}
	eng1.Close()

	// queue.json now has one paused entry pointing at the URL.
	st := queue.LoadFrom(qpath)
	if len(st.Entries) != 1 || !st.Entries[0].Paused || st.Entries[0].TorrentURL != srv.URL {
		t.Fatalf("queue not persisted: %+v", st.Entries)
	}

	// A fresh engine on the same QueuePath restores it, still paused.
	eng2, err := NewAnacrolix(Config{DataDir: dir, QueuePath: qpath})
	if err != nil {
		t.Skipf("cannot start torrent client: %v", err)
	}
	defer eng2.Close()
	// URL restore is asynchronous now (so a dead URL can't stall startup), so
	// poll until the torrent is back and paused.
	var ss []Status
	for i := 0; i < 100; i++ {
		ss = eng2.Statuses()
		if len(ss) == 1 && ss[0].Paused {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(ss) != 1 {
		t.Fatalf("restore: want 1 torrent, got %d", len(ss))
	}
	if !ss[0].Paused {
		t.Fatal("restored torrent should be paused")
	}

	// Remove drops it from the store.
	if err := eng2.Remove(ss[0].InfoHash, false); err != nil {
		t.Fatal(err)
	}
	if len(queue.LoadFrom(qpath).Entries) != 0 {
		t.Fatalf("Remove did not drop the queue entry: %+v", queue.LoadFrom(qpath).Entries)
	}
}

func TestRemoveUnderDirContainment(t *testing.T) {
	base := t.TempDir()

	// a sibling dir OUTSIDE base that a traversal name resolves to — must survive
	outside := filepath.Join(filepath.Dir(base), "victim-"+filepath.Base(base))
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(outside) })
	escaping, _ := filepath.Rel(base, outside) // e.g. "../victim-xxxx"
	if err := removeUnderDir(base, escaping); err == nil {
		t.Fatalf("removeUnderDir must refuse escaping name %q", escaping)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("escaping delete removed an outside dir: %v", err)
	}

	// refuse the data-dir root and empty name
	if err := removeUnderDir(base, "."); err == nil {
		t.Fatal("removeUnderDir must refuse deleting the data dir root")
	}
	if err := removeUnderDir(base, ""); err == nil {
		t.Fatal("removeUnderDir must refuse an empty name")
	}

	// a normal name within base is deleted
	inside := filepath.Join(base, "movie")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := removeUnderDir(base, "movie"); err != nil {
		t.Fatalf("removeUnderDir(normal) = %v", err)
	}
	if _, err := os.Stat(inside); !os.IsNotExist(err) {
		t.Fatalf("normal delete did not remove %q", inside)
	}
}

// A partly-downloaded file must keep its verified pieces across a restart.
// Regression test for the anacrolix part-file default, which re-derives piece
// completion from whole-file rename status on every open and wipes per-piece
// progress for any still-incomplete file.
func TestPartialProgressSurvivesRestart(t *testing.T) {
	const pieceLen = 16384
	dir := t.TempDir()
	qpath := filepath.Join(dir, "queue.json")
	content := bytes.Repeat([]byte("shoal"), 8000) // 40000 bytes = 2 whole pieces + a partial
	data := buildTorrentBytes(t, content)          // single-file torrent named "blob.bin"

	// Simulate a partial download: the first two whole pieces present on disk at
	// the final path, the rest missing.
	want := int64(2 * pieceLen)
	if err := os.WriteFile(filepath.Join(dir, "blob.bin"), content[:want], 0o644); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(data)
	}))
	defer srv.Close()

	completedIs := func(eng *Anacrolix, n int64) bool {
		s := eng.Statuses()
		return len(s) == 1 && s[0].CompletedBytes == n
	}
	waitCompleted := func(eng *Anacrolix, n int64) bool {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if completedIs(eng, n) {
				return true
			}
			time.Sleep(20 * time.Millisecond)
		}
		return false
	}

	eng1, err := NewAnacrolix(Config{DataDir: dir, QueuePath: qpath})
	if err != nil {
		t.Skipf("cannot start torrent client: %v", err)
	}
	if err := eng1.AddTorrentURL(srv.URL, "blob"); err != nil {
		t.Fatalf("AddTorrentURL: %v", err)
	}
	if !waitCompleted(eng1, want) { // initial piece check verifies the 2 on-disk pieces
		t.Fatalf("session 1: CompletedBytes = %d, want %d", eng1.Statuses()[0].CompletedBytes, want)
	}
	eng1.Close()

	eng2, err := NewAnacrolix(Config{DataDir: dir, QueuePath: qpath})
	if err != nil {
		t.Skipf("cannot start torrent client: %v", err)
	}
	defer eng2.Close()
	if !waitCompleted(eng2, want) {
		t.Fatalf("after restart CompletedBytes = %d, want %d — progress reset!", eng2.Statuses()[0].CompletedBytes, want)
	}
}

func TestMagnetDisplayName(t *testing.T) {
	cases := map[string]string{
		"magnet:?xt=urn:btih:aabbccddeeff00112233445566778899aabbccdd&dn=Cool.Movie.2024": "Cool.Movie.2024",
		"magnet:?xt=urn:btih:aabbccddeeff00112233445566778899aabbccdd&dn=Cool%20Movie":    "Cool Movie",
		"magnet:?xt=urn:btih:aabbccddeeff00112233445566778899aabbccdd":                    "", // no dn
		"not a magnet": "",
	}
	for magnet, want := range cases {
		if got := magnetDisplayName(magnet); got != want {
			t.Errorf("magnetDisplayName(%q) = %q, want %q", magnet, got, want)
		}
	}
}

// A magnet carries a display name (dn); it must show before metadata is fetched,
// not the infohash prefix.
func TestMagnetShowsDisplayName(t *testing.T) {
	eng := newEngine(t)
	magnet := "magnet:?xt=urn:btih:aabbccddeeff00112233445566778899aabbccdd&dn=Cool.Movie.2024.1080p"
	if err := eng.AddMagnet(magnet); err != nil {
		t.Fatalf("AddMagnet: %v", err)
	}
	ss := eng.Statuses()
	if len(ss) != 1 {
		t.Fatalf("want 1 status, got %d", len(ss))
	}
	if ss[0].Name != "Cool.Movie.2024.1080p" {
		t.Errorf("Name = %q, want the magnet's dn (not the infohash prefix)", ss[0].Name)
	}
}

func TestVerifiedBytes(t *testing.T) {
	cases := []struct {
		complete        int
		pieceLen, total int64
		want            int64
	}{
		{0, 100, 550, 0},
		{2, 100, 550, 200},
		{5, 100, 550, 500},
		{6, 100, 550, 550}, // caps at total (last piece is shorter than pieceLen)
	}
	for _, c := range cases {
		if got := verifiedBytes(c.complete, c.pieceLen, c.total); got != c.want {
			t.Errorf("verifiedBytes(%d, %d, %d) = %d, want %d", c.complete, c.pieceLen, c.total, got, c.want)
		}
	}
}

// A dead/slow .torrent URL in the restore queue must not stall startup: URL
// re-fetches run in the background, so NewAnacrolix returns promptly.
func TestRestoreDoesNotBlockOnSlowURL(t *testing.T) {
	dir := t.TempDir()
	qpath := filepath.Join(dir, "queue.json")

	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block // never responds until the test releases it
	}))
	defer srv.Close()

	q := queue.LoadFrom(qpath)
	q.Upsert(queue.Entry{InfoHash: "slow", TorrentURL: srv.URL, Name: "slow"})

	type res struct {
		eng *Anacrolix
		err error
	}
	done := make(chan res, 1)
	go func() {
		eng, err := NewAnacrolix(Config{DataDir: dir, QueuePath: qpath})
		done <- res{eng, err}
	}()

	select {
	case r := <-done:
		close(block) // release the server so the background fetch can finish
		if r.err != nil {
			t.Skipf("cannot start torrent client: %v", r.err)
		}
		r.eng.Close()
	case <-time.After(5 * time.Second):
		close(block)
		t.Fatal("NewAnacrolix blocked on a slow .torrent-URL restore; URLs should re-fetch in the background")
	}
}

func TestStatusPath(t *testing.T) {
	eng := newEngine(t)
	content := bytes.Repeat([]byte("shoal"), 8000)
	torrent := buildTorrentBytes(t, content) // single-file torrent named "blob.bin"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(torrent)
	}))
	t.Cleanup(srv.Close)
	if err := eng.AddTorrentURL(srv.URL, "blob"); err != nil {
		t.Fatalf("AddTorrentURL: %v", err)
	}
	var st Status
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		all := eng.Statuses()
		if len(all) == 1 && all[0].TotalBytes > 0 {
			st = all[0]
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	want := filepath.Join(eng.dataDir, "blob.bin")
	if st.Path != want {
		t.Errorf("Status.Path = %q, want %q", st.Path, want)
	}
}

func TestStatusSeeding(t *testing.T) {
	data := buildTorrentBytes(t, bytes.Repeat([]byte("shoal"), 8000))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(data)
	}))
	t.Cleanup(srv.Close)

	// Seed enabled: a loaded torrent reports Seeding.
	on := newEngine(t) // Config{Seed: true}
	if err := on.AddTorrentURL(srv.URL, "x"); err != nil {
		t.Fatalf("AddTorrentURL: %v", err)
	}
	waitMeta(t, on)
	if s := on.Statuses()[0]; !s.Seeding {
		t.Error("Seed=true torrent should report Seeding=true")
	}
	if s := on.Statuses()[0]; s.TotalPeers != 0 {
		t.Errorf("no-peer torrent TotalPeers = %d, want 0", s.TotalPeers)
	}

	// Seed disabled: same torrent does not report Seeding.
	off, err := NewAnacrolix(Config{DataDir: t.TempDir(), Seed: false})
	if err != nil {
		t.Skipf("cannot start torrent client: %v", err)
	}
	t.Cleanup(func() { off.Close() })
	if err := off.AddTorrentURL(srv.URL, "x"); err != nil {
		t.Fatalf("AddTorrentURL: %v", err)
	}
	waitMeta(t, off)
	if s := off.Statuses()[0]; s.Seeding {
		t.Error("Seed=false torrent should report Seeding=false")
	}
}

func waitMeta(t *testing.T, eng *Anacrolix) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if all := eng.Statuses(); len(all) == 1 && all[0].TotalBytes > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("metadata never resolved")
}

func TestSetSequentialUnknownHashErrors(t *testing.T) {
	eng := newEngine(t)
	// Same contract as SetFiles/SetFileGlobs: an unknown hash is an error, so
	// `shoal sequential <id> on` can't report success for nothing.
	if err := eng.SetSequential("deadbeef00000000000000000000000000000000", true); err == nil {
		t.Fatal("SetSequential(unknown hash) = nil, want a not-found error")
	}
}

// addSequentialTestTorrent adds a real multi-piece torrent and returns the
// engine, its hex infohash and the live *torrent.Torrent once its files are
// selected (applyFileSelection runs asynchronously on GotInfo).
func addSequentialTestTorrent(t *testing.T) (*Anacrolix, string, *torrent.Torrent) {
	t.Helper()
	dir := t.TempDir()
	// A queue store is what makes applyFileSelection call f.Download() (file
	// priorities, which is what applySequential spans over) — the daemon always
	// runs with one.
	eng, err := NewAnacrolix(Config{DataDir: dir, QueuePath: filepath.Join(dir, "queue.json")})
	if err != nil {
		t.Skipf("cannot start torrent client in this environment: %v", err)
	}
	t.Cleanup(func() { eng.Close() })
	data := buildTorrentBytes(t, bytes.Repeat([]byte("shoal"), 200000)) // ~1 MB => ~62 pieces
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(data)
	}))
	t.Cleanup(srv.Close)
	if err := eng.AddTorrentURL(srv.URL, "seq-pieces"); err != nil {
		t.Fatalf("AddTorrentURL: %v", err)
	}
	waitMeta(t, eng)
	h := eng.Statuses()[0].InfoHash
	tt, _, ok := eng.torrentByHash(h)
	if !ok {
		t.Fatal("torrent vanished after add")
	}
	// Wait for the file to be selected (applyFileSelection runs off GotInfo) and
	// for piece priorities to read back: effective priority is None until
	// storage completion is cached, which happens asynchronously after the add.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		d, err := eng.Detail(h)
		if err == nil && len(d.Files) == 1 && d.Files[0].Selected &&
			maxPiecePriority(tt) >= torrent.PiecePriorityNormal {
			return eng, h, tt
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("file never became selected with readable piece priorities")
	return nil, "", nil
}

// maxPiecePriority is the highest effective piece priority in t. anacrolix's
// effective priority is max(file, piece), so it reads back the bumps
// applySequential made.
func maxPiecePriority(tt *torrent.Torrent) torrent.PiecePriority {
	max := torrent.PiecePriorityNone
	for i := 0; i < tt.NumPieces(); i++ {
		if p := tt.PieceState(i).Priority; p > max {
			max = p
		}
	}
	return max
}

// Sequential mode raises piece-level priorities; turning it off must lower them
// again. Effective priority is max(file, piece), so leftover Now/High pieces
// would keep a window downloading out of order forever.
func TestSetSequentialOffClearsPieceBumps(t *testing.T) {
	eng, h, tt := addSequentialTestTorrent(t)

	if err := eng.SetSequential(h, true); err != nil {
		t.Fatalf("SetSequential(on): %v", err)
	}
	if got := maxPiecePriority(tt); got <= torrent.PiecePriorityNormal {
		t.Fatalf("max piece priority with sequential on = %v, want > Normal", got)
	}
	if err := eng.SetSequential(h, false); err != nil {
		t.Fatalf("SetSequential(off): %v", err)
	}
	if got := maxPiecePriority(tt); got > torrent.PiecePriorityNormal {
		t.Fatalf("max piece priority after sequential off = %v, want <= Normal (piece bumps not cleared)", got)
	}
}

// An apply that was already in flight when the mode was turned off must not
// re-raise piece priorities: nothing would ever clear them again, and Status
// would report Sequential=false while the window kept downloading.
func TestApplySequentialAfterModeOffIsNoOp(t *testing.T) {
	eng, h, tt := addSequentialTestTorrent(t)

	if err := eng.SetSequential(h, true); err != nil {
		t.Fatalf("SetSequential(on): %v", err)
	}
	if err := eng.SetSequential(h, false); err != nil {
		t.Fatalf("SetSequential(off): %v", err)
	}
	eng.applySequential(tt) // a stale in-flight apply landing after the off
	if got := maxPiecePriority(tt); got > torrent.PiecePriorityNormal {
		t.Fatalf("max piece priority after a stale apply = %v, want <= Normal (stale apply re-raised priorities)", got)
	}
}

// Deselecting a file while sequential is on must also drop its piece bumps —
// otherwise the file keeps downloading despite being deselected.
func TestSetFilesDeselectClearsPieceBumps(t *testing.T) {
	eng, h, tt := addSequentialTestTorrent(t)

	if err := eng.SetSequential(h, true); err != nil {
		t.Fatalf("SetSequential(on): %v", err)
	}
	d, err := eng.Detail(h)
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if err := eng.SetFiles(h, []string{d.Files[0].Path}, false); err != nil {
		t.Fatalf("SetFiles(deselect): %v", err)
	}
	if got := maxPiecePriority(tt); got > torrent.PiecePriorityNone {
		t.Fatalf("max piece priority after deselecting the only file = %v, want None", got)
	}
}

// Deselecting a file must drop its pieces' effective priority to None even
// outside sequential mode (#44): track() used to call t.DownloadAll(), which
// raises every piece's priority to Normal regardless of file selection —
// since effective priority is max(file, piece), that floor could outlive a
// deselected file's own None if nothing ever lowered it back down. Files are
// sized as exact multiples of the piece length so no piece is shared between
// them — an unambiguous per-file read of t.PieceState(i).Priority (effective
// priority: max of file, piece, and reader priority, per anacrolix's
// Piece.purePriority).
func TestDeselectDropsPiecePriorityToNone(t *testing.T) {
	pieceLen := 16384
	keep := bytes.Repeat([]byte{'A'}, pieceLen*4)
	skip := bytes.Repeat([]byte{'B'}, pieceLen*4)

	src := t.TempDir()
	root := filepath.Join(src, "Movie")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "keep.mkv"), keep, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "skip.mkv"), skip, 0o644); err != nil {
		t.Fatal(err)
	}
	data := buildTorrentBytesDir(t, root)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(data)
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	eng, err := NewAnacrolix(Config{DataDir: dir, QueuePath: filepath.Join(dir, "queue.json")})
	if err != nil {
		t.Skipf("cannot start torrent client in this environment: %v", err)
	}
	t.Cleanup(func() { eng.Close() })

	if err := eng.AddTorrentURL(srv.URL, "Movie"); err != nil {
		t.Fatalf("AddTorrentURL: %v", err)
	}
	waitMeta(t, eng)
	h := eng.Statuses()[0].InfoHash
	waitAllSelected(t, eng, h) // both files selected before we deselect one

	if err := eng.SetFiles(h, []string{"skip.mkv"}, false); err != nil {
		t.Fatalf("SetFiles(deselect): %v", err)
	}

	tt, _, ok := eng.torrentByHash(h)
	if !ok {
		t.Fatal("torrent vanished")
	}
	var keepFile, skipFile *torrent.File
	for _, f := range tt.Files() {
		switch f.DisplayPath() {
		case "keep.mkv":
			keepFile = f
		case "skip.mkv":
			skipFile = f
		}
	}
	if keepFile == nil || skipFile == nil {
		t.Fatalf("expected files not found: %+v", tt.Files())
	}

	// Effective priority reads back None per-piece until that piece's storage
	// completion is cached (async after add, and per-piece — same gotcha
	// addSequentialTestTorrent waits out), so wait for every one of keep.mkv's
	// pieces individually before asserting on them.
	waitPieceRangeAtLeast(t, tt, keepFile.BeginPieceIndex(), keepFile.EndPieceIndex(), torrent.PiecePriorityNormal)

	for i := skipFile.BeginPieceIndex(); i < skipFile.EndPieceIndex(); i++ {
		if p := tt.PieceState(i).Priority; p != torrent.PiecePriorityNone {
			t.Errorf("deselected file piece %d priority = %v, want None", i, p)
		}
	}
	for i := keepFile.BeginPieceIndex(); i < keepFile.EndPieceIndex(); i++ {
		if p := tt.PieceState(i).Priority; p < torrent.PiecePriorityNormal {
			t.Errorf("selected sibling file piece %d priority = %v, want >= Normal", i, p)
		}
	}
}

// waitPieceRangeAtLeast blocks until every piece in [begin, end) reads back at
// least want, or the deadline passes (in which case the caller's own
// assertions report exactly which pieces are still lagging).
func waitPieceRangeAtLeast(t *testing.T, tt *torrent.Torrent, begin, end int, want torrent.PiecePriority) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		allReady := true
		for i := begin; i < end; i++ {
			if tt.PieceState(i).Priority < want {
				allReady = false
				break
			}
		}
		if allReady {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestSetSequentialTogglePersists(t *testing.T) {
	dir := t.TempDir()
	qpath := filepath.Join(dir, "queue.json")
	data := buildTorrentBytes(t, bytes.Repeat([]byte("shoal"), 8000))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(data)
	}))
	t.Cleanup(srv.Close)

	eng, err := NewAnacrolix(Config{DataDir: dir, QueuePath: qpath})
	if err != nil {
		t.Skipf("cannot start torrent client: %v", err)
	}
	t.Cleanup(func() { eng.Close() })
	if err := eng.AddTorrentURL(srv.URL, "seq-test"); err != nil {
		t.Fatalf("AddTorrentURL: %v", err)
	}
	h := eng.Statuses()[0].InfoHash
	if eng.Statuses()[0].Sequential {
		t.Fatal("a new torrent should not be sequential")
	}

	if err := eng.SetSequential(h, true); err != nil {
		t.Fatalf("SetSequential(on): %v", err)
	}
	if !eng.Statuses()[0].Sequential {
		t.Fatal("SetSequential(true) did not set Status.Sequential")
	}
	if st, _ := queue.LoadFrom(qpath).Get(h); !st.Sequential {
		t.Fatal("SetSequential(true) did not persist to the queue store")
	}

	if err := eng.SetSequential(h, false); err != nil {
		t.Fatalf("SetSequential(off): %v", err)
	}
	if eng.Statuses()[0].Sequential {
		t.Fatal("SetSequential(false) did not clear Status.Sequential")
	}
	if st, _ := queue.LoadFrom(qpath).Get(h); st.Sequential {
		t.Fatal("SetSequential(false) did not persist to the queue store")
	}
}

// FileDetail's HeadBytes/TailDone exist and read zero-value for a freshly added
// (nothing downloaded) torrent; deeper coverage lives in the pure planner tests.
func TestFileDetailHeadTailZeroValue(t *testing.T) {
	eng := newEngine(t)
	data := buildTorrentBytes(t, bytes.Repeat([]byte("shoal"), 8000))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(data)
	}))
	t.Cleanup(srv.Close)
	if err := eng.AddTorrentURL(srv.URL, "detail-test"); err != nil {
		t.Fatalf("AddTorrentURL: %v", err)
	}
	waitMeta(t, eng)
	h := eng.Statuses()[0].InfoHash

	d, err := eng.Detail(h)
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if len(d.Files) != 1 {
		t.Fatalf("want 1 file, got %d", len(d.Files))
	}
	f := d.Files[0]
	if f.HeadBytes != 0 {
		t.Errorf("HeadBytes = %d, want 0 for an untouched torrent", f.HeadBytes)
	}
	if f.TailDone {
		t.Error("TailDone = true, want false for an untouched torrent")
	}
}

func TestExportedRemoveUnderDirRefusesEscape(t *testing.T) {
	base := t.TempDir()
	if err := RemoveUnderDir(base, "../escape"); err == nil {
		t.Fatal("RemoveUnderDir must refuse a name escaping the dir")
	}
	sub := filepath.Join(base, "keep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := RemoveUnderDir(base, "keep"); err != nil {
		t.Fatalf("RemoveUnderDir on an in-dir name: %v", err)
	}
	if _, err := os.Stat(sub); !os.IsNotExist(err) {
		t.Fatal("RemoveUnderDir should have deleted the in-dir path")
	}
}

// A plain add with persistence off (QueuePath: "") must still establish every
// file's priority: Detail reports each file Selected. The queue store only
// supplies the deselect list; when it's absent (or the entry hasn't been
// upserted yet) files must not be left at the zero-value PiecePriorityNone,
// which silently reads as "deselected" everywhere (Detail, subs auto-fetch).
func TestPlainAddSelectsEveryFileWithoutStore(t *testing.T) {
	src := t.TempDir()
	root := filepath.Join(src, "Pack")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.mkv", "b.mkv"} {
		if err := os.WriteFile(filepath.Join(root, name), bytes.Repeat([]byte("x"), 2000), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	data := buildTorrentBytesDir(t, root)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(data)
	}))
	t.Cleanup(srv.Close)

	eng, err := NewAnacrolix(Config{DataDir: t.TempDir()}) // no QueuePath → no store
	if err != nil {
		t.Skipf("cannot start torrent client: %v", err)
	}
	t.Cleanup(func() { eng.Close() })

	if err := eng.AddTorrentURL(srv.URL, "Pack"); err != nil {
		t.Fatalf("AddTorrentURL: %v", err)
	}
	waitMeta(t, eng)

	var det Detail
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		det, err = eng.Detail(eng.Statuses()[0].InfoHash)
		if err == nil && len(det.Files) == 2 && det.Files[0].Selected && det.Files[1].Selected {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("files never became selected: %+v (err %v)", det.Files, err)
}

// waitAllSelected blocks until every file of infoHash reads Selected — i.e.
// the post-GotInfo applyFileSelection goroutine has run — so a test can act on
// a settled selection instead of racing it.
func waitAllSelected(t *testing.T, eng *Anacrolix, infoHash string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		det, err := eng.Detail(infoHash)
		if err == nil && len(det.Files) > 0 {
			all := true
			for _, f := range det.Files {
				all = all && f.Selected
			}
			if all {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("files never became selected")
}

// swapSubsFetch replaces the subsFetch seam with a recorder for the duration
// of the test, so tests never do HTTP.
func swapSubsFetch(t *testing.T, calls chan<- struct{ apiKey, path, lang string }) {
	t.Helper()
	orig := subsFetch
	subsFetch = func(apiKey, videoPath, lang string) (string, error) {
		calls <- struct{ apiKey, path, lang string }{apiKey, videoPath, lang}
		return "", nil
	}
	t.Cleanup(func() { subsFetch = orig })
}

func TestSubsAutoCompletionOffMakesNoCalls(t *testing.T) {
	calls := make(chan struct{ apiKey, path, lang string }, 4)
	swapSubsFetch(t, calls)

	eng, err := NewAnacrolix(Config{DataDir: t.TempDir(), OpenSubsAPIKey: "key", SubsAuto: false})
	if err != nil {
		t.Skipf("cannot start torrent client: %v", err)
	}
	t.Cleanup(func() { eng.Close() })

	eng.checkSubsCompletion()
	select {
	case <-calls:
		t.Fatal("subsFetch called though SubsAuto is off")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestSubsAutoCompletionNoKeyMakesNoCalls(t *testing.T) {
	calls := make(chan struct{ apiKey, path, lang string }, 4)
	swapSubsFetch(t, calls)

	eng, err := NewAnacrolix(Config{DataDir: t.TempDir(), OpenSubsAPIKey: "", SubsAuto: true})
	if err != nil {
		t.Skipf("cannot start torrent client: %v", err)
	}
	t.Cleanup(func() { eng.Close() })

	eng.checkSubsCompletion()
	select {
	case <-calls:
		t.Fatal("subsFetch called though no API key is configured")
	case <-time.After(200 * time.Millisecond):
	}
}

// A completed torrent with one qualifying video file (right extension, at
// least SubsMinVideoBytes) triggers exactly one subsFetch call with the file's
// absolute on-disk path and the configured language. A second completion
// signal for the same torrent must not fetch again.
func TestSubsAutoCompletionFetchesQualifyingFileOnce(t *testing.T) {
	// Shrink the size threshold instead of hashing a real 100 MiB fixture —
	// keeps the test fast and avoids CPU contention with the rest of the
	// suite under -race.
	origMin := SubsMinVideoBytes
	SubsMinVideoBytes = 1024
	t.Cleanup(func() { SubsMinVideoBytes = origMin })

	dir := t.TempDir()
	content := bytes.Repeat([]byte("shoal"), 400) // 2000 bytes, above the shrunk threshold
	data := buildTorrentBytesNamed(t, "movie.mkv", content)

	// Pre-write the full file at its final on-disk location so the torrent
	// verifies complete immediately without needing peers (same trick as
	// TestPartialProgressSurvivesRestart).
	if err := os.WriteFile(filepath.Join(dir, "movie.mkv"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(data)
	}))
	t.Cleanup(srv.Close)

	calls := make(chan struct{ apiKey, path, lang string }, 4)
	swapSubsFetch(t, calls)

	eng, err := NewAnacrolix(Config{DataDir: dir, OpenSubsAPIKey: "test-key", SubsLang: "fr", SubsAuto: true})
	if err != nil {
		t.Skipf("cannot start torrent client: %v", err)
	}
	t.Cleanup(func() { eng.Close() })

	if err := eng.AddTorrentURL(srv.URL, "movie"); err != nil {
		t.Fatalf("AddTorrentURL: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if s := eng.Statuses(); len(s) == 1 && s[0].Done {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	s := eng.Statuses()
	if len(s) != 1 || !s[0].Done {
		t.Fatalf("torrent never completed: %+v", s)
	}

	// fetchSubsForFiles skips deselected (never-downloaded) files by checking
	// File.Priority(), the same convention Detail() uses for Selected — so
	// wait for the add path's own applyFileSelection to have selected the file.
	waitAllSelected(t, eng, s[0].InfoHash)

	eng.checkSubsCompletion()

	var got struct{ apiKey, path, lang string }
	select {
	case got = <-calls:
	case <-time.After(3 * time.Second):
		t.Fatal("subsFetch was never called")
	}
	if got.apiKey != "test-key" {
		t.Errorf("apiKey = %q, want test-key", got.apiKey)
	}
	if got.lang != "fr" {
		t.Errorf("lang = %q, want fr", got.lang)
	}
	wantPath := filepath.Join(dir, "movie.mkv")
	if got.path != wantPath {
		t.Errorf("path = %q, want %q", got.path, wantPath)
	}

	// A second completion signal for the same torrent must not fetch again.
	eng.checkSubsCompletion()
	select {
	case c := <-calls:
		t.Fatalf("unexpected second subsFetch call: %+v", c)
	case <-time.After(300 * time.Millisecond):
	}
}

// A completed torrent whose subtitle file is already on disk must not be
// fetched again: subsFetched is memory-only, so after a daemon restart every
// restored complete torrent looks "newly done" — refetching would overwrite
// the .srt and burn the user's OpenSubtitles quota. The written file is the record.
func TestSubsAutoSkipsFileWithExistingSrt(t *testing.T) {
	origMin := SubsMinVideoBytes
	SubsMinVideoBytes = 1024
	t.Cleanup(func() { SubsMinVideoBytes = origMin })

	dir := t.TempDir()
	content := bytes.Repeat([]byte("shoal"), 400)
	data := buildTorrentBytesNamed(t, "movie.mkv", content)
	if err := os.WriteFile(filepath.Join(dir, "movie.mkv"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	// The subtitle from a previous run.
	if err := os.WriteFile(filepath.Join(dir, "movie.en.srt"), []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(data)
	}))
	t.Cleanup(srv.Close)

	calls := make(chan struct{ apiKey, path, lang string }, 4)
	swapSubsFetch(t, calls)

	eng, err := NewAnacrolix(Config{DataDir: dir, OpenSubsAPIKey: "test-key", SubsLang: "en", SubsAuto: true})
	if err != nil {
		t.Skipf("cannot start torrent client: %v", err)
	}
	t.Cleanup(func() { eng.Close() })

	if err := eng.AddTorrentURL(srv.URL, "movie"); err != nil {
		t.Fatalf("AddTorrentURL: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if s := eng.Statuses(); len(s) == 1 && s[0].Done {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	s := eng.Statuses()
	if len(s) != 1 || !s[0].Done {
		t.Fatalf("torrent never completed: %+v", s)
	}
	waitAllSelected(t, eng, s[0].InfoHash)

	eng.checkSubsCompletion()
	select {
	case c := <-calls:
		t.Fatalf("subsFetch called though the .srt already exists: %+v", c)
	case <-time.After(500 * time.Millisecond):
	}
	if b, err := os.ReadFile(filepath.Join(dir, "movie.en.srt")); err != nil || string(b) != "1\n" {
		t.Fatalf("existing .srt = %q (err %v), want it left untouched", b, err)
	}
}

// buildTorrentBytesDir builds a real, self-contained multi-file .torrent from
// an existing directory (root's basename becomes the torrent's Info.Name),
// the multi-file counterpart to buildTorrentBytesNamed.
func buildTorrentBytesDir(t *testing.T, root string) []byte {
	t.Helper()
	info := atmetainfo.Info{PieceLength: 16384}
	if err := info.BuildFromFilePath(root); err != nil {
		t.Fatalf("BuildFromFilePath: %v", err)
	}
	ib, err := atbencode.Marshal(info)
	if err != nil {
		t.Fatalf("marshal info: %v", err)
	}
	var buf bytes.Buffer
	if err := (&atmetainfo.MetaInfo{InfoBytes: ib}).Write(&buf); err != nil {
		t.Fatalf("write metainfo: %v", err)
	}
	return buf.Bytes()
}

// A completed multi-file torrent with one deselected qualifying video file
// must not fetch subs for it — it was never downloaded, so a fetch would
// write an orphaned .srt next to nothing.
func TestSubsAutoCompletionSkipsDeselectedFile(t *testing.T) {
	origMin := SubsMinVideoBytes
	SubsMinVideoBytes = 1024
	t.Cleanup(func() { SubsMinVideoBytes = origMin })

	keep := bytes.Repeat([]byte("shoal"), 400) // 2000 bytes, above the shrunk threshold
	skip := bytes.Repeat([]byte("nope!"), 400)

	src := t.TempDir()
	root := filepath.Join(src, "My Show")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "keep.mkv"), keep, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "skip.mkv"), skip, 0o644); err != nil {
		t.Fatal(err)
	}
	data := buildTorrentBytesDir(t, root)

	// Pre-write the files at their final on-disk location so the torrent
	// verifies complete immediately without needing peers (same trick as
	// TestSubsAutoCompletionFetchesQualifyingFileOnce).
	dataDir := t.TempDir()
	finalDir := filepath.Join(dataDir, "My Show")
	if err := os.MkdirAll(finalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(finalDir, "keep.mkv"), keep, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(finalDir, "skip.mkv"), skip, 0o644); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(data)
	}))
	t.Cleanup(srv.Close)

	calls := make(chan struct{ apiKey, path, lang string }, 4)
	swapSubsFetch(t, calls)

	eng, err := NewAnacrolix(Config{DataDir: dataDir, OpenSubsAPIKey: "test-key", SubsLang: "en", SubsAuto: true})
	if err != nil {
		t.Skipf("cannot start torrent client: %v", err)
	}
	t.Cleanup(func() { eng.Close() })

	if err := eng.AddTorrentURL(srv.URL, "My Show"); err != nil {
		t.Fatalf("AddTorrentURL: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if s := eng.Statuses(); len(s) == 1 && s[0].Done {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	statuses := eng.Statuses()
	if len(statuses) != 1 || !statuses[0].Done {
		t.Fatalf("torrent never completed: %+v", statuses)
	}

	// The add path selects every file; deselect one on top of that (as a user
	// would), after waiting so the deselect can't be clobbered by it.
	waitAllSelected(t, eng, statuses[0].InfoHash)
	if err := eng.SetFiles(statuses[0].InfoHash, []string{"skip.mkv"}, false); err != nil {
		t.Fatalf("SetFiles(deselect skip.mkv): %v", err)
	}

	eng.checkSubsCompletion()

	var got struct{ apiKey, path, lang string }
	select {
	case got = <-calls:
	case <-time.After(3 * time.Second):
		t.Fatal("subsFetch was never called for the selected file")
	}
	wantPath := filepath.Join(finalDir, "keep.mkv")
	if got.path != wantPath {
		t.Errorf("path = %q, want %q", got.path, wantPath)
	}

	select {
	case c := <-calls:
		t.Fatalf("subsFetch should not be called for the deselected file: %+v", c)
	case <-time.After(300 * time.Millisecond):
	}
}
