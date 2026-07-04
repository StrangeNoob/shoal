# Shared-engine daemon Phase 1 — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a daemon process that owns shoal's engine and a client that speaks to it over a unix socket while satisfying `engine.Engine`, so later phases can point the CLI/TUI at one shared engine.

**Architecture:** A new `internal/daemon` package: stdlib `net/rpc` + `net/rpc/jsonrpc` over a unix socket. An `EngineService` wraps any `engine.Engine` on the server; a `*Client` implements `engine.Engine` by RPC. A `shoal daemon` command runs the real engine and serves it (foreground). Nothing is wired to it yet.

**Tech Stack:** Go, stdlib `net`/`net/rpc`/`net/rpc/jsonrpc`, existing `internal/engine`.

## Global Constraints

- Go; stdlib + already-vendored deps only — **no new module dependencies**.
- TDD: write the failing test first. Commits carry **no Claude attribution**.
- Transport is a unix socket; the path is `SocketPath()` — env var `SHOAL_DAEMON_SOCK` overrides, else `filepath.Join(os.UserConfigDir(), "shoal", "daemon.sock")`.
- The client's `Close()` closes the connection only — it never closes the shared engine.
- Machine/user output → the provided `out` writer; diagnostics → stderr.
- Windows: `shoal daemon` prints an "unsupported" message and exits 1 (no unix transport yet); the rest of the binary must still cross-compile for Windows.
- Tests never hit the network or start a real anacrolix engine: use a fake `engine.Engine` and a short unix socket under `t.TempDir()` (set `SHOAL_DAEMON_SOCK` to keep the path under the ~104-char macOS `sun_path` limit).
- `gofmt -l` clean (CI enforces it); run `go vet` and `go build ./...` before each commit.

---

### Task 1: Daemon RPC library (`internal/daemon`)

The protocol types, the server that wraps an `engine.Engine`, and the client that implements `engine.Engine` — proven end-to-end with a fake engine over a real socket.

**Files:**
- Create: `internal/daemon/protocol.go`, `internal/daemon/server.go`, `internal/daemon/client.go`
- Test: `internal/daemon/daemon_test.go`

**Interfaces:**
- Consumes: `engine.Engine` (interface), `engine.Status` (struct) from `github.com/StrangeNoob/shoal/internal/engine`.
- Produces:
  - `daemon.SocketPath() string`
  - types `AddMagnetArgs`, `AddTorrentURLArgs`, `RemoveArgs`, `HashArgs`, `Empty`, `StatusesReply`
  - `daemon.Serve(l net.Listener, eng engine.Engine) error`
  - `daemon.Dial(path string) (*daemon.Client, error)`; `*Client` implements `engine.Engine`

- [ ] **Step 1: Write the failing test**

```go
// internal/daemon/daemon_test.go
package daemon

import (
	"errors"
	"net"
	"path/filepath"
	"sync"
	"testing"

	"github.com/StrangeNoob/shoal/internal/engine"
)

// fakeEngine records calls (mutex-guarded so the -race detector is happy across
// the server goroutine and the test goroutine).
type fakeEngine struct {
	mu       sync.Mutex
	magnets  []string
	urls     [][2]string
	removed  []string
	paused   []string
	resumed  []string
	statuses []engine.Status
	addErr   error
}

func (f *fakeEngine) AddMagnet(m string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.magnets = append(f.magnets, m)
	return f.addErr
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
func (f *fakeEngine) Pause(h string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paused = append(f.paused, h)
	return nil
}
func (f *fakeEngine) Resume(h string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resumed = append(f.resumed, h)
	return nil
}
func (f *fakeEngine) Close() error { return nil }

func (f *fakeEngine) snap() ([]string, [][2]string, []string, []string, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.magnets, f.urls, f.removed, f.paused, f.resumed
}

func serveTest(t *testing.T, eng engine.Engine) *Client {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "d.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	go Serve(l, eng)
	t.Cleanup(func() { l.Close() })
	c, err := Dial(sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestRoundTrip(t *testing.T) {
	fake := &fakeEngine{statuses: []engine.Status{
		{Name: "Movie", InfoHash: "abc", TotalBytes: 100, CompletedBytes: 40, Peers: 3},
	}}
	c := serveTest(t, fake)

	for _, err := range []error{
		c.AddMagnet("magnet:x"),
		c.AddTorrentURL("http://x/y.torrent", "Y"),
		c.Remove("h1", true),
		c.Pause("h2"),
		c.Resume("h3"),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	st := c.Statuses()

	magnets, urls, removed, paused, resumed := fake.snap()
	if len(magnets) != 1 || magnets[0] != "magnet:x" {
		t.Errorf("magnets=%v", magnets)
	}
	if len(urls) != 1 || urls[0] != [2]string{"http://x/y.torrent", "Y"} {
		t.Errorf("urls=%v", urls)
	}
	if len(removed) != 1 || removed[0] != "h1" {
		t.Errorf("removed=%v", removed)
	}
	if len(paused) != 1 || paused[0] != "h2" || len(resumed) != 1 || resumed[0] != "h3" {
		t.Errorf("paused=%v resumed=%v", paused, resumed)
	}
	if len(st) != 1 || st[0].Name != "Movie" || st[0].CompletedBytes != 40 || st[0].Peers != 3 {
		t.Errorf("statuses=%+v", st)
	}
}

func TestErrorPropagation(t *testing.T) {
	c := serveTest(t, &fakeEngine{addErr: errors.New("boom")})
	if err := c.AddMagnet("m"); err == nil || err.Error() != "boom" {
		t.Fatalf("want boom error, got %v", err)
	}
}

func TestStatusesOnClosedConn(t *testing.T) {
	c := serveTest(t, &fakeEngine{})
	c.Close()
	if st := c.Statuses(); st != nil {
		t.Fatalf("closed client should return nil statuses, got %v", st)
	}
}

func TestSocketPathEnvOverride(t *testing.T) {
	t.Setenv("SHOAL_DAEMON_SOCK", "/tmp/custom.sock")
	if SocketPath() != "/tmp/custom.sock" {
		t.Fatalf("env override ignored: %s", SocketPath())
	}
}

var _ engine.Engine = (*Client)(nil)
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/daemon/ -v`
Expected: FAIL — `undefined: Client` / `Serve` / `Dial` / `SocketPath` (compile error).

- [ ] **Step 3: Write minimal implementation**

```go
// internal/daemon/protocol.go
package daemon

import (
	"os"
	"path/filepath"

	"github.com/StrangeNoob/shoal/internal/engine"
)

// SocketPath is the unix socket the daemon listens on and clients dial. The
// SHOAL_DAEMON_SOCK env var overrides it (useful for tests and running more than
// one daemon).
func SocketPath() string {
	if p := os.Getenv("SHOAL_DAEMON_SOCK"); p != "" {
		return p
	}
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		return filepath.Join(".", "shoal-daemon.sock")
	}
	return filepath.Join(dir, "shoal", "daemon.sock")
}

type AddMagnetArgs struct{ Magnet string }
type AddTorrentURLArgs struct{ URL, Name string }
type RemoveArgs struct {
	InfoHash   string
	DeleteData bool
}
type HashArgs struct{ InfoHash string }
type Empty struct{}
type StatusesReply struct{ Statuses []engine.Status }
```

```go
// internal/daemon/server.go
package daemon

import (
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"

	"github.com/StrangeNoob/shoal/internal/engine"
)

// EngineService adapts an engine.Engine to net/rpc method shapes (Engine.*).
type EngineService struct{ eng engine.Engine }

func (s *EngineService) AddMagnet(a AddMagnetArgs, _ *Empty) error {
	return s.eng.AddMagnet(a.Magnet)
}
func (s *EngineService) AddTorrentURL(a AddTorrentURLArgs, _ *Empty) error {
	return s.eng.AddTorrentURL(a.URL, a.Name)
}
func (s *EngineService) Statuses(_ Empty, r *StatusesReply) error {
	r.Statuses = s.eng.Statuses()
	return nil
}
func (s *EngineService) Remove(a RemoveArgs, _ *Empty) error {
	return s.eng.Remove(a.InfoHash, a.DeleteData)
}
func (s *EngineService) Pause(a HashArgs, _ *Empty) error {
	return s.eng.Pause(a.InfoHash)
}
func (s *EngineService) Resume(a HashArgs, _ *Empty) error {
	return s.eng.Resume(a.InfoHash)
}

// Serve answers Engine.* RPC calls against eng for every connection accepted on
// l, until l is closed. The wrapped engine is already concurrency-safe.
func Serve(l net.Listener, eng engine.Engine) error {
	srv := rpc.NewServer()
	if err := srv.RegisterName("Engine", &EngineService{eng: eng}); err != nil {
		return err
	}
	for {
		conn, err := l.Accept()
		if err != nil {
			return err // listener closed
		}
		go srv.ServeCodec(jsonrpc.NewServerCodec(conn))
	}
}
```

```go
// internal/daemon/client.go
package daemon

import (
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"

	"github.com/StrangeNoob/shoal/internal/engine"
)

// Client talks to a daemon over a unix socket and implements engine.Engine.
type Client struct{ rpc *rpc.Client }

// Dial connects to the daemon listening at the unix socket path.
func Dial(path string) (*Client, error) {
	conn, err := net.Dial("unix", path)
	if err != nil {
		return nil, err
	}
	return &Client{rpc: jsonrpc.NewClient(conn)}, nil
}

func (c *Client) AddMagnet(m string) error {
	return c.rpc.Call("Engine.AddMagnet", AddMagnetArgs{Magnet: m}, &Empty{})
}
func (c *Client) AddTorrentURL(url, name string) error {
	return c.rpc.Call("Engine.AddTorrentURL", AddTorrentURLArgs{URL: url, Name: name}, &Empty{})
}
func (c *Client) Statuses() []engine.Status {
	var r StatusesReply
	if err := c.rpc.Call("Engine.Statuses", Empty{}, &r); err != nil {
		return nil
	}
	return r.Statuses
}
func (c *Client) Remove(hash string, deleteData bool) error {
	return c.rpc.Call("Engine.Remove", RemoveArgs{InfoHash: hash, DeleteData: deleteData}, &Empty{})
}
func (c *Client) Pause(hash string) error {
	return c.rpc.Call("Engine.Pause", HashArgs{InfoHash: hash}, &Empty{})
}
func (c *Client) Resume(hash string) error {
	return c.rpc.Call("Engine.Resume", HashArgs{InfoHash: hash}, &Empty{})
}

// Close closes the RPC connection only — it does not stop the shared engine.
func (c *Client) Close() error { return c.rpc.Close() }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/daemon/ -v` then `go vet ./internal/daemon/` and `go build ./...`
Expected: PASS (TestRoundTrip, TestErrorPropagation, TestStatusesOnClosedConn, TestSocketPathEnvOverride); the `var _ engine.Engine = (*Client)(nil)` line compiles (interface conformance).

- [ ] **Step 5: Commit**

```bash
git add internal/daemon/protocol.go internal/daemon/server.go internal/daemon/client.go internal/daemon/daemon_test.go
git commit -m "Add daemon RPC library: engine service + client over a unix socket"
```

---

### Task 2: `shoal daemon` command

Wire a foreground `shoal daemon` command that runs the real engine and serves it, with a single-instance guard.

**Files:**
- Create: `cmd/shoal/cli_daemon.go`
- Modify: `cmd/shoal/main.go` (routing + usage)
- Test: `cmd/shoal/cli_daemon_test.go`

**Interfaces:**
- Consumes: `daemon.SocketPath`, `daemon.Dial`, `daemon.Serve` (Task 1); `config.Load`; `engine.NewAnacrolix`, `engine.Config`; `queue.DefaultPath`.
- Produces: `runDaemon(args []string, out io.Writer) int`; `daemonRunning(sock string) bool`; extended `cli()`.

- [ ] **Step 1: Write the failing test**

```go
// cmd/shoal/cli_daemon_test.go
package main

import (
	"bytes"
	"net"
	"path/filepath"
	"testing"
)

func TestDaemonRunningGuard(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "d.sock")
	if daemonRunning(sock) {
		t.Fatal("no socket yet — should be false")
	}
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if !daemonRunning(sock) {
		t.Fatal("a listening socket — should be true")
	}
}

func TestCLIRoutesDaemonGuarded(t *testing.T) {
	// Pretend a daemon is already running so runDaemon hits its guard and returns
	// immediately (no engine, no blocking Serve). SHOAL_DAEMON_SOCK keeps the path
	// short (macOS sun_path limit).
	sock := filepath.Join(t.TempDir(), "d.sock")
	t.Setenv("SHOAL_DAEMON_SOCK", sock)
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	var buf bytes.Buffer
	handled, code := cli([]string{"shoal", "daemon"}, "1.0.0", &buf)
	if !handled {
		t.Error("cli did not handle daemon")
	}
	if code != 1 {
		t.Errorf("already-running guard should exit 1, got %d", code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/shoal/ -run 'TestDaemonRunningGuard|TestCLIRoutesDaemonGuarded' -v`
Expected: FAIL — `undefined: daemonRunning`, and `cli` does not handle `daemon`.

- [ ] **Step 3: Write minimal implementation**

```go
// cmd/shoal/cli_daemon.go
package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/StrangeNoob/shoal/internal/config"
	"github.com/StrangeNoob/shoal/internal/daemon"
	"github.com/StrangeNoob/shoal/internal/engine"
	"github.com/StrangeNoob/shoal/internal/queue"
)

// daemonRunning reports whether a daemon is already accepting connections at sock.
func daemonRunning(sock string) bool {
	c, err := daemon.Dial(sock)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// runDaemon runs the shared engine and serves it on the unix socket (foreground).
func runDaemon(args []string, out io.Writer) int {
	if runtime.GOOS == "windows" {
		fmt.Fprintln(os.Stderr, "the shoal daemon is not yet supported on Windows")
		return 1
	}
	sock := daemon.SocketPath()
	if daemonRunning(sock) {
		fmt.Fprintln(os.Stderr, "shoal daemon already running at", sock)
		return 1
	}
	_ = os.Remove(sock) // clear a stale socket file
	if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
		fmt.Fprintln(os.Stderr, "shoal daemon:", err)
		return 1
	}

	cfg := config.Load()
	eng, err := engine.NewAnacrolix(engine.Config{
		DataDir:    cfg.DataDir,
		ListenPort: cfg.ListenPort,
		MaxPeers:   cfg.MaxPeers,
		Seed:       cfg.Seed,
		SeedRatio:  cfg.SeedRatio,
		QueuePath:  queue.DefaultPath(),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "shoal daemon: engine:", err)
		return 1
	}
	defer eng.Close()

	l, err := net.Listen("unix", sock)
	if err != nil {
		fmt.Fprintln(os.Stderr, "shoal daemon: listen:", err)
		return 1
	}
	fmt.Fprintln(out, "shoal daemon listening on", sock)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		l.Close()
	}()

	_ = daemon.Serve(l, eng) // returns when the listener closes
	_ = os.Remove(sock)
	return 0
}
```

In `cmd/shoal/main.go`, add a case to the `cli()` switch (next to the other subcommands, before `default:`):

```go
	case "daemon":
		return true, runDaemon(args[2:], out)
```

And add a usage line (after the `status` line):

```go
  shoal daemon                  run the shared background engine (experimental)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/shoal/ -run 'TestDaemonRunningGuard|TestCLIRoutesDaemonGuarded' -v` then the full `go test ./cmd/shoal/`, `go vet ./cmd/shoal/`, `go build ./...`, `GOOS=windows go build ./cmd/shoal/`, and `gofmt -l cmd/shoal/ internal/daemon/` (empty).
Expected: PASS; Windows cross-compile clean.

**Do NOT add `"daemon"` to the existing `TestCLIRoutesNewSubcommands` loop** — invoking `shoal daemon` with no daemon running would start a real engine and block on `Serve`. `TestCLIRoutesDaemonGuarded` covers its routing via the already-running guard path.

- [ ] **Step 5: Commit**

```bash
git add cmd/shoal/cli_daemon.go cmd/shoal/main.go cmd/shoal/cli_daemon_test.go
git commit -m "Add 'shoal daemon' command (foreground shared engine over a socket)"
```

---

## Self-Review

**Spec coverage:**
- Transport (unix socket + net/rpc jsonrpc), `SocketPath` (+ env override) → Task 1. ✓
- Protocol types, `EngineService`, `Serve` → Task 1. ✓
- `Client` implementing `engine.Engine`, local-only `Close` → Task 1 (compile-time `var _ engine.Engine`). ✓
- `shoal daemon` command: engine from config, listen/serve/SIGINT cleanup, single-instance guard, Windows guard → Task 2. ✓
- Round-trip / error-propagation / closed-conn / interface-conformance tests → Task 1; guard + routing tests → Task 2. ✓
- No wiring of CLI/TUI, no auto-start/detach, no Windows transport — correctly absent (later phases). ✓

**Placeholder scan:** none — every step carries complete code; the one narrative note (don't add `daemon` to the existing routing loop) is a concrete instruction, not a placeholder.

**Type consistency:** `AddMagnetArgs`/`AddTorrentURLArgs`/`RemoveArgs`/`HashArgs`/`Empty`/`StatusesReply`, `Serve`, `Dial`, `Client`, `SocketPath`, `daemonRunning`, `runDaemon` are used identically across tasks. The client's method set matches `engine.Engine` exactly (6 RPC methods + local `Close`), and the server's `Engine.*` method names match the client's `Call` strings.
