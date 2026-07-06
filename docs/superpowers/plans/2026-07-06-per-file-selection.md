# Per-file selection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the user choose which files of a multi-file torrent to download — live-toggleable in the TUI details screen and via `shoal download --files <glob>` / `shoal files <id>` in the CLI — persisted across restarts.

**Architecture:** A per-torrent set of deselected file *paths* is the source of truth, persisted in `queue.Entry`. Anacrolix enforces it via per-file priority (`File.Download()` / `SetPriority(PiecePriorityNone)`). A pure glob matcher resolves `--files` patterns to paths. New engine methods (`SetFiles`, `SetFileGlobs`) sit on a `fileSelector` interface kept off `engine.Engine` (type-asserted, like the existing `Detail`/`Reorder`), threaded through the daemon RPC like those.

**Tech Stack:** Go 1.24, anacrolix/torrent v1.61.0, net/rpc (jsonrpc) over a unix socket, Bubble Tea TUI.

## Global Constraints

- Go 1.24+; `go build ./...`, `go vet ./...`, `go test ./...`, and **`gofmt -l .` (must be empty)** all clean before every commit — CI fails on non-gofmt-clean files.
- No new third-party dependencies (stdlib `path.Match` for globbing).
- Commit style: no Claude attribution / Co-Authored-By trailer.
- New per-torrent RPC methods stay **off** the `engine.Engine` interface (type-asserted in the daemon server), so `daemon.Client`, the UI/CLI fakes, and any minimal engine need not implement them.
- Cross-compile must stay green: `GOOS=windows go build ./...`.

---

## File Structure

- `internal/glob/glob.go` (new) — pure glob matcher `Match(patterns []string, filePath string) bool`.
- `internal/glob/glob_test.go` (new) — matcher tests.
- `internal/queue/queue.go` (modify) — add `FileGlobs`/`Deselected` to `Entry`; `SetFileGlobs`, `SetDeselected` store methods.
- `internal/queue/queue_test.go` (modify) — persistence round-trip.
- `internal/engine/engine.go` (modify) — `FileDetail.Selected` field.
- `internal/engine/files.go` (new) — pure `resolveDeselected(paths, globs) []string`; its test in `files_test.go`.
- `internal/engine/anacrolix.go` (modify) — `SetFiles`, `SetFileGlobs`, `Detail.Selected`, apply-on-`GotInfo`.
- `internal/daemon/protocol.go` (modify) — `SetFilesArgs`, `SetFileGlobsArgs`.
- `internal/daemon/server.go` (modify) — `fileSelector` type-assert + two handlers.
- `internal/daemon/client.go` (modify) — `SetFiles`, `SetFileGlobs` client methods.
- `cmd/shoal/daemon_poller.go` (modify) — poller `SetFiles`, `SetFileGlobs`.
- `cmd/shoal/cli_files.go` (new) — `shoal files <id>` (+ `--only`).
- `cmd/shoal/cli_files_test.go` (new) — files-listing / `--only` glob resolution.
- `cmd/shoal/cli_download.go` (modify) — `--files` flag → `SetFileGlobs` after add.
- `cmd/shoal/main.go` (modify) — dispatch `files`; usage line.
- `internal/ui/model.go` (modify) — details-screen file cursor, `space` toggle, `fileSelector`/`setFilesCmd`.
- `internal/ui/view.go` (modify) — render ✓/✗ + cursor in `dlDetailView`; footer.
- `internal/ui/model_test.go` (modify) — toggle test + fake `SetFiles`.

---

## Task 1: Glob matcher (`internal/glob`)

**Files:**
- Create: `internal/glob/glob.go`
- Test: `internal/glob/glob_test.go`

**Interfaces:**
- Produces: `func Match(patterns []string, filePath string) bool` — case-insensitive; true if any non-empty pattern matches `filePath` by full path OR basename (stdlib `path.Match`). Empty/all-empty `patterns` → false.

- [ ] **Step 1: Write the failing test**

```go
package glob

import "testing"

func TestMatch(t *testing.T) {
	cases := []struct {
		patterns []string
		path     string
		want     bool
	}{
		{[]string{"*.mkv"}, "movie.mkv", true},
		{[]string{"*.mkv"}, "Season 1/ep01.mkv", true}, // basename match crosses dirs
		{[]string{"Season 1/*"}, "Season 1/ep01.mkv", true},
		{[]string{"*.mkv"}, "notes.txt", false},
		{[]string{"*.MKV"}, "movie.mkv", true},        // case-insensitive
		{[]string{"*.srt", "*.mkv"}, "movie.mkv", true}, // OR of patterns
		{[]string{}, "movie.mkv", false},               // no patterns
		{[]string{""}, "movie.mkv", false},             // empty pattern ignored
	}
	for _, c := range cases {
		if got := Match(c.patterns, c.path); got != c.want {
			t.Errorf("Match(%v, %q) = %v, want %v", c.patterns, c.path, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/glob/`
Expected: FAIL — package/`Match` undefined.

- [ ] **Step 3: Write minimal implementation**

```go
// Package glob matches file paths against simple shell-style patterns for the
// --files selector. Case-insensitive; each pattern is tried against the whole
// path and the basename (so "*.mkv" catches nested files). No "**" — stdlib
// path.Match only.
package glob

import (
	"path"
	"strings"
)

// Match reports whether filePath matches any non-empty pattern.
func Match(patterns []string, filePath string) bool {
	lp := strings.ToLower(filePath)
	base := path.Base(lp)
	for _, p := range patterns {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		if ok, _ := path.Match(p, lp); ok {
			return true
		}
		if ok, _ := path.Match(p, base); ok {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/glob/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/glob/
git add internal/glob/
git commit -m "glob: case-insensitive path/basename matcher for --files"
```

---

## Task 2: Persist selection in `queue.Entry`

**Files:**
- Modify: `internal/queue/queue.go`
- Test: `internal/queue/queue_test.go`

**Interfaces:**
- Consumes: existing `queue.Store`/`Entry` (fields `InfoHash`, `Magnet`, `Paused`; methods `Save`, `Add`, `SetPaused`).
- Produces:
  - `Entry.FileGlobs []string` (json `file_globs,omitempty`), `Entry.Deselected []string` (json `deselected,omitempty`).
  - `func (s *Store) SetFileGlobs(infoHash string, globs []string)` — sets on the matching entry + persists.
  - `func (s *Store) SetDeselected(infoHash string, paths []string)` — sets on the matching entry + persists.

- [ ] **Step 1: Write the failing test**

First read `internal/queue/queue.go` to match the existing `Entry` struct tags and how `SetPaused` locates an entry / persists (mirror it exactly).

```go
func TestSelectionPersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "queue.json")
	s := LoadFrom(path)
	s.Add(Entry{InfoHash: "abc", Magnet: "magnet:?xt=urn:btih:abc"})

	s.SetFileGlobs("abc", []string{"*.mkv"})
	s.SetDeselected("abc", []string{"extras/sample.mkv"})

	got := LoadFrom(path)
	e := got.Entries[0]
	if len(e.FileGlobs) != 1 || e.FileGlobs[0] != "*.mkv" {
		t.Fatalf("FileGlobs = %v", e.FileGlobs)
	}
	if len(e.Deselected) != 1 || e.Deselected[0] != "extras/sample.mkv" {
		t.Fatalf("Deselected = %v", e.Deselected)
	}
}
```

(Match the test's imports/helpers to the existing `queue_test.go`; if `LoadFrom`/`Add` differ, adapt to the real API found in step-1 reading.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/queue/ -run TestSelectionPersists`
Expected: FAIL — `FileGlobs`/`SetFileGlobs` undefined.

- [ ] **Step 3: Add fields and setters**

In `Entry` (after `Paused`):

```go
	FileGlobs  []string `json:"file_globs,omitempty"`  // add-time --files patterns, until resolved
	Deselected []string `json:"deselected,omitempty"`  // file paths not being downloaded
```

Add methods (mirror `SetPaused`'s locate-and-persist exactly — same locking, same `Save`):

```go
// SetFileGlobs records add-time --files patterns for infoHash (if present) and persists.
func (s *Store) SetFileGlobs(infoHash string, globs []string) {
	s.mu.Lock()
	for i := range s.Entries {
		if s.Entries[i].InfoHash == infoHash {
			s.Entries[i].FileGlobs = globs
		}
	}
	s.mu.Unlock()
	s.Save()
}

// SetDeselected records the deselected file paths for infoHash (if present) and persists.
func (s *Store) SetDeselected(infoHash string, paths []string) {
	s.mu.Lock()
	for i := range s.Entries {
		if s.Entries[i].InfoHash == infoHash {
			s.Entries[i].Deselected = paths
		}
	}
	s.mu.Unlock()
	s.Save()
}
```

(If `SetPaused` uses a different lock field name or a non-locking `Save`, copy that exact shape instead.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/queue/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/queue/
git add internal/queue/
git commit -m "queue: persist per-torrent file selection (globs + deselected paths)"
```

---

## Task 3: Pure deselection resolver + `FileDetail.Selected`

**Files:**
- Modify: `internal/engine/engine.go` (add `Selected bool` to `FileDetail`)
- Create: `internal/engine/files.go`, `internal/engine/files_test.go`

**Interfaces:**
- Consumes: `glob.Match` (Task 1).
- Produces: `func resolveDeselected(paths, globs []string) []string` — the file paths that should be **deselected** given `--files` globs (every path not matching any glob). Empty `globs` → nil (nothing deselected).

- [ ] **Step 1: Add the `Selected` field**

In `engine.go`, `FileDetail`:

```go
type FileDetail struct {
	Path      string
	Length    int64
	Completed int64
	Selected  bool // false = deselected (priority none)
}
```

- [ ] **Step 2: Write the failing test**

`internal/engine/files_test.go`:

```go
package engine

import (
	"reflect"
	"testing"
)

func TestResolveDeselected(t *testing.T) {
	paths := []string{"movie.mkv", "extras/sample.mkv", "readme.txt"}
	// --files *.mkv keeps the two .mkv files, deselects readme.txt.
	got := resolveDeselected(paths, []string{"*.mkv"})
	if !reflect.DeepEqual(got, []string{"readme.txt"}) {
		t.Fatalf("resolveDeselected = %v, want [readme.txt]", got)
	}
	// No globs → nothing deselected.
	if got := resolveDeselected(paths, nil); got != nil {
		t.Fatalf("no globs should deselect nothing, got %v", got)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/engine/ -run TestResolveDeselected`
Expected: FAIL — `resolveDeselected` undefined.

- [ ] **Step 4: Implement**

`internal/engine/files.go`:

```go
package engine

import "github.com/StrangeNoob/shoal/internal/glob"

// resolveDeselected returns the file paths to deselect for the given --files
// globs: every path that matches none of the globs. Empty globs → nil (keep all).
func resolveDeselected(paths, globs []string) []string {
	if len(globs) == 0 {
		return nil
	}
	var out []string
	for _, p := range paths {
		if !glob.Match(globs, p) {
			out = append(out, p)
		}
	}
	return out
}
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/engine/ -run 'TestResolveDeselected|TestStatus'`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
gofmt -w internal/engine/
git add internal/engine/engine.go internal/engine/files.go internal/engine/files_test.go
git commit -m "engine: FileDetail.Selected + pure resolveDeselected for --files"
```

---

## Task 4: Engine `SetFiles`, `SetFileGlobs`, apply-on-metadata

**Files:**
- Modify: `internal/engine/anacrolix.go`

**Interfaces:**
- Consumes: `resolveDeselected` (Task 3), `queue.Store.SetFileGlobs`/`SetDeselected` (Task 2), `torrentByHash`, `a.store`, `a.mu`.
- Produces (methods on `*Anacrolix`):
  - `func (a *Anacrolix) SetFiles(infoHash string, paths []string, selected bool) error`
  - `func (a *Anacrolix) SetFileGlobs(infoHash string, globs []string) error`
  - `Detail` now sets `FileDetail.Selected` from `f.Priority() != torrent.PiecePriorityNone`.
  - `applyFileSelection(t)` called from the `GotInfo` goroutine in `track`.

This task has no standalone unit test (needs a live client); it's exercised through Task 5's RPC test and Task 7's TUI test. Verify with `go build` + full suite.

- [ ] **Step 1: Set `Selected` in `Detail`**

In `Detail`'s file loop (read the current loop first), set each file:

```go
d.Files = append(d.Files, FileDetail{
	Path:      f.DisplayPath(),
	Length:    f.Length(),
	Completed: f.BytesCompleted(),
	Selected:  f.Priority() != torrent.PiecePriorityNone,
})
```

- [ ] **Step 2: Add `SetFiles`**

```go
// SetFiles selects or deselects the named files (by DisplayPath) live, and
// persists the resulting deselected set. Unknown paths are ignored; a no-op
// before metadata (no files yet).
func (a *Anacrolix) SetFiles(infoHash string, paths []string, selected bool) error {
	a.mu.Lock()
	t, _, ok := a.torrentByHash(infoHash)
	a.mu.Unlock()
	if !ok {
		return nil
	}
	want := make(map[string]bool, len(paths))
	for _, p := range paths {
		want[p] = true
	}
	for _, f := range t.Files() {
		if !want[f.DisplayPath()] {
			continue
		}
		if selected {
			f.Download()
		} else {
			f.SetPriority(torrent.PiecePriorityNone)
		}
	}
	a.persistDeselected(t)
	return nil
}

// persistDeselected records which of t's files are currently deselected.
func (a *Anacrolix) persistDeselected(t *torrent.Torrent) {
	if a.store == nil {
		return
	}
	var off []string
	for _, f := range t.Files() {
		if f.Priority() == torrent.PiecePriorityNone {
			off = append(off, f.DisplayPath())
		}
	}
	a.store.SetDeselected(t.InfoHash().HexString(), off)
}
```

- [ ] **Step 3: Add `SetFileGlobs`**

```go
// SetFileGlobs registers add-time --files patterns. If metadata is already in,
// they're applied immediately; otherwise applyFileSelection applies them on
// GotInfo. Persisted so a restart before metadata still applies them.
func (a *Anacrolix) SetFileGlobs(infoHash string, globs []string) error {
	a.mu.Lock()
	t, _, ok := a.torrentByHash(infoHash)
	if ok && a.store != nil {
		a.store.SetFileGlobs(infoHash, globs)
	}
	a.mu.Unlock()
	if !ok {
		return nil
	}
	if t.Info() != nil { // metadata already here → apply now
		a.applyFileSelection(t)
	}
	return nil
}
```

- [ ] **Step 4: Add `applyFileSelection` and call it on `GotInfo`**

```go
// applyFileSelection deselects files per the torrent's persisted FileGlobs
// (resolved once, then cleared) or its persisted Deselected set (restart path).
func (a *Anacrolix) applyFileSelection(t *torrent.Torrent) {
	if a.store == nil {
		return
	}
	hash := t.InfoHash().HexString()
	entry, ok := a.store.Get(hash) // add Store.Get if absent (see note)
	if !ok {
		return
	}
	var deselect []string
	if len(entry.FileGlobs) > 0 {
		var paths []string
		for _, f := range t.Files() {
			paths = append(paths, f.DisplayPath())
		}
		deselect = resolveDeselected(paths, entry.FileGlobs)
		a.store.SetFileGlobs(hash, nil) // resolved once
	} else {
		deselect = entry.Deselected
	}
	off := make(map[string]bool, len(deselect))
	for _, p := range deselect {
		off[p] = true
	}
	for _, f := range t.Files() {
		if off[f.DisplayPath()] {
			f.SetPriority(torrent.PiecePriorityNone)
		}
	}
	a.persistDeselected(t)
}
```

In `track`, inside the existing `go func(){ <-t.GotInfo(); ... }()` goroutine, after the name is resolved and `a.mu.Unlock()`, add:

```go
	a.applyFileSelection(t)
```

**Note (Store.Get):** if `queue.Store` has no `Get(infoHash) (Entry, bool)`, add one in Task 2 (locate under lock, return a copy + ok). Adjust Task 2's commit to include it. It must return by value (no aliasing the stored slice).

- [ ] **Step 5: Build + full suite**

Run: `go build ./... && go test ./internal/engine/ ./internal/queue/`
Expected: PASS. Then `GOOS=windows go build ./...` clean.

- [ ] **Step 6: Commit**

```bash
gofmt -w internal/engine/ internal/queue/
git add internal/engine/anacrolix.go internal/queue/queue.go
git commit -m "engine: live SetFiles/SetFileGlobs + apply file selection on metadata"
```

---

## Task 5: Daemon RPC + CLI (`files`, `--files`)

**Files:**
- Modify: `internal/daemon/protocol.go`, `internal/daemon/server.go`, `internal/daemon/client.go`, `cmd/shoal/daemon_poller.go`, `cmd/shoal/cli_download.go`, `cmd/shoal/main.go`
- Create: `cmd/shoal/cli_files.go`, `cmd/shoal/cli_files_test.go`

**Interfaces:**
- Consumes: `engine.Detail`/`FileDetail` (has `Selected`), `daemon.Client.Detail`, `glob.Match`.
- Produces:
  - Protocol: `SetFilesArgs{InfoHash string; Paths []string; Selected bool}`, `SetFileGlobsArgs{InfoHash string; Globs []string}`.
  - `daemon.Client.SetFiles(infoHash string, paths []string, selected bool) error`, `.SetFileGlobs(infoHash string, globs []string) error`.
  - Poller `SetFiles`/`SetFileGlobs` (via `callWithTimeout`).
  - `runFiles(args []string, out io.Writer) int` — `shoal files <id> [--only <glob>] [--json]`.
  - `download --files '<glob>'` flag.

- [ ] **Step 1: Protocol + server + client + poller**

`protocol.go` (near `HashArgs`):

```go
type SetFilesArgs struct {
	InfoHash string
	Paths    []string
	Selected bool
}
type SetFileGlobsArgs struct {
	InfoHash string
	Globs    []string
}
```

`server.go` (after the `detailer`/`reorderer` handlers):

```go
type fileSelector interface {
	SetFiles(infoHash string, paths []string, selected bool) error
	SetFileGlobs(infoHash string, globs []string) error
}

func (s *EngineService) SetFiles(a SetFilesArgs, _ *Empty) error {
	if fs, ok := s.eng.(fileSelector); ok {
		return fs.SetFiles(a.InfoHash, a.Paths, a.Selected)
	}
	return nil
}
func (s *EngineService) SetFileGlobs(a SetFileGlobsArgs, _ *Empty) error {
	if fs, ok := s.eng.(fileSelector); ok {
		return fs.SetFileGlobs(a.InfoHash, a.Globs)
	}
	return nil
}
```

`client.go`:

```go
func (c *Client) SetFiles(infoHash string, paths []string, selected bool) error {
	return c.rpc.Call("Engine.SetFiles", SetFilesArgs{InfoHash: infoHash, Paths: paths, Selected: selected}, &Empty{})
}
func (c *Client) SetFileGlobs(infoHash string, globs []string) error {
	return c.rpc.Call("Engine.SetFileGlobs", SetFileGlobsArgs{InfoHash: infoHash, Globs: globs}, &Empty{})
}
```

`daemon_poller.go` (next to `Reorder`):

```go
func (p *daemonPoller) SetFiles(h string, paths []string, sel bool) error {
	return p.callWithTimeout(func(c *daemon.Client) error { return c.SetFiles(h, paths, sel) })
}
func (p *daemonPoller) SetFileGlobs(h string, globs []string) error {
	return p.callWithTimeout(func(c *daemon.Client) error { return c.SetFileGlobs(h, globs) })
}
```

- [ ] **Step 2: Write the failing CLI test**

`cmd/shoal/cli_files_test.go` — drive `runFiles` against the fake daemon (reuse `serveFakeDaemon`/`fakeEngine`; give `fakeEngine` a `Detail` returning two files, and record `SetFiles` calls). First read `fake_engine_test.go` to see current fields, then extend it in this test file only if it's the same package.

```go
func TestFilesListAndOnly(t *testing.T) {
	fake := &fakeEngine{detail: engine.Detail{Files: []engine.FileDetail{
		{Path: "movie.mkv", Length: 100, Completed: 50, Selected: true},
		{Path: "readme.txt", Length: 10, Selected: true},
	}}}
	serveFakeDaemon(t, fake)

	var buf bytes.Buffer
	if code := runFiles([]string{"abc"}, &buf); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(buf.String(), "movie.mkv") || !strings.Contains(buf.String(), "readme.txt") {
		t.Fatalf("files listing missing entries:\n%s", buf.String())
	}

	// --only '*.mkv' should deselect readme.txt (SetFiles paths=[readme.txt] selected=false)
	if code := runFiles([]string{"abc", "--only", "*.mkv"}, &buf); code != 0 {
		t.Fatalf("--only exit = %d", code)
	}
	off := fake.setFilesDeselected() // helper on the fake: paths passed with selected=false
	if len(off) != 1 || off[0] != "readme.txt" {
		t.Fatalf("--only deselected %v, want [readme.txt]", off)
	}
}
```

Give `fakeEngine` (in `fake_engine_test.go`): a `detail engine.Detail`; `Detail(h) (engine.Detail, error)`; `SetFiles`/`SetFileGlobs` that record calls; and `setFilesDeselected()` returning paths from the last `selected=false` call.

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./cmd/shoal/ -run TestFilesListAndOnly`
Expected: FAIL — `runFiles` undefined.

- [ ] **Step 4: Implement `cli_files.go`**

```go
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/StrangeNoob/shoal/internal/daemon"
	"github.com/StrangeNoob/shoal/internal/glob"
)

// runFiles: `shoal files <id> [--only <glob>] [--json]`.
func runFiles(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("files", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit JSON")
	only := fs.String("only", "", "select only files matching this glob (deselect the rest)")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return 2
	}
	if len(pos) == 0 || pos[0] == "" {
		fmt.Fprintln(os.Stderr, "usage: shoal files <id> [--only <glob>] [--json]")
		return 2
	}
	c, err := daemon.Dial(daemon.SocketPath())
	if err != nil {
		fmt.Fprintln(os.Stderr, "shoal files: no daemon running")
		return 1
	}
	defer c.Close()
	hash, ok := resolveStatusID(c, pos[0]) // see note
	if !ok {
		fmt.Fprintf(os.Stderr, "no download matches %q\n", pos[0])
		return 1
	}
	det, err := c.Detail(hash)
	if err != nil {
		fmt.Fprintln(os.Stderr, "shoal files:", err)
		return 1
	}
	if *only != "" {
		var deselect []string
		globs := splitGlobs(*only)
		for _, f := range det.Files {
			if !glob.Match(globs, f.Path) {
				deselect = append(deselect, f.Path)
			}
		}
		var sel []string
		for _, f := range det.Files {
			if glob.Match(globs, f.Path) {
				sel = append(sel, f.Path)
			}
		}
		_ = c.SetFiles(hash, sel, true)
		_ = c.SetFiles(hash, deselect, false)
		det, _ = c.Detail(hash) // refresh for display
	}
	if *jsonOut {
		b, _ := json.MarshalIndent(det.Files, "", "  ")
		fmt.Fprintln(out, string(b))
		return 0
	}
	rows := make([][]string, 0, len(det.Files))
	for i, f := range det.Files {
		mark := "✓"
		if !f.Selected {
			mark = "✗"
		}
		rows = append(rows, []string{fmt.Sprintf("%d", i+1), mark, humanBytes(f.Length), f.Path})
	}
	printTable(out, []string{"#", "SEL", "SIZE", "PATH"}, rows)
	return 0
}

// splitGlobs splits a comma-separated --files/--only value.
func splitGlobs(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
```

Add the `strings` import. **Note (`resolveStatusID`):** if no shared helper exists to turn a short id/infohash-prefix into a full infohash from `c.Statuses()`, add a small one in `cli_files.go` (iterate `c.Statuses()`, match `strings.HasPrefix(lower(InfoHash), lower(arg))`, return the full `InfoHash`). Reuse an existing helper if one is already present (check `cli_status.go`'s prefix logic / `filterStatuses`).

- [ ] **Step 5: Wire dispatch + `--files` flag**

`main.go` — add to `cli` switch and usage:

```go
	case "files":
		return true, runFiles(args[2:], out)
```
```
  shoal files <id> [--only <glob>]  list / select a torrent's files
```

`cli_download.go` — add the flag and, after a successful add of a magnet/hash/id target (i.e. when `tgt.Magnet != "" && tgt.Handle != ""`), register globs:

```go
	files := fs.String("files", "", "download only files matching this glob (comma-separated)")
	// ... after the successful Add + "started:" print:
	if *files != "" && tgt.Handle != "" {
		if err := eng.SetFileGlobs(fullHashFor(tgt), splitGlobs(*files)); err != nil {
			fmt.Fprintln(os.Stderr, "note: could not set file selection:", err)
		}
	}
```

`eng` is `*daemonPoller`; add `SetFileGlobs` to the poller (done in Step 1). For the infohash: a magnet/hash target's `tgt.Handle` is an 8-char prefix, but `SetFileGlobs` needs the full infohash. Derive it: for `tgt.Magnet`, parse via `source.ParseMagnetInfoHash(tgt.Magnet)` (already used in `resolveTarget`) → full 40-hex. Add a tiny local `fullHashFor(tgt dlTarget) string` returning that (empty for URL targets, where `--files` is unsupported for v1 — print a note if `*files != "" && tgt.URL != ""`).

- [ ] **Step 6: Run tests**

Run: `go test ./cmd/shoal/ -run 'TestFiles|TestResolveTarget|TestDownload'`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
gofmt -w internal/daemon/ cmd/shoal/
git add internal/daemon/ cmd/shoal/
git commit -m "cli+daemon: shoal files (list/--only) and download --files via SetFiles/SetFileGlobs RPC"
```

---

## Task 6: Interactive TUI details screen

**Files:**
- Modify: `internal/ui/model.go`, `internal/ui/view.go`, `internal/ui/model_test.go`

**Interfaces:**
- Consumes: `dlDetail engine.Detail` (has `Files[].Selected`), `dlDetailHash`, existing `showDlDetail`, `fetchDetailCmd`.
- Produces:
  - `m.dlFileCursor int` field; `↑ ↓` move it within `len(m.dlDetail.Files)`.
  - `space` toggles the file under the cursor: optimistic `Selected` flip + a `setFilesCmd`, then a `Detail` refetch.
  - `fileSelector` interface `{ SetFiles(infoHash string, paths []string, selected bool) error }` and `setFilesCmd(eng, hash, path, selected) tea.Cmd`.
  - `dlDetailView` renders `[✓]`/`[ ]` per file and a cursor marker; footer `↑↓ move · space select · esc back`.

- [ ] **Step 1: Write the failing test**

In `model_test.go`, give the UI `fakeEngine` a `SetFiles` recorder and a `detail` field (if not already added in Task 5's package — the UI fake is a different `fakeEngine` in `internal/ui`). Then:

```go
func TestDetailToggleFile(t *testing.T) {
	eng := &fakeEngine{
		statuses: []engine.Status{{Name: "Pack", InfoHash: "a", TotalBytes: 200, CompletedBytes: 10}},
		detail: engine.Detail{Files: []engine.FileDetail{
			{Path: "a.mkv", Length: 100, Selected: true},
			{Path: "b.mkv", Length: 100, Selected: true},
		}},
	}
	m := ready(New(&fakeSource{}, eng))
	m.section = sectionDownloads
	m.statuses = eng.statuses
	m, cmd := update(m, key("enter")) // open details
	m, _ = update(m, cmd())           // deliver detail

	m, _ = update(m, key("down"))     // cursor to b.mkv
	m, cmd = update(m, key(" "))      // space toggles b.mkv off
	if cmd != nil {
		cmd()
	}
	if eng.setFilesPath != "b.mkv" || eng.setFilesSelected {
		t.Fatalf("space toggled %q selected=%v, want b.mkv false", eng.setFilesPath, eng.setFilesSelected)
	}
	if m.dlDetail.Files[1].Selected {
		t.Fatal("optimistic flip should mark b.mkv deselected")
	}
}
```

Add to the UI `fakeEngine`: `detail engine.Detail`; `Detail(h) (engine.Detail, error)` returning it; `SetFiles(h, paths, sel)` recording `setFilesPath = paths[0]`, `setFilesSelected = sel`. (`key(" ")` produces a space `KeyRunes` via the existing `key` default case.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ui/ -run TestDetailToggleFile`
Expected: FAIL — space does nothing / `setFilesPath` unset.

- [ ] **Step 3: Add cursor field + reset on open**

`model.go`: add `dlFileCursor int` to `Model`. In `activate`'s `sectionDownloads` branch, set `m.dlFileCursor = 0` when opening.

- [ ] **Step 4: Handle keys in the `showDlDetail` branch**

Replace the `showDlDetail` key branch in `handleKey` with:

```go
	if m.showDlDetail {
		switch msg.String() {
		case "esc", "q":
			m.showDlDetail = false
		case "ctrl+c":
			return m, tea.Quit
		case "up", "k":
			if m.dlFileCursor > 0 {
				m.dlFileCursor--
			}
		case "down", "j":
			if m.dlFileCursor < len(m.dlDetail.Files)-1 {
				m.dlFileCursor++
			}
		case " ", "enter":
			if i := m.dlFileCursor; i >= 0 && i < len(m.dlDetail.Files) {
				f := &m.dlDetail.Files[i]
				f.Selected = !f.Selected // optimistic
				return m, tea.Batch(
					setFilesCmd(m.eng, m.dlDetailHash, f.Path, f.Selected),
					fetchDetailCmd(m.eng, engine.Status{InfoHash: m.dlDetailHash, Name: m.dlDetailName}),
				)
			}
		}
		return m, nil
	}
```

- [ ] **Step 5: Add the command + interface**

`model.go`, near `fetchDetailCmd`:

```go
type fileSelector interface {
	SetFiles(infoHash string, paths []string, selected bool) error
}

func setFilesCmd(eng engine.Engine, infoHash, path string, selected bool) tea.Cmd {
	fs, ok := eng.(fileSelector)
	if !ok {
		return nil
	}
	return func() tea.Msg {
		_ = fs.SetFiles(infoHash, []string{path}, selected)
		return nil
	}
}
```

- [ ] **Step 6: Render checkboxes + cursor**

In `dlDetailView` (view.go), change the file loop to prefix each file with a cursor marker and a checkbox, driven by `m.dlFileCursor` and `f.Selected`:

```go
			marker := "  "
			if i == m.dlFileCursor {
				marker = st.Accent.Render(glyphCursor + " ")
			}
			box := "[✓] "
			if !f.Selected {
				box = "[ ] "
			}
			b.WriteString(marker + box + st.Row.Render(padRight(truncate(stripControl(f.Path), nameW), nameW)) + ...)
```

Update the footer case for `m.showDlDetail`:

```go
	case m.showDlDetail:
		parts = []string{hint("↑↓", "move"), hint("space", "select"), hint("esc", "back")}
```

(Keep the file cap; when files are capped, clamp `dlFileCursor` to the shown range in Step 4's `down` handler by using the same cap — for v1 the cap is generous; if the plan's cap logic hides the cursor target, reduce cap or scroll. Acceptable to leave as-is for v1 and note it.)

- [ ] **Step 7: Run tests**

Run: `go test ./internal/ui/ -run 'TestDetail|TestDownloadDetail|TestClick'`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
gofmt -w internal/ui/
git add internal/ui/
git commit -m "ui: interactive file selection in the download details screen (space toggles)"
```

---

## Task 7: Docs + full verification

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Update the README**

- Downloads/details paragraph: `enter` details now shows per-file `[✓]/[ ]`; `space` toggles a file; unchecked files stop downloading.
- CLI section code block + bullets: `shoal download <target> --files '<glob>'`, `shoal files <id>` (list), `shoal files <id> --only '<glob>'`.
- Roadmap: move **Per-file selection** from "Still planned" to "Shipped".

- [ ] **Step 2: Full verification**

Run each; all must be clean:
```bash
go build ./...
go vet ./...
go test ./...
gofmt -l .        # must print nothing
GOOS=windows go build ./...
```

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs: document per-file selection (TUI checkboxes, --files, files); update roadmap"
```

---

## Self-Review

**Spec coverage:**
- Deselected-paths source of truth → Task 2 (persist) + Task 4 (`persistDeselected`). ✅
- Live toggle → Task 4 `SetFiles` + Task 6 `space`. ✅
- Add-time `--files` (deferred for magnets) → Task 4 `SetFileGlobs`/`applyFileSelection` + Task 5 `download --files`. ✅
- Glob: path+basename, case-insensitive, comma-OR → Task 1. ✅
- `FileDetail.Selected` ✓/✗ → Task 3 + rendered in Tasks 5/6. ✅
- CLI `files` list + `--only` → Task 5. ✅
- TUI interactive details → Task 6. ✅
- Persistence round-trip / restart re-apply → Task 2 test + Task 4 `applyFileSelection` else-branch. ✅
- Testing (glob, persistence, TUI toggle, CLI) → Tasks 1,2,5,6. ✅

**Placeholder scan:** Two flagged `Note:` items (`Store.Get`, `resolveStatusID`) are conditional — "add if absent, reusing the existing helper if present." The implementer reads the file first (each task's step 1) and follows the note. Acceptable: they carry the exact behavior + signature. No bare TODOs.

**Type consistency:** `SetFiles(infoHash string, paths []string, selected bool)` and `SetFileGlobs(infoHash string, globs []string)` are identical across engine (Task 4), daemon interface/client/poller (Task 5), and UI `fileSelector` (Task 6, single-method subset). `FileDetail.Selected` defined Task 3, consumed Tasks 4/5/6. `resolveDeselected(paths, globs)` defined Task 3, used Task 4. `glob.Match(patterns, path)` defined Task 1, used Tasks 3/5.
