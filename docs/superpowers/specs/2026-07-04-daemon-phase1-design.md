# Shared-engine daemon — Phase 1: daemon core + engine RPC

**Date:** 2026-07-04
**Component:** `internal/daemon` (new), `cmd/shoal`
**Status:** Approved (design)
**Part of:** the TUI↔CLI live-sync project (Phase 1 of 4).

## Summary

Phase 1 builds the foundation for making shoal's TUI and CLI share one download
engine: a background daemon process that owns the engine and answers requests over
a local socket, plus a client library that speaks to it and satisfies the existing
`engine.Engine` interface. Nothing is wired to the daemon yet — Phase 1 delivers the
daemon process, the RPC protocol, and the client, all proven by tests.

Later phases point the CLI (Phase 2) and TUI (Phase 3) at this client, achieving
real sync; Phase 4 hardens lifecycle. This spec covers **Phase 1 only**.

## Why a daemon (context)

The TUI and CLI currently run separate engines, and anacrolix stores its
piece-completion DB as a single-writer bolt DB inside the data dir
(`ClientBaseDir: DataDir`), so only one process can own a download folder at a time.
The only way to sync live downloads across surfaces is a single shared engine that
both talk to. The existing `engine.Engine` interface is the seam: the daemon holds
the real `*Anacrolix`; clients implement `engine.Engine` by RPC, so the TUI/CLI
barely change in later phases. `Statuses()` is already poll-based, so the protocol
is plain request/response — no streaming.

## Goals (Phase 1)

- A `shoal daemon` process that runs the real engine and serves requests on a unix socket.
- A stdlib-only RPC protocol covering the engine's mutating + query methods.
- A client that implements `engine.Engine` over the socket, so later phases drop it in unchanged.
- Full test coverage of the server+client round-trip with a fake engine (no network, no real anacrolix).

## Non-goals (deferred to later phases)

- Wiring the CLI (Phase 2) or TUI (Phase 3) to the daemon — Phase 1 ships the daemon + client only.
- Auto-start / detachment of the daemon, `shoal daemon stop` / `status`, idle auto-shutdown (Phase 4).
- Windows transport (named pipe) — Phase 1 is unix-socket only; the `daemon` command may simply error on Windows for now (Phase 4).
- Streaming status updates (not needed — clients poll `Statuses()`).
- Any change to `queue.json` / `active/*.json` semantics (Phase 2 revisits the CLI worker model).

## 1. Transport & protocol

- **Transport:** unix domain socket at `filepath.Join(os.UserConfigDir(), "shoal", "daemon.sock")`. (The config dir path is short enough to stay under the ~104-char sun_path limit on macOS; `internal/daemon` exposes `SocketPath() string` so all parties agree.)
- **Protocol:** stdlib `net/rpc` with the `net/rpc/jsonrpc` codec (JSON over the socket — debuggable, no new dependency). Request/response only.
- Method set exposed as an RPC service `Engine.*`:
  `AddMagnet`, `AddTorrentURL`, `Statuses`, `Remove`, `Pause`, `Resume`.
  `engine.Engine.Close()` is **not** an RPC method — a client's `Close()` closes its own
  connection; the shared engine is only closed when the daemon shuts down.

## 2. Protocol types (`internal/daemon/protocol.go`)

Request/response structs (JSON-tagged, exported fields):

```go
type AddMagnetArgs struct{ Magnet string }
type AddTorrentURLArgs struct{ URL, Name string }
type RemoveArgs struct{ InfoHash string; DeleteData bool }
type HashArgs struct{ InfoHash string } // Pause, Resume
type Empty struct{}
type StatusesReply struct{ Statuses []engine.Status }
```

`engine.Status` already has exported, JSON-friendly fields (including `time.Time
AddedAt`), so it serializes as-is. Mutating calls reply with `*Empty`.

## 3. Server (`internal/daemon/server.go`)

- `type EngineService struct { eng engine.Engine }` with net/rpc-shaped methods that
  delegate to the wrapped engine, e.g.:

  ```go
  func (s *EngineService) AddMagnet(a AddMagnetArgs, _ *Empty) error { return s.eng.AddMagnet(a.Magnet) }
  func (s *EngineService) Statuses(_ Empty, r *StatusesReply) error { r.Statuses = s.eng.Statuses(); return nil }
  func (s *EngineService) AddTorrentURL(a AddTorrentURLArgs, _ *Empty) error { return s.eng.AddTorrentURL(a.URL, a.Name) }
  func (s *EngineService) Remove(a RemoveArgs, _ *Empty) error { return s.eng.Remove(a.InfoHash, a.DeleteData) }
  func (s *EngineService) Pause(a HashArgs, _ *Empty) error { return s.eng.Pause(a.InfoHash) }
  func (s *EngineService) Resume(a HashArgs, _ *Empty) error { return s.eng.Resume(a.InfoHash) }
  ```

- `func Serve(l net.Listener, eng engine.Engine) error` registers an `EngineService`
  on a fresh `*rpc.Server` (not the default, to keep tests isolated) and, per accepted
  connection, runs `srv.ServeCodec(jsonrpc.NewServerCodec(conn))` in a goroutine.
  Returns when the listener is closed. The wrapped engine (`*Anacrolix`) is already
  concurrency-safe, so concurrent connections need no extra locking.

## 4. Client (`internal/daemon/client.go`)

- `type Client struct { rpc *rpc.Client }` implementing every `engine.Engine` method
  by calling the corresponding `Engine.*` RPC:

  ```go
  func Dial(path string) (*Client, error)   // net.Dial("unix", path) + jsonrpc.NewClient(conn)
  func (c *Client) AddMagnet(m string) error
  func (c *Client) AddTorrentURL(url, name string) error
  func (c *Client) Statuses() []engine.Status  // returns nil on RPC error
  func (c *Client) Remove(hash string, deleteData bool) error
  func (c *Client) Pause(hash string) error
  func (c *Client) Resume(hash string) error
  func (c *Client) Close() error               // closes the RPC connection only
  ```

- A compile-time assertion `var _ engine.Engine = (*Client)(nil)` guarantees the
  client stays a drop-in for later phases.
- `Statuses()` returns `nil` on transport error (matching the interface's non-erroring
  signature); mutating methods return the RPC/server error.

## 5. `shoal daemon` command (`cmd/shoal/cli_daemon.go`)

- `runDaemon(args, out) int`:
  - Single-instance guard: if `daemon.Dial(SocketPath())` succeeds, another daemon is
    live → print "shoal daemon already running" to stderr, exit 1. Otherwise remove any
    stale socket file.
  - Build the real engine from `config.Load()` with the same fields the TUI uses:
    `DataDir, ListenPort, MaxPeers, Seed, SeedRatio, QueuePath: queue.DefaultPath()`.
  - `net.Listen("unix", SocketPath())`; print `listening on <path>` to `out`.
  - Run `daemon.Serve(l, eng)` until SIGINT/SIGTERM; on signal, close the listener,
    `eng.Close()`, and `os.Remove(SocketPath())`. Foreground (blocks).
- Routed via `cli()` in `main.go` (`case "daemon"`), added to the usage text.
- On Windows (no unix sockets by default), the command prints "the daemon is not yet
  supported on Windows" and exits 1 (Phase 4 adds a named-pipe transport). Guarded so
  the rest of the binary still cross-compiles.

## 6. Testing (TDD)

All tests use a **fake `engine.Engine`** and a real unix socket in `t.TempDir()` — no
network, no anacrolix, no `shoal daemon` process:

- **Round-trip per method:** start `Serve` on a `t.TempDir()` socket with a recording
  fake engine; `Dial`; assert `AddMagnet("m")` forwards `"m"`, `AddTorrentURL` forwards
  both args, `Remove`/`Pause`/`Resume` forward their args, and `Statuses()` returns the
  fake's slice with all `engine.Status` fields intact (incl. `AddedAt`).
- **Error propagation:** the fake returns an error from `AddMagnet`; the client sees a
  non-nil error with the message.
- **`Statuses` on a closed connection** returns `nil`, not a panic.
- **Interface conformance** is compile-time (`var _ engine.Engine = (*Client)(nil)`).
- The `runDaemon` single-instance guard is covered by a focused test that pre-creates a
  listening socket and asserts a second `runDaemon` exits non-zero (or the guard logic
  is factored into a testable helper).

## Files touched

- New `internal/daemon/protocol.go`, `server.go`, `client.go` (+ `daemon_test.go`).
- New `internal/daemon/socket_unix.go` / `socket_other.go` (build-tagged `SocketPath` /
  unix-vs-unsupported), if needed to keep Windows compiling.
- New `cmd/shoal/cli_daemon.go` (+ test for the guard helper).
- `cmd/shoal/main.go` — `case "daemon"` routing + usage line.

## Open questions

None. Transport (stdlib net/rpc + jsonrpc over a unix socket), the request/response
model, the six-method surface with a local-only client `Close`, and the foreground
Phase-1 daemon are all decided; auto-start/detach/Windows are explicitly Phase 4.
