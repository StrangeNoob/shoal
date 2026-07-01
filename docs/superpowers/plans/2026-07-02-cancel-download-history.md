# Cancel Downloads + Download History Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the user cancel an in-progress download (choosing keep-or-delete partial files at confirm time) and keep a persistent history of completed downloads shown in the Seeding pane.

**Architecture:** Add an infohash + `Remove` to the engine interface; a small `internal/history` JSON store; a Downloads-pane selection cursor and modal cancel-confirm in the model; completion detection reusing the tick's prev/next snapshot (same pattern as the speed sampler); and Seeding-pane rendering of the persisted history.

**Tech Stack:** Go 1.26, Bubble Tea v1.3.10, Lipgloss v1.1.0, anacrolix/torrent v1.61.0, stdlib `encoding/json`.

## Global Constraints

- TDD: write the failing test first for every behavior.
- No Claude attribution in commits (no `Co-Authored-By` / "Generated with" trailer).
- Git repo on branch `feature/cancel-and-history`. Each task's gate is its `go test` passing; commit when green.
- Reuse existing helpers/styles: `truncate`, `max`, `min`, `sizeOrDash`, `relTime`, `formatBytes`, glyphs `glyphDown`/`glyphDone`/`glyphCursor`/`glyphMore`, styles `st.Row`/`st.RowSel`/`st.Accent`/`st.Meta`/`st.Faint`/`st.Good`/`st.Bad`/`st.Key`/`st.SectionHead`/`st.KeyDesc`. Do not modify `theme.go`.
- Cancel keys: `x` opens confirm; in confirm, `k` = keep files, `d` = delete files, `esc`/`n` = abort.
- Engine removal: `Remove(infoHash string, deleteData bool) error`; unknown hash → `nil`.
- History persists across restarts as `history.json` in the OS config dir; dedup by infohash.
- Full gate before a task is done: `go test ./... -race`, `go vet ./...`, `gofmt -l internal/ cmd/` (empty), `go build ./...`.

---

### Task 1: Engine — infohash + `Remove`

**Files:**
- Modify: `internal/engine/engine.go` (Status field + interface method)
- Modify: `internal/engine/anacrolix.go` (dataDir field, InfoHash fill, Remove impl, imports)
- Modify: `internal/engine/anacrolix_test.go` (Remove test)
- Modify: `internal/ui/model_test.go` (add `fakeEngine.Remove` so package `ui` still builds)

**Interfaces:**
- Produces (consumed by Tasks 3–6):
  - `engine.Status.InfoHash string`
  - `engine.Engine.Remove(infoHash string, deleteData bool) error`
  - `fakeEngine` fields `removedHash string`, `removedDelete bool`, `removeErr error`

- [ ] **Step 1: Write the failing engine test.**

Add to `internal/engine/anacrolix_test.go` (imports `bytes`, `net/http`, `net/http/httptest`, `time` are already present):
```go
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
```

- [ ] **Step 2: Run test to verify it fails.**

Run: `go test ./internal/engine/ -run TestAnacrolixRemoveDropsTorrent -v`
Expected: FAIL — `eng.Remove undefined` and `all[0].InfoHash undefined` (compile error).

- [ ] **Step 3: Add the Status field and interface method.**

In `internal/engine/engine.go`, add to `type Status struct` (after `Name string`):
```go
	InfoHash string // lowercase hex infohash; "" only while metadata is still fetching
```
And add to the `Engine` interface (after `Statuses()`):
```go
	// Remove stops the torrent with the given hex infohash and forgets it. When
	// deleteData is true, its downloaded file/dir under the data dir is also
	// removed. An unknown hash is a no-op (nil error).
	Remove(infoHash string, deleteData bool) error
```

- [ ] **Step 4: Implement in anacrolix.go.**

Add `"os"` and `"path/filepath"` to the imports. Add a `dataDir` field to the struct:
```go
type Anacrolix struct {
	client  *torrent.Client
	http    *http.Client
	dataDir string

	seedRatio float64
	// ... rest unchanged ...
}
```
In `NewAnacrolix`, set it on the struct literal (alongside `client:`):
```go
		dataDir:   c.DataDir,
```
In `Statuses()`, add `InfoHash` to the `Status{...}` literal:
```go
			InfoHash:       h.HexString(),
```
Add the method:
```go
func (a *Anacrolix) Remove(infoHash string, deleteData bool) error {
	a.mu.Lock()
	var (
		found *torrent.Torrent
		hash  metainfo.Hash
		name  string
	)
	for _, t := range a.client.Torrents() {
		if t.InfoHash().HexString() == infoHash {
			found = t
			hash = t.InfoHash()
			if name = a.names[hash]; name == "" {
				name = t.Name()
			}
			break
		}
	}
	if found == nil {
		a.mu.Unlock()
		return nil // already gone
	}
	found.Drop()
	delete(a.names, hash)
	delete(a.addedAt, hash)
	a.mu.Unlock()

	if deleteData && name != "" {
		return os.RemoveAll(filepath.Join(a.dataDir, name))
	}
	return nil
}
```

- [ ] **Step 5: Add `fakeEngine.Remove` so package `ui` still builds.**

In `internal/ui/model_test.go`, add to the `fakeEngine` struct:
```go
	removedHash   string
	removedDelete bool
	removeErr     error
```
And the method (next to the other `fakeEngine` methods):
```go
func (e *fakeEngine) Remove(infoHash string, deleteData bool) error {
	e.removedHash = infoHash
	e.removedDelete = deleteData
	return e.removeErr
}
```

- [ ] **Step 6: Run tests to verify they pass.**

Run: `go test ./internal/engine/ -run TestAnacrolixRemove -v` then `go build ./... && go test ./internal/ui/`
Expected: engine test PASS (or `SKIP` if a client can't start in this env — matches the existing lifecycle test); ui builds and its tests pass.

- [ ] **Step 7: Checkpoint.**

Run: `go build ./... && go vet ./... && go test ./internal/engine/ ./internal/ui/`
Expected: clean; `ok`. (Commit.)

---

### Task 2: `internal/history` store

**Files:**
- Create: `internal/history/history.go`
- Create: `internal/history/history_test.go`

**Interfaces:**
- Produces (consumed by Tasks 4–6):
  - `type history.Entry struct { InfoHash string; Name string; Size int64; CompletedAt time.Time }` (JSON-tagged)
  - `type history.Store struct { Path string; Entries []Entry }`
  - `func history.Load() Store`, `func history.LoadFrom(path string) Store`
  - `func (s *Store) Append(e Entry)` (dedup by InfoHash, newest-first, persists)
  - `func (s Store) Save() error` (no-op when `Path == ""`)

- [ ] **Step 1: Write the failing test.**

Create `internal/history/history_test.go`:
```go
package history

import (
	"path/filepath"
	"testing"
	"time"
)

func TestAppendSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	s := LoadFrom(path) // missing file → empty
	if len(s.Entries) != 0 {
		t.Fatalf("fresh store = %d entries, want 0", len(s.Entries))
	}

	s.Append(Entry{InfoHash: "a", Name: "First", Size: 100, CompletedAt: time.Unix(1000, 0)})
	s.Append(Entry{InfoHash: "b", Name: "Second", Size: 200, CompletedAt: time.Unix(2000, 0)})
	s.Append(Entry{InfoHash: "a", Name: "First-dup", Size: 999, CompletedAt: time.Unix(3000, 0)}) // dup infohash: ignored

	got := LoadFrom(path)
	if len(got.Entries) != 2 {
		t.Fatalf("loaded %d entries, want 2 (dup ignored): %+v", len(got.Entries), got.Entries)
	}
	if got.Entries[0].InfoHash != "b" {
		t.Fatalf("newest-first expected b first, got %q", got.Entries[0].InfoHash)
	}
}

func TestSaveNoopWithoutPath(t *testing.T) {
	s := Store{} // Path == ""
	s.Append(Entry{InfoHash: "x", Name: "X"})
	if err := s.Save(); err != nil {
		t.Fatalf("Save with empty Path should be a no-op nil, got %v", err)
	}
	if len(s.Entries) != 1 {
		t.Fatalf("Append should still update in-memory entries, got %d", len(s.Entries))
	}
}
```

- [ ] **Step 2: Run test to verify it fails.**

Run: `go test ./internal/history/ -v`
Expected: FAIL — package/identifiers undefined.

- [ ] **Step 3: Implement the store.**

Create `internal/history/history.go`:
```go
// Package history persists a record of completed downloads as JSON in the OS
// user-config dir (e.g. ~/.config/shoal/history.json), newest first.
package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Entry is one completed download.
type Entry struct {
	InfoHash    string    `json:"info_hash"`
	Name        string    `json:"name"`
	Size        int64     `json:"size"`
	CompletedAt time.Time `json:"completed_at"`
}

// Store is the persisted history. Path is where it loads/saves; an empty Path
// disables Save (used by tests).
type Store struct {
	Path    string  `json:"-"`
	Entries []Entry `json:"entries"`
}

func defaultPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "shoal", "history.json")
}

// Load reads history from the default config-dir path.
func Load() Store { return LoadFrom(defaultPath()) }

// LoadFrom reads history from path; a missing or corrupt file yields an empty
// (but writable) store.
func LoadFrom(path string) Store {
	s := Store{Path: path}
	b, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	_ = json.Unmarshal(b, &s)
	s.Path = path // Unmarshal can't set the json:"-" field
	return s
}

// Append prepends e (newest first) unless an entry with the same InfoHash is
// already recorded, then persists. Dedup makes re-recording harmless.
func (s *Store) Append(e Entry) {
	for _, existing := range s.Entries {
		if existing.InfoHash == e.InfoHash {
			return
		}
	}
	s.Entries = append([]Entry{e}, s.Entries...)
	_ = s.Save()
}

// Save writes the store to Path (creating the dir). No-op when Path is empty.
func (s Store) Save() error {
	if s.Path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.Path, b, 0o644)
}
```

- [ ] **Step 4: Run test to verify it passes.**

Run: `go test ./internal/history/ -v`
Expected: PASS.

- [ ] **Step 5: Checkpoint.**

Run: `go build ./... && go vet ./internal/history/`
Expected: clean. (Commit.)

---

### Task 3: Model — Downloads cursor + cancel confirm

**Files:**
- Modify: `internal/ui/model.go`
- Test: `internal/ui/model_test.go`

**Interfaces:**
- Consumes: `engine.Status.InfoHash`, `engine.Engine.Remove` (Task 1).
- Produces (consumed by Task 5): `Model` fields `dlCursor int`, `cancelConfirm bool`, `cancelTarget engine.Status`; `removedMsg`; `removeCmd`; `handleCancelKey`.

- [ ] **Step 1: Write the failing tests.**

Add to `internal/ui/model_test.go`:
```go
func TestDownloadsCursorMoves(t *testing.T) {
	eng := &fakeEngine{statuses: []engine.Status{
		{Name: "A", InfoHash: "aa", TotalBytes: 100, CompletedBytes: 10},
		{Name: "B", InfoHash: "bb", TotalBytes: 100, CompletedBytes: 20},
	}}
	m := ready(New(&fakeSource{}, eng))
	m, _ = update(m, tickMsg(time.Now())) // load statuses
	m.editing = false
	m.section = sectionDownloads
	m, _ = update(m, key("down"))
	if m.dlCursor != 1 {
		t.Fatalf("dlCursor after down = %d, want 1", m.dlCursor)
	}
	m, _ = update(m, key("up"))
	if m.dlCursor != 0 {
		t.Fatalf("dlCursor after up = %d, want 0", m.dlCursor)
	}
}

func TestCancelConfirmDeletePath(t *testing.T) {
	eng := &fakeEngine{statuses: []engine.Status{{Name: "Movie", InfoHash: "abc123", TotalBytes: 100, CompletedBytes: 10}}}
	m := ready(New(&fakeSource{}, eng))
	m, _ = update(m, tickMsg(time.Now()))
	m.editing = false
	m.section = sectionDownloads
	m.dlCursor = 0

	m, _ = update(m, key("x")) // open confirm
	if !m.cancelConfirm || m.cancelTarget.InfoHash != "abc123" {
		t.Fatalf("x did not open confirm: confirm=%v target=%+v", m.cancelConfirm, m.cancelTarget)
	}
	m, cmd := update(m, key("d")) // cancel + delete files
	if m.cancelConfirm {
		t.Fatalf("d should close the confirm")
	}
	if cmd == nil {
		t.Fatalf("d should return a remove command")
	}
	cmd()
	if eng.removedHash != "abc123" || !eng.removedDelete {
		t.Fatalf("remove got hash=%q delete=%v, want abc123/true", eng.removedHash, eng.removedDelete)
	}
}

func TestCancelKeepAndAbort(t *testing.T) {
	eng := &fakeEngine{statuses: []engine.Status{{Name: "Movie", InfoHash: "h1", TotalBytes: 100, CompletedBytes: 10}}}
	m := ready(New(&fakeSource{}, eng))
	m, _ = update(m, tickMsg(time.Now()))
	m.editing = false
	m.section = sectionDownloads

	m, _ = update(m, key("x"))
	m, cmd := update(m, key("esc")) // abort
	if m.cancelConfirm || cmd != nil {
		t.Fatalf("esc should abort with no command")
	}
	if eng.removedHash != "" {
		t.Fatalf("esc must not call Remove, got %q", eng.removedHash)
	}

	m, _ = update(m, key("x"))
	m, cmd = update(m, key("k")) // keep files
	cmd()
	if eng.removedHash != "h1" || eng.removedDelete {
		t.Fatalf("k should Remove(keep): hash=%q delete=%v", eng.removedHash, eng.removedDelete)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail.**

Run: `go test ./internal/ui/ -run 'DownloadsCursor|CancelConfirm|CancelKeep' -v`
Expected: FAIL — undefined fields/identifiers.

- [ ] **Step 3: Add fields, message, command, and key handling.**

In `internal/ui/model.go`, add to the `Model` struct (near `statuses`):
```go
	dlCursor      int // selection in the Downloads pane
	cancelConfirm bool
	cancelTarget  engine.Status
```
Add the message and command near `addCmd`/`addedMsg`:
```go
type removedMsg struct {
	name    string
	deleted bool
	err     error
}

func removeCmd(eng engine.Engine, infoHash, name string, deleteData bool) tea.Cmd {
	return func() tea.Msg {
		err := eng.Remove(infoHash, deleteData)
		return removedMsg{name: name, deleted: deleteData, err: err}
	}
}
```
Add the `removedMsg` case to `Update` (next to `addedMsg`):
```go
	case removedMsg:
		switch {
		case msg.err != nil:
			m.setError("Couldn't remove: " + msg.err.Error())
		case msg.deleted:
			m.setNotice("Deleted: " + truncate(msg.name, 48))
		default:
			m.setNotice("Cancelled: " + truncate(msg.name, 48))
		}
		if n := len(m.downloading()); m.dlCursor >= n {
			m.dlCursor = max(0, n-1)
		}
		return m, nil
```
Extend `moveUp`/`moveDown` with a `sectionDownloads` case:
```go
// in moveUp's switch:
	case sectionDownloads:
		if m.dlCursor > 0 {
			m.dlCursor--
		}
// in moveDown's switch:
	case sectionDownloads:
		if m.dlCursor < len(m.downloading())-1 {
			m.dlCursor++
		}
```
In `handleKey`, add the modal guard among the other modal guards (after the `m.showDetail`/`m.sortMode` guards, before the command switch):
```go
	if m.cancelConfirm {
		return m.handleCancelKey(msg)
	}
```
Add an `x` case to the command-mode `switch`:
```go
	case "x":
		if m.section == sectionDownloads {
			ds := m.downloading()
			if len(ds) > 0 && m.dlCursor < len(ds) {
				m.cancelConfirm = true
				m.cancelTarget = ds[m.dlCursor]
			}
		}
		return m, nil
```
Add the handler method:
```go
func (m Model) handleCancelKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "k":
		m.cancelConfirm = false
		return m, removeCmd(m.eng, m.cancelTarget.InfoHash, m.cancelTarget.Name, false)
	case "d":
		m.cancelConfirm = false
		return m, removeCmd(m.eng, m.cancelTarget.InfoHash, m.cancelTarget.Name, true)
	case "esc", "n":
		m.cancelConfirm = false
	case "q", "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}
```

- [ ] **Step 4: Run tests to verify they pass.**

Run: `go test ./internal/ui/ -run 'DownloadsCursor|CancelConfirm|CancelKeep' -v`
Expected: PASS.

- [ ] **Step 5: Checkpoint.**

Run: `go build ./... && go test ./internal/ui/`
Expected: clean; `ok`. (Commit.)

---

### Task 4: Model — history detection + wiring

**Files:**
- Modify: `internal/ui/model.go`
- Modify: `cmd/shoal/main.go`
- Test: `internal/ui/model_test.go`

**Interfaces:**
- Consumes: `history.Store`/`history.Entry`/`history.Load` (Task 2); `engine.Status.InfoHash`/`Done` (Task 1).
- Produces (consumed by Task 6): `Model.history history.Store`; `func (m Model) WithHistory(h history.Store) Model`; `func newlyCompleted(prev, next []engine.Status) []engine.Status`.

- [ ] **Step 1: Write the failing tests.**

Add to `internal/ui/model_test.go` (add `"shoal/internal/history"` to its imports):
```go
func TestNewlyCompleted(t *testing.T) {
	prev := []engine.Status{{InfoHash: "a", Done: false}, {InfoHash: "b", Done: true}}
	next := []engine.Status{{InfoHash: "a", Done: true}, {InfoHash: "b", Done: true}}
	got := newlyCompleted(prev, next)
	if len(got) != 1 || got[0].InfoHash != "a" {
		t.Fatalf("newlyCompleted = %+v, want just a", got)
	}
}

func TestTickRecordsHistory(t *testing.T) {
	eng := &fakeEngine{statuses: []engine.Status{{Name: "Movie", InfoHash: "hh", TotalBytes: 2048, CompletedBytes: 0}}}
	m := ready(New(&fakeSource{}, eng))
	t0 := time.Unix(1_000_000, 0)
	m, _ = update(m, tickMsg(t0)) // not done yet → no history
	eng.statuses = []engine.Status{{Name: "Movie", InfoHash: "hh", TotalBytes: 2048, CompletedBytes: 2048, Done: true}}
	m, _ = update(m, tickMsg(t0.Add(time.Second))) // completes → recorded
	if len(m.history.Entries) != 1 || m.history.Entries[0].InfoHash != "hh" {
		t.Fatalf("history = %+v, want one entry for hh", m.history.Entries)
	}
	m, _ = update(m, tickMsg(t0.Add(2*time.Second))) // still done → no dup
	if len(m.history.Entries) != 1 {
		t.Fatalf("history duplicated: %+v", m.history.Entries)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail.**

Run: `go test ./internal/ui/ -run 'NewlyCompleted|TickRecordsHistory' -v`
Expected: FAIL — undefined `newlyCompleted` / `m.history`.

- [ ] **Step 3: Add the field, `WithHistory`, helper, and tick detection.**

In `internal/ui/model.go`, add `"shoal/internal/history"` to imports. Add to the `Model` struct (near `statuses`):
```go
	history history.Store
```
Add the injector method (near `New`/`NewWithConfig`):
```go
// WithHistory attaches a loaded history store (main wires history.Load(); tests
// leave it empty so Save is a no-op).
func (m Model) WithHistory(h history.Store) Model {
	m.history = h
	return m
}
```
Add the helper:
```go
// newlyCompleted returns torrents that flipped Done false→true (or first appeared
// already Done) between two snapshots, keyed by InfoHash.
func newlyCompleted(prev, next []engine.Status) []engine.Status {
	was := make(map[string]bool, len(prev))
	for _, s := range prev {
		if s.Done {
			was[s.InfoHash] = true
		}
	}
	var out []engine.Status
	for _, s := range next {
		if s.Done && s.InfoHash != "" && !was[s.InfoHash] {
			out = append(out, s)
		}
	}
	return out
}
```
Update the `tickMsg` case so it records completions from the prev/next snapshot. It currently looks like:
```go
	case tickMsg:
		if m.eng != nil {
			now := time.Time(msg)
			next := m.eng.Statuses()
			dt := now.Sub(m.lastTick)
			m.dlSpeed = computeRates(m.statuses, next, dt, func(s engine.Status) int64 { return s.CompletedBytes })
			m.ulSpeed = computeRates(m.statuses, next, dt, func(s engine.Status) int64 { return s.Uploaded })
			m.statuses = next
			m.lastTick = now
		}
		if m.notice != "" && time.Now().After(m.noticeUntil) {
			m.notice = ""
			m.noticeErr = false
		}
		return m, tickCmd()
```
Change the inner block to detect completions before overwriting `m.statuses`, and clamp `dlCursor`:
```go
	case tickMsg:
		if m.eng != nil {
			now := time.Time(msg)
			next := m.eng.Statuses()
			dt := now.Sub(m.lastTick)
			m.dlSpeed = computeRates(m.statuses, next, dt, func(s engine.Status) int64 { return s.CompletedBytes })
			m.ulSpeed = computeRates(m.statuses, next, dt, func(s engine.Status) int64 { return s.Uploaded })
			for _, s := range newlyCompleted(m.statuses, next) {
				m.history.Append(history.Entry{InfoHash: s.InfoHash, Name: s.Name, Size: s.TotalBytes, CompletedAt: now})
			}
			m.statuses = next
			m.lastTick = now
			if n := len(m.downloading()); m.dlCursor >= n {
				m.dlCursor = max(0, n-1)
			}
		}
		if m.notice != "" && time.Now().After(m.noticeUntil) {
			m.notice = ""
			m.noticeErr = false
		}
		return m, tickCmd()
```

- [ ] **Step 4: Wire history in main.go.**

In `cmd/shoal/main.go`, add `"shoal/internal/history"` to imports and change the model construction:
```go
	p := tea.NewProgram(
		ui.NewWithConfig(src, eng, cfg).WithHistory(history.Load()),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
```

- [ ] **Step 5: Run tests to verify they pass.**

Run: `go test ./internal/ui/ -run 'NewlyCompleted|TickRecordsHistory' -v && go build ./...`
Expected: PASS; build clean.

- [ ] **Step 6: Checkpoint.**

Run: `go build ./... && go test ./internal/ui/`
Expected: clean; `ok`. (Commit.)

---

### Task 5: View — Downloads selection + cancel prompt + footer/help

**Files:**
- Modify: `internal/ui/view.go`
- Test: `internal/ui/view_test.go`

**Interfaces:**
- Consumes: `dlCursor`/`cancelConfirm`/`cancelTarget` (Task 3).
- Produces: updated `renderDownloads`, `renderFooter`, `helpView`.

- [ ] **Step 1: Write the failing test.**

Add to `internal/ui/view_test.go`:
```go
func TestRenderDownloadsCancelPrompt(t *testing.T) {
	m := ready(New(&fakeSource{}, &fakeEngine{}))
	m.section = sectionDownloads
	m.statuses = []engine.Status{{Name: "Movie", InfoHash: "h", TotalBytes: 100, CompletedBytes: 10}}
	m.cancelConfirm = true
	m.cancelTarget = m.statuses[0]
	v := m.View()
	for _, want := range []string{"Cancel", "keep files", "delete files"} {
		if !strings.Contains(v, want) {
			t.Fatalf("cancel prompt missing %q:\n%s", want, v)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails.**

Run: `go test ./internal/ui/ -run TestRenderDownloadsCancelPrompt -v`
Expected: FAIL — prompt text absent.

- [ ] **Step 3: Update `renderDownloads` for selection + prompt.**

Replace the body of `renderDownloads` (`internal/ui/view.go`) with:
```go
func (m Model) renderDownloads(w, h int) string {
	ds := m.downloading()
	if len(ds) == 0 {
		return "  " + st.Meta.Render("No active downloads. Find something in ") +
			st.Accent.Render("Search") + st.Meta.Render(" and press ") + st.Key.Render("d") + st.Meta.Render(".")
	}

	const perItem = 4
	visible := max(1, h/perItem)
	barWidth := max(10, min(48, w-24))

	var b strings.Builder
	if m.cancelConfirm {
		b.WriteString("  " + st.Bad.Render("Cancel ") +
			st.Row.Render("\""+truncate(m.cancelTarget.Name, max(8, w-32))+"\"") + st.Meta.Render("?   ") +
			st.Key.Render("k") + st.Meta.Render(" keep files   ·   ") +
			st.Key.Render("d") + st.Meta.Render(" delete files   ·   ") +
			st.Key.Render("esc") + st.Meta.Render(" back") + "\n\n")
	}

	shown := min(len(ds), visible)
	for i := 0; i < shown; i++ {
		s := ds[i]
		head, nameStyle := st.Accent.Render(glyphDown+" "), st.Row
		if i == m.dlCursor {
			head, nameStyle = st.Accent.Render(glyphCursor+" "), st.RowSel
		}
		b.WriteString(head + nameStyle.Render(truncate(s.Name, max(4, w-4))) + "\n")

		p := m.prog
		p.Width = barWidth
		bar := p.ViewAs(s.Percent())

		state := fmt.Sprintf("%5.1f%%", s.Percent()*100)
		detail := fmt.Sprintf("%s / %s  ·  %d peers", formatBytes(s.CompletedBytes), sizeOrDash(s.TotalBytes), s.Peers)
		if sp := m.dlSpeed[s.Name]; sp > 0 {
			detail += fmt.Sprintf("  ·  %s/s", formatBytes(sp))
		}

		b.WriteString("  " + bar + "  " + st.Row.Render(state) + "\n")
		b.WriteString("  " + st.Meta.Render(detail) + "\n")
		if i < shown-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}
```
(This preserves the speed suffix added earlier; only the selection prefix and confirm prompt are new. ponytail: the Downloads list shows the first `visible` rows and does not scroll to keep a far-down cursor on screen — acceptable since active downloads are few.)

- [ ] **Step 4: Update the footer and help.**

In `renderFooter`, add these cases BEFORE the `m.section == sectionSearch` case:
```go
	case m.cancelConfirm:
		parts = []string{hint("k", "keep files"), hint("d", "delete files"), hint("esc", "back")}
	case m.section == sectionDownloads:
		parts = []string{hint("↑↓", "move"), hint("x", "cancel"), hint("tab", "panes"), hint("?", "help"), hint("q", "quit")}
```
In `helpView`, add a row to the `rows` slice (after the `d` row):
```go
		{"x", "cancel the selected download (keep or delete files)"},
```

- [ ] **Step 5: Run test to verify it passes.**

Run: `go test ./internal/ui/ -run TestRenderDownloadsCancelPrompt -v`
Expected: PASS.

- [ ] **Step 6: Checkpoint.**

Run: `go build ./... && go test ./internal/ui/`
Expected: clean; `ok`. (Commit.)

---

### Task 6: View — Seeding pane history section

**Files:**
- Modify: `internal/ui/view.go`
- Test: `internal/ui/view_test.go`

**Interfaces:**
- Consumes: `m.history` (Task 4), `m.seeding()`, `relTime`/`sizeOrDash` helpers.
- Produces: updated `renderSeeding`.

- [ ] **Step 1: Write the failing tests.**

Add to `internal/ui/view_test.go` (add `"shoal/internal/history"` to its imports):
```go
func TestRenderSeedingHistorySection(t *testing.T) {
	m := ready(New(&fakeSource{}, &fakeEngine{}))
	m.section = sectionSeeding
	m.history = history.Store{Entries: []history.Entry{
		{InfoHash: "h1", Name: "Old Movie", Size: 1_500_000_000, CompletedAt: time.Now().Add(-48 * time.Hour)},
	}}
	v := m.View()
	for _, want := range []string{"HISTORY", "Old Movie"} {
		if !strings.Contains(v, want) {
			t.Fatalf("seeding history missing %q:\n%s", want, v)
		}
	}
}

func TestSeedingHistoryDedupsActive(t *testing.T) {
	eng := &fakeEngine{statuses: []engine.Status{
		{Name: "Now Seeding", InfoHash: "dup", TotalBytes: 1000, CompletedBytes: 1000, Uploaded: 500, Done: true},
	}}
	m := ready(New(&fakeSource{}, eng))
	m, _ = update(m, tickMsg(time.Now()))
	m.section = sectionSeeding
	m.history = history.Store{Entries: []history.Entry{
		{InfoHash: "dup", Name: "Now Seeding", Size: 1000, CompletedAt: time.Now()},
	}}
	if got := strings.Count(m.View(), "Now Seeding"); got != 1 {
		t.Fatalf("actively-seeding torrent must not also appear under HISTORY, count=%d", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail.**

Run: `go test ./internal/ui/ -run 'SeedingHistory|HistorySection' -v`
Expected: FAIL — no HISTORY section yet.

- [ ] **Step 3: Update `renderSeeding` to add the history section.**

Add `"shoal/internal/history"` to `view.go` imports. Replace the body of `renderSeeding` with:
```go
func (m Model) renderSeeding(w, h int) string {
	ss := m.seeding()
	active := make(map[string]bool, len(ss))
	for _, s := range ss {
		active[s.InfoHash] = true
	}
	var hist []history.Entry
	for _, e := range m.history.Entries {
		if !active[e.InfoHash] {
			hist = append(hist, e)
		}
	}

	if len(ss) == 0 && len(hist) == 0 {
		return "  " + st.Meta.Render("Nothing seeding yet. Completed downloads keep sharing here.")
	}

	const perItem = 3
	visible := max(1, h/perItem)

	var b strings.Builder
	shown := min(len(ss), visible)
	for i := 0; i < shown; i++ {
		s := ss[i]
		b.WriteString(st.Good.Render(glyphSeed+" ") + st.Row.Render(truncate(s.Name, max(4, w-4))) + "\n")

		detail := fmt.Sprintf("  ·  %d peers", s.Peers)
		if s.Uploaded > 0 {
			detail = fmt.Sprintf("  ·  ratio %.2f  ·  %s %s  ·  %d peers", s.Ratio(), glyphSeed, formatBytes(s.Uploaded), s.Peers)
		}
		if sp := m.ulSpeed[s.Name]; sp > 0 {
			detail += fmt.Sprintf("  ·  %s/s", formatBytes(sp))
		}
		b.WriteString("  " + st.Good.Render(glyphDone+" complete") + st.Meta.Render(truncate(detail, max(4, w-14))) + "\n")
		if i < shown-1 {
			b.WriteString("\n")
		}
	}

	if len(hist) > 0 {
		if len(ss) > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(st.SectionHead.Render("HISTORY") + "\n")
		const histMax = 50
		for i, e := range hist {
			if i >= histMax {
				b.WriteString("  " + st.Faint.Render(fmt.Sprintf("%s %d more %s", glyphMore, len(hist)-histMax, glyphDown)) + "\n")
				break
			}
			meta := "  ·  " + sizeOrDash(e.Size) + "  ·  " + relTime(e.CompletedAt.Unix())
			b.WriteString("  " + st.Good.Render(glyphDone+" ") + st.Row.Render(truncate(e.Name, max(4, w-24))) + st.Meta.Render(meta) + "\n")
		}
	}
	return b.String()
}
```
(This keeps the existing active-seeding rendering — ratio/complete/speed — byte-for-byte and appends the history section. ponytail: history capped at 50 rows with a "more" indicator; no scroll.)

- [ ] **Step 4: Run tests to verify they pass.**

Run: `go test ./internal/ui/ -run 'SeedingHistory|HistorySection|RenderSeeding' -v`
Expected: PASS (including the pre-existing `TestRenderSeedingShowsRatio` and `TestRenderSeedingShowsSpeed`).

- [ ] **Step 5: Full checkpoint.**

Run: `go build ./... && go vet ./... && go test ./... -race && gofmt -l internal/ cmd/`
Expected: build clean, vet clean, all tests `ok`, `gofmt -l` prints nothing. (Commit.)

---

## Self-Review Notes

- **Spec coverage:** engine InfoHash+Remove (Task 1); history store (Task 2); Downloads cursor + cancel confirm keep/delete (Task 3); history detection + persistence wiring (Task 4); Downloads selection/prompt + footer/help (Task 5); Seeding history section + dedup (Task 6). All spec sections mapped.
- **Type consistency:** `Remove(infoHash string, deleteData bool) error`, `Status.InfoHash`, `removeCmd(eng, infoHash, name, deleteData)`, `removedMsg{name, deleted, err}`, `history.Entry{InfoHash,Name,Size,CompletedAt}`, `history.Store{Path,Entries}`, `WithHistory`, `newlyCompleted` used consistently across tasks.
- **Test hygiene:** model/view tests reuse `ready`/`key`/`update`/`fakeEngine`; history/engine tests use temp dirs and the existing offline `.torrent` harness; the engine test may `SKIP` where a client can't start (matches the existing lifecycle test).
- **Known ceilings (ponytail-marked):** Downloads list and Seeding history don't scroll to an off-screen cursor / beyond their caps; acceptable for typical counts.
