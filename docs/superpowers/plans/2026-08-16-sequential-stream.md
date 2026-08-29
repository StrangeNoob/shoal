# Sequential Download + `shoal stream` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** qBittorrent-style sequential piece priority per torrent, plus `shoal stream <id|magnet>` that prints the file's path once it's playable.

**Architecture:** A pure piece-priority planner in the engine applied on the existing poll path; a `Sequential` flag persisted in the queue store and flowing engine → daemon RPC → CLI → TUI along the exact path per-file selection (PR #36) built. `stream` is a CLI-side wait on extended per-file Detail data; playback is the player's job (file storage means the player reads the growing file).

**Tech Stack:** Go, anacrolix/torrent v1.61.0, net/rpc daemon, Bubble Tea TUI.

**Spec:** docs/superpowers/specs/2026-08-16-sequential-stream-design.md

## Global Constraints

- TDD: every behavior lands test-first; RED evidence (command + failing output) then GREEN in each task report.
- All existing tests keep passing: `go test ./...`; `go vet ./...` clean; `gofmt -l .` prints nothing before each commit.
- Commit messages conventional (`feat:`/`fix:`/`docs:`), NO Co-Authored-By / "Generated with" trailers.
- Mirror the per-file-selection (PR #36) plumbing patterns exactly where named; minimal diffs, match surrounding style.
- Verified anacrolix v1.61 APIs (do not re-derive): `t.Piece(i).SetPriority(prio)` (piece.go:270), `t.PieceState(i).Complete`, `f.BeginPieceIndex()/f.EndPieceIndex()` (file.go:210/215), `f.SetPriority(prio)` (file.go:192), `f.State() []FilePieceState` (file.go:149). Priorities: `PiecePriorityNone/Normal/High/Readahead/Next/Now`.
- `stream` prints ONLY the file path to stdout; all progress/messages go to stderr (command-substitution contract: `mpv "$(shoal stream <id>)"`).

---

### Task 1: Pure sequential piece planner

**Files:**
- Create: `internal/engine/sequential.go`
- Test: `internal/engine/sequential_test.go`

**Interfaces:**
- Consumes: nothing (pure; only `torrent.PiecePriority` constants from `github.com/anacrolix/torrent`).
- Produces (Task 2 relies on these exact names):
  ```go
  // pieceSpan is one selected file's piece range [begin, end).
  type pieceSpan struct{ begin, end int }
  // seqWindowBytes is the rolling high-priority window (~32 MiB).
  const seqWindowBytes = int64(32) << 20
  // planSequential returns the priorities to apply for a sequential torrent:
  // per selected file, its first and last incomplete pieces -> PiecePriorityNow,
  // then the earliest incomplete pieces (up to windowBytes) -> PiecePriorityHigh.
  // Pieces not in the map are left untouched.
  func planSequential(spans []pieceSpan, complete func(i int) bool, pieceLen, windowBytes int64) map[int]torrent.PiecePriority
  ```

- [ ] **Step 1: Write the failing tests**

```go
package engine

import (
	"testing"

	"github.com/anacrolix/torrent"
)

func noneComplete(int) bool  { return false }

func TestPlanSequentialFirstAndLastPieceNow(t *testing.T) {
	// one file spanning pieces [10, 20)
	got := planSequential([]pieceSpan{{10, 20}}, noneComplete, 1<<20, 4<<20)
	if got[10] != torrent.PiecePriorityNow {
		t.Fatalf("first piece = %v, want Now", got[10])
	}
	if got[19] != torrent.PiecePriorityNow {
		t.Fatalf("last piece = %v, want Now", got[19])
	}
}

func TestPlanSequentialWindowFromEarliestIncomplete(t *testing.T) {
	// pieces 10..13 already complete; window = 4 pieces of 1 MiB
	complete := func(i int) bool { return i >= 10 && i <= 13 }
	got := planSequential([]pieceSpan{{10, 30}}, complete, 1<<20, 4<<20)
	for i := 14; i <= 17; i++ {
		if got[i] != torrent.PiecePriorityHigh {
			t.Fatalf("piece %d = %v, want High (window)", i, got[i])
		}
	}
	if _, ok := got[18]; ok {
		t.Fatalf("piece 18 beyond window should be untouched, got %v", got[18])
	}
	if _, ok := got[10]; ok {
		t.Fatalf("complete piece 10 should be untouched (no first-piece bump needed)")
	}
	if got[29] != torrent.PiecePriorityNow {
		t.Fatalf("incomplete last piece = %v, want Now", got[29])
	}
}

func TestPlanSequentialSingleAndTinyFiles(t *testing.T) {
	// single-piece file: first == last -> one Now entry, no window duplicates
	got := planSequential([]pieceSpan{{5, 6}}, noneComplete, 1<<20, 4<<20)
	if len(got) != 1 || got[5] != torrent.PiecePriorityNow {
		t.Fatalf("single-piece file: got %v, want {5:Now}", got)
	}
	// empty span slice -> empty plan
	if got := planSequential(nil, noneComplete, 1<<20, 4<<20); len(got) != 0 {
		t.Fatalf("no spans should plan nothing, got %v", got)
	}
}

func TestPlanSequentialMultipleFilesIndependentWindows(t *testing.T) {
	got := planSequential([]pieceSpan{{0, 10}, {10, 20}}, noneComplete, 1<<20, 2<<20)
	for _, want := range []int{0, 9, 10, 19} {
		if got[want] != torrent.PiecePriorityNow {
			t.Fatalf("piece %d = %v, want Now (file boundary)", want, got[want])
		}
	}
	// each file gets its own 2-piece window past the boundary pieces
	if got[1] != torrent.PiecePriorityHigh || got[11] != torrent.PiecePriorityHigh {
		t.Fatalf("window pieces 1/11 should be High, got %v/%v", got[1], got[11])
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/engine/ -run TestPlanSequential -v`
Expected: FAIL — `undefined: planSequential` / `pieceSpan`.

- [ ] **Step 3: Implement `internal/engine/sequential.go`**

```go
package engine

import "github.com/anacrolix/torrent"

// pieceSpan is one selected file's piece range [begin, end).
type pieceSpan struct{ begin, end int }

// seqWindowBytes is the rolling high-priority window for sequential mode.
const seqWindowBytes = int64(32) << 20

// planSequential returns the priorities to apply for a sequential torrent.
// Per selected file: the first and last incomplete pieces get PiecePriorityNow
// (video containers keep metadata at either end), then the earliest incomplete
// pieces up to windowBytes get PiecePriorityHigh. Complete pieces and pieces
// beyond the window are omitted — the engine leaves them untouched.
func planSequential(spans []pieceSpan, complete func(i int) bool, pieceLen, windowBytes int64) map[int]torrent.PiecePriority {
	plan := make(map[int]torrent.PiecePriority)
	for _, sp := range spans {
		if sp.end <= sp.begin {
			continue
		}
		if !complete(sp.begin) {
			plan[sp.begin] = torrent.PiecePriorityNow
		}
		if last := sp.end - 1; last != sp.begin && !complete(last) {
			plan[last] = torrent.PiecePriorityNow
		}
		var budget int64
		for i := sp.begin; i < sp.end && budget < windowBytes; i++ {
			if complete(i) {
				continue
			}
			if _, boundary := plan[i]; !boundary {
				plan[i] = torrent.PiecePriorityHigh
			}
			budget += pieceLen
		}
	}
	return plan
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/engine/ -run TestPlanSequential -v` → PASS; then `go test ./internal/engine/`.

- [ ] **Step 5: Commit**

`git add internal/engine/sequential.go internal/engine/sequential_test.go && git commit -m "feat: pure sequential piece planner (first/last Now + rolling High window)"`

---

### Task 2: Engine flag, persistence, Status/Detail fields, priority application

**Files:**
- Modify: `internal/engine/engine.go` (Status + FileDetail fields), `internal/engine/anacrolix.go`, `internal/queue/queue.go` (Entry field only)
- Test: `internal/engine/anacrolix_test.go`, `internal/queue/queue_test.go`

**Interfaces:**
- Consumes (Task 1): `planSequential`, `pieceSpan`, `seqWindowBytes`.
- Produces (Tasks 3-6 rely on these exact names):
  - `engine.Status.Sequential bool` — with comment noting it reports sequential mode.
  - `engine.FileDetail.HeadBytes int64` (contiguous complete bytes from file start) and `engine.FileDetail.TailDone bool` (file's last piece complete) — `stream` playability inputs.
  - Anacrolix engine method: `func (e *Anacrolix) SetSequential(infoHash string, on bool) error` (exact receiver name: match the file's existing methods). Unknown hash = no-op nil, like Pause.
  - `queue.Entry.Sequential bool` with tag `json:"sequential,omitempty"`.

- [ ] **Step 1: Failing tests.** In `internal/queue/queue_test.go`, extend the existing round-trip test style: an Entry with `Sequential: true` survives Save/LoadFrom. In `internal/engine/anacrolix_test.go`, follow the file's existing fake/backend test style (read the file first): (a) `SetSequential` on an unknown hash returns nil; (b) after `SetSequential(h, true)`, `Statuses()` reports `Sequential: true` for that torrent and the queue store entry has `Sequential: true`; after `SetSequential(h, false)` both flip back. (c) `FileDetail` head/tail: if the file's existing Detail tests use a real in-memory torrent, assert `HeadBytes == 0`/`TailDone == false` for an empty torrent and that the fields exist; deeper coverage lives in the pure planner + CLI fake tests.
- [ ] **Step 2: Run to verify failures** (`go test ./internal/queue/ ./internal/engine/`). Expected: compile errors for the new fields/method.
- [ ] **Step 3: Implement.**
  - `queue.Entry`: add `Sequential bool \`json:"sequential,omitempty"\`` after `Paused`.
  - `engine.Status`: add `Sequential bool` after `Queued` with a one-line comment. `engine.FileDetail`: add `HeadBytes int64` and `TailDone bool` with one-line comments.
  - `anacrolix.go`:
    - Track sequential infohashes (mirror however paused state is tracked — read the file; likely a map guarded by the engine mutex). `SetSequential` sets/clears it, persists via the queue store the way `SetFileGlobs` persists, and on disable resets every **selected** file to `PiecePriorityNormal` via `f.SetPriority` (deselected files stay None).
    - Restore the flag on startup where queue entries are re-added (where `Paused`/`FileGlobs` are restored).
    - Apply the plan: in the same place `planQueue`'s holds/releases are applied on the poll path (locate the `planQueue` call in anacrolix.go), for each sequential torrent with metadata: build `spans` from its selected files via `f.BeginPieceIndex()/f.EndPieceIndex()` (skip files with `f.Priority() == torrent.PiecePriorityNone`), `complete := func(i int) bool { return t.PieceState(i).Complete }`, `pieceLen` from the torrent info, then `for i, p := range planSequential(spans, complete, pieceLen, seqWindowBytes) { t.Piece(i).SetPriority(p) }`. Also re-apply immediately at the end of `SetSequential(h, true)`, after the metadata-arrival hook (where `DownloadAll` + deselection run, ~line 409), and after `SetFiles`/`SetFileGlobs`.
    - Detail (~line 600 where `FileDetail` is built): compute `HeadBytes` by walking `f.State()` from the start while `ps.Complete` (`HeadBytes += ps.Bytes`; stop at first incomplete) and `TailDone` from the last element of `f.State()`. **API verification point:** confirm `FilePieceState` field names (`Bytes`, `PieceState`) in v1.61 before using.
- [ ] **Step 4: Run to verify pass:** `go test ./internal/engine/ ./internal/queue/`, then full `go test ./...`.
- [ ] **Step 5: Commit** — `feat: engine sequential mode (persisted flag, poll-path priority application, playability detail fields)`.

---

### Task 3: Daemon RPC + TUI toggle

**Files:**
- Modify: `internal/daemon/protocol.go`, `internal/daemon/server.go`, `internal/daemon/client.go`, `internal/ui/model.go`, `internal/ui/view.go`
- Test: `internal/daemon/daemon_test.go`, `internal/ui/model_test.go`, `internal/ui/view_test.go`

**Interfaces:**
- Consumes (Task 2): `SetSequential(infoHash string, on bool) error` on the engine; `Status.Sequential`.
- Produces (Tasks 4-5 rely on): `daemon.SetSequentialArgs{InfoHash string, On bool}`; RPC method `Engine.SetSequential`; `func (c *Client) SetSequential(infoHash string, on bool) error`.

- [ ] **Step 1: Failing tests.** Daemon: mirror the existing `SetFiles` RPC round-trip test in `daemon_test.go` (fake engine records the call; client method reaches it; an engine without the optional interface returns the same "unsupported" error `SetFiles` uses). TUI: (a) pressing `s` in the Downloads pane on a selected download calls the engine's `SetSequential` with the row's infohash and toggled value (extend the `fakeEngine` in the ui tests the way the file-toggle tests did); (b) a Status with `Sequential: true` renders a `▶ sequential` tag in its detail line in `renderDownloads`; (c) on RPC error the optimistic toggle reverts (mirror `TestDetailToggleRevertsOnError`).
- [ ] **Step 2: Run to verify failures.**
- [ ] **Step 3: Implement.** Protocol/server/client: copy the `SetFiles` triple exactly (args struct, optional-interface guard in `server.go` — add `SetSequential(infoHash string, on bool) error` to the optional interface or a sibling one matching the file's pattern — client wrapper). TUI: key `s` in the Downloads-section key handling (near the `p` pause handling), optimistic `Sequential` flip on the local status + async cmd + revert-on-error message, mirroring the file-selection toggle flow; `renderDownloads` detail `switch` gains a `s.Sequential` tag `  ·  ▶ sequential` (ordered after paused/queued, before speed); footer Downloads row gains `hint("s", "sequential")`.
- [ ] **Step 4: Full verify** (`go test ./...`, vet, gofmt).
- [ ] **Step 5: Commit** — `feat: SetSequential RPC + TUI toggle ('s' in Downloads, ▶ sequential tag)`.

---

### Task 4: CLI — `download --sequential` and `shoal sequential <id> on|off`

**Files:**
- Modify: `cmd/shoal/cli_download.go`, `cmd/shoal/main.go` (command registration — read how `files` registers)
- Create: `cmd/shoal/cli_sequential.go`
- Test: `cmd/shoal/cli_download_test.go`, `cmd/shoal/cli_sequential_test.go`

**Interfaces:**
- Consumes (Task 3): `Client.SetSequential`. Reuse the existing id/infohash-prefix resolution helper `cli_control.go`/`cli_files.go` use (read those files; use the same helper by name).
- Produces: commands `shoal download --sequential ...` and `shoal sequential <id|prefix> on|off`.

- [ ] **Step 1: Failing tests** against the package's `fake_engine_test.go` harness (read it first; extend the fake with a recorded `SetSequential`): (a) `download --sequential <magnet>` adds then calls `SetSequential(h, true)`; (b) `sequential <prefix> on` resolves the prefix and calls with `true`; `off` with `false`; (c) unknown id exits non-zero with the same not-found wording sibling commands use; (d) `sequential` with a bad third arg (not on/off) errors with usage.
- [ ] **Step 2: Verify failures.**
- [ ] **Step 3: Implement** following `cli_files.go` structure (flag parsing, daemon client acquisition, output style). `--sequential` on download: after the add succeeds and the infohash is known (the `--files` flow already resolves it — same spot), call `SetSequential(h, true)`.
- [ ] **Step 4: Full verify.**
- [ ] **Step 5: Commit** — `feat: CLI sequential toggles (download --sequential, shoal sequential on|off)`.

---

### Task 5: CLI — `shoal stream`

**Files:**
- Create: `cmd/shoal/cli_stream.go`
- Test: `cmd/shoal/cli_stream_test.go`
- Modify: `cmd/shoal/main.go` (registration)

**Interfaces:**
- Consumes: `Client.SetSequential` (Task 3); `Client.Detail` + `FileDetail.HeadBytes/TailDone/Path/Length` (Task 2); magnet add + id resolution as in Task 4.
- Produces: `shoal stream <id|magnet> [--files <glob>]`.

- [ ] **Step 1: Failing tests** (fake engine, synthetic Detail states, no network):
  - target pick: multi-file Detail → largest file with extension in `{.mkv,.mp4,.avi,.webm,.mov,.m4v}` wins; no video extension → largest file; `--files` glob (reuse `internal/glob`) overrides; glob matching nothing → non-zero exit, stderr message.
  - playability wait: fake Detail advances `HeadBytes`/`TailDone` across polls → command polls until `HeadBytes >= min(streamHeadBytes, Length) && TailDone`, then prints exactly the absolute file path + `\n` to stdout (nothing else on stdout) and exits 0. `const streamHeadBytes = int64(8) << 20`.
  - deselected target file (`Selected == false`) → error suggesting `shoal files`.
  - magnet argument → engine add called, then sequential enabled.
  Poll with a short injectable interval so tests run fast (mirror how `download --wait` tests inject timing — read `cli_download_test.go` first).
- [ ] **Step 2: Verify failures.**
- [ ] **Step 3: Implement.** Structure mirrors `download --wait`'s follow loop (progress line to stderr, ticker poll of the daemon). Absolute path: **verification point** — read how `Status.Path` is built in anacrolix.go and how `FileDetail.Path` relates (torrent-name prefix or not) and join with the engine data dir accordingly; the tests must pin the joined result.
- [ ] **Step 4: Full verify.**
- [ ] **Step 5: Commit** — `feat: shoal stream — sequential download, print path once playable`.

---

### Task 6: Docs

**Files:**
- Modify: `README.md` (roadmap: move Streaming to Shipped, document `--sequential`, `sequential`, `stream`, TUI `s`), `.claude/skills/shoal-download/SKILL.md` if it lists CLI commands (check; update only if it does).

- [ ] **Step 1:** Update the Shipped list with one bullet in the established voice; document the three CLI surfaces in the command sections where `files`/`download --files` are documented; add `s` to the TUI keys table if README has one.
- [ ] **Step 2:** `go test ./...` (docs-only, still run), gofmt no-op.
- [ ] **Step 3: Commit** — `docs: document sequential mode and shoal stream; roadmap update`.
