# Shared-engine daemon — Phase 2: CLI becomes a daemon client

**Date:** 2026-07-04
**Component:** `cmd/shoal` (`cli_download.go`, `cli_status.go`, `state.go`, platform files), docs
**Status:** Approved (design)
**Part of:** the TUI↔CLI live-sync project (Phase 2 of 4). Phase 1 (the `internal/daemon` RPC library + `shoal daemon` command) is merged.

## Summary

Point the CLI at the daemon. `shoal download` connects to a (auto-started) daemon
and adds the torrent to its shared engine instead of spawning a detached per-download
worker; `shoal status` reads the daemon's live `Statuses()`. This retires the
`active/*.json` worker model. All CLI downloads now share one persistent engine and
show up together in `status`. (The TUI still runs its own engine until Phase 3.)

## Goals

- `shoal download` adds the torrent to the daemon (auto-starting it if needed) and returns immediately.
- `shoal status` reports the daemon's live download state.
- Remove the detached-worker + `active/*.json` machinery.
- Keep the search → short-id → download flow (the `last-search.json` cache) working.

## Non-goals (deferred)

- Wiring the **TUI** to the daemon (Phase 3) — the TUI keeps its own engine this phase.
- Daemon auto-shutdown / `daemon stop` / `daemon status`, Windows transport, bind-first mutual exclusion, graceful drain (Phase 4).
- Per-torrent download folders (`--out`) — the shared engine has one data dir; `--out` is dropped (see §4).
- Pausing/removing torrents from the CLI beyond `status --clear` (possible later; not now).

## Decisions (from brainstorming)

- **Auto-start:** `download`/`status` transparently spawn a detached `shoal daemon` when none is running, then connect.
- **`--out`:** dropped. Downloads land in the configured data dir; passing `--out` prints a one-line notice pointing at Settings/config.

## 1. `ensureDaemon` (auto-start helper) — `cmd/shoal/cli_daemon.go`

```go
// ensureDaemon returns a client connected to the daemon, auto-starting a detached
// `shoal daemon` if none is running.
func ensureDaemon() (*daemon.Client, error)
```

- `daemon.Dial(daemon.SocketPath())`; on success return the client.
- On failure: `os.Executable()` → spawn `<exe> daemon` detached (reuse `detachSysProcAttr()`; stdio → `…/shoal/logs/daemon.log`), then poll `daemon.Dial` for up to ~5s (e.g. 25×200ms) until it connects. Return the client, or an error ("could not start shoal daemon; see …/logs/daemon.log") if it never comes up.
- Windows: `runtime.GOOS == "windows"` → return an error ("the shoal daemon is not yet supported on Windows"), since the daemon transport is unix-only until Phase 4.

## 2. `shoal download` — `cmd/shoal/cli_download.go`

`runDownload(args []string, out io.Writer) int`:

- Flags: `--out <dir>` (accepted for back-compat; if non-empty, print to stderr: `note: downloads use the configured folder (change it in Settings or config.json)` and otherwise ignore it). Drop the hidden `--worker`/`--id` flags.
- Positional arg → `resolveTarget(arg, lookupCache-closure)` (unchanged: magnet / `.torrent` URL / 40-hex infohash / 8-hex short-id from `last-search.json`).
- `eng, err := ensureDaemon()`; on error → stderr, exit 1. `defer eng.Close()`.
- Add: magnet targets → `eng.AddMagnet(tgt.Magnet)`; URL targets → `eng.AddTorrentURL(tgt.URL, "")`. On error → stderr, exit 1.
- Print `started: <displayName(tgt)>` (+ ` (<handle>)` when the handle/infohash is known, matching today's line). Return 0.
- **Removed:** `downloadWorker`, `spawnWorker`, `runWorker`, `stepWorker`, `firstStatus`.

## 3. `shoal status` — `cmd/shoal/cli_status.go`

`runStatus(args []string, out io.Writer) int`:

- Flags: `--json`, `--clear`; optional positional `<id>` (infohash prefix, case-insensitive).
- Connect: `daemon.Dial(daemon.SocketPath())`. **No daemon → not an error:** print `no downloads` (text) / `[]` (json), exit 0. (A read must not auto-start a daemon.)
- `client.Statuses()` → filter to entries whose `InfoHash` has the `<id>` prefix if given.
- **Classification from `engine.Status`** (`statusState(s engine.Status) string`):
  - `s.Done && s.Seeding` → `seeding`
  - `s.Done` → `done`
  - `s.Paused` → `paused`
  - else → `downloading`
- Output rows: short id (`InfoHash[:8]`), name, `percent` (`s.Percent()`), `completed/total`, peers, state. `--json`: array of `{id, name, info_hash, percent, completed, total, peers, state, seeding, done, path}`.
- `--clear`: for each `Done` torrent, `client.Remove(s.InfoHash, false)` (stop/forget, keep files); then print the remaining list.
- **Removed:** the `Active`-based `stateOf`/`printStatus`, `pidAlive`.

## 4. Trim the retired state — `cmd/shoal/state.go`, platform files

- Remove the `Active` struct and `writeActive`/`readActive`/`listActive`/`clearFinished`/`activeDir`. **Keep** `cacheEntry`, `writeCache`, `lookupCache`, `cachePath`, `configDir` (the search short-id cache is still used).
- `platform_unix.go` / `platform_windows.go`: remove `pidAlive` (no longer used); **keep** `detachSysProcAttr` (used by `ensureDaemon`).

## 5. Docs — `.claude/skills/shoal-download/SKILL.md`, `README.md`

- Skill: downloads now go to a shared background daemon (auto-started); `status` reads it; note that `--out` is gone (configured folder). The search → pick → `download` → poll `status` flow is unchanged from the caller's view.
- README "Command line" section: drop `--out` from `download`; add a line that CLI downloads run in a shared `shoal daemon` (auto-started), so multiple downloads and `status` all share one engine.

## 6. Known limitation (documented, resolved in Phase 3)

The TUI still creates its own engine in Phase 2. Because anacrolix locks the
data-dir's piece-completion DB (and binds the configured port), running the TUI and
a CLI-started daemon simultaneously conflicts — the same TUI-vs-CLI-worker conflict
that exists today, not a regression. If the TUI is open, an auto-started daemon will
fail to bind and `download` reports the engine error. Phase 3 makes the TUI a daemon
client, removing the second engine and the conflict.

## 7. Testing (TDD)

Against an **in-process fake daemon** — a fake `engine.Engine` served by `daemon.Serve`
on a temp unix socket, with `SHOAL_DAEMON_SOCK` set so the CLI connects to it (no real
daemon, no network):

- **download:** `runDownload` on a magnet forwards `AddMagnet` to the fake engine; on a URL forwards `AddTorrentURL`; a short-id resolves via the cache then forwards; a passed `--out` writes the notice to stderr and still downloads.
- **status:** scripted `Statuses()` → assert the table + `--json` shapes and the `statusState` classification (downloading/done/seeding/paused); `<id>` prefix filter; `--clear` removes done torrents (fake records the `Remove` call); the no-daemon path prints `no downloads` / `[]` and exits 0.
- **statusState** unit table (each state).
- **ensureDaemon:** the already-running path returns a client (pre-listen a socket at `SHOAL_DAEMON_SOCK`); the real detached spawn is not unit-tested.
- Delete the retired worker/active/`stateOf` tests.

## Files touched

- `cmd/shoal/cli_download.go` — daemon-backed `runDownload`; remove worker funcs.
- `cmd/shoal/cli_daemon.go` — add `ensureDaemon`.
- `cmd/shoal/cli_status.go` — daemon-backed `runStatus` + `statusState`; remove `Active` printing.
- `cmd/shoal/state.go` — trim to the search cache.
- `cmd/shoal/platform_unix.go` / `platform_windows.go` — remove `pidAlive`.
- Tests: `cli_download_test.go`, `cli_status_test.go`, `state_test.go` updated; `cli_daemon_test.go` gains the `ensureDaemon` already-running test.
- `.claude/skills/shoal-download/SKILL.md`, `README.md` — updated.

## Open questions

None. Auto-start, dropping `--out`, the daemon-backed status classification, and the
retirement of the worker/active model are all decided; TUI wiring and daemon lifecycle
hardening are explicitly later phases.
