# Sequential download mode + `shoal stream` — design spec

Issue: #39 (subtask of #37). Approved by the maintainer on 2026-08-16.

## Problem

Video files download in rarest-first piece order, so they aren't playable until
the download is nearly done. qBittorrent-style sequential downloading makes a
file playable almost immediately; the roadmap already plans `shoal stream`
("sequential piece priority so you can play a file while it downloads, e.g.
pipe the path to mpv").

## Approach (decided)

Priority-window sequential in the engine + a path-printing `stream` command.
Files land on disk via the engine's file storage, so a player simply reads the
growing file; no HTTP serving, no reader plumbed through the daemon. An
HTTP-range alternative was considered and rejected (new network surface in the
daemon for marginal seeking gain).

## 1. Engine

- Per-torrent `Sequential bool`, persisted in the queue store (like file-selection
  globs), applied on add/restore and toggleable live: `SetSequential(infoHash
  string, on bool) error` on the engine.
- A pure, unit-testable planner (sibling of `planQueue`) computes the piece
  priorities for one sequential torrent from plain inputs (per-file piece
  ranges, piece completion bitmap, selection):
  - For each **selected** file: the file's first and last pieces get the top
    grade (mp4 `moov` atoms live at either end), then the earliest incomplete
    pieces up to a ~32 MiB window get high priority; remaining selected pieces
    normal.
  - Deselected files are never touched (they stay `PiecePriorityNone`).
- The engine applies the planner's output on its existing poll cadence for
  torrents with `Sequential == true`, and re-applies after metadata arrives or
  file selection changes (same hooks per-file selection uses).
- **API verification point** (anacrolix/torrent v1.61.0): the exact per-piece
  priority setter (`t.Piece(i).SetPriority(...)` or equivalent) and the piece
  completion query. The implementer verifies against the vendored module before
  building on it.

## 2. Daemon RPC

Mirror the `SetFiles` pattern exactly:
- `protocol.go`: `SetSequentialArgs{InfoHash string, On bool}`.
- `server.go`: `Engine.SetSequential` method, optional-interface guard like
  file selection uses.
- `client.go`: `Client.SetSequential(infoHash string, on bool) error`.
- `engine.Status` gains `Sequential bool` so CLI `status` and the TUI can show it.

## 3. CLI

- `shoal download --sequential`: enable on add.
- `shoal sequential <id|prefix> on|off`: toggle on an existing torrent
  (id resolution reuses the existing infohash-prefix helper).
- `shoal stream <id|magnet> [--files <glob>]`:
  1. Resolves the target: existing torrent by id/prefix, or adds a magnet.
  2. Enables sequential mode.
  3. Picks the target file: `--files` glob if given, else the largest file with
     a video extension (fall back to the largest file).
  4. Waits, printing a progress line to **stderr**, until the target file is
     "playable": its first ~8 MiB (or the whole file if smaller) and its last
     piece are complete.
  5. Prints the absolute on-disk file path to **stdout** and exits 0. The
     download continues in the daemon. `mpv "$(shoal stream <id>)"` is the
     canonical use.
  - Errors (no daemon, no match, deselected target) exit non-zero with a
    message on stderr; `--json` not needed (path-on-stdout IS the script
    interface).

## 4. TUI

- `s` in the Downloads pane toggles sequential on the selected download
  (optimistic toggle + revert on RPC error, like file selection).
- A `▶ sequential` tag in the download's detail line; footer hint gains `s`.
- No Settings entry: the mode is per-torrent, not global.

## 5. Testing

TDD throughout (failing test first):
- Planner: pure unit tests (window math, first+last piece priority, selection
  interplay, small files, single-piece files).
- Engine: persistence round-trip of the flag; re-apply on metadata/selection.
- Daemon/CLI: fake-engine tests for the RPC and all three commands; `stream`
  playability-wait tested against synthetic completion states (no real network).
- TUI: keybind toggle + render tests (tag shown, footer hint).

## 6. Docs

README: move Streaming from "Still planned" to "Shipped" with the three CLI
surfaces and the TUI keybind; roadmap keeps HTTP-range serving unmentioned
(YAGNI).

## Out of scope

Global always-sequential setting; HTTP serving; playback integration beyond
printing the path; subtitle handling (#41); open-file shortcut (#40).
