# Pause/Resume + Persisted Queue Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Pause/resume individual downloads, and persist all added torrents so they're restored (downloads + seeding + paused state) on the next launch.

**Architecture:** A new `internal/queue` package persists the set of added torrents as `queue.json` (mirroring `internal/history`). The anacrolix engine gains `Pause`/`Resume` (via `DisallowDataDownload`/`AllowDataDownload` + `SetMaxEstablishedConns`), a `Status.Paused` flag, and owns the queue store — writing it on add/remove/pause and re-adding every entry on startup. The UI adds a `p` key on the Downloads pane.

**Tech Stack:** Go 1.24, anacrolix/torrent v1.61, Bubble Tea, stdlib encoding/json.

## Global Constraints

- TDD: failing test first for every behavior.
- No Claude attribution in commit messages. Branch: `feature/pause-resume-queue`.
- `queue.json` lives in the OS config dir; file mode `0o600`, dir mode `0o700` (mirror `internal/history`).
- Pause = `t.DisallowDataDownload()` + `t.SetMaxEstablishedConns(0)`; Resume = `t.AllowDataDownload()` + `t.SetMaxEstablishedConns(maxConns)` where `maxConns` = `Config.MaxPeers` if `>0` else `50`.
- Persist ALL added torrents (downloads + seeding) + paused state. Restore runs synchronously in `NewAnacrolix`, best-effort (a failed `.torrent`-URL re-fetch is skipped, entry kept).
- Unknown infohash to `Pause`/`Resume`/`Remove` → no-op, nil error. Nil store (`QueuePath == ""`) → persistence is a no-op.
- Full gate before a task is done: `go build ./...`, `go vet ./...`, `gofmt -l internal/ cmd/` (empty), `go test -race ./...`.

---

### Task 1: `internal/queue` package

**Files:**
- Create: `internal/queue/queue.go`, `internal/queue/queue_test.go`

**Interfaces:**
- Produces (consumed by Task 3 + Task 3's main wiring):
  - `type Entry struct { InfoHash, Magnet, TorrentURL, Name string; Paused bool }`
  - `func DefaultPath() string`, `func Load() *Store`, `func LoadFrom(path string) *Store`
  - `func (s *Store) Upsert(e Entry)`, `func (s *Store) Remove(infoHash string)`, `func (s *Store) SetPaused(infoHash string, paused bool)`, `func (s *Store) Save() error`
  - `type Store struct { Path string; Entries []Entry }`

- [ ] **Step 1: Write the failing tests.**

Create `internal/queue/queue_test.go`:
```go
package queue

import (
	"os"
	"path/filepath"
	"testing"
)

func tmpStore(t *testing.T) *Store {
	t.Helper()
	return LoadFrom(filepath.Join(t.TempDir(), "queue.json"))
}

func TestUpsertRoundTrip(t *testing.T) {
	s := tmpStore(t)
	s.Upsert(Entry{InfoHash: "aaa", Magnet: "magnet:?a", Name: "A"})
	s.Upsert(Entry{InfoHash: "bbb", TorrentURL: "http://x/b.torrent", Name: "B", Paused: true})

	got := LoadFrom(s.Path)
	if len(got.Entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(got.Entries))
	}
	if got.Entries[0].InfoHash != "aaa" || got.Entries[0].Magnet != "magnet:?a" {
		t.Errorf("entry 0 = %+v", got.Entries[0])
	}
	if got.Entries[1].TorrentURL != "http://x/b.torrent" || !got.Entries[1].Paused {
		t.Errorf("entry 1 = %+v", got.Entries[1])
	}
}

func TestUpsertReplacesByInfoHash(t *testing.T) {
	s := tmpStore(t)
	s.Upsert(Entry{InfoHash: "aaa", Name: "old"})
	s.Upsert(Entry{InfoHash: "aaa", Name: "new"})
	if len(s.Entries) != 1 || s.Entries[0].Name != "new" {
		t.Fatalf("upsert should replace by hash: %+v", s.Entries)
	}
}

func TestRemove(t *testing.T) {
	s := tmpStore(t)
	s.Upsert(Entry{InfoHash: "aaa"})
	s.Upsert(Entry{InfoHash: "bbb"})
	s.Remove("aaa")
	if len(s.Entries) != 1 || s.Entries[0].InfoHash != "bbb" {
		t.Fatalf("remove failed: %+v", s.Entries)
	}
	if len(LoadFrom(s.Path).Entries) != 1 {
		t.Fatal("remove not persisted")
	}
}

func TestSetPaused(t *testing.T) {
	s := tmpStore(t)
	s.Upsert(Entry{InfoHash: "aaa"})
	s.SetPaused("aaa", true)
	if !LoadFrom(s.Path).Entries[0].Paused {
		t.Fatal("SetPaused not persisted")
	}
}

func TestSaveUsesOwnerOnlyPerms(t *testing.T) {
	s := tmpStore(t)
	s.Upsert(Entry{InfoHash: "aaa"})
	fi, err := os.Stat(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %o, want 600", fi.Mode().Perm())
	}
	di, err := os.Stat(filepath.Dir(s.Path))
	if err != nil {
		t.Fatal(err)
	}
	if di.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %o, want 700", di.Mode().Perm())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail.**

Run: `go test ./internal/queue/ -v`
Expected: FAIL — package/identifiers not defined.

- [ ] **Step 3: Implement `internal/queue/queue.go`.**

```go
// Package queue persists the set of torrents the user has added (downloads and
// seeding) as JSON in the OS user-config dir (queue.json), so they can be
// re-added on the next launch. It is a persisted set, not a scheduler.
package queue

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Entry is one persisted torrent. Exactly one of Magnet / TorrentURL is set.
type Entry struct {
	InfoHash   string `json:"info_hash"`
	Magnet     string `json:"magnet,omitempty"`
	TorrentURL string `json:"torrent_url,omitempty"`
	Name       string `json:"name"`
	Paused     bool   `json:"paused"`
}

// Store is the persisted queue. An empty Path disables Save (used by tests
// without a temp file, and when persistence is turned off).
type Store struct {
	Path    string  `json:"-"`
	Entries []Entry `json:"entries"`
}

// DefaultPath is <config dir>/shoal/queue.json ("" if the config dir is unknown).
func DefaultPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "shoal", "queue.json")
}

// Load reads the queue from the default config-dir path.
func Load() *Store { return LoadFrom(DefaultPath()) }

// LoadFrom reads the queue from path; a missing or corrupt file yields an empty
// (but writable) store.
func LoadFrom(path string) *Store {
	s := &Store{Path: path}
	b, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	_ = json.Unmarshal(b, s)
	s.Path = path // Unmarshal can't set the json:"-" field
	return s
}

// Upsert replaces the entry with the same InfoHash, or appends it, then persists.
func (s *Store) Upsert(e Entry) {
	for i := range s.Entries {
		if s.Entries[i].InfoHash == e.InfoHash {
			s.Entries[i] = e
			_ = s.Save()
			return
		}
	}
	s.Entries = append(s.Entries, e)
	_ = s.Save()
}

// Remove drops the entry with infoHash (if present) and persists.
func (s *Store) Remove(infoHash string) {
	kept := make([]Entry, 0, len(s.Entries))
	changed := false
	for _, e := range s.Entries {
		if e.InfoHash == infoHash {
			changed = true
			continue
		}
		kept = append(kept, e)
	}
	if changed {
		s.Entries = kept
		_ = s.Save()
	}
}

// SetPaused updates the paused flag for infoHash (if present) and persists.
func (s *Store) SetPaused(infoHash string, paused bool) {
	for i := range s.Entries {
		if s.Entries[i].InfoHash == infoHash {
			s.Entries[i].Paused = paused
			_ = s.Save()
			return
		}
	}
}

// Save writes the store to Path (creating the dir). No-op when Path is empty.
func (s *Store) Save() error {
	if s.Path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.Path, b, 0o600)
}
```

- [ ] **Step 4: Run tests to verify they pass.**

Run: `go test ./internal/queue/ -v && go vet ./internal/queue/`
Expected: PASS; vet clean.

- [ ] **Step 5: Commit.**

```bash
git add internal/queue/
git commit -m "Add internal/queue package to persist added torrents"
```

---

### Task 2: Engine pause/resume + `Status.Paused`

**Files:**
- Modify: `internal/engine/engine.go` (interface + `Status.Paused`)
- Modify: `internal/engine/anacrolix.go` (`paused` map, `maxConns`, `torrentByHash`, `Pause`/`Resume`, `Statuses`)
- Modify: `internal/ui/model_test.go` (`fakeEngine` gains `Pause`/`Resume` so the widened interface still compiles)
- Test: `internal/engine/anacrolix_test.go`

**Interfaces:**
- Produces (consumed by Task 4): `Engine.Pause(infoHash string) error`, `Engine.Resume(infoHash string) error`, `Status.Paused bool`.
- Note: existing test helpers `newEngine(t)` and `buildTorrentBytes(t)` are in `anacrolix_test.go`; reuse them.

- [ ] **Step 1: Write the failing test.**

Add to `internal/engine/anacrolix_test.go` (imports `net/http`, `net/http/httptest` are already present):
```go
func TestAnacrolixPauseResume(t *testing.T) {
	eng := newEngine(t)
	defer eng.Close()

	data := buildTorrentBytes(t)
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
```

- [ ] **Step 2: Run it to verify it fails.**

Run: `go test ./internal/engine/ -run TestAnacrolixPauseResume -v`
Expected: FAIL — `eng.Pause` / `Status.Paused` undefined.

- [ ] **Step 3: Add the interface methods + `Status.Paused`.**

In `internal/engine/engine.go`, add to the `Status` struct (after `Done bool`):
```go
	Paused         bool
```
In the `Engine` interface, add (after `Remove(...)`):
```go
	// Pause halts the torrent with the given hex infohash (stops downloading
	// and uploading). An unknown hash is a no-op (nil error).
	Pause(infoHash string) error
	// Resume restarts a paused torrent. An unknown hash is a no-op (nil error).
	Resume(infoHash string) error
```

Widening `Engine` breaks the one other implementer, `fakeEngine` in
`internal/ui/model_test.go`, so update it now (keeps `go test ./...` compiling; the
`paused` field is used by Task 4's tests). Add a field to the `fakeEngine` struct:
```go
	paused map[string]bool
```
and the two methods (near its other methods):
```go
func (e *fakeEngine) Pause(infoHash string) error {
	if e.paused == nil {
		e.paused = map[string]bool{}
	}
	e.paused[infoHash] = true
	return nil
}
func (e *fakeEngine) Resume(infoHash string) error {
	if e.paused == nil {
		e.paused = map[string]bool{}
	}
	e.paused[infoHash] = false
	return nil
}
```

- [ ] **Step 4: Implement in `anacrolix.go`.**

Add fields to the `Anacrolix` struct (next to `names`):
```go
	paused   map[metainfo.Hash]bool
	maxConns int
```
In `NewAnacrolix`, after computing the config and before/at struct construction, set `maxConns` and init `paused`. Change the `a := &Anacrolix{...}` literal to include:
```go
		paused:    map[metainfo.Hash]bool{},
		maxConns:  maxConnsFor(c.MaxPeers),
```
Add the helper (top-level):
```go
// maxConnsFor is the per-torrent connection cap to restore on resume: the
// configured value, or anacrolix's default of 50 when unset.
func maxConnsFor(configured int) int {
	if configured > 0 {
		return configured
	}
	return 50
}
```
Add a lookup helper and refactor `Remove` to use it. Add:
```go
// torrentByHash returns the tracked torrent for a hex infohash. Caller holds mu.
func (a *Anacrolix) torrentByHash(hex string) (*torrent.Torrent, metainfo.Hash, bool) {
	for _, t := range a.client.Torrents() {
		if h := t.InfoHash(); h.HexString() == hex {
			return t, h, true
		}
	}
	return nil, metainfo.Hash{}, false
}
```
Replace the manual find loop at the top of `Remove` with:
```go
	a.mu.Lock()
	found, hash, ok := a.torrentByHash(infoHash)
	if !ok {
		a.mu.Unlock()
		return nil // already gone
	}
	delete(a.paused, hash)
```
(keep the rest of `Remove` — `diskName := found.Name()`, `found.Drop()`, `delete(a.names, hash)`, `delete(a.addedAt, hash)`, unlock, and the `removeUnderDir` tail — unchanged.)

Add `Pause`/`Resume`:
```go
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
```
In `Statuses`, add `Paused: a.paused[h],` to the `Status{...}` literal (mu is already held there).

- [ ] **Step 5: Run tests to verify they pass.**

Run: `go test ./internal/engine/ -run TestAnacrolixPauseResume -v && go build ./... && go test ./...`
Expected: PASS (the new engine test, the existing engine suite, and `internal/ui` still compiles thanks to the `fakeEngine` update).

- [ ] **Step 6: Commit.**

```bash
git add internal/engine/ internal/ui/model_test.go
git commit -m "Add engine Pause/Resume and Status.Paused"
```

---

### Task 3: Engine persistence + restore + main wiring

**Files:**
- Modify: `internal/engine/engine.go` (`Config.QueuePath`)
- Modify: `internal/engine/anacrolix.go` (store field, internal add helpers, restore, sync on add/remove/pause/resume)
- Modify: `cmd/shoal/main.go` (pass `QueuePath`)
- Test: `internal/engine/anacrolix_test.go`

**Interfaces:**
- Consumes: `internal/queue` (Task 1); `Pause`/`Resume` (Task 2).
- Produces: `engine.Config.QueuePath string`; self-restoring engine.

- [ ] **Step 1: Write the failing test.**

Add to `internal/engine/anacrolix_test.go` (add imports `path/filepath` and `github.com/StrangeNoob/shoal/internal/queue` if not present):
```go
func TestAnacrolixPersistsAndRestores(t *testing.T) {
	dir := t.TempDir()
	qpath := filepath.Join(dir, "queue.json")
	data := buildTorrentBytes(t)
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
	ss := eng2.Statuses()
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
```

- [ ] **Step 2: Run it to verify it fails.**

Run: `go test ./internal/engine/ -run TestAnacrolixPersistsAndRestores -v`
Expected: FAIL — `Config.QueuePath` undefined / no persistence.

- [ ] **Step 3: Add `Config.QueuePath` and the store field.**

In `internal/engine/engine.go` `Config`, add (after `SeedRatio`):
```go
	QueuePath  string  // where to persist the set of added torrents ("" = disabled)
```
In `anacrolix.go`, add to the struct (next to `paused`):
```go
	store *queue.Store
```
Add the import `"github.com/StrangeNoob/shoal/internal/queue"` to `anacrolix.go`.

- [ ] **Step 4: Wire the store into `NewAnacrolix` + refactor the add path.**

In `NewAnacrolix`, after building `a := &Anacrolix{...}` (and before starting the seed-ratio loop / returning), load and restore:
```go
	if c.QueuePath != "" {
		a.store = queue.LoadFrom(c.QueuePath)
		a.restore()
	}
```
Refactor the two public add methods to persist, via internal helpers. Replace the bodies of `AddTorrentURL` and `AddMagnet`:
```go
func (a *Anacrolix) AddTorrentURL(url, name string) error {
	return a.addTorrentURL(url, name, true)
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
	return a.addMagnet(magnet, true)
}

func (a *Anacrolix) addMagnet(magnet string, persist bool) error {
	t, err := a.client.AddMagnet(magnet)
	if err != nil {
		return err
	}
	a.track(t, "")
	if persist {
		a.persist(t.InfoHash(), queue.Entry{Magnet: magnet})
	}
	return nil
}
```
Add the persist helper + restore (top-level methods):
```go
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
	for _, e := range a.store.Entries {
		var err error
		switch {
		case e.Magnet != "":
			err = a.addMagnet(e.Magnet, false)
		case e.TorrentURL != "":
			err = a.addTorrentURL(e.TorrentURL, e.Name, false)
		default:
			continue
		}
		if err != nil {
			continue // dead URL / bad magnet: skip, keep the entry
		}
		if e.Paused {
			_ = a.Pause(e.InfoHash)
		}
	}
}
```
Update `Remove`, `Pause`, `Resume` to sync the store. In `Remove`, after the existing `delete(a.addedAt, hash)` and before `a.mu.Unlock()`, add:
```go
		if a.store != nil {
			a.store.Remove(infoHash)
		}
```
In `Pause`, after `a.paused[h] = true`, add:
```go
	if a.store != nil {
		a.store.SetPaused(infoHash, true)
	}
```
In `Resume`, after `a.paused[h] = false`, add:
```go
	if a.store != nil {
		a.store.SetPaused(infoHash, false)
	}
```
(`store.Remove`/`SetPaused`/`Upsert` each call `Save`; all these sites already hold `a.mu`, so store access is serialized.)

- [ ] **Step 5: Wire `main`.**

In `cmd/shoal/main.go`, add the import `"github.com/StrangeNoob/shoal/internal/queue"`, and add `QueuePath` to the `engine.Config{...}` literal:
```go
		QueuePath:  queue.DefaultPath(),
```

- [ ] **Step 6: Run tests to verify they pass.**

Run: `go test ./internal/engine/ -run 'TestAnacrolixPersistsAndRestores|TestAnacrolixPauseResume' -v && go build ./... && go test ./internal/engine/`
Expected: PASS; build clean.

- [ ] **Step 7: Commit.**

```bash
git add internal/engine/ cmd/shoal/main.go
git commit -m "Persist added torrents and restore them on startup"
```

---

### Task 4: UI — pause key, paused render, footer hint

**Files:**
- Modify: `internal/ui/model.go` (`p` key handling)
- Modify: `internal/ui/view.go` (paused render + footer hint)
- Test: `internal/ui/model_test.go` (new tests; `fakeEngine` Pause/Resume already added in Task 2)

**Interfaces:**
- Consumes: `Engine.Pause`/`Resume`, `Status.Paused` (Task 2).

- [ ] **Step 1: Write the failing tests.**

`fakeEngine` already has `Pause`/`Resume` and a `paused map[string]bool` field (added in Task 2). Add these tests to `internal/ui/model_test.go`:
```go
func TestPauseKeyPausesSelectedDownload(t *testing.T) {
	fe := &fakeEngine{statuses: []engine.Status{
		{Name: "dl", InfoHash: "aaa", TotalBytes: 100, CompletedBytes: 10},
	}}
	m := ready(New(&fakeSource{}, fe))
	m.section = sectionDownloads
	m.dlCursor = 0

	m2, _ := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	_ = m2
	if !fe.paused["aaa"] {
		t.Fatal("p should pause the selected download")
	}

	// A paused status → p resumes.
	fe.statuses[0].Paused = true
	m3 := ready(New(&fakeSource{}, fe))
	m3.section = sectionDownloads
	m3.dlCursor = 0
	update(m3, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	if fe.paused["aaa"] {
		t.Fatal("p on a paused download should resume it")
	}
}

func TestPausedDownloadRendersPaused(t *testing.T) {
	fe := &fakeEngine{statuses: []engine.Status{
		{Name: "dl", InfoHash: "aaa", TotalBytes: 100, CompletedBytes: 10, Paused: true},
	}}
	m := ready(New(&fakeSource{}, fe))
	m.section = sectionDownloads
	if !strings.Contains(m.View(), "paused") {
		t.Fatalf("a paused download should render 'paused':\n%s", m.View())
	}
}
```
(If `fakeEngine` uses keyed `KeyMsg` differently in existing tests, match that style; the `update` helper is `model_test.go`'s existing `func update(m Model, msg tea.Msg) (Model, tea.Cmd)`.)

- [ ] **Step 2: Run tests to verify they fail.**

Run: `go test ./internal/ui/ -run 'PauseKey|PausedDownload' -v`
Expected: FAIL — the `p` handler doesn't exist yet (`fe.paused` stays empty) and `renderDownloads` doesn't emit "paused".

- [ ] **Step 3: Handle the `p` key.**

In `internal/ui/model.go`, add a case in the key switch next to the `"x"` case:
```go
	case "p":
		if m.section == sectionDownloads {
			ds := m.downloading()
			if len(ds) > 0 && m.dlCursor < len(ds) {
				sel := ds[m.dlCursor]
				if sel.Paused {
					return m, func() tea.Msg { m.eng.Resume(sel.InfoHash); return nil }
				}
				return m, func() tea.Msg { m.eng.Pause(sel.InfoHash); return nil }
			}
		}
		return m, nil
```

- [ ] **Step 4: Render paused + footer hint.**

In `internal/ui/view.go` `renderDownloads`, change the detail line so a paused torrent shows `paused` instead of a speed. Replace:
```go
		detail := fmt.Sprintf("%s / %s  ·  %d peers", formatBytes(s.CompletedBytes), sizeOrDash(s.TotalBytes), s.Peers)
		if sp := m.dlSpeed[s.Name]; sp > 0 {
			detail += fmt.Sprintf("  ·  %s/s", formatBytes(sp))
		}
```
with:
```go
		detail := fmt.Sprintf("%s / %s  ·  %d peers", formatBytes(s.CompletedBytes), sizeOrDash(s.TotalBytes), s.Peers)
		switch {
		case s.Paused:
			detail += "  ·  ⏸ paused"
		case m.dlSpeed[s.Name] > 0:
			detail += fmt.Sprintf("  ·  %s/s", formatBytes(m.dlSpeed[s.Name]))
		}
```
In `renderFooter`, the Downloads case is:
```go
		parts = []string{hint("↑↓", "move"), hint("x", "cancel"), hint("tab", "panes"), hint("?", "help"), hint("q", "quit")}
```
Change it to add the pause hint:
```go
		parts = []string{hint("↑↓", "move"), hint("p", "pause"), hint("x", "cancel"), hint("tab", "panes"), hint("?", "help"), hint("q", "quit")}
```

- [ ] **Step 5: Run tests to verify they pass.**

Run: `go test ./internal/ui/ -run 'PauseKey|PausedDownload' -v && go test ./internal/ui/`
Expected: PASS (new tests + full ui suite).

- [ ] **Step 6: Full checkpoint + commit.**

Run: `go build ./... && go vet ./... && go test ./... -race && gofmt -l internal/ cmd/`
Expected: build/vet clean, all tests `ok`, gofmt clean.
```bash
git add internal/ui/
git commit -m "Add p to pause/resume downloads; show paused state"
```

---

## Self-Review Notes

- **Spec coverage:** `internal/queue` (Task 1); engine `Pause`/`Resume` + `Status.Paused` (Task 2); persistence + self-restore + `QueuePath` + main wiring (Task 3); UI `p` key + paused render + footer hint (Task 4). All spec sections mapped.
- **Type consistency:** `queue.Entry{InfoHash,Magnet,TorrentURL,Name,Paused}`, `queue.LoadFrom`, `Store.Upsert/Remove/SetPaused/Save`, `queue.DefaultPath` (Task 1) used verbatim in Task 3; `Engine.Pause/Resume(string) error` + `Status.Paused` (Task 2) used in Tasks 3 (restore) and 4 (UI + fakeEngine).
- **Green-at-each-task:** Task 2 widens the `Engine` interface AND updates its only two implementers in the same task — `Anacrolix` (`anacrolix.go`) and `fakeEngine` (`internal/ui/model_test.go`) — so `go build ./...` and `go test ./...` both stay green after Task 2. Task 3 adds the store wiring behind `if a.store != nil` guards, so the Task 2 tests (which set no `QueuePath`) keep passing. Task 4 only adds the `p` key, the paused render, and their tests (the `fakeEngine` methods/field already exist).
