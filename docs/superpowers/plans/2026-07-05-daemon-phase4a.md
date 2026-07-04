# Shared-engine daemon Phase 4a — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the shared daemon well-behaved — bind-first (no double-start), `daemon stop`/`status` from the CLI, and idle auto-shutdown of an empty, unused daemon.

**Architecture:** A new `daemon.Server` struct owns lifecycle state (start time, open-connection count, a shutdown trigger, idle timeout) and registers `Engine.*` plus a new `Control.*` (Shutdown, Status) RPC service. `runDaemon` binds the socket first (dropping the check-then-listen TOCTOU) and an idle monitor exits the daemon after a configurable timeout. The old free `Serve(l, eng)` stays as a thin wrapper so existing callers/tests are untouched.

**Tech Stack:** Go, stdlib (`net/rpc`, `net/rpc/jsonrpc`, unix sockets), the existing `internal/engine`/`internal/config`.

## Global Constraints

- Go; stdlib + already-vendored deps only — **no new module dependencies**.
- TDD: write the failing test first. Commits carry **no Claude attribution**.
- Unix/macOS only (Windows transport is Phase 4c). The binary must still `GOOS=windows go build ./cmd/shoal/` cleanly.
- Idle = `openConns == 0 && len(eng.Statuses()) == 0` held continuously for the timeout; a torrent or an open connection keeps it alive. Default 10 min (`daemon_idle_minutes`; `0` disables).
- `daemon stop` uses a `Control.Shutdown` RPC (no pidfile). `daemon status` is human-readable (no `--json`).
- Tests use an in-process `daemon.Server`/`Serve` on a **short** temp unix socket (`os.MkdirTemp`, not `t.TempDir()` for socket dirs — `t.TempDir()` embeds the test name and can overflow macOS's ~104-byte `sun_path`).
- `gofmt -l` clean (CI enforces it); run `go vet`, `go build ./...`, `go test ./...` before each commit.

**Interfaces already present:** `engine.Engine` (AddMagnet/AddTorrentURL/Statuses/Remove/Pause/Resume/Close); `engine.Status{Name, InfoHash string; TotalBytes, CompletedBytes int64; Done, Seeding bool; ...}`. `daemon.Dial(path) (*Client, error)`, `daemon.SocketPath()`, existing `Empty`/`StatusesReply` in `protocol.go`, `EngineService` in `server.go`. `internal/daemon/daemon_test.go` has a reusable `fakeEngine{statuses []engine.Status, addErr error, ...}` and `serveTest(t, eng) *Client`. `daemonRunning(sock string) bool` and `recordCompletions(...)` in `cmd/shoal/cli_daemon.go`. `config.Load()`/`config.Default()` unmarshal over defaults.

---

### Task 1: config `DaemonIdleMinutes`

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces: `Config.DaemonIdleMinutes int` (`json:"daemon_idle_minutes"`), default `10`.

- [ ] **Step 1: Write the failing test**

Append to `internal/config/config_test.go`:

```go
func TestDaemonIdleMinutesDefault(t *testing.T) {
	if got := config.Default().DaemonIdleMinutes; got != 10 {
		t.Fatalf("Default().DaemonIdleMinutes = %d, want 10", got)
	}
}
```

(If `config_test.go` uses `package config` rather than `config_test`, call `Default()` unqualified. Match the file's existing package clause.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestDaemonIdleMinutesDefault -v`
Expected: FAIL — `DaemonIdleMinutes` is not a field / is `0`.

- [ ] **Step 3: Write minimal implementation**

In `internal/config/config.go`, add the field to the `Config` struct (in the "Downloads / engine" group, after `ListenPort`):

```go
	// Daemon
	DaemonIdleMinutes int `json:"daemon_idle_minutes"` // minutes idle (no torrents, no client) before auto-shutdown; 0 disables
```

And in `Default()`, add:

```go
		DaemonIdleMinutes: 10,
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -v` then `go build ./...`
Expected: PASS. (`Load()` already unmarshals over `Default()`, so an existing `config.json` without the key keeps `10`; an explicit `0` disables — no extra defaulting code needed.)

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "config: add DaemonIdleMinutes (default 10) for daemon idle shutdown"
```

---

### Task 2: `daemon.Server`, `Control` RPC, and client methods

**Files:**
- Modify: `internal/daemon/server.go` (add `Server`, `Control`, keep `Serve` wrapper)
- Modify: `internal/daemon/protocol.go` (add `StatusReply`)
- Modify: `internal/daemon/client.go` (add `Shutdown`, `Status`)
- Create: `internal/daemon/lifecycle_test.go`

**Interfaces:**
- Consumes: `engine.Engine`, the test `fakeEngine`.
- Produces: `NewServer(eng engine.Engine, started time.Time, idleTimeout time.Duration) *Server`; `(*Server).Serve(l net.Listener) error`; `(*Server).Shutdown()`; RPC `Control.Shutdown`/`Control.Status`; `StatusReply{Uptime time.Duration; Torrents, Downloading, Seeding, Pid int}`; `(*Client).Shutdown() error`; `(*Client).Status() (StatusReply, error)`. Back-compat `Serve(l net.Listener, eng engine.Engine) error`.

- [ ] **Step 1: Write the failing tests**

Create `internal/daemon/lifecycle_test.go`:

```go
package daemon

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/StrangeNoob/shoal/internal/engine"
)

// serveServer runs srv on a short-path temp unix socket and returns a connected
// Client plus a channel carrying Serve's return value.
func serveServer(t *testing.T, srv *Server) (*Client, <-chan error) {
	t.Helper()
	dir, err := os.MkdirTemp("", "shoal-d") // short path — macOS sun_path limit
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "d.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(l) }()
	t.Cleanup(func() { l.Close() })
	c, err := Dial(sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c, errc
}

func TestControlShutdownStopsServe(t *testing.T) {
	srv := NewServer(&fakeEngine{}, time.Now(), 0)
	c, errc := serveServer(t, srv)
	if err := c.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	select {
	case err := <-errc:
		if err != nil {
			t.Fatalf("Serve returned %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after Control.Shutdown")
	}
}

func TestControlStatusCounts(t *testing.T) {
	fake := &fakeEngine{statuses: []engine.Status{
		{InfoHash: "a", Done: false},
		{InfoHash: "b", Done: true},
	}}
	srv := NewServer(fake, time.Now(), 0)
	c, _ := serveServer(t, srv)
	st, err := c.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Torrents != 2 || st.Downloading != 1 || st.Seeding != 1 {
		t.Fatalf("counts = %+v, want 2/1/1", st)
	}
	if st.Pid != os.Getpid() {
		t.Fatalf("Pid = %d, want %d", st.Pid, os.Getpid())
	}
	if st.Uptime <= 0 {
		t.Fatalf("Uptime = %v, want > 0", st.Uptime)
	}
}

func TestIdleShutdownFires(t *testing.T) {
	// No client connected, no torrents → idle → Serve returns within the timeout.
	dir, err := os.MkdirTemp("", "shoal-d")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	l, err := net.Listen("unix", filepath.Join(dir, "d.sock"))
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(&fakeEngine{}, time.Now(), 60*time.Millisecond)
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(l) }()
	select {
	case <-errc: // Serve returned = idle shutdown fired
	case <-time.After(2 * time.Second):
		l.Close()
		t.Fatal("idle shutdown did not fire")
	}
}

func TestIdleHeldOffByTorrent(t *testing.T) {
	dir, err := os.MkdirTemp("", "shoal-d")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	l, err := net.Listen("unix", filepath.Join(dir, "d.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	srv := NewServer(&fakeEngine{statuses: []engine.Status{{InfoHash: "a"}}}, time.Now(), 60*time.Millisecond)
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(l) }()
	select {
	case <-errc:
		t.Fatal("Serve returned; a torrent should keep the daemon alive")
	case <-time.After(300 * time.Millisecond): // still running — good
	}
}

func TestIdleHeldOffByConnection(t *testing.T) {
	dir, err := os.MkdirTemp("", "shoal-d")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "d.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	srv := NewServer(&fakeEngine{}, time.Now(), 60*time.Millisecond)
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(l) }()
	conn, err := net.Dial("unix", sock) // hold a connection open
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-errc:
		conn.Close()
		t.Fatal("Serve returned; an open connection should keep the daemon alive")
	case <-time.After(300 * time.Millisecond): // still running — good
	}
	conn.Close() // now idle → should shut down
	select {
	case <-errc:
	case <-time.After(2 * time.Second):
		t.Fatal("idle shutdown did not fire after the connection closed")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/daemon/ -run 'TestControl|TestIdle' -v`
Expected: FAIL — `NewServer`, `(*Client).Shutdown`, `(*Client).Status`, `StatusReply` undefined.

- [ ] **Step 3: Write the implementation**

Add to `internal/daemon/protocol.go` (needs a `"time"` import):

```go
type StatusReply struct {
	Uptime      time.Duration
	Torrents    int
	Downloading int
	Seeding     int
	Pid         int
}
```

Replace `internal/daemon/server.go`'s `Serve` function (keep the `EngineService` type and its methods above it unchanged) with the `Server` type plus a back-compat wrapper. The full new tail of `server.go` (imports become `errors`, `net`, `net/rpc`, `net/rpc/jsonrpc`, `os`, `sync`, `time`, and `engine`):

```go
// Server owns a daemon's lifecycle: it serves Engine.* and Control.* RPC over a
// listener, tracks open connections, and shuts itself down when idle.
type Server struct {
	eng         engine.Engine
	started     time.Time
	idleTimeout time.Duration // 0 = no idle shutdown
	mu          sync.Mutex
	conns       int
	shutdown    chan struct{}
	once        sync.Once
}

func NewServer(eng engine.Engine, started time.Time, idleTimeout time.Duration) *Server {
	return &Server{eng: eng, started: started, idleTimeout: idleTimeout, shutdown: make(chan struct{})}
}

// Shutdown triggers a graceful stop (idempotent): the listener closes and Serve
// returns. Called by the Control.Shutdown RPC, the idle monitor, and the daemon's
// signal handler.
func (s *Server) Shutdown() { s.once.Do(func() { close(s.shutdown) }) }

// Serve registers Engine.* and Control.*, runs the idle monitor, and accepts
// connections (tracking the open count) until the listener closes.
func (s *Server) Serve(l net.Listener) error {
	srv := rpc.NewServer()
	if err := srv.RegisterName("Engine", &EngineService{eng: s.eng}); err != nil {
		return err
	}
	if err := srv.RegisterName("Control", &controlService{s: s}); err != nil {
		return err
	}
	go func() { <-s.shutdown; l.Close() }()
	go s.monitorIdle()
	for {
		conn, err := l.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil // graceful shutdown: the listener was closed
			}
			return err
		}
		s.mu.Lock()
		s.conns++
		s.mu.Unlock()
		go func() {
			defer func() { s.mu.Lock(); s.conns--; s.mu.Unlock() }()
			srv.ServeCodec(jsonrpc.NewServerCodec(conn))
		}()
	}
}

func (s *Server) idle() bool {
	s.mu.Lock()
	c := s.conns
	s.mu.Unlock()
	return c == 0 && len(s.eng.Statuses()) == 0
}

// monitorIdle triggers Shutdown once the daemon has been idle for idleTimeout.
func (s *Server) monitorIdle() {
	if s.idleTimeout <= 0 {
		return
	}
	check := s.idleTimeout / 4
	if check < 10*time.Millisecond {
		check = 10 * time.Millisecond
	}
	if check > time.Minute {
		check = time.Minute
	}
	t := time.NewTicker(check)
	defer t.Stop()
	var idleSince time.Time
	for {
		select {
		case <-s.shutdown:
			return
		case now := <-t.C:
			if s.idle() {
				if idleSince.IsZero() {
					idleSince = now
				} else if now.Sub(idleSince) >= s.idleTimeout {
					s.Shutdown()
					return
				}
			} else {
				idleSince = time.Time{}
			}
		}
	}
}

// controlService exposes daemon lifecycle over RPC (Control.*).
type controlService struct{ s *Server }

func (c *controlService) Shutdown(_ Empty, _ *Empty) error {
	c.s.Shutdown()
	return nil
}

func (c *controlService) Status(_ Empty, r *StatusReply) error {
	ss := c.s.eng.Statuses()
	r.Uptime = time.Since(c.s.started)
	r.Torrents = len(ss)
	for _, st := range ss {
		if st.Done {
			r.Seeding++
		} else {
			r.Downloading++
		}
	}
	r.Pid = os.Getpid()
	return nil
}

// Serve answers Engine.* RPC against eng until l closes (no idle shutdown). Kept
// for callers that don't need lifecycle control (tests, the CLI's fake daemon).
func Serve(l net.Listener, eng engine.Engine) error {
	return NewServer(eng, time.Now(), 0).Serve(l)
}
```

Add to `internal/daemon/client.go`:

```go
// Shutdown asks the daemon to stop gracefully.
func (c *Client) Shutdown() error {
	return c.rpc.Call("Control.Shutdown", Empty{}, &Empty{})
}

// Status reports the daemon's uptime and torrent counts.
func (c *Client) Status() (StatusReply, error) {
	var r StatusReply
	err := c.rpc.Call("Control.Status", Empty{}, &r)
	return r, err
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/daemon/ -run 'TestControl|TestIdle|TestRoundTrip' -v` then the full `go test ./internal/daemon/`, `go test -race ./internal/daemon/`, `go build ./...`, `go vet ./internal/daemon/`, `gofmt -l internal/daemon/` (empty).
Expected: PASS; the existing `TestRoundTrip` (via the `Serve` wrapper) still passes; `-race` clean (conn counter + engine access are mutex-guarded).

- [ ] **Step 5: Commit**

```bash
git add internal/daemon/server.go internal/daemon/protocol.go internal/daemon/client.go internal/daemon/lifecycle_test.go
git commit -m "daemon: Server with Control.Shutdown/Status RPC and idle auto-shutdown"
```

---

### Task 3: bind-first listen + wire the Server into `runDaemon`

**Files:**
- Modify: `cmd/shoal/cli_daemon.go` (`listenDaemon`, rewrite `runDaemon`)
- Test: `cmd/shoal/cli_daemon_test.go` (append `listenDaemon` tests)

**Interfaces:**
- Consumes: `daemon.NewServer` / `(*Server).Shutdown` / `(*Server).Serve` (Task 2), `config.DaemonIdleMinutes` (Task 1), existing `daemonRunning`, `recordCompletions`.
- Produces: `listenDaemon(sock string) (net.Listener, error)`.

- [ ] **Step 1: Write the failing test**

Append to `cmd/shoal/cli_daemon_test.go`:

```go
func TestListenDaemonReclaimsStaleSocket(t *testing.T) {
	dir, err := os.MkdirTemp("", "shoal-d")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "d.sock")
	// A stale socket *file* with nothing listening (create then close a listener).
	l0, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	l0.Close()               // leaves (or removes) the file; either way no daemon answers
	_ = os.WriteFile(sock, nil, 0o600) // ensure a leftover file is present to reclaim
	l, err := listenDaemon(sock)
	if err != nil {
		t.Fatalf("listenDaemon should reclaim a stale socket: %v", err)
	}
	l.Close()
}

func TestListenDaemonRefusesLiveDaemon(t *testing.T) {
	dir, err := os.MkdirTemp("", "shoal-d")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "d.sock")
	fake := &fakeEngine{}
	l0, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	go daemon.Serve(l0, fake) // a live daemon is answering
	t.Cleanup(func() { l0.Close() })
	if _, err := listenDaemon(sock); err == nil {
		t.Fatal("listenDaemon should refuse when a live daemon already answers")
	}
}
```

(`os`, `net`, `path/filepath`, `daemon` are already imported in `cli_daemon_test.go`; `fakeEngine` lives in `cmd/shoal/fake_engine_test.go`.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/shoal/ -run TestListenDaemon -v`
Expected: FAIL — `listenDaemon` undefined.

- [ ] **Step 3: Write the implementation**

Add `listenDaemon` to `cmd/shoal/cli_daemon.go`:

```go
// listenDaemon binds the daemon socket, refusing to start if a live daemon
// already answers and reclaiming a stale socket file otherwise (bind-first, so a
// racing second daemon fails here instead of via a check-then-listen TOCTOU).
func listenDaemon(sock string) (net.Listener, error) {
	if l, err := net.Listen("unix", sock); err == nil {
		return l, nil
	}
	if daemonRunning(sock) {
		return nil, fmt.Errorf("shoal daemon already running at %s", sock)
	}
	_ = os.Remove(sock) // stale socket file — reclaim it
	return net.Listen("unix", sock)
}
```

Rewrite `runDaemon` so it binds first (before building the engine) and serves via a `daemon.Server` with the configured idle timeout. Replace the current body from `sock := daemon.SocketPath()` through the end of the function with:

```go
	cfg := config.Load()
	sock := daemon.SocketPath()
	if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
		fmt.Fprintln(os.Stderr, "shoal daemon:", err)
		return 1
	}
	l, err := listenDaemon(sock) // bind first — a second daemon fails here
	if err != nil {
		fmt.Fprintln(os.Stderr, "shoal daemon:", err)
		return 1
	}

	eng, err := engine.NewAnacrolix(engine.Config{
		DataDir:    cfg.DataDir,
		ListenPort: cfg.ListenPort,
		MaxPeers:   cfg.MaxPeers,
		Seed:       cfg.Seed,
		SeedRatio:  cfg.SeedRatio,
		QueuePath:  queue.DefaultPath(),
	})
	if err != nil {
		l.Close()
		_ = os.Remove(sock)
		fmt.Fprintln(os.Stderr, "shoal daemon: engine:", err)
		return 1
	}
	defer eng.Close()

	srv := daemon.NewServer(eng, time.Now(), time.Duration(cfg.DaemonIdleMinutes)*time.Minute)
	fmt.Fprintln(out, "shoal daemon listening on", sock)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() { <-sig; srv.Shutdown() }()

	hist := history.Load()
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		recordCompletions(eng, &hist, time.Second, stop)
		close(done)
	}()

	_ = srv.Serve(l) // returns when the listener closes (stop/idle/signal)
	close(stop)
	<-done // wait for recordCompletions to exit before eng.Close() runs (deferred)
	_ = os.Remove(sock)
	return 0
```

(Keep the `runtime.GOOS == "windows"` guard at the top of `runDaemon` unchanged. The old `if daemonRunning(sock) {…}` pre-check and the standalone `_ = os.Remove(sock)` before listening are removed — `listenDaemon` subsumes them.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/shoal/ -run 'TestListenDaemon|TestCLIRoutesDaemonGuarded' -v` then the full `go test ./cmd/shoal/`, `go build ./...`, `go vet ./cmd/shoal/`, `gofmt -l cmd/shoal/` (empty), `GOOS=windows go build ./cmd/shoal/` (clean); remove any stray `shoal.exe`.
Expected: PASS. `TestCLIRoutesDaemonGuarded` (a live socket → `cli(["shoal","daemon"])` → exit 1) still passes: `listenDaemon` sees the live daemon and returns the "already running" error.

- [ ] **Step 5: Commit**

```bash
git add cmd/shoal/cli_daemon.go cmd/shoal/cli_daemon_test.go
git commit -m "daemon: bind-first listen; serve via daemon.Server with idle timeout"
```

---

### Task 4: `daemon stop` / `daemon status` subcommands

**Files:**
- Modify: `cmd/shoal/cli_daemon.go` (`runDaemonCmd`, `runDaemonStop`, `runDaemonStatus`)
- Modify: `cmd/shoal/main.go` (route `daemon` → `runDaemonCmd`; help text)
- Test: `cmd/shoal/cli_daemon_test.go` (append stop/status tests)

**Interfaces:**
- Consumes: `daemon.Dial`, `(*Client).Shutdown`/`Status` (Task 2), `daemonRunning`, `serveFakeDaemon` (in `cmd/shoal/fake_engine_test.go`).
- Produces: `runDaemonCmd(args []string, out io.Writer) int`, `runDaemonStop`, `runDaemonStatus`.

- [ ] **Step 1: Write the failing tests**

Append to `cmd/shoal/cli_daemon_test.go`:

```go
func TestDaemonStopStopsRunning(t *testing.T) {
	serveFakeDaemon(t, &fakeEngine{}) // serves via daemon.Serve (registers Control)
	var buf bytes.Buffer
	if code := runDaemonStop(&buf); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(buf.String(), "daemon stopped") {
		t.Fatalf("output = %q, want 'daemon stopped'", buf.String())
	}
}

func TestDaemonStopNoDaemon(t *testing.T) {
	t.Setenv("SHOAL_DAEMON_SOCK", filepath.Join(t.TempDir(), "absent.sock"))
	var buf bytes.Buffer
	if code := runDaemonStop(&buf); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(buf.String(), "daemon not running") {
		t.Fatalf("output = %q, want 'daemon not running'", buf.String())
	}
}

func TestDaemonStatusReportsCounts(t *testing.T) {
	serveFakeDaemon(t, &fakeEngine{statuses: []engine.Status{
		{InfoHash: "a", Done: false},
		{InfoHash: "b", Done: true},
	}})
	var buf bytes.Buffer
	if code := runDaemonStatus(&buf); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	out := buf.String()
	if !strings.Contains(out, "running") || !strings.Contains(out, "2 torrents") ||
		!strings.Contains(out, "1 downloading") || !strings.Contains(out, "1 seeding") {
		t.Fatalf("status output = %q", out)
	}
}

func TestDaemonStatusNoDaemon(t *testing.T) {
	t.Setenv("SHOAL_DAEMON_SOCK", filepath.Join(t.TempDir(), "absent.sock"))
	var buf bytes.Buffer
	if code := runDaemonStatus(&buf); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(buf.String(), "daemon not running") {
		t.Fatalf("output = %q, want 'daemon not running'", buf.String())
	}
}
```

(`bytes`, `strings`, `path/filepath`, `engine` are imported in the cmd/shoal test files; `serveFakeDaemon` sets `SHOAL_DAEMON_SOCK` to the served socket. NOTE for the implementer: `serveFakeDaemon` currently serves via `daemon.Serve`; after Task 2 that registers `Control`, so `Shutdown`/`Status` work against it.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/shoal/ -run TestDaemonSt -v`
Expected: FAIL — `runDaemonStop`/`runDaemonStatus` undefined.

- [ ] **Step 3: Write the implementation**

Add to `cmd/shoal/cli_daemon.go`:

```go
// runDaemonCmd dispatches `daemon` (run), `daemon stop`, and `daemon status`.
func runDaemonCmd(args []string, out io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "stop":
			return runDaemonStop(out)
		case "status":
			return runDaemonStatus(out)
		}
	}
	return runDaemon(args, out)
}

func runDaemonStop(out io.Writer) int {
	c, err := daemon.Dial(daemon.SocketPath())
	if err != nil {
		fmt.Fprintln(out, "daemon not running")
		return 0
	}
	defer c.Close()
	if err := c.Shutdown(); err != nil {
		fmt.Fprintln(os.Stderr, "shoal daemon stop:", err)
		return 1
	}
	for i := 0; i < 25; i++ { // poll ~5s for it to exit
		time.Sleep(200 * time.Millisecond)
		if !daemonRunning(daemon.SocketPath()) {
			fmt.Fprintln(out, "daemon stopped")
			return 0
		}
	}
	fmt.Fprintln(out, "daemon stop requested (still shutting down)")
	return 0
}

func runDaemonStatus(out io.Writer) int {
	c, err := daemon.Dial(daemon.SocketPath())
	if err != nil {
		fmt.Fprintln(out, "daemon not running")
		return 0
	}
	defer c.Close()
	st, err := c.Status()
	if err != nil {
		fmt.Fprintln(os.Stderr, "shoal daemon status:", err)
		return 1
	}
	fmt.Fprintf(out, "running  ·  up %s  ·  %d torrents (%d downloading, %d seeding)  ·  pid %d\n",
		st.Uptime.Round(time.Second), st.Torrents, st.Downloading, st.Seeding, st.Pid)
	return 0
}
```

In `cmd/shoal/main.go`, change the `daemon` route:

```go
	case "daemon":
		return true, runDaemonCmd(args[2:], out)
```

And in the help text (the usage block), under the `shoal daemon` line, add:

```
  shoal daemon stop             stop the shared daemon
  shoal daemon status           show the daemon's status
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/shoal/ -run TestDaemon -v` then the full `go test ./...`, `go build ./...`, `go vet ./cmd/shoal/`, `gofmt -l cmd/shoal/` (empty), `GOOS=windows go build ./cmd/shoal/` (clean); remove any stray `shoal.exe`.
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/shoal/cli_daemon.go cmd/shoal/main.go cmd/shoal/cli_daemon_test.go
git commit -m "shoal daemon stop/status subcommands"
```

---

### Task 5: Docs

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Update the README**

In the daemon/CLI section, document the new commands and idle behavior:

> - `shoal daemon status` — show whether the shared daemon is running, its uptime, and torrent counts.
> - `shoal daemon stop` — stop the shared daemon (it also stops on its own when idle).
>
> The daemon auto-starts on the first `download` or when the TUI launches, and
> **auto-stops when it's been empty (no torrents) with no TUI connected for
> `daemon_idle_minutes` (default 10; set `0` in `config.json` to disable)**. A
> seeding torrent or an open TUI keeps it running.

- [ ] **Step 2: Verify + commit**

Run: `go build ./...` (sanity — no Go changed).

```bash
git add README.md
git commit -m "docs: document daemon stop/status and idle auto-shutdown"
```

---

## Self-Review

**Spec coverage:**
- Bind-first exclusion → Task 3 (`listenDaemon`). ✓
- `daemon stop` (Control.Shutdown RPC) → Task 2 (RPC + client) + Task 4 (command). ✓
- `daemon status` (Control.Status) → Task 2 + Task 4. ✓
- Idle auto-shutdown (0 torrents + 0 conns, configurable) → Task 2 (monitor) + Task 1 (config) + Task 3 (wire timeout). ✓
- `daemon.Server` owns lifecycle; `Serve` wrapper keeps callers working → Task 2. ✓
- Windows: `stop`/`status` → "daemon not running" (Dial fails); cross-compiles → Tasks 3/4 verify `GOOS=windows build`. ✓
- Docs → Task 5. ✓

**Placeholder scan:** none — every code step carries complete code; test bodies are concrete.

**Type consistency:** `NewServer(eng, started time.Time, idleTimeout time.Duration) *Server`, `(*Server).Serve`/`Shutdown`, `controlService.Shutdown`/`Status`, `StatusReply{Uptime, Torrents, Downloading, Seeding, Pid}`, `(*Client).Shutdown`/`Status`, `listenDaemon(sock) (net.Listener, error)`, `runDaemonCmd`/`runDaemonStop`/`runDaemonStatus`, and `Config.DaemonIdleMinutes` are used identically across tasks. **Deviation from the spec (noted):** the shutdown trigger is the **exported** `(*Server).Shutdown()` (not `triggerShutdown`), because `runDaemon`'s signal handler in `cmd/shoal` must call it; `controlService.Shutdown` and the idle monitor call the same method. The `Serve(l, eng)` wrapper preserves the old signature so `serveTest`/`serveFakeDaemon`/`TestRoundTrip` are untouched.
