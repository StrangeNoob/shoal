# Shared-engine daemon — Phase 4a: daemon lifecycle & control

**Date:** 2026-07-05
**Component:** `internal/daemon`, `cmd/shoal/cli_daemon.go`, `internal/config`, docs
**Status:** Approved (design)
**Part of:** the TUI↔CLI live-sync project. Phases 1–3 (daemon, CLI client, TUI client) are merged. Phase 4 is lifecycle hardening, split into **4a (this, lifecycle & control)**, 4b (TUI/CLI robustness), 4c (Windows transport).

## Summary

Make the shared daemon well-behaved: it can't double-start, it can be stopped and
inspected from the CLI, and it shuts itself down when there's nothing left to do.
A new `daemon.Server` struct owns the daemon's lifecycle state; a `Control.*` RPC
service exposes shutdown and status; `runDaemon` binds first (no TOCTOU); and an
idle monitor exits an empty, unused daemon after a configurable timeout.

## Goals

- **Bind-first exclusion:** two daemons can never both run; a racing auto-spawn's loser exits cleanly (resolves the Phase-2 CodeRabbit startup-lock finding without a lock file).
- **`shoal daemon stop`:** gracefully stop the running daemon.
- **`shoal daemon status`:** report whether it's running, its uptime, and torrent counts.
- **Idle auto-shutdown:** an empty daemon with no connected client exits after a configurable timeout, so an auto-started daemon doesn't linger orphaned.

## Non-goals (deferred)

- TUI/CLI reconnect + async status poll when the daemon dies/restarts — **Phase 4b**.
- Windows transport (unix-socket only here) — **Phase 4c**. `daemon stop`/`status` error on Windows like `daemon`/`ensureDaemon` already do.
- `daemon status --json` (human-readable only for now; add later if a script needs it).
- Seed-ratio enforcement / auto-removing finished torrents (unchanged; out of scope).

## Decisions (from brainstorming)

- **Idle = `openConns == 0 && len(eng.Statuses()) == 0`** held continuously for the timeout. A seeding torrent (any torrent) keeps it alive; the TUI's persistent RPC connection keeps it alive. Default **10 minutes**, configurable via `config.json` (`daemon_idle_minutes`; `0` disables).
- **`stop` uses a Shutdown RPC**, not a pidfile/signal — reuses the socket, no extra state.

## 1. `daemon.Server` — `internal/daemon/server.go`

Replace the free `Serve` function's internals with a struct that owns lifecycle state:

```go
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

// triggerShutdown closes the shutdown channel exactly once.
func (s *Server) triggerShutdown() { s.once.Do(func() { close(s.shutdown) }) }

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
				return nil // graceful shutdown
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

// monitorIdle triggers shutdown once the daemon has been idle for idleTimeout.
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
					s.triggerShutdown()
					return
				}
			} else {
				idleSince = time.Time{}
			}
		}
	}
}
```

**Back-compat wrapper** (keeps every existing caller/test — `runDaemon`, `serveFakeDaemon`, `server_test.go` — working unchanged; idle disabled):

```go
func Serve(l net.Listener, eng engine.Engine) error {
	return NewServer(eng, time.Now(), 0).Serve(l)
}
```

## 2. `Control` RPC service — `internal/daemon/server.go`, `protocol.go`

`StatusReply` (in `protocol.go` with the other reply types):

```go
type StatusReply struct {
	Uptime      time.Duration
	Torrents    int
	Downloading int
	Seeding     int
	Pid         int
}
```

Control service (in `server.go`):

```go
type controlService struct{ s *Server }

func (c *controlService) Shutdown(_ Empty, _ *Empty) error {
	c.s.triggerShutdown()
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
```

The `Shutdown` handler returns its reply to the client *before* the listener closes (the shutdown goroutine closes `l` asynchronously), so `daemon stop` always gets a clean reply.

## 3. Client methods — `internal/daemon/client.go`

```go
func (c *Client) Shutdown() error {
	return c.rpc.Call("Control.Shutdown", Empty{}, &Empty{})
}

func (c *Client) Status() (StatusReply, error) {
	var r StatusReply
	err := c.rpc.Call("Control.Status", Empty{}, &r)
	return r, err
}
```

(`var _ engine.Engine = (*Client)(nil)` still holds — extra methods are fine.)

## 4. Bind-first + `stop`/`status` — `cmd/shoal/cli_daemon.go`

**Bind-first listen** (replaces the `daemonRunning` pre-check + unconditional `os.Remove` in `runDaemon`):

```go
// listenDaemon binds the daemon socket, refusing to start if a live daemon
// already answers and reclaiming a stale socket file otherwise.
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

`runDaemon` changes: after `MkdirAll(filepath.Dir(sock))`, call `listenDaemon(sock)` (error → stderr, exit 1); build the engine; then
`srv := daemon.NewServer(eng, time.Now(), time.Duration(cfg.DaemonIdleMinutes)*time.Minute)`;
the signal goroutine calls `srv.triggerShutdown()` (not `l.Close()` directly); `srv.Serve(l)` blocks until shutdown; then `close(stop); <-done; eng.Close()` (deferred); `os.Remove(sock)`. `recordCompletions` is unchanged.

**Routing + subcommands.** The `daemon` route dispatches on the first arg:

```go
func runDaemonCmd(args []string, out io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "stop":
			return runDaemonStop(out)
		case "status":
			return runDaemonStatus(out)
		}
	}
	return runDaemon(args, out) // no subcommand → run the daemon (foreground)
}
```

`main.go`'s `cli()` routes `"daemon"` → `runDaemonCmd` (instead of `runDaemon`).

```go
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

Windows: `runDaemonStop`/`runDaemonStatus` reach `daemon.Dial`, which fails (no daemon possible on Windows) → "daemon not running", consistent with the platform's unsupported state. (No new Windows guard needed.)

## 5. Config — `internal/config/config.go`

- Add to `Config`: `DaemonIdleMinutes int json:"daemon_idle_minutes"` (minutes of idle before auto-shutdown; `0` disables).
- Add to `Default()`: `DaemonIdleMinutes: 10`.
- `Load()` already unmarshals over `Default()`, so an existing `config.json` without the key keeps `10`; an explicit `0` disables idle shutdown.

## 6. Docs — `README.md`

Document `shoal daemon stop` / `shoal daemon status` and the idle-shutdown behavior (empty + no client for `daemon_idle_minutes`, default 10) in the daemon/CLI section.

## 7. Data flow

- **Start:** `runDaemon` → `listenDaemon` (bind-first) → `NewServer(eng, now, idle)` → `Serve`. A racing second daemon's `listenDaemon` sees the live socket → "already running", exit 1.
- **stop:** `daemon stop` → `Control.Shutdown` → `triggerShutdown` → listener closes → `Serve` returns → drain → `eng.Close()` → socket removed → process exits.
- **status:** `daemon status` → `Control.Status` → counts from `eng.Statuses()` + uptime from `started`.
- **idle:** monitor sees `conns==0 && 0 torrents` for `idleTimeout` → `triggerShutdown` (same path as stop).

## 8. Testing (TDD)

`internal/daemon` (in-process `daemon.Server` on a temp unix socket + a fake `engine.Engine`; env `SHOAL_DAEMON_SOCK` where the CLI is involved):
- **Control.Shutdown** makes `Serve` return: `NewServer(fake, now, 0)`, `go Serve(l)`, `Dial`, `Shutdown()`, assert `Serve` returns.
- **Control.Status** counts: fake with 2 torrents (1 `Done`) → `Status()` → `Torrents==2, Downloading==1, Seeding==1, Uptime>0, Pid==os.Getpid()`.
- **Idle shutdown fires** when empty: `NewServer(fake{0 torrents}, now, 60ms)`, `go Serve(l)` → `Serve` returns within ~1s.
- **Idle held off** by a torrent: `NewServer(fake{1 torrent}, now, 60ms)` → `Serve` still running after ~300ms.
- **Idle held off** by a connection: `fake{0 torrents}`, timeout 60ms, hold an open `Dial` conn → `Serve` still running after ~300ms; close the conn → `Serve` returns.

`cmd/shoal` (`serveFakeDaemon` already serves via the `Serve` wrapper, which now registers `Control`):
- **`runDaemonStop`** against a fake-served daemon → prints `daemon stopped`; **no daemon** → `daemon not running`.
- **`runDaemonStatus`** against a fake with scripted statuses → output shows the counts; **no daemon** → `daemon not running`.
- **`listenDaemon`**: on a socket a live listener holds → error "already running"; on a stale socket *file* (no listener) → succeeds (reclaims it).

Full `go build ./...`, `go test ./...`, `-race` on `internal/daemon`, `gofmt -l`, and `GOOS=windows go build ./cmd/shoal/` stay green.

## Files touched

- `internal/daemon/server.go` — `Server`, `NewServer`, `Serve` method, idle monitor, `controlService`; keep `Serve` wrapper.
- `internal/daemon/protocol.go` — `StatusReply`.
- `internal/daemon/client.go` — `Shutdown`, `Status`.
- `cmd/shoal/cli_daemon.go` — `listenDaemon`, `runDaemonCmd`, `runDaemonStop`, `runDaemonStatus`; wire `NewServer` + idle timeout into `runDaemon`.
- `cmd/shoal/main.go` — route `daemon` → `runDaemonCmd`.
- `internal/config/config.go` — `DaemonIdleMinutes` (default 10).
- Tests: `internal/daemon/server_test.go`, `cmd/shoal/cli_daemon_test.go`, `internal/config/config_test.go`.
- `README.md`.

## Known limitations (documented)

- Unix/macOS only (Windows is 4c). `daemon stop`/`status` report "daemon not running" on Windows.
- Reconnect/async-poll when the daemon idle-shuts under a *just-disconnected* client is 4b; idle only fires when no client is connected, so the common case is safe.

## Open questions

None. Idle policy (empty + no client, 10 min, configurable), the Shutdown-RPC stop mechanism, and bind-first exclusion are all decided; TUI robustness and Windows transport are explicitly 4b/4c.
