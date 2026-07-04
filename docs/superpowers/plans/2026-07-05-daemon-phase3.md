# Shared-engine daemon Phase 3 — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Point the TUI at the shared daemon (instead of its own engine) so downloads/seeding/status are consistent between the TUI and CLI, live.

**Architecture:** The TUI already depends only on `engine.Engine`, so `main.go` hands it a `*daemon.Client` from `ensureDaemon()` in place of `*engine.Anacrolix`. The daemon is the sole engine owner and history recorder (Phase 2), so the TUI stops appending history and instead reloads `history.json` when a torrent completes.

**Tech Stack:** Go, stdlib, Bubble Tea TUI, the `internal/daemon` client, existing `internal/engine`/`internal/history`.

## Global Constraints

- Go; stdlib + already-vendored deps only — **no new module dependencies**.
- TDD: write the failing test first. Commits carry **no Claude attribution**.
- The TUI **always** uses the daemon (`ensureDaemon()`); on error (incl. Windows) it `fatal`s. No local-engine fallback.
- The daemon is the sole history recorder; the TUI **reloads** `history.json` on completion, never appends.
- Engine settings apply at daemon (re)start; a Settings/README note says so.
- `gofmt -l` clean (CI enforces it); run `go vet`, `go build ./...`, `go test ./...`, and `GOOS=windows go build ./cmd/shoal/` before each commit.

**Interfaces already present:** `ensureDaemon() (*daemon.Client, error)` (in `cmd/shoal/cli_daemon.go`) returns a client implementing `engine.Engine`. `history.Load() history.Store`, `(*history.Store).Append`, `history.Entry{InfoHash, Name string; Size int64; CompletedAt time.Time; Path string}`. `newlyCompleted(prev, next []engine.Status) []engine.Status` (in `internal/ui/model.go`). The Model holds `eng engine.Engine`, `history history.Store`; its tick handler polls `m.eng.Statuses()`.

---

### Task 1: TUI reloads history on completion instead of recording it

**Files:**
- Modify: `internal/ui/model.go` (the `tickMsg` handler)
- Test: `internal/ui/model_test.go` (replace `TestTickRecordsHistory`)

**Interfaces:**
- Consumes: `newlyCompleted`, `history.Load`, the Model's `history`/`eng` fields.
- Produces: a tick that sets `m.history = history.Load()` on new completions (no `Append`).

- [ ] **Step 1: Write the failing test**

Replace `TestTickRecordsHistory` (currently around `internal/ui/model_test.go:832`) with:

```go
func TestTickReloadsHistoryOnCompletion(t *testing.T) {
	// Isolate the history file; the daemon (simulated by the pre-write below) is the recorder.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, ".config"))
	seed := history.Load()
	seed.Append(history.Entry{InfoHash: "disk1", Name: "FromDaemon"}) // writes history.json

	eng := &fakeEngine{statuses: []engine.Status{{Name: "Movie", InfoHash: "hh", TotalBytes: 2048}}}
	m := ready(New(&fakeSource{}, eng))
	t0 := time.Unix(1_000_000, 0)
	m, _ = update(m, tickMsg(t0)) // not done → no reload

	eng.statuses = []engine.Status{{Name: "Movie", InfoHash: "hh", TotalBytes: 2048, CompletedBytes: 2048, Done: true}}
	m, _ = update(m, tickMsg(t0.Add(time.Second))) // completes → TUI reloads history from disk

	// The TUI must NOT append the completed torrent ("hh"); it reloads the daemon's file ("disk1").
	if len(m.history.Entries) != 1 || m.history.Entries[0].InfoHash != "disk1" {
		t.Fatalf("expected reload from disk (disk1), got %+v", m.history.Entries)
	}
}
```

Add `"path/filepath"` to `internal/ui/model_test.go`'s import block if not already present.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ui/ -run TestTickReloadsHistoryOnCompletion -v`
Expected: FAIL — the current tick appends `"hh"` (so `m.history.Entries` contains `hh`, not the reloaded `disk1`).

- [ ] **Step 3: Write minimal implementation**

In `internal/ui/model.go`, in the `case tickMsg:` handler, replace the history-append loop:

```go
			for _, s := range newlyCompleted(m.statuses, next) {
				m.history.Append(history.Entry{InfoHash: s.InfoHash, Name: s.Name, Size: s.TotalBytes, CompletedAt: now, Path: s.Path})
			}
```

with a reload on completion:

```go
			if len(newlyCompleted(m.statuses, next)) > 0 {
				m.history = history.Load() // the daemon records completions; refresh the History pane
			}
```

(`history` is still imported — `history.Load` and the `history.Store` type are used; `history.Entry` is no longer referenced in this file but the import stays valid.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/ui/ -run 'TestTickReloads|TestNewlyCompleted|TestTickComputesTransferSpeeds' -v` then the full `go test ./internal/ui/`, `go build ./...`, `go vet ./internal/ui/`, `gofmt -l internal/ui/` (empty).
Expected: PASS; the other tick tests (speeds, newlyCompleted) still pass.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/model.go internal/ui/model_test.go
git commit -m "TUI: reload history on completion (daemon is the recorder), don't append"
```

---

### Task 2: TUI runs on the daemon (`cmd/shoal/main.go`)

**Files:**
- Modify: `cmd/shoal/main.go` (the TUI path in `main()`)

**Interfaces:**
- Consumes: `ensureDaemon()` (returns `*daemon.Client`, implements `engine.Engine`), `config.Load`, `source.NewDefault`, `history.Load`, `ui.NewWithConfig`.
- Produces: a TUI backed by the shared daemon.

- [ ] **Step 1: Make the change**

In `cmd/shoal/main.go`'s `main()`, replace the local-engine wiring. The current block is:

```go
	cfg := config.Load()

	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		fatal(err)
	}

	eng, err := engine.NewAnacrolix(engine.Config{
		DataDir:    cfg.DataDir,
		ListenPort: cfg.ListenPort,
		MaxPeers:   cfg.MaxPeers,
		Seed:       cfg.Seed,
		SeedRatio:  cfg.SeedRatio,
		QueuePath:  queue.DefaultPath(),
	})
	if err != nil {
		fatal(fmt.Errorf("starting torrent engine: %w", err))
	}
	defer eng.Close()
```

Replace it with:

```go
	cfg := config.Load()

	// The TUI drives the shared daemon (auto-started), so it stays in sync with the
	// CLI. ensureDaemon errors on Windows (unix-socket transport only, until Phase 4)
	// and when the daemon can't start.
	eng, err := ensureDaemon()
	if err != nil {
		fatal(err)
	}
	defer eng.Close() // closes the connection only; the daemon and its downloads persist
```

(`ui.NewWithConfig(src, eng, cfg).WithHistory(history.Load()).WithVersion(v)` and the rest of `main()` stay unchanged — `eng` is now the daemon client, which satisfies `engine.Engine`.)

- [ ] **Step 2: Fix imports**

Removing `engine.NewAnacrolix`/`engine.Config` and `queue.DefaultPath()` from `main.go` makes the `github.com/StrangeNoob/shoal/internal/engine` and `github.com/StrangeNoob/shoal/internal/queue` imports unused **in this file**. Remove both from `main.go`'s import block. (They remain used elsewhere in the package — `cli_daemon.go` — so don't touch those.) Keep `os`, `fmt`, `config`, `source`, `history`, `ui`, `update`, `tea` etc.

- [ ] **Step 3: Verify it builds and the suite passes**

Run: `go build ./...` (must be clean — no unused imports), `go vet ./cmd/shoal/`, `go test ./cmd/shoal/ ./internal/ui/`, `GOOS=windows go build ./cmd/shoal/`, `gofmt -l cmd/shoal/` (empty).
Expected: all clean. (`main()`'s TUI path is not unit-tested — it launches Bubble Tea — so verification is the build, the existing `cli()` routing tests, and `ensureDaemon`'s own Phase-2 tests. A live TUI↔CLI smoke happens after the branch is complete.)

- [ ] **Step 4: Commit**

```bash
git add cmd/shoal/main.go
git commit -m "TUI runs on the shared daemon (ensureDaemon), not its own engine"
```

---

### Task 3: Settings note + docs

**Files:**
- Modify: `internal/ui/view.go` (a note in the Settings render)
- Modify: `README.md`

**Interfaces:** none (UI copy + docs).

- [ ] **Step 1: Add the Settings note**

In `internal/ui/view.go`'s `renderSettings`, the `about` slice (always rendered at the bottom of the pane) currently looks like:

```go
	about := []string{
		"",
		st.SectionHead.Render("ABOUT"),
		"  " + st.SetLabel.Render(padOrTrim("shoal", 13)) + "  " + st.Meta.Render(upd.DisplayVersion(m.version)+"  ·  anacrolix engine"),
	}
```

Add a note line so users know engine settings need a daemon restart:

```go
	about := []string{
		"",
		st.SectionHead.Render("ABOUT"),
		"  " + st.SetLabel.Render(padOrTrim("shoal", 13)) + "  " + st.Meta.Render(upd.DisplayVersion(m.version)+"  ·  anacrolix engine"),
		"  " + st.Meta.Render("engine settings (port, peers, save-to, seed) apply when the daemon restarts"),
	}
```

- [ ] **Step 2: Update the README**

In `README.md`, add to the **Command line (scripting)** / daemon area (near where the shared daemon is described) a short note:

> The TUI runs on the same shared `shoal daemon` as the CLI, so downloads and
> seeding stay in sync between them. Engine settings (listen port, max peers,
> save-to, seed) configure the daemon and take effect when it restarts.
> (The daemon-backed TUI is unix/macOS only for now; Windows support is planned.)

- [ ] **Step 3: Verify + commit**

Run: `go build ./...` and `go test ./internal/ui/` (the extra `about` line doesn't break `TestRenderSettingsPane`, which asserts on "ABOUT" and specific rows, all still present), `gofmt -l internal/ui/` (empty).

```bash
git add internal/ui/view.go README.md
git commit -m "docs: note engine settings apply at daemon restart; TUI shares the daemon"
```

---

## Self-Review

**Spec coverage:**
- TUI uses `ensureDaemon()`, `fatal` on error, `defer eng.Close()` connection-only → Task 2. ✓
- Drop local engine wiring / `os.MkdirAll` / `queue.DefaultPath` / engine+queue imports → Task 2. ✓
- History: reload on completion, never append → Task 1. ✓
- Settings note + README (engine settings at daemon restart; Windows pending) → Task 3. ✓
- Existing model tests keep working (fake engine injected) → verified in Tasks 1–2. ✓
- Windows still cross-compiles → Task 2 step 3. ✓
- Sync data flow (Add/Pause/Remove/Statuses via the daemon) is inherent once `eng` is the client — no code change needed in the TUI's engine calls. ✓

**Placeholder scan:** none — Tasks 1/2 carry complete before/after code; Task 3's note text is exact.

**Type consistency:** `ensureDaemon() (*daemon.Client, error)` matches its Phase-2 definition; `history.Load()`/`history.Store`/`history.Entry` and `newlyCompleted` match their existing signatures; the Model's `eng engine.Engine`/`history history.Store` fields are unchanged, so the daemon client and the reload both type-check. The removed `history.Entry` reference in `model.go` is the only symbol dropped, and `history` stays imported for `Load`/`Store`.
