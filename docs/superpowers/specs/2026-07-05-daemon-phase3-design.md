# Shared-engine daemon — Phase 3: TUI becomes a daemon client (full sync)

**Date:** 2026-07-05
**Component:** `cmd/shoal/main.go`, `internal/ui/model.go`, docs
**Status:** Approved (design)
**Part of:** the TUI↔CLI live-sync project (Phase 3 of 4). Phases 1 (daemon + client) and 2 (CLI → daemon) are merged.

## Summary

Point the TUI at the daemon instead of its own engine. The TUI already depends
only on the `engine.Engine` interface, so it receives a `*daemon.Client` in place
of `*engine.Anacrolix`. With the TUI and CLI now driving the same engine,
**downloads added from either surface appear in both, live — this is the sync**
that motivated the whole project. Because the daemon (Phase 2) is the single owner
of the engine and already records completions to history, the TUI stops recording
history itself and instead reloads it from disk to display the daemon's records.

## Goals

- The TUI adds/removes/pauses torrents and reads status through the shared daemon.
- Downloads and seeding are consistent between the TUI and `shoal` CLI, live.
- The TUI's History pane reflects the daemon's completion records.
- The TUI auto-starts the daemon (reusing `ensureDaemon`), like the CLI.

## Non-goals (deferred)

- Windows support for the daemon-backed TUI — the daemon transport is unix-only until Phase 4, so the TUI **errors on Windows** this phase (documented limitation).
- Applying engine settings (port/peers/data-dir/seed) live — they configure the daemon and take effect at daemon (re)start; `daemon stop`/restart is Phase 4.
- Any change to the daemon, the RPC protocol, or the CLI (Phases 1–2 unchanged).
- A daemon-side history RPC — the TUI reads `history.json` directly (the shared file the daemon writes).

## Decisions (from brainstorming)

- **Require the daemon everywhere:** the TUI always uses the daemon (no local-engine fallback). On Windows (or if the daemon can't start), the TUI prints the error and exits.
- **Daemon is the sole history recorder;** the TUI reloads `history.json` to display it (never appends).

## 1. TUI uses the daemon (`cmd/shoal/main.go`)

Replace the TUI path's engine wiring:

- Remove `os.MkdirAll(cfg.DataDir, …)`, `engine.NewAnacrolix(engine.Config{…})`, and the `queue.DefaultPath()` reference from `main()` (the daemon owns the data dir, engine, and queue).
- Add `eng, err := ensureDaemon()` (the existing helper in `cli_daemon.go`); on error, `fatal(err)` — this covers Windows ("the shoal daemon is not yet supported on Windows") and a daemon that fails to start.
- `defer eng.Close()` stays — for a `*daemon.Client` it closes only the connection, so the daemon (and its downloads/seeding) outlives the TUI.
- `ui.NewWithConfig(src, eng, cfg).WithHistory(history.Load()).WithVersion(v)` is unchanged (`eng` is now the client; it satisfies `engine.Engine`).
- Imports: `engine` and `queue` may become unused in `main.go` after this change — drop them if so (they remain used elsewhere in the package, e.g. `cli_daemon.go`, so no package-wide removal).

## 2. History: reload instead of record (`internal/ui/model.go`)

In the `tickMsg` handler, the loop currently does:

```go
for _, s := range newlyCompleted(m.statuses, next) {
    m.history.Append(history.Entry{InfoHash: s.InfoHash, Name: s.Name, Size: s.TotalBytes, CompletedAt: now, Path: s.Path})
}
```

Replace it so the TUI no longer writes history (the daemon does) but reflects new
completions by reloading the shared file:

```go
if len(newlyCompleted(m.statuses, next)) > 0 {
    m.history = history.Load() // the daemon recorded them; refresh the History pane
}
```

- `newlyCompleted` is still computed (to know *when* to reload). Speed sampling
  (`m.dlSpeed`/`m.ulSpeed`) and the rest of the tick are unchanged.
- Startup still seeds the pane via `WithHistory(history.Load())` in `main.go`.
- Because the TUI is always daemon-backed (decision above), there is no local-engine
  path that would need to record history — the removal is unconditional.

## 3. Engine settings (documentation + a Settings note)

The Settings pane keeps its engine rows (Save to, When done, Seed ratio, Max peers,
Listen port), which persist to `config.json`. They now configure the **daemon** and
apply at daemon (re)start, since the TUI no longer owns the engine. Add:

- A one-line note in the Settings pane render (e.g. under the DOWNLOADS/engine group)
  that engine settings apply when the daemon restarts.
- A README line to the same effect.

Appearance settings (theme, color mode) still apply live — unchanged.

## 4. Data flow (the sync)

- **TUI add** (`d` / paste magnet) → `eng.AddMagnet`/`AddTorrentURL` → daemon engine.
- **TUI pause/cancel** (`p`/`x`) → `eng.Pause`/`Resume`/`Remove` → daemon engine.
- **TUI tick** → `eng.Statuses()` → daemon's live state (includes torrents the CLI added).
- **CLI `download`** → same daemon → appears in the TUI's Downloads pane on the next tick.
- **CLI `status`** → same daemon → shows torrents the TUI added.
- **Completion** → daemon records to `history.json` → TUI reloads it (this phase) and `shoal status`/history reflect it.

## 5. Testing (TDD)

- **model tick reloads history, never appends:** with the existing injected fake
  `engine.Engine`, script a torrent going `Done`; assert the TUI does **not** add a
  history entry via `Append`, and that after a completion it reloads from the shared
  file. Isolate the history file with `t.Setenv("HOME", …)` + `XDG_CONFIG_HOME`
  (as the config/CLI tests do) and pre-write a `history.json` to prove the reload
  picks up the daemon's records. Adjust/replace the existing history-on-tick test.
- The engine-backed model tests (search flow, pause/cancel, seeding) already inject
  a fake `engine.Engine` and keep working unchanged.
- `main.go`'s `ensureDaemon` wiring is an integration concern (not unit-tested), same
  as the CLI's `download` path; the model tests never touch a real daemon.
- Full `go build ./...`, `go test ./...`, `gofmt -l`, and `GOOS=windows go build ./cmd/shoal/` stay green.

## Files touched

- `cmd/shoal/main.go` — TUI uses `ensureDaemon()`; drop the local engine wiring.
- `internal/ui/model.go` — tick reloads history instead of appending.
- `internal/ui/view.go` — a one-line "engine settings apply at daemon restart" note in Settings (small).
- `README.md` — note the TUI runs on the shared daemon; engine settings apply at daemon restart; Windows TUI pending Phase 4.
- Tests: `internal/ui/model_test.go` (history-on-tick test updated).

## Known limitations (documented)

- **Windows:** the daemon-backed TUI errors and exits (unix-socket transport only until Phase 4). The binary still cross-compiles.
- **Engine settings** need a daemon (re)start to take effect; convenient restart lands in Phase 4.

## Open questions

None. The daemon-everywhere engine selection, the sole-recorder history model (TUI
reloads), and the engine-settings-at-daemon-restart behavior are all decided; the
Windows path and daemon lifecycle are explicitly Phase 4.
