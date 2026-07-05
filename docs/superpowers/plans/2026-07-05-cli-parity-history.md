# Full CLI client + download-history management — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the CLI the TUI's history + control actions (`history` list/delete, `pause`/`resume`/`remove`/`open`) and add history deletion to both the CLI and the TUI, with containment-safe optional file deletion.

**Architecture:** New CLI verbs drive the existing daemon `Client` (`Pause`/`Resume`/`Remove`/`Statuses`) and the shared `history.json`. The `history` package gains `Remove`/`Clear`; the engine's containment-safe deletion is exported as `RemoveUnderDir`; the file-manager open logic is extracted to a stdlib-only `internal/opener` so the CLI needn't import Bubble Tea. The TUI's Seeding-pane HISTORY rows gain an `x` keep/delete confirm.

**Tech Stack:** Go, stdlib, the `internal/daemon` client, `internal/history`, `internal/engine`, Bubble Tea (TUI only).

## Global Constraints

- Go; stdlib + already-vendored deps only — **no new module dependencies**.
- TDD: write the failing test first. Commits carry **no Claude attribution**.
- Delete = records-only by default; `--delete-files` (CLI) / `[d]` (TUI) also deletes files. Files are **never** deleted outside the configured data dir (reuse the engine's containment guard).
- `pause`/`resume`/`remove`/`open` act on the running daemon and do **not** auto-start it: no daemon → print `no active downloads`, exit 0. `history` commands read/write `history.json` directly (work with no daemon).
- Machine/user output → the `out` writer; diagnostics → stderr.
- `gofmt -l` clean; run `go vet`, `go build ./...`, `go test ./...`, and `GOOS=windows go build ./cmd/shoal/` before each commit.

**Interfaces already present:** `daemon.Dial(path) (*daemon.Client, error)`, `daemon.SocketPath()`; `(*Client)` has `Statuses() []engine.Status`, `Pause/Resume(hash) error`, `Remove(hash string, deleteData bool) error`, `Close()`. `engine.Status{Name, InfoHash string; TotalBytes, CompletedBytes int64; Done, Seeding, Paused bool; Path string; Percent() float64}`. `history.Load() Store`, `history.LoadFrom(path) Store`, `Store{Path string; Entries []Entry}`, `Entry{InfoHash, Name string; Size int64; CompletedAt time.Time; Path string}`, `(*Store).Append`, `Save`. `config.Load().DataDir`. In `cmd/shoal`: `parseArgs(fs *flag.FlagSet, args []string) ([]string, error)`, `humanBytes(int64) string`, the `serveFakeDaemon(t, eng)` test harness + recording `fakeEngine` (in `fake_engine_test.go`), `configDir()`. In `internal/ui`: `openCommand`/`openInFileManager` (helpers.go), `openFolderCmd`/`folderOpenedMsg` (model.go:378-382), `seeding()`/`seedHistory()`/`seedCursor`, `stopConfirm`/`handleStopKey`, `setNotice`/`setError`, `m.history history.Store`, `m.cfg config.Config`.

---

### Task 1: `history.Remove` + `history.Clear`

**Files:**
- Modify: `internal/history/history.go`
- Test: `internal/history/history_test.go`

**Interfaces:**
- Produces: `func (s *Store) Remove(infoHash string) bool`, `func (s *Store) Clear()`.

- [ ] **Step 1: Write the failing test**

Append to `internal/history/history_test.go`:

```go
func TestRemoveAndClear(t *testing.T) {
	p := filepath.Join(t.TempDir(), "history.json")
	s := history.Store{Path: p}
	s.Append(history.Entry{InfoHash: "aaa", Name: "A"})
	s.Append(history.Entry{InfoHash: "bbb", Name: "B"})

	if !s.Remove("aaa") {
		t.Fatal("Remove should report true when it removes an entry")
	}
	if s.Remove("zzz") {
		t.Fatal("Remove should report false for an absent infohash")
	}
	got := history.LoadFrom(p)
	if len(got.Entries) != 1 || got.Entries[0].InfoHash != "bbb" {
		t.Fatalf("after Remove, entries = %+v, want [bbb]", got.Entries)
	}

	s.Clear()
	if reloaded := history.LoadFrom(p); len(reloaded.Entries) != 0 {
		t.Fatalf("after Clear, entries = %+v, want none", reloaded.Entries)
	}
}
```

(`filepath` and `history` are already imported by `history_test.go`.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/history/ -run TestRemoveAndClear -v`
Expected: FAIL — `s.Remove`/`s.Clear` undefined.

- [ ] **Step 3: Write minimal implementation**

Add to `internal/history/history.go` (after `Append`):

```go
// Remove drops the entry with the given InfoHash (exact match) and persists,
// reporting whether one was removed.
func (s *Store) Remove(infoHash string) bool {
	for i, e := range s.Entries {
		if e.InfoHash == infoHash {
			s.Entries = append(s.Entries[:i], s.Entries[i+1:]...)
			_ = s.Save()
			return true
		}
	}
	return false
}

// Clear removes all entries and persists.
func (s *Store) Clear() {
	s.Entries = nil
	_ = s.Save()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/history/ -v` then `go build ./...`, `gofmt -l internal/history/` (empty).
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/history/history.go internal/history/history_test.go
git commit -m "history: add Remove and Clear"
```

---

### Task 2: shared helpers — exported `engine.RemoveUnderDir` + `internal/opener`

**Files:**
- Modify: `internal/engine/anacrolix.go` (export a wrapper)
- Test: `internal/engine/anacrolix_test.go` (export smoke)
- Create: `internal/opener/opener.go`, `internal/opener/opener_test.go`
- Modify: `internal/ui/helpers.go` (delegate to opener; drop the local open funcs), `internal/ui/model.go` (openFolderCmd → opener.Open), `internal/ui/helpers_test.go` (move the openCommand test out)

**Interfaces:**
- Produces: `func engine.RemoveUnderDir(dir, name string) error`; `func opener.Command(goos, path string) (name string, args []string)`, `func opener.Open(path string) error`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/engine/anacrolix_test.go`:

```go
func TestExportedRemoveUnderDirRefusesEscape(t *testing.T) {
	base := t.TempDir()
	if err := engine.RemoveUnderDir(base, "../escape"); err == nil {
		t.Fatal("RemoveUnderDir must refuse a name escaping the dir")
	}
	sub := filepath.Join(base, "keep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := engine.RemoveUnderDir(base, "keep"); err != nil {
		t.Fatalf("RemoveUnderDir on an in-dir name: %v", err)
	}
	if _, err := os.Stat(sub); !os.IsNotExist(err) {
		t.Fatal("RemoveUnderDir should have deleted the in-dir path")
	}
}
```

(If `anacrolix_test.go` is `package engine` internal-test, call `RemoveUnderDir` unqualified. Check its `package` clause and match it; `os`/`filepath` are already imported there.)

Create `internal/opener/opener_test.go`:

```go
package opener

import "testing"

func TestCommand(t *testing.T) {
	cases := []struct {
		goos, wantName string
	}{
		{"darwin", "open"},
		{"windows", "explorer"},
		{"linux", "xdg-open"},
	}
	for _, c := range cases {
		name, args := Command(c.goos, "/some/dir")
		if name != c.wantName || len(args) != 1 || args[0] != "/some/dir" {
			t.Errorf("Command(%q) = %q %v, want %q [/some/dir]", c.goos, name, args, c.wantName)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/engine/ -run TestExportedRemoveUnderDir` and `go test ./internal/opener/`
Expected: FAIL — `engine.RemoveUnderDir` undefined; `internal/opener` package doesn't exist.

- [ ] **Step 3: Implement**

In `internal/engine/anacrolix.go`, add next to `removeUnderDir`:

```go
// RemoveUnderDir deletes name within dir, refusing any path that escapes dir.
// Exported so the CLI/TUI can safely delete a finished download's files when the
// torrent is no longer in the daemon.
func RemoveUnderDir(dir, name string) error { return removeUnderDir(dir, name) }
```

Create `internal/opener/opener.go`:

```go
// Package opener launches the OS file manager to reveal a path.
package opener

import (
	"os/exec"
	"runtime"
)

// Command returns the file-manager command + args to open path on goos.
func Command(goos, path string) (name string, args []string) {
	switch goos {
	case "darwin":
		return "open", []string{path}
	case "windows":
		return "explorer", []string{path}
	default:
		return "xdg-open", []string{path}
	}
}

// Open reveals path in the OS file manager, detached so a slow/failed manager
// never blocks the caller. Returns an error if the command can't be started
// (e.g. headless with no opener installed).
func Open(path string) error {
	name, args := Command(runtime.GOOS, path)
	return exec.Command(name, args...).Start()
}
```

Now delegate the ui code to `opener` and drop the duplicates:
- In `internal/ui/helpers.go`, DELETE `openCommand` and `openInFileManager`, and remove any now-unused imports (`os/exec`, `runtime`) if they were only used by them.
- In `internal/ui/model.go`, change `openFolderCmd`:

```go
func openFolderCmd(dir string) tea.Cmd {
	return func() tea.Msg { return folderOpenedMsg{err: opener.Open(dir)} }
}
```

  and add `"github.com/StrangeNoob/shoal/internal/opener"` to `model.go`'s imports.
- In `internal/ui/helpers_test.go`, DELETE the `openCommand` test (the case block around line 231) — its coverage moved to `internal/opener/opener_test.go`. Remove now-unused imports in `helpers_test.go` if any.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/engine/ ./internal/opener/ ./internal/ui/`, then `go build ./...`, `go vet ./internal/...`, `gofmt -l internal/` (empty), `GOOS=windows go build ./cmd/shoal/` (clean).
Expected: PASS; the ui still builds and its tests pass (open now delegates).

- [ ] **Step 5: Commit**

```bash
git add internal/engine/anacrolix.go internal/engine/anacrolix_test.go internal/opener/ internal/ui/helpers.go internal/ui/model.go internal/ui/helpers_test.go
git commit -m "Export engine.RemoveUnderDir; extract internal/opener from the TUI"
```

---

### Task 3: CLI `history` (list / rm / clear)

**Files:**
- Create: `cmd/shoal/cli_history.go`, `cmd/shoal/cli_history_test.go`
- Modify: `cmd/shoal/main.go` (route `history`)

**Interfaces:**
- Consumes: `history.Load`/`LoadFrom`/`Remove`/`Clear` (Task 1), `engine.RemoveUnderDir` (Task 2), `daemon.Dial`/`SocketPath`/`Statuses`/`Remove`, `config.Load().DataDir`, `humanBytes`, `parseArgs`.
- Produces: `runHistory(args []string, out io.Writer) int`.

- [ ] **Step 1: Write the failing tests**

Create `cmd/shoal/cli_history_test.go`:

```go
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/StrangeNoob/shoal/internal/history"
)

// isolateHistory points HOME/XDG at a temp dir so history.Load() uses a scratch file.
func isolateHistory(t *testing.T) history.Store {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))
	return history.Load()
}

func TestHistoryListAndRm(t *testing.T) {
	s := isolateHistory(t)
	s.Append(history.Entry{InfoHash: "aaaa1111", Name: "Movie A", Size: 100})
	s.Append(history.Entry{InfoHash: "bbbb2222", Name: "Movie B", Size: 200})

	var buf bytes.Buffer
	if code := runHistory(nil, &buf); code != 0 {
		t.Fatalf("history list exit = %d", code)
	}
	if !strings.Contains(buf.String(), "Movie A") || !strings.Contains(buf.String(), "Movie B") {
		t.Fatalf("history list missing entries:\n%s", buf.String())
	}

	buf.Reset()
	if code := runHistory([]string{"rm", "aaaa"}, &buf); code != 0 {
		t.Fatalf("history rm exit = %d", code)
	}
	if got := history.Load().Entries; len(got) != 1 || got[0].InfoHash != "bbbb2222" {
		t.Fatalf("after rm, entries = %+v, want [bbbb2222]", got)
	}
}

func TestHistoryClear(t *testing.T) {
	s := isolateHistory(t)
	s.Append(history.Entry{InfoHash: "aaaa1111", Name: "A"})
	var buf bytes.Buffer
	if code := runHistory([]string{"clear"}, &buf); code != 0 {
		t.Fatalf("history clear exit = %d", code)
	}
	if got := history.Load().Entries; len(got) != 0 {
		t.Fatalf("after clear, entries = %+v, want none", got)
	}
}

func TestHistoryRmDeleteFilesRefusesEscape(t *testing.T) {
	s := isolateHistory(t)
	// An entry whose Name escapes the data dir must not delete anything outside it.
	s.Append(history.Entry{InfoHash: "cccc3333", Name: "../evil"})
	// create a file OUTSIDE the data dir that must survive
	outside := filepath.Join(t.TempDir(), "evil")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	runHistory([]string{"rm", "cccc", "--delete-files"}, &buf) // no daemon running → direct delete path
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("--delete-files must never delete a path escaping the data dir")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/shoal/ -run TestHistory -v`
Expected: FAIL — `runHistory` undefined.

- [ ] **Step 3: Implement**

Create `cmd/shoal/cli_history.go`:

```go
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/StrangeNoob/shoal/internal/config"
	"github.com/StrangeNoob/shoal/internal/daemon"
	"github.com/StrangeNoob/shoal/internal/engine"
	"github.com/StrangeNoob/shoal/internal/history"
)

type historyRow struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	InfoHash    string `json:"info_hash"`
	Size        int64  `json:"size"`
	CompletedAt string `json:"completed_at"`
	Path        string `json:"path,omitempty"`
}

// runHistory dispatches `history` (list), `history rm <id>`, and `history clear`.
func runHistory(args []string, out io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "rm":
			return runHistoryRm(args[1:], out)
		case "clear":
			return runHistoryClear(args[1:], out)
		}
	}
	return runHistoryList(args, out)
}

func runHistoryList(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("history", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit JSON")
	if _, err := parseArgs(fs, args); err != nil {
		return 2
	}
	entries := history.Load().Entries
	if *jsonOut {
		rows := make([]historyRow, 0, len(entries))
		for _, e := range entries {
			rows = append(rows, toHistoryRow(e))
		}
		b, _ := json.MarshalIndent(rows, "", "  ")
		fmt.Fprintln(out, string(b))
		return 0
	}
	if len(entries) == 0 {
		fmt.Fprintln(out, "no history")
		return 0
	}
	for _, e := range entries {
		r := toHistoryRow(e)
		fmt.Fprintf(out, "%-8s  %-40.40s  %10s  %s\n", r.ID, r.Name, humanBytes(r.Size), r.CompletedAt)
	}
	return 0
}

func toHistoryRow(e history.Entry) historyRow {
	id := e.InfoHash
	if len(id) > 8 {
		id = id[:8]
	}
	return historyRow{ID: id, Name: e.Name, InfoHash: e.InfoHash, Size: e.Size,
		CompletedAt: e.CompletedAt.Format("2006-01-02 15:04"), Path: e.Path}
}

func runHistoryRm(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("history rm", flag.ContinueOnError)
	del := fs.Bool("delete-files", false, "also delete the downloaded files")
	positionals, err := parseArgs(fs, args)
	if err != nil {
		return 2
	}
	if len(positionals) == 0 || positionals[0] == "" {
		fmt.Fprintln(os.Stderr, "usage: shoal history rm <id> [--delete-files]")
		return 2
	}
	s := history.Load()
	e, ok := findHistoryEntry(s.Entries, positionals[0])
	if !ok {
		fmt.Fprintln(os.Stderr, "no history entry:", positionals[0])
		return 1
	}
	if *del {
		if err := deleteEntryFiles(e); err != nil {
			fmt.Fprintln(os.Stderr, "warning: could not delete files:", err)
		}
	}
	s.Remove(e.InfoHash)
	fmt.Fprintf(out, "removed: %s (%s)\n", e.Name, e.InfoHash[:min(8, len(e.InfoHash))])
	return 0
}

func runHistoryClear(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("history clear", flag.ContinueOnError)
	del := fs.Bool("delete-files", false, "also delete the downloaded files")
	if _, err := parseArgs(fs, args); err != nil {
		return 2
	}
	s := history.Load()
	n := len(s.Entries)
	if *del {
		for _, e := range s.Entries {
			if err := deleteEntryFiles(e); err != nil {
				fmt.Fprintln(os.Stderr, "warning:", err)
			}
		}
	}
	s.Clear()
	fmt.Fprintf(out, "cleared %d history entr%s\n", n, plural(n, "y", "ies"))
	return 0
}

// findHistoryEntry returns the unique entry whose infohash starts with prefix.
func findHistoryEntry(entries []history.Entry, prefix string) (history.Entry, bool) {
	prefix = strings.ToLower(prefix)
	found, ok := history.Entry{}, false
	for _, e := range entries {
		if strings.HasPrefix(strings.ToLower(e.InfoHash), prefix) {
			if ok {
				return history.Entry{}, false // ambiguous
			}
			found, ok = e, true
		}
	}
	return found, ok
}

// deleteEntryFiles removes e's files: via the daemon if the torrent is live
// (clean stop + delete), else directly under the data dir (containment-safe).
func deleteEntryFiles(e history.Entry) error {
	if c, err := daemon.Dial(daemon.SocketPath()); err == nil {
		defer c.Close()
		for _, s := range c.Statuses() {
			if s.InfoHash == e.InfoHash {
				return c.Remove(e.InfoHash, true)
			}
		}
	}
	if e.Name == "" {
		return nil
	}
	return engine.RemoveUnderDir(config.Load().DataDir, e.Name)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
```

In `cmd/shoal/main.go`'s `cli()` switch, add:

```go
	case "history":
		return true, runHistory(args[2:], out)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/shoal/ -run TestHistory -v` then the full `go test ./cmd/shoal/`, `go build ./...`, `go vet ./cmd/shoal/`, `gofmt -l cmd/shoal/` (empty), `GOOS=windows go build ./cmd/shoal/`.
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/shoal/cli_history.go cmd/shoal/cli_history_test.go cmd/shoal/main.go
git commit -m "CLI: shoal history (list, rm, clear) with opt-in --delete-files"
```

---

### Task 4: CLI control — `pause` / `resume` / `remove` / `open`

**Files:**
- Create: `cmd/shoal/cli_control.go`, `cmd/shoal/cli_control_test.go`
- Modify: `cmd/shoal/main.go` (route `pause`/`resume`/`remove`/`open`)

**Interfaces:**
- Consumes: `daemon.Dial`/`SocketPath`/`Statuses`/`Pause`/`Resume`/`Remove`, `opener.Open` (Task 2), `history.Load`, `config.Load().DataDir`, `parseArgs`, the `serveFakeDaemon`/`fakeEngine` test harness.
- Produces: `runPause`/`runResume`/`runRemove`/`runOpen(args []string, out io.Writer) int`.

- [ ] **Step 1: Write the failing tests**

Create `cmd/shoal/cli_control_test.go`:

```go
package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/StrangeNoob/shoal/internal/engine"
)

func TestPauseResumeRemoveForwardRPC(t *testing.T) {
	fake := &fakeEngine{statuses: []engine.Status{{InfoHash: "abcdef0123", Name: "M"}}}
	serveFakeDaemon(t, fake)

	var buf bytes.Buffer
	if code := runPause([]string{"abcdef"}, &buf); code != 0 {
		t.Fatalf("pause exit = %d", code)
	}
	if p := fake.gotPaused(); len(p) != 1 || p[0] != "abcdef0123" {
		t.Fatalf("pause did not forward: %v", p)
	}
	if code := runResume([]string{"abcdef"}, &buf); code != 0 {
		t.Fatalf("resume exit = %d", code)
	}
	if r := fake.gotResumed(); len(r) != 1 {
		t.Fatalf("resume did not forward: %v", r)
	}
	if code := runRemove([]string{"abcdef", "--delete-files"}, &buf); code != 0 {
		t.Fatalf("remove exit = %d", code)
	}
	r, d := fake.gotRemoved(), fake.gotRemovedDeleteFlags()
	if len(r) != 1 || r[0] != "abcdef0123" || len(d) != 1 || !d[0] {
		t.Fatalf("remove did not forward deleteData=true: %v %v", r, d)
	}
}

func TestControlNoDaemon(t *testing.T) {
	t.Setenv("SHOAL_DAEMON_SOCK", t.TempDir()+"/absent.sock")
	var buf bytes.Buffer
	if code := runPause([]string{"x"}, &buf); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(buf.String(), "no active downloads") {
		t.Fatalf("output = %q", buf.String())
	}
}

func TestControlAmbiguousAndMissing(t *testing.T) {
	fake := &fakeEngine{statuses: []engine.Status{
		{InfoHash: "aa11"}, {InfoHash: "aa22"},
	}}
	serveFakeDaemon(t, fake)
	var buf bytes.Buffer
	if code := runPause([]string{"aa"}, &buf); code == 0 {
		t.Fatal("ambiguous id should be a non-zero exit")
	}
	if code := runPause([]string{"zz"}, &buf); code == 0 {
		t.Fatal("missing id should be a non-zero exit")
	}
}
```

**NOTE for the implementer:** the `fakeEngine` in `cmd/shoal/fake_engine_test.go` records magnets/urls/removed but likely doesn't expose `gotPaused`/`gotResumed` or record `deleteData`. Add to that fake, without changing the existing `gotRemoved() []string` signature (its `cli_status_test.go` caller must keep compiling): `paused`/`resumed` string slices recorded in `Pause`/`Resume` with getters `gotPaused()`/`gotResumed() []string`; a `removedDelete []bool` recorded in `Remove` alongside the existing `removed`, with a new getter `gotRemovedDeleteFlags() []bool`. (Guard the slices with the fake's existing mutex if it has one.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/shoal/ -run 'TestPauseResume|TestControl' -v`
Expected: FAIL — `runPause`/`runResume`/`runRemove` undefined (and the fake getters).

- [ ] **Step 3: Implement**

Create `cmd/shoal/cli_control.go`:

```go
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/StrangeNoob/shoal/internal/config"
	"github.com/StrangeNoob/shoal/internal/daemon"
	"github.com/StrangeNoob/shoal/internal/engine"
	"github.com/StrangeNoob/shoal/internal/history"
	"github.com/StrangeNoob/shoal/internal/opener"
)

// withDaemon dials the daemon and resolves idPrefix to exactly one live torrent,
// then runs fn. No daemon → "no active downloads" (exit 0); 0/2+ matches → exit 1.
func withDaemon(idPrefix string, out io.Writer, fn func(c *daemon.Client, s engine.Status) error) int {
	c, err := daemon.Dial(daemon.SocketPath())
	if err != nil {
		fmt.Fprintln(out, "no active downloads")
		return 0
	}
	defer c.Close()
	s, err := resolveOne(c, idPrefix)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := fn(c, s); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// resolveOne returns the unique live torrent whose infohash starts with idPrefix.
func resolveOne(c *daemon.Client, idPrefix string) (engine.Status, error) {
	prefix := strings.ToLower(idPrefix)
	var matches []engine.Status
	for _, s := range c.Statuses() {
		if strings.HasPrefix(strings.ToLower(s.InfoHash), prefix) {
			matches = append(matches, s)
		}
	}
	switch len(matches) {
	case 0:
		return engine.Status{}, fmt.Errorf("no such download: %s", idPrefix)
	case 1:
		return matches[0], nil
	default:
		return engine.Status{}, fmt.Errorf("ambiguous id %q matches %d downloads", idPrefix, len(matches))
	}
}

func firstArg(name string, args []string, out io.Writer) (string, bool) {
	positionals, err := parseArgs(flag.NewFlagSet(name, flag.ContinueOnError), args)
	if err != nil {
		return "", false
	}
	if len(positionals) == 0 || positionals[0] == "" {
		fmt.Fprintf(os.Stderr, "usage: shoal %s <id>\n", name)
		return "", false
	}
	return positionals[0], true
}

func runPause(args []string, out io.Writer) int {
	id, ok := firstArg("pause", args, out)
	if !ok {
		return 2
	}
	return withDaemon(id, out, func(c *daemon.Client, s engine.Status) error {
		if err := c.Pause(s.InfoHash); err != nil {
			return err
		}
		fmt.Fprintf(out, "paused: %s\n", s.Name)
		return nil
	})
}

func runResume(args []string, out io.Writer) int {
	id, ok := firstArg("resume", args, out)
	if !ok {
		return 2
	}
	return withDaemon(id, out, func(c *daemon.Client, s engine.Status) error {
		if err := c.Resume(s.InfoHash); err != nil {
			return err
		}
		fmt.Fprintf(out, "resumed: %s\n", s.Name)
		return nil
	})
}

func runRemove(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("remove", flag.ContinueOnError)
	del := fs.Bool("delete-files", false, "also delete the downloaded files")
	positionals, err := parseArgs(fs, args)
	if err != nil {
		return 2
	}
	if len(positionals) == 0 || positionals[0] == "" {
		fmt.Fprintln(os.Stderr, "usage: shoal remove <id> [--delete-files]")
		return 2
	}
	return withDaemon(positionals[0], out, func(c *daemon.Client, s engine.Status) error {
		if err := c.Remove(s.InfoHash, *del); err != nil {
			return err
		}
		verb := "removed"
		if *del {
			verb = "removed + deleted files for"
		}
		fmt.Fprintf(out, "%s: %s\n", verb, s.Name)
		return nil
	})
}

func runOpen(args []string, out io.Writer) int {
	id, ok := firstArg("open", args, out)
	if !ok {
		return 2
	}
	path := resolveOpenPath(id)
	if path == "" {
		fmt.Fprintln(os.Stderr, "no such download:", id)
		return 1
	}
	if err := opener.Open(path); err != nil {
		fmt.Fprintln(out, path) // headless / no opener: print the path
	}
	return 0
}

// resolveOpenPath finds a folder to open for idPrefix: a live torrent's path,
// else a history entry's path, else <dataDir>/<name> if it exists.
func resolveOpenPath(idPrefix string) string {
	prefix := strings.ToLower(idPrefix)
	if c, err := daemon.Dial(daemon.SocketPath()); err == nil {
		defer c.Close()
		for _, s := range c.Statuses() {
			if strings.HasPrefix(strings.ToLower(s.InfoHash), prefix) && s.Path != "" {
				return s.Path
			}
		}
	}
	dataDir := config.Load().DataDir
	for _, e := range history.Load().Entries {
		if !strings.HasPrefix(strings.ToLower(e.InfoHash), prefix) {
			continue
		}
		if e.Path != "" && pathExists(e.Path) {
			return e.Path
		}
		if e.Name != "" {
			if guess := filepath.Join(dataDir, e.Name); pathExists(guess) {
				return guess
			}
		}
	}
	return ""
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
```

In `cmd/shoal/main.go`'s `cli()` switch, add:

```go
	case "pause":
		return true, runPause(args[2:], out)
	case "resume":
		return true, runResume(args[2:], out)
	case "remove":
		return true, runRemove(args[2:], out)
	case "open":
		return true, runOpen(args[2:], out)
```

(If `pathExists` already exists elsewhere in `cmd/shoal`, reuse it and drop this copy.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/shoal/ -run 'TestPauseResume|TestControl' -v` then the full `go test ./...`, `go build ./...`, `go vet ./cmd/shoal/`, `gofmt -l cmd/shoal/` (empty), `GOOS=windows go build ./cmd/shoal/`.
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/shoal/cli_control.go cmd/shoal/cli_control_test.go cmd/shoal/fake_engine_test.go cmd/shoal/main.go
git commit -m "CLI: pause/resume/remove/open on the shared daemon"
```

---

### Task 5: TUI delete-history (Seeding-pane HISTORY row)

**Files:**
- Modify: `internal/ui/model.go` (confirm state + key routing + handler)
- Modify: `internal/ui/view.go` (confirm render + hint)
- Test: `internal/ui/model_test.go`

**Interfaces:**
- Consumes: `history.Remove` (Task 1), `engine.RemoveUnderDir` (Task 2), `seedHistory()`/`seeding()`/`seedCursor`, `removeCmd`, `history.Load`.
- Produces: `histConfirm`/`histTarget` fields; `handleHistKey`; a `deleteHistoryFilesCmd`.

- [ ] **Step 1: Write the failing test**

Append to `internal/ui/model_test.go`:

```go
func TestHistoryRowDeleteConfirm(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, ".config"))
	seed := history.Load()
	seed.Append(history.Entry{InfoHash: "hh", Name: "Old Movie"})

	// no active torrents → the seeding pane shows only the history row
	m := ready(New(&fakeSource{}, &fakeEngine{}))
	m = tick(m, time.Unix(1000, 0)) // loads statuses (none) + m.history from disk
	m.section = sectionSeeding
	m.seedCursor = 0 // the single history row

	m, _ = update(m, key("x"))
	if !m.histConfirm {
		t.Fatal("x on a history row should open the delete confirm")
	}
	m, _ = update(m, key("k")) // [k] remove entry, keep files
	if m.histConfirm {
		t.Fatal("confirm should close after a choice")
	}
	if len(history.Load().Entries) != 0 {
		t.Fatalf("history entry should be removed, got %+v", history.Load().Entries)
	}
}
```

(`filepath`, `history`, `time` are imported in `model_test.go`; `tick`/`ready`/`update`/`key`/`New`/`fakeEngine`/`fakeSource` exist.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ui/ -run TestHistoryRowDeleteConfirm -v`
Expected: FAIL — `m.histConfirm` undefined.

- [ ] **Step 3: Implement**

In `internal/ui/model.go`:

(a) Add fields to the `Model` struct (next to `stopConfirm`/`stopTarget`):

```go
	histConfirm bool          // Seeding pane: confirm deleting a HISTORY entry
	histTarget  history.Entry // the history entry being deleted
```

(b) In the key handler, route the confirm (next to the `stopConfirm` route, ~model.go:702):

```go
	if m.histConfirm {
		return m.handleHistKey(msg)
	}
```

(c) In the Seeding-pane `x` binding — the block that currently sets `stopConfirm` when `m.seedCursor < len(ss)` (~model.go:754) — add an `else` for history rows:

```go
		ss := m.seeding()
		if len(ss) > 0 && m.seedCursor < len(ss) {
			m.stopConfirm = true
			m.stopTarget = ss[m.seedCursor]
		} else if hist := m.seedHistory(); len(hist) > 0 {
			if hi := m.seedCursor - len(ss); hi >= 0 && hi < len(hist) {
				m.histConfirm = true
				m.histTarget = hist[hi]
			}
		}
```

(d) Add the handler (near `handleStopKey`):

```go
// handleHistKey handles the HISTORY-row delete confirm: k = remove entry (keep
// files), d = remove entry + delete files, esc = cancel.
func (m Model) handleHistKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	e := m.histTarget
	switch msg.String() {
	case "k":
		m.histConfirm = false
		m.history.Remove(e.InfoHash)
		m.history = history.Load()
		m.setNotice("Removed from history: " + truncate(e.Name, 40))
		return m, nil
	case "d":
		m.histConfirm = false
		var cmd tea.Cmd
		live := false
		for _, s := range m.statuses {
			if s.InfoHash == e.InfoHash {
				live = true
				break
			}
		}
		if live {
			cmd = removeCmd(m.eng, e.InfoHash, e.Name, true) // daemon stops + deletes
		} else {
			cmd = deleteHistoryFilesCmd(m.cfg.DataDir, e.Name)
		}
		m.history.Remove(e.InfoHash)
		m.history = history.Load()
		m.setNotice("Deleted: " + truncate(e.Name, 40))
		return m, cmd
	case "esc":
		m.histConfirm = false
		return m, nil
	}
	return m, nil
}

// deleteHistoryFilesCmd removes a finished download's files off the UI thread.
func deleteHistoryFilesCmd(dataDir, name string) tea.Cmd {
	return func() tea.Msg {
		if name != "" {
			_ = engine.RemoveUnderDir(dataDir, name)
		}
		return nil
	}
}
```

Add `"github.com/StrangeNoob/shoal/internal/engine"` and `"github.com/StrangeNoob/shoal/internal/history"` to `model.go`'s imports if not already present (`history` almost certainly is; `engine` is). Confirm `removeCmd`'s signature matches `removeCmd(eng, infoHash, name string, deleteData bool)` — if different, adapt the call.

(e) In `internal/ui/view.go`, where the Seeding-pane confirm (`stopConfirm`) modal renders, add a parallel `histConfirm` prompt, e.g. `Delete "<name>" from history?  [k] remove  [d] +delete files  [esc]`, and add the history-row `x` action to the Seeding footer hint and the `?` help. Make `m.View()` contain a recognizable prompt when `m.histConfirm` (not asserted by the Task-5 test, but keep it visible/consistent with the stop confirm).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/ui/ -run 'TestHistoryRowDelete|TestSeeding|TestStop' -v` then the full `go test ./internal/ui/`, `go build ./...`, `go vet ./internal/ui/`, `gofmt -l internal/ui/` (empty).
Expected: PASS; the existing seeding/stop tests still pass (active-seeder `x` unchanged).

- [ ] **Step 5: Commit**

```bash
git add internal/ui/model.go internal/ui/view.go internal/ui/model_test.go
git commit -m "TUI: delete history entries from the Seeding pane (x → keep/delete)"
```

---

### Task 6: Docs

**Files:**
- Modify: `.claude/skills/shoal-download/SKILL.md` and `cmd/shoal/skill/SKILL.md` (keep byte-identical)
- Modify: `README.md`

- [ ] **Step 1: Update the skill (both copies)**

In `.claude/skills/shoal-download/SKILL.md`, document the new commands in the CLI reference: `shoal history [--json]`, `shoal history rm <id> [--delete-files]`, `shoal history clear [--delete-files]`, `shoal pause <id>`, `shoal resume <id>`, `shoal remove <id> [--delete-files]`, `shoal open <id>`. Note that `<id>` is an infohash prefix from `shoal status`/`shoal history`, that `pause/resume/remove/open` need a running daemon (they don't auto-start one), and that `history` works without a daemon. Then sync the embedded copy: `cp .claude/skills/shoal-download/SKILL.md cmd/shoal/skill/SKILL.md` and confirm `diff` shows no difference (a drift-guard test enforces it).

- [ ] **Step 2: Update the README**

In `README.md`'s command-line section, add the new commands under the existing `search`/`download`/`status`/`sources` list, with a one-line note on `--delete-files` (opt-in; records-only otherwise) and that history deletion is available in the TUI too (Seeding pane, `x` on a history row).

- [ ] **Step 3: Verify + commit**

Run: `go test ./cmd/shoal/ -run TestEmbeddedSkill` (drift-guard passes) and `go build ./...`.

```bash
git add .claude/skills/shoal-download/SKILL.md cmd/shoal/skill/SKILL.md README.md
git commit -m "docs: document CLI history/pause/resume/remove/open and TUI history delete"
```

---

## Self-Review

**Spec coverage:**
- `history.Remove`/`Clear` → Task 1. ✓
- Exported `engine.RemoveUnderDir` + `internal/opener` (ui delegates) → Task 2. ✓
- CLI `history` list/rm/clear + `--delete-files` + containment refusal → Task 3. ✓
- CLI `pause`/`resume`/`remove`/`open` + id resolution + no-daemon policy → Task 4. ✓
- File-deletion rule (live → daemon.Remove(true); else RemoveUnderDir) → Tasks 3 (`deleteEntryFiles`) & 5 (TUI `d`). ✓
- TUI history-row delete (`x` → k/d/esc) → Task 5. ✓
- Docs (skill both copies + README) → Task 6. ✓
- No new deps; no-Claude-attribution; gofmt/windows gates → Global Constraints + each task. ✓

**Placeholder scan:** none — every code step carries complete code. Task 4 flags the `fakeEngine` getters the implementer must add (with exact fields/getters named), which is concrete, not a placeholder.

**Type consistency:** `runHistory`/`runHistoryRm`/`runHistoryClear`/`runPause`/`runResume`/`runRemove`/`runOpen(args []string, out io.Writer) int`; `deleteEntryFiles(history.Entry) error`; `resolveOne(*daemon.Client, string) (engine.Status, error)`; `findHistoryEntry([]history.Entry, string) (history.Entry, bool)`; `opener.Command`/`opener.Open`; `engine.RemoveUnderDir(dir, name string) error`; `history.Store.Remove(string) bool`/`Clear()`; `Model.histConfirm`/`histTarget`/`handleHistKey`/`deleteHistoryFilesCmd` are used consistently across tasks. The `fakeEngine` getter additions in Task 4 (`gotPaused`/`gotResumed() []string`, `gotRemovedDeleteFlags() []bool`) are named where introduced and keep the existing `gotRemoved() []string` signature intact, so `cli_status_test.go` needs no change.
