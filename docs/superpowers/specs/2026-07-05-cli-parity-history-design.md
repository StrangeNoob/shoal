# Full CLI client + download-history management (CLI & TUI)

**Date:** 2026-07-05
**Component:** `cmd/shoal` (new CLI commands), `internal/ui` (TUI history delete), `internal/history`, `internal/engine`, new `internal/opener`, docs
**Status:** Approved (design)
**Builds on:** the shared-engine daemon (Phases 1–4, released v0.7.0). The daemon `Client` already exposes `Pause`/`Resume`/`Remove(hash, deleteData)`/`Statuses`; `history.json` is the shared completed-downloads log.

## Summary

Close the CLI↔TUI gap: give the CLI the download **history** plus the control
actions the TUI has (pause/resume, cancel/remove with keep-or-delete-files, open
folder), and add **history deletion** — records-only by default, with an opt-in
`--delete-files` — to **both** the CLI and the TUI. Today the CLI has
`search`/`download`/`status`/`sources`; the TUI additionally shows history and can
pause/resume/cancel/open. After this, both surfaces can do all of it, and either
can prune the history log.

## Goals

- CLI commands mirroring the TUI's engine/history capabilities: `history` (list + delete), `pause`, `resume`, `remove`, `open`.
- Delete download history from the CLI **and** the TUI; `--delete-files` (CLI) / `[d]` (TUI) also removes the files, records-only otherwise.
- File deletion is containment-safe (never deletes outside the configured data dir), reusing the engine's proven guard.
- No new module dependencies (stdlib + already-vendored).

## Non-goals (deferred)

- CLI equivalents of purely-visual TUI features (theme, splash, live nav, sort chips) — those are UI-only; engine/data features are what parity means here.
- Editing engine settings from dedicated CLI commands — settings are already editable via `config.json` and the TUI Settings pane; not re-exposed as CLI verbs.
- Search/download/status/sources — already exist; unchanged except `status` keeps its role.

## Decisions (from brainstorming)

- **Delete = records only by default; `--delete-files` (CLI) / `[d]` (TUI) also deletes the files.** Files on disk are never touched without the explicit flag/choice.
- **Delete-history is added to BOTH the CLI and the TUI** (symmetry; same `history.json`).
- **Single `remove <id>` verb** (no separate `cancel`) — it's the same `daemon.Remove(hash, deleteData)` the TUI's `x` uses; works on an in-progress download or a seeder.

## 1. Shared helpers

### `internal/history` — delete ops (new)
Today the package has only `Append`. Add:
- `func (s *Store) Remove(infoHash string) bool` — drop the entry with that infohash (exact match), returns whether one was removed, then `Save`.
- `func (s *Store) Clear()` — empty `Entries`, then `Save`.

### `internal/engine` — export the containment guard
`removeUnderDir(dir, name string) error` (in `anacrolix.go`) refuses any path escaping `dir`. Add a thin exported wrapper so the CLI/TUI can safely delete a history entry's files when its torrent is no longer in the daemon:
```go
// RemoveUnderDir deletes name within dir, refusing any path that escapes dir.
func RemoveUnderDir(dir, name string) error { return removeUnderDir(dir, name) }
```

### `internal/opener` — file-manager open (extracted)
The OS file-manager launch (`open`/`xdg-open`/`explorer`) currently lives in
`internal/ui` (`openCommand`/`openInFileManager`), which pulls in Bubble Tea. Extract
it to a tiny stdlib-only `internal/opener` package: `func Open(path string) error`
(builds the per-OS command and `Start()`s it, detached). `internal/ui` delegates to
it (no behavior change); the CLI uses it directly without importing `internal/ui`.

## 2. File-deletion rule (used by CLI `--delete-files` and TUI `[d]`)

For a history entry `e` (has `InfoHash`, `Name`, `Path`), deleting its files:
1. If the daemon is running **and** has a torrent with `e.InfoHash` → `daemon.Remove(e.InfoHash, deleteData=true)` (cleanly stops the torrent and deletes via the engine's `removeUnderDir`).
2. Otherwise → `engine.RemoveUnderDir(cfg.DataDir, e.Name)` (direct, containment-safe; refuses anything escaping the data dir; no-op if `e.Name` is empty).
Then always `history.Remove(e.InfoHash)` + `Save`.
Records-only deletion skips steps 1–2 entirely (just `history.Remove`/`Clear` + `Save`).

## 3. CLI commands — `cmd/shoal`

`<id>` is a case-insensitive **infohash prefix**; for daemon actions it must resolve
to exactly one live torrent (0 → "no such download", exit 1; >1 → "ambiguous id …",
exit 1). `pause`/`resume`/`remove`/`open` act on the running daemon and do **not**
auto-start it (no daemon → "no active downloads", exit 0 — same policy as `status`).
`history` reads/writes `history.json` directly (works with no daemon).

- **`shoal history [--json]`** — list completed downloads from `history.json`: short id (`InfoHash[:8]`), name, size, completed-at, path. `--json` emits an array. Empty → "no history".
- **`shoal history rm <id> [--delete-files]`** — remove the matching entry (infohash prefix, unique). `--delete-files` applies §2. Prints what it removed.
- **`shoal history clear [--delete-files]`** — wipe the log. `--delete-files` applies §2 to every entry. Prints the count.
- **`shoal pause <id>`** / **`shoal resume <id>`** — `daemon.Pause`/`Resume` on the resolved live torrent.
- **`shoal remove <id> [--delete-files]`** — `daemon.Remove(hash, deleteData=--delete-files)`; stops an in-progress download or a seeder (keep files by default). (`status --clear` still bulk-removes done torrents.)
- **`shoal open <id>`** — resolve the folder (live `Status.Path` → history `Entry.Path` → `filepath.Join(cfg.DataDir, name)`), then `opener.Open(path)`; if no opener is available/fails (headless/ssh), print the path to stdout instead.

Routing: `cli()` in `main.go` gains `history`, `pause`, `resume`, `remove`, `open`
cases; `history` dispatches its `rm`/`clear` sub-commands like `daemon` does `stop`/`status`.

## 4. TUI delete-history — `internal/ui`

The Seeding pane already lists active seeders then HISTORY rows (`seeding()` +
`seedHistory()`, one `seedCursor`; `o` already distinguishes them at `model.go:786-792`).
Add delete on a **history row**:
- `x` on a history row (`seedCursor >= len(seeding())`) opens a new confirm (a `histConfirm bool` + `histTarget history.Entry`, mirroring the existing `stopConfirm`/`handleStopKey`): `[k]` remove entry (keep files) · `[d]` remove entry + delete files · `[esc]` cancel.
- `[k]` → `history.Remove(hash)` + `Save`, then refresh `m.history`. `[d]` → the §2 file-deletion (live torrent → `removeCmd(hash, deleteData=true)` (existing); else `engine.RemoveUnderDir(cfg.DataDir, e.Name)`), then `history.Remove` + refresh.
- `x` on an **active seeder** row is unchanged (`stopConfirm` = stop seeding, files kept). The handler picks by cursor, exactly as `o` already does.
- The `?` help + the Seeding footer hint gain the history-row `x` action.

## 5. Docs

- `.claude/skills/shoal-download/SKILL.md` (+ its embedded copy `cmd/shoal/skill/SKILL.md`, kept byte-identical) and `README.md`: document `history`/`history rm`/`history clear`, `pause`/`resume`/`remove`/`open`, and `--delete-files`.

## 6. Testing (TDD)

- **`internal/history`:** `Remove` (removes the right entry, returns false when absent), `Clear` (empties + saves), both round-trip through `LoadFrom`.
- **`internal/engine`:** `RemoveUnderDir` is exported and still refuses escaping names (the existing `removeUnderDir` containment test already covers the guard; add a one-line export smoke).
- **`internal/opener`:** `Open` builds the right per-OS command (test the command construction, not a real launch); ui still opens via the delegate.
- **CLI (`cmd/shoal`)** against the in-process fake daemon (`serveFakeDaemon`) + an isolated history file (`t.Setenv` HOME/XDG): `pause`/`resume`/`remove` forward the right RPC (fake records it); `remove --delete-files` forwards `deleteData=true`; ambiguous/no-match id → exit 1; no-daemon → "no active downloads", exit 0. `history` lists entries (+ `--json` shape); `history rm`/`clear` mutate the log; `--delete-files` deletes files under a temp data dir and **refuses** a path escaping it; `open` prints the path when no opener is available.
- **TUI (`internal/ui`)** via the model harness: `x` on a history row opens the confirm; `[k]` removes the entry (files kept); `[d]` removes + triggers file deletion; `x` on an active seeder still opens the stop confirm.
- Full `go build ./...`, `go test ./...`, `-race` where already applied, `gofmt -l`, `GOOS=windows go build ./cmd/shoal/` stay green.

## 7. Files touched

- `internal/history/history.go` — `Remove`, `Clear`.
- `internal/engine/anacrolix.go` — exported `RemoveUnderDir` wrapper.
- `internal/opener/opener.go` (new) — `Open`; `internal/ui` delegates to it.
- `cmd/shoal/` — new `cli_history.go`, `cli_control.go` (pause/resume/remove/open) + tests; `main.go` routing; reuse `resolveTarget`/id helpers.
- `internal/ui/model.go` (+ `view.go`) — history-row `x` delete confirm + help/hint.
- `.claude/skills/shoal-download/SKILL.md` (+ embedded copy), `README.md`.

## 8. Known limitations (documented)

- Deleting a history entry whose torrent is still **seeding** in the daemon: with records-only, the entry may reappear if the daemon restarts and re-records the still-Done torrent (its in-memory "recorded" set resets on restart). Rare and self-explanatory; `--delete-files`/`remove` avoids it by dropping the torrent.
- `open` reveals a GUI file manager; over ssh/headless it prints the path instead (no display to open into).

## 9. Open questions

None. Delete semantics (records + opt-in files), TUI-and-CLI scope, the single `remove` verb, and the containment-safe file deletion are all decided.
