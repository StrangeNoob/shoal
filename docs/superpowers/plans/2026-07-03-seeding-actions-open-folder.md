# Seeding-pane actions + open download folder Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add per-torrent Seeding-pane actions (`p` pause/resume, `x` stop) and an `o` "open folder" action to the Downloads and Seeding panes.

**Architecture:** A selection cursor + key handling are added to the Seeding pane, mirroring the existing Downloads pane (`dlCursor`, cancel-confirm). `p`/`x` reuse the engine's `Pause`/`Resume`/`Remove`. Opening a folder uses a new `Status.Path` from the engine plus an OS-specific opener run as a background `tea.Cmd`.

**Tech Stack:** Go 1.24, Bubble Tea, anacrolix/torrent, os/exec.

## Global Constraints

- TDD: write the failing test first for every behavior.
- No Claude attribution in commit messages. Branch: `feature/seeding-actions-open-folder`.
- `x` stop always keeps files (`deleteData=false`).
- Full gate before a task is done: `go build ./...`, `go vet ./...`, `gofmt -l internal/` (empty), `go test ./... -race`.
- Existing patterns to mirror: Downloads uses `dlCursor`, `cancelConfirm`/`cancelTarget`, `handleCancelKey`, `removeCmd(eng, infoHash, name, deleteData)`; the selected row uses `glyphCursor` (`❯`) + `st.RowSel`.

---

### Task 1: Engine `Status.Path`

**Files:**
- Modify: `internal/engine/engine.go` (add `Status.Path`)
- Modify: `internal/engine/anacrolix.go` (set `Path` in `Statuses`)
- Test: `internal/engine/anacrolix_test.go`

**Interfaces:**
- Produces (consumed by Task 4): `Status.Path string` — the torrent's top-level on-disk path (`<dataDir>/<t.Name()>`), or `""` when metadata isn't available.

- [ ] **Step 1: Write the failing test.**

Add to `internal/engine/anacrolix_test.go`:
```go
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
```

- [ ] **Step 2: Run it to verify it fails.**

Run: `go test ./internal/engine/ -run TestStatusPath -v`
Expected: FAIL — `st.Path` undefined.

- [ ] **Step 3: Add the field.**

In `internal/engine/engine.go`, add to `Status` (after `Paused bool`):
```go
	// Path is the torrent's top-level on-disk location (<data dir>/<name>), or
	// "" before metadata is known. Used by the UI to open the download folder.
	Path string
```

- [ ] **Step 4: Set it in `Statuses`.**

In `internal/engine/anacrolix.go` `Statuses`, the metadata block currently reads:
```go
		var total, pieceLen int64
		if info := t.Info(); info != nil {
			total = info.TotalLength()
			pieceLen = info.PieceLength
			if name == "" {
				name = t.Name()
			}
		}
```
Change it to also compute the on-disk path:
```go
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
```
Add `Path: diskPath,` to the `Status{...}` literal (after `Paused: a.paused[h],`).

- [ ] **Step 5: Run tests + gate.**

Run: `go test ./internal/engine/ -run TestStatusPath -v && go build ./... && gofmt -l internal/engine/`
Expected: PASS; build clean; gofmt clean. (Commit.)

---

### Task 2: Seeding cursor + pause/resume

**Files:**
- Modify: `internal/ui/model.go` (`seedCursor`, `moveUp`/`moveDown`, tick clamp, `pauseToggleCmd`, `p` handler)
- Modify: `internal/ui/view.go` (`renderSeeding` cursor + paused, footer Seeding case)
- Test: `internal/ui/model_test.go`, `internal/ui/view_test.go`

**Interfaces:**
- Consumes: `Engine.Pause`/`Resume`, `Status.Paused` (existing); `m.seeding() []engine.Status` (existing).
- Produces (consumed by Tasks 3–4): `Model.seedCursor int`; `pauseToggleCmd(eng engine.Engine, s engine.Status) tea.Cmd`.

- [ ] **Step 1: Write the failing tests.**

Add to `internal/ui/model_test.go`:
```go
func TestSeedingPauseAndCursor(t *testing.T) {
	fe := &fakeEngine{statuses: []engine.Status{
		{Name: "A", InfoHash: "a", TotalBytes: 100, CompletedBytes: 100, Done: true},
		{Name: "B", InfoHash: "b", TotalBytes: 100, CompletedBytes: 100, Done: true},
	}}
	m := ready(New(&fakeSource{}, fe))
	m.section = sectionSeeding

	// down moves the seed cursor
	m, _ = update(m, key("down"))
	if m.seedCursor != 1 {
		t.Fatalf("seedCursor after down = %d, want 1", m.seedCursor)
	}
	// p pauses the selected seeder (B)
	m2, cmd := update(m, key("p"))
	if cmd == nil {
		t.Fatal("p should return a pause command")
	}
	cmd()
	if !fe.paused["b"] {
		t.Fatal("p should pause the selected seeding torrent")
	}
	_ = m2
	// p again on a paused seeder resumes it
	fe.statuses[1].Paused = true
	m.seedCursor = 1
	_, cmd = update(m, key("p"))
	cmd()
	if fe.paused["b"] {
		t.Fatal("p on a paused seeder should resume it")
	}
}
```
Add to `internal/ui/view_test.go`:
```go
func TestSeedingRendersPausedAndFooter(t *testing.T) {
	fe := &fakeEngine{statuses: []engine.Status{
		{Name: "Paused Movie", InfoHash: "a", TotalBytes: 100, CompletedBytes: 100, Done: true, Paused: true},
	}}
	m := ready(New(&fakeSource{}, fe))
	m.section = sectionSeeding
	v := m.View()
	if !strings.Contains(v, "paused") {
		t.Errorf("a paused seeder should render 'paused':\n%s", v)
	}
	if !strings.Contains(v, "pause/resume") {
		t.Errorf("Seeding footer should show 'pause/resume':\n%s", v)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail.**

Run: `go test ./internal/ui/ -run 'SeedingPauseAndCursor|SeedingRendersPaused' -v`
Expected: FAIL — `seedCursor` undefined / footer + paused render missing.

- [ ] **Step 3: Add `seedCursor` + navigation + tick clamp.**

In `internal/ui/model.go`, add to the `Model` struct (next to `dlCursor`):
```go
	seedCursor int // selection in the Seeding pane
```
In `moveUp()`, add a case after the `sectionDownloads` case:
```go
	case sectionSeeding:
		if m.seedCursor > 0 {
			m.seedCursor--
		}
```
In `moveDown()`, add:
```go
	case sectionSeeding:
		if m.seedCursor < len(m.seeding())-1 {
			m.seedCursor++
		}
```
In the `tickMsg` case, right after the `dlCursor` clamp (`if n := len(m.downloading()); m.dlCursor >= n { ... }`), add:
```go
			if n := len(m.seeding()); m.seedCursor >= n {
				m.seedCursor = max(0, n-1)
			}
```

- [ ] **Step 4: Extract `pauseToggleCmd` and handle `p` in both panes.**

In `internal/ui/model.go`, add the helper (near `removeCmd`):
```go
// pauseToggleCmd pauses a running torrent or resumes a paused one.
func pauseToggleCmd(eng engine.Engine, s engine.Status) tea.Cmd {
	return func() tea.Msg {
		if s.Paused {
			eng.Resume(s.InfoHash)
		} else {
			eng.Pause(s.InfoHash)
		}
		return nil
	}
}
```
Replace the existing `case "p":` block with:
```go
	case "p":
		switch m.section {
		case sectionDownloads:
			ds := m.downloading()
			if len(ds) > 0 && m.dlCursor < len(ds) {
				return m, pauseToggleCmd(m.eng, ds[m.dlCursor])
			}
		case sectionSeeding:
			ss := m.seeding()
			if len(ss) > 0 && m.seedCursor < len(ss) {
				return m, pauseToggleCmd(m.eng, ss[m.seedCursor])
			}
		}
		return m, nil
```

- [ ] **Step 5: Render the cursor + paused state in `renderSeeding`.**

In `internal/ui/view.go` `renderSeeding`, replace the per-item loop body (the `for i := 0; i < shown; i++ { ... }` block that renders name + the `complete · …` detail) with:
```go
	for i := 0; i < shown; i++ {
		s := ss[i]
		head, nameStyle := st.Good.Render(glyphSeed+" "), st.Row
		if i == m.seedCursor {
			head, nameStyle = st.Accent.Render(glyphCursor+" "), st.RowSel
		}
		b.WriteString(head + nameStyle.Render(truncate(s.Name, max(4, w-4))) + "\n")

		if s.Paused {
			b.WriteString("  " + st.Meta.Render("⏸ paused (not sharing)") + "\n")
		} else {
			detail := fmt.Sprintf("  ·  %d peers", s.Peers)
			if s.Uploaded > 0 {
				detail = fmt.Sprintf("  ·  ratio %.2f  ·  %s %s  ·  %d peers", s.Ratio(), glyphSeed, formatBytes(s.Uploaded), s.Peers)
			}
			if sp := m.ulSpeed[s.Name]; sp > 0 {
				detail += fmt.Sprintf("  ·  %s/s", formatBytes(sp))
			}
			b.WriteString("  " + st.Good.Render(glyphDone+" complete") + st.Meta.Render(truncate(detail, max(4, w-14))) + "\n")
		}
		if i < shown-1 {
			b.WriteString("\n")
		}
	}
```

- [ ] **Step 6: Add the Seeding footer case.**

In `internal/ui/view.go` `renderFooter`, add a case immediately before the `default:` case:
```go
	case m.section == sectionSeeding:
		parts = []string{hint("↑↓", "move"), hint("p", "pause/resume"), hint("tab", "panes"), hint("?", "help"), hint("q", "quit")}
```

- [ ] **Step 7: Run tests + gate.**

Run: `go test ./internal/ui/ -run 'SeedingPauseAndCursor|SeedingRendersPaused' -v && go test ./internal/ui/`
Expected: PASS (new tests + the full ui suite). Then `go build ./... && gofmt -l internal/`. (Commit.)

---

### Task 3: Seeding stop + confirm

**Files:**
- Modify: `internal/ui/model.go` (`stopConfirm`/`stopTarget`, dispatch, `handleStopKey`, tick clear, `x` handler)
- Modify: `internal/ui/view.go` (`renderSeeding` stop prompt, footer stopConfirm case + `x` in the Seeding case)
- Test: `internal/ui/model_test.go`

**Interfaces:**
- Consumes: `removeCmd(eng, infoHash, name, deleteData)` (existing); `Model.seedCursor` (Task 2).
- Produces: `Model.stopConfirm bool`, `Model.stopTarget engine.Status`.

- [ ] **Step 1: Write the failing test.**

Add to `internal/ui/model_test.go`:
```go
func TestSeedingStopFlow(t *testing.T) {
	fe := &fakeEngine{statuses: []engine.Status{
		{Name: "Movie", InfoHash: "h1", TotalBytes: 100, CompletedBytes: 100, Done: true},
	}}
	m := ready(New(&fakeSource{}, fe))
	m.section = sectionSeeding

	// x opens the stop confirm
	m, _ = update(m, key("x"))
	if !m.stopConfirm || m.stopTarget.InfoHash != "h1" {
		t.Fatalf("x should open the stop confirm for the selected seeder, got confirm=%v target=%q", m.stopConfirm, m.stopTarget.InfoHash)
	}
	// enter stops it (removeCmd with deleteData=false)
	m2, cmd := update(m, key("enter"))
	if m2.stopConfirm {
		t.Fatal("enter should close the stop confirm")
	}
	if cmd == nil {
		t.Fatal("enter should return a remove command")
	}
	msg := cmd().(removedMsg)
	if fe.removedHash != "h1" || fe.removedDelete {
		t.Fatalf("stop should Remove(h1, deleteData=false); got hash=%q delete=%v", fe.removedHash, fe.removedDelete)
	}
	_ = msg

	// esc cancels the confirm without removing
	m, _ = update(m, key("x"))
	m3, _ := update(m, key("esc"))
	if m3.stopConfirm {
		t.Fatal("esc should cancel the stop confirm")
	}
}
```
This assumes `fakeEngine` records `removedHash`/`removedDelete` in its `Remove`. If it doesn't yet, extend `fakeEngine.Remove` in `model_test.go`:
```go
func (e *fakeEngine) Remove(infoHash string, deleteData bool) error {
	e.removedHash = infoHash
	e.removedDelete = deleteData
	return nil
}
```
and add `removedHash string` + `removedDelete bool` fields to the `fakeEngine` struct (only if not already present — check first).

- [ ] **Step 2: Run it to verify it fails.**

Run: `go test ./internal/ui/ -run TestSeedingStopFlow -v`
Expected: FAIL — `stopConfirm`/`stopTarget` undefined.

- [ ] **Step 3: Add the state + dispatch + handler.**

In `internal/ui/model.go`, add to the `Model` struct (near `cancelConfirm`):
```go
	stopConfirm bool          // Seeding pane: confirm "stop seeding"
	stopTarget  engine.Status // the torrent being stopped
```
In `handleKey`, right after the `if m.cancelConfirm { return m.handleCancelKey(msg) }` line, add:
```go
	if m.stopConfirm {
		return m.handleStopKey(msg)
	}
```
Add the handler (next to `handleCancelKey`):
```go
// handleStopKey handles the Seeding "stop seeding" confirm. Files are always
// kept — this just stops sharing and forgets the torrent.
func (m Model) handleStopKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter", "y":
		m.stopConfirm = false
		return m, removeCmd(m.eng, m.stopTarget.InfoHash, m.stopTarget.Name, false)
	case "esc", "n":
		m.stopConfirm = false
	case "q", "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}
```

- [ ] **Step 4: Handle `x` in the Seeding pane + tick clear.**

In `internal/ui/model.go`, replace the existing `case "x":` block with:
```go
	case "x":
		switch m.section {
		case sectionDownloads:
			ds := m.downloading()
			if len(ds) > 0 && m.dlCursor < len(ds) {
				m.cancelConfirm = true
				m.cancelTarget = ds[m.dlCursor]
			}
		case sectionSeeding:
			ss := m.seeding()
			if len(ss) > 0 && m.seedCursor < len(ss) {
				m.stopConfirm = true
				m.stopTarget = ss[m.seedCursor]
			}
		}
		return m, nil
```
In the `tickMsg` case, after the cancel-confirm clear block, add a matching clear for stop:
```go
			if m.stopConfirm {
				stillSeeding := false
				for _, s := range m.seeding() {
					if s.InfoHash == m.stopTarget.InfoHash {
						stillSeeding = true
						break
					}
				}
				if !stillSeeding {
					m.stopConfirm = false
				}
			}
```

- [ ] **Step 5: Render the stop prompt + footer.**

In `internal/ui/view.go` `renderSeeding`, at the very top of the function body (before the `ss := m.seeding()` / empty-check, or right after building `b` and before the item loop — place it just before `shown := min(...)`), prepend the confirm prompt when active:
```go
	if m.stopConfirm {
		b.WriteString("  " + st.Bad.Render("Stop seeding ") +
			st.Row.Render("\""+truncate(m.stopTarget.Name, max(8, w-32))+"\"") + st.Meta.Render("?   ") +
			st.Key.Render("enter") + st.Meta.Render(" stop (keep files)   ·   ") +
			st.Key.Render("esc") + st.Meta.Render(" back") + "\n\n")
	}
```
(Match the style of the Downloads cancel prompt in `renderDownloads`.)

In `renderFooter`, add a `stopConfirm` case before the section cases (next to `case m.cancelConfirm:`):
```go
	case m.stopConfirm:
		parts = []string{hint("enter", "stop"), hint("esc", "back")}
```
Update the Seeding footer case (from Task 2) to add `x`:
```go
	case m.section == sectionSeeding:
		parts = []string{hint("↑↓", "move"), hint("p", "pause/resume"), hint("x", "stop"), hint("tab", "panes"), hint("?", "help"), hint("q", "quit")}
```

- [ ] **Step 6: Run tests + gate.**

Run: `go test ./internal/ui/ -run TestSeedingStopFlow -v && go test ./internal/ui/`
Expected: PASS. Then `go build ./... && gofmt -l internal/`. (Commit.)

---

### Task 4: Open download folder (`o`)

**Files:**
- Modify: `internal/ui/helpers.go` (`openCommand`, `openInFileManager`)
- Modify: `internal/ui/model.go` (`openFolderCmd`, `folderOpenedMsg`, `o` handler, message handling)
- Modify: `internal/ui/view.go` (footer `o` hint in Downloads + Seeding, help row)
- Test: `internal/ui/helpers_test.go`, `internal/ui/model_test.go`

**Interfaces:**
- Consumes: `Status.Path` (Task 1); `Model.dlCursor`/`seedCursor`, `m.downloading()`/`m.seeding()`; `m.setNotice`.

- [ ] **Step 1: Write the failing tests.**

Add to `internal/ui/helpers_test.go`:
```go
func TestOpenCommand(t *testing.T) {
	cases := []struct {
		goos, wantName string
	}{
		{"darwin", "open"},
		{"windows", "explorer"},
		{"linux", "xdg-open"},
		{"freebsd", "xdg-open"},
	}
	for _, c := range cases {
		name, args := openCommand(c.goos, "/some/dir")
		if name != c.wantName || len(args) != 1 || args[0] != "/some/dir" {
			t.Errorf("openCommand(%q) = %q %v, want %q [/some/dir]", c.goos, name, args, c.wantName)
		}
	}
}
```
Add to `internal/ui/model_test.go`:
```go
func TestOpenFolderNotices(t *testing.T) {
	// not ready: no on-disk path yet
	fe := &fakeEngine{statuses: []engine.Status{
		{Name: "A", InfoHash: "a", TotalBytes: 100, CompletedBytes: 10, Path: ""},
	}}
	m := ready(New(&fakeSource{}, fe))
	m.section = sectionDownloads
	m2, _ := update(m, key("o"))
	if !strings.Contains(m2.notice, "ready") {
		t.Errorf("o with no path should notice 'not ready', got %q", m2.notice)
	}

	// deleted: path set but missing
	fe.statuses[0].Path = filepath.Join(t.TempDir(), "gone")
	m3, _ := update(m, key("o"))
	if !strings.Contains(m3.notice, "deleted") || !m3.noticeErr {
		t.Errorf("o on a missing path should notice 'deleted' (err), got %q err=%v", m3.notice, m3.noticeErr)
	}

	// existing dir → returns a command (folder open)
	dir := t.TempDir()
	fe.statuses[0].Path = dir
	_, cmd := update(m, key("o"))
	if cmd == nil {
		t.Error("o on an existing folder should return an open command")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail.**

Run: `go test ./internal/ui/ -run 'OpenCommand|OpenFolderNotices' -v`
Expected: FAIL — `openCommand` / `o` handler undefined.

- [ ] **Step 3: Add the opener helpers.**

In `internal/ui/helpers.go`, add the imports `"os/exec"` and `"runtime"` (if not present), and:
```go
// openCommand returns the OS file-manager command + args to open path.
func openCommand(goos, path string) (name string, args []string) {
	switch goos {
	case "darwin":
		return "open", []string{path}
	case "windows":
		return "explorer", []string{path}
	default:
		return "xdg-open", []string{path}
	}
}

// openInFileManager opens path in the OS file manager, detached so a slow or
// failed file manager never blocks the caller.
func openInFileManager(path string) error {
	name, args := openCommand(runtime.GOOS, path)
	return exec.Command(name, args...).Start()
}
```

- [ ] **Step 4: Add the command, message, and `o` handler.**

In `internal/ui/model.go`, add near `removeCmd`:
```go
type folderOpenedMsg struct{ err error }

func openFolderCmd(dir string) tea.Cmd {
	return func() tea.Msg { return folderOpenedMsg{err: openInFileManager(dir)} }
}

// openSelectedFolder opens the selected torrent's folder, or sets a notice when
// it isn't ready or is missing. dir is Path if it's a directory, else its parent.
func (m *Model) openSelected(s engine.Status) tea.Cmd {
	if s.Path == "" {
		m.setNotice("download folder isn't ready yet")
		return nil
	}
	fi, err := os.Stat(s.Path)
	if err != nil {
		m.setNotice("folder not found — it may have been deleted")
		m.noticeErr = true
		return nil
	}
	dir := s.Path
	if !fi.IsDir() {
		dir = filepath.Dir(s.Path)
	}
	return openFolderCmd(dir)
}
```
Add the `os`, `path/filepath` imports to `model.go` if missing. Add the key case (next to `p`/`x`):
```go
	case "o":
		// Assign the command first: openSelected has a pointer receiver and sets
		// the notice on m, so it must run before `return m` copies m.
		switch m.section {
		case sectionDownloads:
			ds := m.downloading()
			if len(ds) > 0 && m.dlCursor < len(ds) {
				cmd := m.openSelected(ds[m.dlCursor])
				return m, cmd
			}
		case sectionSeeding:
			ss := m.seeding()
			if len(ss) > 0 && m.seedCursor < len(ss) {
				cmd := m.openSelected(ss[m.seedCursor])
				return m, cmd
			}
		}
		return m, nil
```
Add message handling in `Update` (next to `removedMsg`):
```go
	case folderOpenedMsg:
		if msg.err != nil {
			m.setNotice("couldn't open the folder")
			m.noticeErr = true
		}
		return m, nil
```
Note: `openSelected` has a pointer receiver and mutates `m` (`setNotice`/`noticeErr`); call it as `m.openSelected(...)` — `m` is addressable in the `Update` value receiver, so the mutation is preserved on the returned `m`.

- [ ] **Step 5: Add the footer + help hints.**

In `internal/ui/view.go` `renderFooter`, add `hint("o", "open")` to the Downloads case (after `hint("↑↓","move")`) and the Seeding case (after `hint("↑↓","move")`):
```go
	case m.section == sectionDownloads:
		parts = []string{hint("↑↓", "move"), hint("o", "open"), hint("p", "pause/resume"), hint("x", "cancel"), hint("tab", "panes"), hint("?", "help"), hint("q", "quit")}
	...
	case m.section == sectionSeeding:
		parts = []string{hint("↑↓", "move"), hint("o", "open"), hint("p", "pause/resume"), hint("x", "stop"), hint("tab", "panes"), hint("?", "help"), hint("q", "quit")}
```
In `helpView`, add a row (after the `{"x", ...}` row):
```go
		{"o", "open the download's folder"},
```

- [ ] **Step 6: Full gate + commit.**

Run: `go build ./... && go vet ./... && go test ./... -race && gofmt -l internal/`
Expected: build/vet clean, all tests `ok`, gofmt clean. (Commit.)

---

## Self-Review Notes

- **Spec coverage:** `Status.Path` (Task 1); Seeding cursor + `p` pause/resume + paused render (Task 2); `x` stop + confirm + files-kept `Remove(...,false)` (Task 3); `o` open folder with not-ready/deleted notices + footer/help (Task 4). All spec sections mapped.
- **Type consistency:** `pauseToggleCmd(engine.Engine, engine.Status) tea.Cmd` (Task 2) reused by `p` in both panes; `stopConfirm`/`stopTarget`/`handleStopKey` (Task 3) mirror `cancelConfirm`/`cancelTarget`/`handleCancelKey`; `openCommand(goos, path)`/`openInFileManager`/`openFolderCmd`/`folderOpenedMsg`/`openSelected` (Task 4) consistent; `Status.Path` (Task 1) consumed in Task 4.
- **Green-at-each-task:** Task 2 refactors the `p` handler and adds the Seeding footer case; Task 3 edits the same Seeding footer case (adds `x`) and the `x` handler; Task 4 edits both footer cases (adds `o`). Run tasks in order. The `fakeEngine` may already record removed-hash — Task 3 Step 1 says to check before adding fields.
- **Note:** `openInFileManager` uses `exec.Command(...).Start()` (detached), so a real file manager launch is never awaited; tests only exercise `openCommand` (pure) and the notice/command-returned paths, never launching a real opener.
