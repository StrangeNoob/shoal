# Shared-engine daemon — Phase 4b: TUI/CLI robustness

**Date:** 2026-07-05
**Component:** `internal/daemon`, `cmd/shoal`, `internal/ui`, docs
**Status:** Approved (design)
**Part of:** the TUI↔CLI live-sync project. Phases 1–3 + 4a are merged. Phase 4 splits into 4a (lifecycle & control, merged), **4b (this)**, 4c (Windows transport).

## Summary

Make the daemon-backed TUI resilient to the daemon going away. Today the TUI polls
`m.eng.Statuses()` **synchronously on the UI thread** (a slow/hung daemon freezes
the UI) and the client **swallows RPC errors** (a dead daemon renders as a silently
empty pane). Phase 4b moves the poll off the UI thread into a Bubble Tea Cmd, makes
it error-aware, and reconnects — auto-starting the daemon via `ensureDaemon` — so a
crashed or stopped daemon self-heals within one tick while the TUI shows a
"reconnecting…" indicator and keeps its last view.

## Goals

- The status poll never blocks the UI thread (async Cmd).
- A gone daemon shows a persistent "reconnecting…" indicator, not a blank pane; the last view is preserved.
- The connection self-heals: on poll failure the TUI reconnects via `ensureDaemon` (auto-starting the daemon if down), so downloads resume automatically.
- Mutations (add/pause/remove) also reconnect on failure (they already surface errors via their result messages).

## Non-goals (deferred)

- Windows transport — **4c** (the poller reconnects via `ensureDaemon`, which still errors on Windows; unchanged).
- Changing the daemon, the RPC protocol shapes, or the CLI commands (4a unchanged).
- A configurable reconnect/backoff cadence — reconnect rides the existing 700ms tick (fast heal, no new knob).

## Decisions (from brainstorming)

- **Auto-restart (heal):** on poll failure the poller reconnects via `ensureDaemon()`, which auto-starts a down daemon. A crash heals automatically; `daemon stop` while the TUI is open effectively restarts the daemon (close the TUI to fully stop) — consistent with 4a's "an open TUI keeps the daemon alive."
- The disconnected indicator is **persistent** (a `daemonDown` flag rendered until the next successful poll), not a transient `setNotice`.

## 1. Error-returning status — `internal/daemon/client.go`

`engine.Engine.Statuses()` has no error return, so add a sibling that surfaces it:

```go
// StatusesErr is Statuses with the RPC error exposed, for callers (the TUI poll)
// that need to detect a dead daemon rather than see an empty list.
func (c *Client) StatusesErr() ([]engine.Status, error) {
	var r StatusesReply
	err := c.rpc.Call("Engine.Statuses", Empty{}, &r)
	return r.Statuses, err
}
```

`Statuses()` is unchanged (still swallows the error, satisfying `engine.Engine`).

## 2. `daemonPoller` — `cmd/shoal`

A self-healing wrapper that is both an `engine.Engine` (for the TUI's mutations) and
a status poller. It holds the daemon client behind a mutex and reconnects via
`ensureDaemon()` (which auto-starts a down daemon) whenever it has no live client.

```go
type daemonPoller struct {
	mu sync.Mutex
	c  *daemon.Client // nil when disconnected
}

func newDaemonPoller() *daemonPoller { return &daemonPoller{} }

// client returns a live client, (re)connecting via ensureDaemon (auto-start) if needed.
func (p *daemonPoller) client() (*daemon.Client, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.c != nil {
		return p.c, nil
	}
	c, err := ensureDaemon()
	if err != nil {
		return nil, err
	}
	p.c = c
	return c, nil
}

// drop closes and forgets the current client so the next call reconnects.
func (p *daemonPoller) drop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.c != nil {
		p.c.Close()
		p.c = nil
	}
}

// Poll returns the daemon's statuses and any connection error (for the TUI).
func (p *daemonPoller) Poll() ([]engine.Status, error) {
	c, err := p.client()
	if err != nil {
		return nil, err
	}
	ss, err := c.StatusesErr()
	if err != nil {
		p.drop() // force a reconnect next time
	}
	return ss, err
}

func (p *daemonPoller) Statuses() []engine.Status { ss, _ := p.Poll(); return ss }

func (p *daemonPoller) AddMagnet(m string) error      { return p.do(func(c *daemon.Client) error { return c.AddMagnet(m) }) }
func (p *daemonPoller) AddTorrentURL(u, n string) error { return p.do(func(c *daemon.Client) error { return c.AddTorrentURL(u, n) }) }
func (p *daemonPoller) Remove(h string, del bool) error { return p.do(func(c *daemon.Client) error { return c.Remove(h, del) }) }
func (p *daemonPoller) Pause(h string) error           { return p.do(func(c *daemon.Client) error { return c.Pause(h) }) }
func (p *daemonPoller) Resume(h string) error          { return p.do(func(c *daemon.Client) error { return c.Resume(h) }) }
func (p *daemonPoller) Close() error                   { p.drop(); return nil }

// do runs an engine op through a live client, dropping the client on error so the
// next op reconnects.
func (p *daemonPoller) do(fn func(*daemon.Client) error) error {
	c, err := p.client()
	if err != nil {
		return err
	}
	if err := fn(c); err != nil {
		p.drop()
		return err
	}
	return nil
}

var _ engine.Engine = (*daemonPoller)(nil)
```

**`main.go` wiring.** Replace the TUI path's `eng, err := ensureDaemon()` with the poller, keeping the startup fatal-check (Windows / daemon can't start):

```go
	eng := newDaemonPoller()
	if _, err := eng.client(); err != nil { // startup connect (auto-starts); Windows/failure → fatal
		fatal(err)
	}
	defer eng.Close()
	// ui.NewWithConfig(src, eng, cfg)... unchanged — eng satisfies engine.Engine.
```

## 3. Async, error-aware poll — `internal/ui/model.go`

- New interface + message:

```go
type statusPoller interface{ Poll() ([]engine.Status, error) }

type statusMsg struct {
	at       time.Time // the tick time that fired this poll (drives the speed dt)
	statuses []engine.Status
	err      error
}

func pollCmd(p statusPoller, at time.Time) tea.Cmd {
	return func() tea.Msg {
		ss, err := p.Poll()
		return statusMsg{at: at, statuses: ss, err: err}
	}
}
```

- `Model` gains `poll statusPoller` and `daemonDown bool`. `New`/`NewWithConfig` set `poll` from `eng` when it implements `statusPoller`: `if p, ok := eng.(statusPoller); ok { m.poll = p }`.
- `tickMsg` handler: stop calling `m.eng.Statuses()` synchronously. Instead reschedule the tick and fire the poll, passing the tick time:
  `now := time.Time(msg); return m, tea.Batch(tickCmd(), pollCmd(m.poll, now))` (when `m.poll != nil`; if nil — a minimal test engine — fall back to delivering a synchronous `statusMsg{at: now, statuses: m.eng.Statuses()}` so behavior is preserved).
- `statusMsg` handler carries the body currently in `tickMsg`, using `msg.at` as `now` (so tests keep exact control of the speed `dt`):
  - `err != nil` → `m.daemonDown = true`; keep `m.statuses`, `m.dlSpeed`, `m.ulSpeed` as-is; return (no pane blanking).
  - `err == nil` → `m.daemonDown = false`; run the existing logic with `now := msg.at` and `next := msg.statuses` (compute dl/ul rates from `m.statuses`→`next` over `now - m.lastTick`, the bounded history-reload, `m.statuses = next`, `m.lastTick = now`, cursor clamps, cancel-confirm clearing).

## 4. Reconnecting indicator — `internal/ui/view.go`

While `m.daemonDown`, render a compact indicator in the status/header line, e.g. `⟳ reconnecting to daemon…`, styled like the existing meta/error text. It clears on the next successful `statusMsg`.

## 5. Tests — `internal/ui/model_test.go`, `cmd/shoal`

- `fakeEngine` (ui test) gains `pollErr error` and `Poll() ([]engine.Status, error)` returning `(f.statuses, f.pollErr)`.
- A helper drives the now-async poll in one step:

```go
func tick(m Model, t time.Time) Model {
	out, _ := update(m, tickMsg(t))
	m = out
	ss, err := m.poll.Poll()
	out, _ = update(m, statusMsg{at: t, statuses: ss, err: err})
	return out
}
```

  Rewrite the existing tick-dependent tests (`TestDownloadsAndSeedingSplit`, `TestDownloadsCursorMoves`, `TestTickReloadsHistoryOnCompletion`, `TestTickReloadsHistoryUntilRecorded`, `TestTickComputesTransferSpeeds`, `TestTickPollsEngineAndReschedules`, `TestCancelConfirmClearsWhenTargetCompletes`) to use `tick(m, t)` instead of `update(m, tickMsg(t))`.
- New tests:
  - **poll error sets daemonDown, keeps view:** seed statuses via a first `tick`; set `fake.pollErr = errors.New("gone")`; `tick` again → `m.daemonDown == true` and `m.statuses` unchanged (not blanked).
  - **recovery clears daemonDown:** from the error state, clear `fake.pollErr` and add a new status; `tick` → `daemonDown == false` and `m.statuses` reflects the new poll.
  - **tick fires an async poll:** `update(m, tickMsg(t))` returns a non-nil Cmd (the batch includes `pollCmd`); `m.statuses` is NOT updated by `tickMsg` alone (only by the subsequent `statusMsg`).
- `cmd/shoal`: a `daemonPoller` test against `serveFakeDaemon` — `Poll()` returns the served statuses; after the daemon stops, `Poll()` returns an error and drops; when a daemon is served again at the same socket, a later `Poll()` reconnects and succeeds. (Uses the existing fake-daemon harness; `ensureDaemon` connects to the `SHOAL_DAEMON_SOCK` it sets.)
- Full `go build ./...`, `go test ./...`, `-race` on `internal/ui`, `gofmt -l`, and `GOOS=windows go build ./cmd/shoal/` stay green.

## 6. Data flow

- **Healthy:** tick → `pollCmd` → `poller.Poll()` → `statusMsg{statuses, nil}` → panes update; `daemonDown` cleared.
- **Daemon gone:** tick → `pollCmd` → `Poll()` fails (client dropped) → `statusMsg{nil, err}` → `daemonDown = true`, last view kept, banner shown. Next tick → `Poll()` → `client()` re-`ensureDaemon` (auto-start) → reconnect → `statusMsg{statuses, nil}` → banner clears, downloads resume.
- **Mutation while down:** `AddMagnet`/etc. → `do` → `client()` reconnects (auto-start) → op runs (or its error surfaces via the existing `addedMsg`/`removedMsg`).

## Files touched

- `internal/daemon/client.go` — `StatusesErr`.
- `cmd/shoal/` — `daemonPoller` (new, e.g. `daemon_poller.go`) + `main.go` wiring; a `daemon_poller_test.go`.
- `internal/ui/model.go` — `statusPoller`, `statusMsg`, `pollCmd`, `daemonDown`, async tick.
- `internal/ui/view.go` — reconnecting indicator.
- `internal/ui/model_test.go` — `Poll` on `fakeEngine`, `tick` helper, rewritten + new tests.
- `README.md` — a line that the TUI reconnects/auto-restarts the daemon.

## Known limitations (documented)

- Reconnect rides the 700ms tick (no exponential backoff); on a persistently-down daemon (e.g. Windows, or a failing engine build) each tick retries `ensureDaemon`, which fails fast — acceptable, but noisy in `daemon.log` if the daemon can't start at all.
- `daemon stop` while the TUI is open restarts the daemon (by design — close the TUI to fully stop).

## Open questions

None. Async poll, auto-restart-on-failure, and the persistent reconnecting indicator are all decided; Windows transport is 4c.
