package engine

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	atbencode "github.com/anacrolix/torrent/bencode"
	atmetainfo "github.com/anacrolix/torrent/metainfo"

	"github.com/StrangeNoob/shoal/internal/queue"
)

// buildTorrentBytes builds a real, self-contained .torrent (no trackers) for a
// temp file, entirely offline.
func buildTorrentBytes(t *testing.T, content []byte) []byte {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "blob.bin")
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
