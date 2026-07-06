# Per-file selection — design

**Date:** 2026-07-06
**Status:** approved (pre-implementation)

## Summary

Let the user choose *which files* of a multi-file torrent to download — pick
episodes from a season pack, skip sample/extras files in a movie release. Two
surfaces, one engine mechanism: interactive checkboxes in the TUI details
screen, and `shoal download --files <glob>` / `shoal files <id>` in the CLI.
Selection is changeable **live** (toggle while downloading) and **persists**
across restarts.

Built on anacrolix per-file priority: `File.Download()` selects, and
`File.SetPriority(PiecePriorityNone)` deselects (stops fetching its pieces).

## Selection model

- Every torrent starts with **all files selected** — unchanged default behavior.
- The source of truth is a per-torrent **set of deselected file paths**. Paths
  are stable, human-meaningful, and survive restarts. All three entry points
  (add-time `--files`, TUI toggle, CLI `files --only`) converge on this set.
- Deselecting a file → `SetPriority(PiecePriorityNone)`. Reselecting →
  `File.Download()`.
- A fully-deselected torrent downloads nothing (valid state; it just idles).

## Component 1 — Engine (`internal/engine`)

- `FileDetail` gains `Selected bool` (the `Detail` RPC already returns files;
  this adds the flag driving ✓/✗).
- New methods on a `fileSelector` interface, kept **off** the `Engine`
  interface like `Detail`/`Reorder`, so fakes needn't implement it:

  ```go
  SetFiles(infoHash string, paths []string, selected bool) error
  SetFileGlobs(infoHash string, globs []string) error
  ```

  `SetFiles` flips the given files' priority live and updates the persisted
  deselected set. Unknown paths are ignored (no error). Before metadata
  (`GotInfo`) there are no files, so it is a no-op. `SetFileGlobs` records
  add-time patterns to resolve on `GotInfo` (see Persistence).

- Anacrolix `Detail` sets `FileDetail.Selected` from `f.Priority() !=
  PiecePriorityNone`.

### Persistence (`internal/queue`)

`queue.Entry` gains two fields:

- `FileGlobs []string` — the add-time `--files` patterns. Kept until metadata
  resolves them the first time, then cleared. Needed so a magnet added with
  `--files` still applies the selection if the daemon restarts before metadata
  arrives.
- `Deselected []string` — the persisted deselected file paths (the live state).

On `GotInfo` for a torrent:
1. If `FileGlobs` is non-empty, resolve them → deselect every file **not**
   matching any glob, record those paths in `Deselected`, clear `FileGlobs`.
2. Else re-apply `Deselected` (restart path).

A live `SetFiles` call updates `Deselected` and persists immediately.

## Component 2 — Glob matching (`internal/glob`, new small package)

Pure, dependency-free, unit-tested:

```go
func Match(patterns []string, filePath string) bool
```

- Patterns come comma-separated from one `--files` value; `Match` takes the
  already-split list (OR semantics).
- Case-insensitive (lowercase both sides).
- Each pattern is tried against **both** the full path and the basename via
  stdlib `path.Match`, so `*.mkv` catches nested files by basename and
  `Season 1/*` matches by path.
- An empty pattern list matches nothing (caller decides default).

## Component 3 — CLI (`cmd/shoal`)

- `shoal download <target> --files '<glob>[,<glob>]'` — after the normal add,
  the CLI calls a new `SetFileGlobs(infoHash, globs)` RPC (chosen over new
  add-args to keep `AddMagnet`/`AddTorrentURL` unchanged). The daemon stores the
  globs on the torrent and applies them when metadata arrives, so it works for
  magnets.
- `shoal files <id>` — lists files as a table: `#  ✓/✗  SIZE  PATH` (via
  `Detail`). Reuses the existing `printTable` helper.
- `shoal files <id> --only '<glob>'` — select exactly the matches, deselect the
  rest (resolve against `Detail`, then `SetFiles`). `--select`/`--deselect`
  additive variants are optional follow-ons, not required for v1.

## Component 4 — TUI (`internal/ui`)

The details screen (built in the previous batch, currently read-only) becomes
interactive for the file list:

- `↑ ↓` move a cursor over the files; the selected file is highlighted.
- `space` toggles the file under the cursor: optimistic ✓/✗ flip + a `SetFiles`
  command, then a `Detail` refetch to reconcile.
- ✓/✗ rendered per file from `FileDetail.Selected`.
- `esc` closes (unchanged). Footer: `↑↓ move · space select · esc back`.
- Trackers section and the file cap stay as-is; the cursor only ranges over
  files.

## Daemon RPC (`internal/daemon`)

- `SetFilesArgs{ InfoHash string; Paths []string; Selected bool }` → server
  type-asserts a `fileSelector` (like `detailer`/`reorderer`); client + poller
  methods mirror the existing pattern.
- `SetFileGlobs(infoHash, globs)` RPC (server type-asserts the same
  `fileSelector`) — the CLI calls it right after `download` to register add-time
  `--files` patterns; the engine stores them in `queue.Entry.FileGlobs` and
  resolves on `GotInfo`.

## Testing

- **glob**: nested paths, basename match, multiple comma-separated patterns,
  case-insensitivity, empty list.
- **engine**: `SetFiles` persistence round-trip via `queue.Store`; `Detail`
  reports `Selected` correctly.
- **queue**: `FileGlobs`/`Deselected` round-trip through Save/Load.
- **TUI**: render-anchored test — `space` on a file toggles its rendered ✓/✗ and
  issues a `SetFiles` command; the fake engine records it.
- **CLI**: `--files`/`--only` resolve globs to the expected file set; `files`
  listing renders the table with ✓/✗.

## Out of scope (this spec)

- Streaming / sequential download (separate roadmap item; composes later).
- Additive `files --select`/`--deselect` (optional follow-on).
- Per-file *priority ordering* (only binary select/deselect here).

## Compatibility / risks

- Old `config.json`/`queue.json` without the new fields load fine (absent →
  empty; all files selected).
- CLI `--files` on a magnet is deferred until metadata; `shoal files <id>`
  shows "waiting for metadata" (empty file list) until then.
- Glob `path.Match` has no `**`; matching basename + full path covers the common
  cases without a globbing dependency.
