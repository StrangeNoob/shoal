# Shared-engine daemon Phase 2 — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Point the CLI at the daemon — `shoal download` adds torrents to a shared (auto-started) daemon engine and `shoal status` reads its live state — retiring the detached-worker + `active/*.json` model.

**Architecture:** `download`/`status` become daemon clients. An `ensureDaemon()` helper dials the daemon (auto-starting a detached `shoal daemon` if none is running). `download` calls `AddMagnet`/`AddTorrentURL`; `status` calls `Statuses()` and classifies from `engine.Status`. The per-download worker, `Active` state files, and `pidAlive` are removed.

**Tech Stack:** Go, stdlib, the `internal/daemon` RPC library from Phase 1, existing `internal/engine`/`internal/source`.

## Global Constraints

- Go; stdlib + already-vendored deps only — **no new module dependencies**.
- TDD: write the failing test first. Commits carry **no Claude attribution**.
- The daemon is auto-started by `download`/`status` when absent; `status` on a missing daemon prints `no downloads` and exits 0 (a read never spawns a daemon — only `download` auto-starts).
- `--out` is dropped from `download`: downloads use the configured data dir; a non-empty `--out` prints a one-line notice to stderr and is otherwise ignored.
- Machine/user output → the `out` writer; diagnostics/notices → stderr.
- Tests never hit the network or start a real daemon: an in-process fake `engine.Engine` served by `daemon.Serve` over a temp unix socket, with `t.Setenv("SHOAL_DAEMON_SOCK", <temp>)` so the CLI connects to it.
- `gofmt -l` clean (CI enforces it); run `go vet`, `go build ./...`, and `GOOS=windows go build ./cmd/shoal/` before each commit.

**Daemon API from Phase 1 (all in package `daemon`):** `daemon.SocketPath() string`; `daemon.Dial(path string) (*daemon.Client, error)`; `daemon.Serve(l net.Listener, eng engine.Engine) error`. `*daemon.Client` implements `engine.Engine`: `AddMagnet(string) error`, `AddTorrentURL(url, name string) error`, `Statuses() []engine.Status`, `Remove(hash string, deleteData bool) error`, `Pause`/`Resume`/`Close`.
**`engine.Status` fields:** `Name, InfoHash string; TotalBytes, CompletedBytes, Peers int64/int; Done, Paused, Seeding bool; Path string`; method `Percent() float64`.
**`detachSysProcAttr()`** lives in `cmd/shoal/platform_unix.go` / `platform_windows.go` (keep it).

---

### Task 1: `ensureDaemon` auto-start helper

**Files:**
- Modify: `cmd/shoal/cli_daemon.go` (add `ensureDaemon`)
- Test: `cmd/shoal/cli_daemon_test.go` (append)

**Interfaces:**
- Consumes: `daemon.Dial`, `daemon.SocketPath`, `detachSysProcAttr`, `configDir`.
- Produces: `func ensureDaemon() (*daemon.Client, error)`

- [ ] **Step 1: Write the failing test**

```go
// append to cmd/shoal/cli_daemon_test.go
func TestEnsureDaemonUsesRunning(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "d.sock")
	t.Setenv("SHOAL_DAEMON_SOCK", sock)
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	c, err := ensureDaemon() // must connect to the already-running socket, not spawn
	if err != nil {
		t.Fatalf("ensureDaemon should connect to the running daemon: %v", err)
	}
	c.Close()
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/shoal/ -run TestEnsureDaemonUsesRunning -v`
Expected: FAIL — `undefined: ensureDaemon`.

- [ ] **Step 3: Write minimal implementation**

Add `"os/exec"` and `"time"` to `cmd/shoal/cli_daemon.go`'s import block, then add:

```go
// ensureDaemon returns a client connected to the shared daemon, auto-starting a
// detached `shoal daemon` if none is running.
func ensureDaemon() (*daemon.Client, error) {
	if runtime.GOOS == "windows" {
		return nil, fmt.Errorf("the shoal daemon is not yet supported on Windows")
	}
	sock := daemon.SocketPath()
	if c, err := daemon.Dial(sock); err == nil {
		return c, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	logDir := filepath.Join(configDir(), "logs")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return nil, err
	}
	logf, err := os.OpenFile(filepath.Join(logDir, "daemon.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	defer logf.Close()
	cmd := exec.Command(exe, "daemon")
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.SysProcAttr = detachSysProcAttr()
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	for i := 0; i < 25; i++ { // poll the socket for ~5s
		time.Sleep(200 * time.Millisecond)
		if c, err := daemon.Dial(sock); err == nil {
			return c, nil
		}
	}
	return nil, fmt.Errorf("could not start shoal daemon (see %s)", filepath.Join(logDir, "daemon.log"))
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/shoal/ -run TestEnsureDaemonUsesRunning -v` then `go build ./...`
Expected: PASS; build clean.

- [ ] **Step 5: Commit**

```bash
git add cmd/shoal/cli_daemon.go cmd/shoal/cli_daemon_test.go
git commit -m "Add ensureDaemon: connect to (or auto-start) the shared daemon"
```

---

### Task 2: `shoal download` → daemon

**Files:**
- Modify: `cmd/shoal/cli_download.go` (rewrite `runDownload`; remove worker funcs)
- Create: `cmd/shoal/fake_engine_test.go` (shared recording fake + daemon test harness)
- Modify: `cmd/shoal/cli_download_test.go` (remove worker tests + old fake; add download-forwarding test)

**Interfaces:**
- Consumes: `ensureDaemon` (Task 1), `resolveTarget`, `displayName`, `lookupCache`, `configDir`.
- Produces: daemon-backed `runDownload`; test helpers `fakeEngine` + `serveFakeDaemon`.

- [ ] **Step 1: Write the failing test**

Create `cmd/shoal/fake_engine_test.go` (the shared recording fake + harness used by this task and Task 3):

```go
package main

import (
	"net"
	"sync"
	"testing"

	"github.com/StrangeNoob/shoal/internal/daemon"
	"github.com/StrangeNoob/shoal/internal/engine"
)

type fakeEngine struct {
	mu       sync.Mutex
	magnets  []string
	urls     [][2]string
	removed  []string
	statuses []engine.Status
}

func (f *fakeEngine) AddMagnet(m string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.magnets = append(f.magnets, m)
	return nil
}
func (f *fakeEngine) AddTorrentURL(u, n string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.urls = append(f.urls, [2]string{u, n})
	return nil
}
func (f *fakeEngine) Statuses() []engine.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.statuses
}
func (f *fakeEngine) Remove(h string, _ bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, h)
	return nil
}
func (f *fakeEngine) Pause(string) error  { return nil }
func (f *fakeEngine) Resume(string) error { return nil }
func (f *fakeEngine) Close() error        { return nil }

func (f *fakeEngine) gotMagnets() []string { f.mu.Lock(); defer f.mu.Unlock(); return append([]string(nil), f.magnets...) }
func (f *fakeEngine) gotURLs() [][2]string { f.mu.Lock(); defer f.mu.Unlock(); return append([][2]string(nil), f.urls...) }
func (f *fakeEngine) gotRemoved() []string { f.mu.Lock(); defer f.mu.Unlock(); return append([]string(nil), f.removed...) }

// serveFakeDaemon starts a daemon backed by fake on a temp socket and points
// SHOAL_DAEMON_SOCK at it so the CLI connects to it.
func serveFakeDaemon(t *testing.T, fake engine.Engine) {
	t.Helper()
	sock := t.TempDir() + "/d.sock"
	t.Setenv("SHOAL_DAEMON_SOCK", sock)
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	go daemon.Serve(l, fake)
	t.Cleanup(func() { l.Close() })
}
```

Append to `cmd/shoal/cli_download_test.go`:

```go
func TestDownloadForwardsMagnetToDaemon(t *testing.T) {
	fake := &fakeEngine{}
	serveFakeDaemon(t, fake)
	const ih = "0123456789abcdef0123456789abcdef01234567"
	var buf bytes.Buffer
	if code := runDownload([]string{"magnet:?xt=urn:btih:" + ih}, &buf); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if m := fake.gotMagnets(); len(m) != 1 || !strings.Contains(m[0], ih) {
		t.Fatalf("daemon did not receive the magnet: %v", m)
	}
	if !strings.Contains(buf.String(), "started:") {
		t.Fatalf("expected a 'started:' line, got %q", buf.String())
	}
}

func TestDownloadForwardsURLToDaemon(t *testing.T) {
	fake := &fakeEngine{}
	serveFakeDaemon(t, fake)
	var buf bytes.Buffer
	if code := runDownload([]string{"https://example.com/x.torrent"}, &buf); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if u := fake.gotURLs(); len(u) != 1 || u[0][0] != "https://example.com/x.torrent" {
		t.Fatalf("daemon did not receive the URL: %v", u)
	}
}
```

Ensure `cli_download_test.go` imports `bytes` and `strings` (add if missing).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/shoal/ -run 'TestDownloadForwards' -v`
Expected: FAIL — a duplicate `fakeEngine` is declared (old one still in `cli_download_test.go`) and/or `runDownload` still spawns a worker. This confirms the old worker model is still present.

- [ ] **Step 3: Write minimal implementation**

Replace `runDownload` in `cmd/shoal/cli_download.go` and DELETE `downloadWorker`, `spawnWorker`, `runWorker`, `stepWorker`, and `firstStatus`. The new `runDownload`:

```go
func runDownload(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("download", flag.ContinueOnError)
	outDir := fs.String("out", "", "(deprecated) downloads use the configured folder")
	positionals, err := parseArgs(fs, args)
	if err != nil {
		return 2
	}
	var arg string
	if len(positionals) > 0 {
		arg = positionals[0]
	}
	if arg == "" {
		fmt.Fprintln(os.Stderr, "usage: shoal download <magnet|url|infohash|id>")
		return 2
	}
	if *outDir != "" {
		fmt.Fprintln(os.Stderr, "note: downloads use the configured folder (change it in Settings or config.json)")
	}

	tgt, err := resolveTarget(arg, func(sid string) (string, bool) {
		e, ok := lookupCache(configDir(), sid)
		return e.Magnet, ok
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	eng, err := ensureDaemon()
	if err != nil {
		fmt.Fprintln(os.Stderr, "shoal download:", err)
		return 1
	}
	defer eng.Close()

	if tgt.Magnet != "" {
		err = eng.AddMagnet(tgt.Magnet)
	} else {
		err = eng.AddTorrentURL(tgt.URL, "")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "shoal download:", err)
		return 1
	}
	if tgt.Handle != "" {
		fmt.Fprintf(out, "started: %s (%s)\n", displayName(tgt), tgt.Handle)
	} else {
		fmt.Fprintf(out, "started: %s\n", displayName(tgt))
	}
	return 0
}
```

Update `cmd/shoal/cli_download.go`'s import block to exactly (the worker removals drop `os/exec`, `path/filepath`, `time`, and the `config`/`engine`/`history` internal imports):

```go
import (
	"crypto/sha1"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/StrangeNoob/shoal/internal/source"
)
```

In `cmd/shoal/cli_download_test.go`, DELETE the old `fakeEngine` type (the `frames`-based one) and the tests `TestStepWorkerProgressAndDone` and `TestRunWorkerRecordsHistory` (the worker they test is gone). Keep `TestResolveTarget`, `TestResolveTargetNilLookup`, `TestDisplayName`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/shoal/ -run 'TestDownloadForwards|TestResolveTarget|TestDisplayName' -v` then `go build ./...`, `go vet ./cmd/shoal/`, `gofmt -l cmd/shoal/` (empty).
Expected: PASS; build/vet clean. (Note: `state.go`'s `writeActive`/`Active` etc. are now unused but still compile — Task 4 removes them.)

- [ ] **Step 5: Commit**

```bash
git add cmd/shoal/cli_download.go cmd/shoal/fake_engine_test.go cmd/shoal/cli_download_test.go
git commit -m "shoal download: add torrents to the daemon instead of spawning a worker"
```

---

### Task 3: `shoal status` → daemon

**Files:**
- Modify: `cmd/shoal/cli_status.go` (rewrite)
- Modify: `cmd/shoal/cli_status_test.go` (replace tests)

**Interfaces:**
- Consumes: `daemon.Dial`, `daemon.SocketPath`, `engine.Status`, `humanBytes`, `parseArgs`, and the test helpers `fakeEngine`/`serveFakeDaemon` (Task 2).
- Produces: `statusState`, `toStatusRows`, `printStatus`, daemon-backed `runStatus`.

- [ ] **Step 1: Write the failing test**

Replace the body of `cmd/shoal/cli_status_test.go` with:

```go
package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/StrangeNoob/shoal/internal/engine"
)

func TestStatusState(t *testing.T) {
	cases := []struct {
		s    engine.Status
		want string
	}{
		{engine.Status{Done: true, Seeding: true}, "seeding"},
		{engine.Status{Done: true}, "done"},
		{engine.Status{Paused: true}, "paused"},
		{engine.Status{}, "downloading"},
	}
	for _, c := range cases {
		if got := statusState(c.s); got != c.want {
			t.Errorf("statusState(%+v) = %q, want %q", c.s, got, c.want)
		}
	}
}

func TestStatusFromDaemonJSON(t *testing.T) {
	fake := &fakeEngine{statuses: []engine.Status{
		{Name: "Movie", InfoHash: "abcdef0123456789", TotalBytes: 200, CompletedBytes: 100, Peers: 4},
	}}
	serveFakeDaemon(t, fake)
	var buf bytes.Buffer
	if code := runStatus([]string{"--json"}, &buf); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	var rows []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rows); err != nil {
		t.Fatalf("bad json: %v\n%s", err, buf.String())
	}
	if len(rows) != 1 || rows[0]["state"] != "downloading" || rows[0]["id"] != "abcdef01" || rows[0]["percent"].(float64) != 0.5 {
		t.Fatalf("row = %+v", rows[0])
	}
}

func TestStatusNoDaemon(t *testing.T) {
	// point at a socket with no listener → no daemon → "no downloads", exit 0
	t.Setenv("SHOAL_DAEMON_SOCK", filepath.Join(t.TempDir(), "absent.sock"))
	var buf bytes.Buffer
	if code := runStatus(nil, &buf); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(buf.String(), "no downloads") {
		t.Fatalf("expected 'no downloads', got %q", buf.String())
	}
}

func TestStatusClearRemovesDone(t *testing.T) {
	fake := &fakeEngine{statuses: []engine.Status{
		{Name: "Done", InfoHash: "aaaa1111bbbb2222", Done: true},
	}}
	serveFakeDaemon(t, fake)
	var buf bytes.Buffer
	if code := runStatus([]string{"--clear"}, &buf); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if r := fake.gotRemoved(); len(r) != 1 || r[0] != "aaaa1111bbbb2222" {
		t.Fatalf("--clear should Remove the done torrent, got %v", r)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/shoal/ -run 'TestStatus' -v`
Expected: FAIL — `undefined: statusState` and the old `runStatus` reads `active/*.json` (no daemon), so the daemon-backed assertions fail.

- [ ] **Step 3: Write minimal implementation**

Replace the entire body of `cmd/shoal/cli_status.go` with:

```go
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/StrangeNoob/shoal/internal/daemon"
	"github.com/StrangeNoob/shoal/internal/engine"
)

// statusState classifies a torrent for display.
func statusState(s engine.Status) string {
	switch {
	case s.Done && s.Seeding:
		return "seeding"
	case s.Done:
		return "done"
	case s.Paused:
		return "paused"
	default:
		return "downloading"
	}
}

type statusRow struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	InfoHash  string  `json:"info_hash"`
	Percent   float64 `json:"percent"`
	Completed int64   `json:"completed"`
	Total     int64   `json:"total"`
	Peers     int     `json:"peers"`
	State     string  `json:"state"`
	Seeding   bool    `json:"seeding"`
	Done      bool    `json:"done"`
	Path      string  `json:"path,omitempty"`
}

func toStatusRows(ss []engine.Status) []statusRow {
	rows := make([]statusRow, 0, len(ss))
	for _, s := range ss {
		id := s.InfoHash
		if len(id) > 8 {
			id = id[:8]
		}
		rows = append(rows, statusRow{
			ID: id, Name: s.Name, InfoHash: s.InfoHash, Percent: s.Percent(),
			Completed: s.CompletedBytes, Total: s.TotalBytes, Peers: s.Peers,
			State: statusState(s), Seeding: s.Seeding, Done: s.Done, Path: s.Path,
		})
	}
	return rows
}

func printStatus(out io.Writer, rows []statusRow, asJSON bool) {
	if asJSON {
		b, _ := json.MarshalIndent(rows, "", "  ")
		fmt.Fprintln(out, string(b))
		return
	}
	if len(rows) == 0 {
		fmt.Fprintln(out, "no downloads")
		return
	}
	for _, r := range rows {
		fmt.Fprintf(out, "%-8s  %-40.40s  %5.1f%%  %10s/%-10s  %d peers  %s\n",
			r.ID, r.Name, r.Percent*100, humanBytes(r.Completed), humanBytes(r.Total), r.Peers, r.State)
	}
}

func runStatus(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit JSON")
	clear := fs.Bool("clear", false, "remove finished torrents (keeps files)")
	positionals, err := parseArgs(fs, args)
	if err != nil {
		return 2
	}

	c, err := daemon.Dial(daemon.SocketPath())
	if err != nil {
		printStatus(out, nil, *jsonOut) // no daemon running → nothing to show
		return 0
	}
	defer c.Close()

	statuses := c.Statuses()
	if *clear {
		for _, s := range statuses {
			if s.Done {
				_ = c.Remove(s.InfoHash, false)
			}
		}
		statuses = c.Statuses() // refresh after removals
	}
	if len(positionals) > 0 && positionals[0] != "" {
		prefix := strings.ToLower(positionals[0])
		var filtered []engine.Status
		for _, s := range statuses {
			if strings.HasPrefix(strings.ToLower(s.InfoHash), prefix) {
				filtered = append(filtered, s)
			}
		}
		statuses = filtered
	}
	printStatus(out, toStatusRows(statuses), *jsonOut)
	return 0
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/shoal/ -run 'TestStatus' -v` then `go build ./...`, `go vet ./cmd/shoal/`, `gofmt -l cmd/shoal/` (empty).
Expected: PASS; build/vet clean. (`Active`/`stateOf`/`pidAlive` are now unused but still compile — Task 4 removes them.)

- [ ] **Step 5: Commit**

```bash
git add cmd/shoal/cli_status.go cmd/shoal/cli_status_test.go
git commit -m "shoal status: read the daemon's live state; classify from engine.Status"
```

---

### Task 4: Retire the worker/active state

Remove the now-dead `Active` model and `pidAlive`. After Tasks 2 & 3 nothing references them; this task deletes them and their tests.

**Files:**
- Modify: `cmd/shoal/state.go` (trim to the search cache)
- Modify: `cmd/shoal/platform_unix.go`, `cmd/shoal/platform_windows.go` (remove `pidAlive`)
- Modify: `cmd/shoal/state_test.go` (remove the Active test)

**Interfaces:**
- Produces: trimmed `state.go` (keeps `cacheEntry`, `cachePath`, `configDir`, `writeCache`, `lookupCache`); `detachSysProcAttr` unchanged.

- [ ] **Step 1: Verify what still references the removed symbols**

Run: `grep -rn "Active\b\|writeActive\|readActive\|listActive\|clearFinished\|activeDir\|pidAlive\|idRE" cmd/shoal/*.go | grep -v _test.go`
Expected: only definitions in `state.go` / `platform_*.go` (no callers in non-test code). If any caller remains, STOP — Tasks 2/3 are incomplete.

- [ ] **Step 2: Remove the dead code**

Replace the entire body of `cmd/shoal/state.go` with:

```go
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// cacheEntry is what `download <short-id>` needs to start a prior search hit.
type cacheEntry struct {
	Magnet     string `json:"magnet"`
	TorrentURL string `json:"torrent_url"`
	Title      string `json:"title"`
}

func cachePath(base string) string { return filepath.Join(base, "last-search.json") }

// configDir is the shoal config dir (…/shoal), base for the search cache.
func configDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "shoal")
}

func writeCache(base string, m map[string]cacheEntry) error {
	if err := os.MkdirAll(base, 0o700); err != nil {
		return err
	}
	// MkdirAll won't chmod an existing dir; tighten the shared shoal dir explicitly.
	if err := os.Chmod(base, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(cachePath(base), b, 0o600)
}

func lookupCache(base, id string) (cacheEntry, bool) {
	b, err := os.ReadFile(cachePath(base))
	if err != nil {
		return cacheEntry{}, false
	}
	var m map[string]cacheEntry
	if json.Unmarshal(b, &m) != nil {
		return cacheEntry{}, false
	}
	e, ok := m[id]
	return e, ok
}
```

In `cmd/shoal/platform_unix.go`, delete the `pidAlive` function (keep `detachSysProcAttr`). If `pidAlive` was the only user of an import, drop that import (the `//go:build !windows` build tag and `detachSysProcAttr` remain; `syscall` is still used by `detachSysProcAttr`).

In `cmd/shoal/platform_windows.go`, delete the `pidAlive` function (keep `detachSysProcAttr`).

In `cmd/shoal/state_test.go`, delete `TestActiveRoundTripAndClear` (it tested `writeActive`/`listActive`/`clearFinished`). Keep `TestCacheRoundTripAndMiss`.

- [ ] **Step 3: Run tests to verify green**

Run: `go test ./cmd/shoal/` then `go build ./...`, `go vet ./cmd/shoal/`, `GOOS=windows go build ./cmd/shoal/`, `gofmt -l cmd/shoal/` (empty).
Expected: PASS; unix + windows build clean; no unused-symbol or unused-import errors.

- [ ] **Step 4: Commit**

```bash
git add cmd/shoal/state.go cmd/shoal/platform_unix.go cmd/shoal/platform_windows.go cmd/shoal/state_test.go
git commit -m "Remove the retired per-download worker/active state and pidAlive"
```

---

### Task 5: Docs

**Files:**
- Modify: `.claude/skills/shoal-download/SKILL.md`
- Modify: `README.md`

- [ ] **Step 1: Update the skill**

In `.claude/skills/shoal-download/SKILL.md`, update the download/flow guidance so it reflects the daemon: downloads now go to a shared background `shoal daemon` (auto-started on first `download`), and `status` reads it. Remove any `--out` mention; add: "All downloads land in shoal's configured folder (Settings / config.json)." The search → pick → `download '<magnet>'` → poll `status <id> --json` flow is unchanged from the caller's view.

- [ ] **Step 2: Update the README**

In `README.md`'s **Command line (scripting)** section: drop `--out` from the `download` line/description, and add a sentence: "CLI downloads run in a shared background `shoal daemon` (started automatically on the first `download`), so multiple downloads and `shoal status` all share one engine and one download folder."

- [ ] **Step 3: Verify + commit**

Run: `go build ./...` (sanity — no Go changed).

```bash
git add .claude/skills/shoal-download/SKILL.md README.md
git commit -m "docs: CLI downloads now run through the shared daemon"
```

---

## Self-Review

**Spec coverage:**
- `ensureDaemon` auto-start → Task 1. ✓
- `download` → daemon, `--out` notice, no worker → Task 2. ✓
- `status` → daemon, classification, no-daemon path, `--clear` → Task 3. ✓
- Retire `Active`/worker/`pidAlive`, trim `state.go` → Task 4. ✓
- Docs (SKILL.md + README) → Task 5. ✓
- No-network tests via in-process fake daemon → Tasks 1–3. ✓
- Windows still cross-compiles (`ensureDaemon` guards; `detachSysProcAttr` kept) → Tasks 1, 4. ✓

**Placeholder scan:** none — every code step carries complete code; removals name exact symbols. The two doc steps describe concrete edits.

**Type consistency:** `ensureDaemon() (*daemon.Client, error)`, `resolveTarget`/`dlTarget`, `statusState`/`toStatusRows`/`statusRow`/`printStatus`, `fakeEngine`/`serveFakeDaemon` are used identically across tasks. `runDownload` calls `eng.AddMagnet`/`AddTorrentURL`/`Close` on the `*daemon.Client` (which implements `engine.Engine`); `runStatus` calls `c.Statuses()`/`c.Remove(hash,false)`. The `fakeEngine` in `fake_engine_test.go` (Task 2) is the single shared test fake used by Tasks 2 and 3; the old `frames`-based `fakeEngine` in `cli_download_test.go` is removed in Task 2 to avoid a duplicate declaration.
