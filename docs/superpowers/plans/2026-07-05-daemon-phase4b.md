# Shared-engine daemon Phase 4b — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the daemon-backed TUI resilient — poll the daemon off the UI thread, and reconnect (auto-starting the daemon) when it goes away, showing a "reconnecting…" indicator instead of a blank pane.

**Architecture:** The TUI's status poll moves into a Bubble Tea Cmd whose result arrives as a `statusMsg`; a `daemonPoller` (in `cmd/shoal`) wraps the daemon client, reconnecting via `ensureDaemon` on failure, and implements both `engine.Engine` (mutations) and an error-returning `Poll()`. A persistent `daemonDown` flag drives the indicator.

**Tech Stack:** Go, stdlib, Bubble Tea, the `internal/daemon` client, existing `internal/engine`.

## Global Constraints

- Go; stdlib + already-vendored deps only — **no new module dependencies**.
- TDD: write the failing test first. Commits carry **no Claude attribution**.
- The status poll must not call `Statuses()` on the UI thread (async Cmd only).
- A gone daemon → persistent `daemonDown` indicator + last view preserved (no pane blanking); reconnect auto-starts the daemon via `ensureDaemon`.
- `gofmt -l` clean; run `go vet`, `go build ./...`, `go test ./...`, `-race` on `internal/ui`, and `GOOS=windows go build ./cmd/shoal/` before each commit.

**Interfaces already present:** `engine.Engine` (AddMagnet/AddTorrentURL/Statuses/Remove/Pause/Resume/Close); `engine.Status{Name, InfoHash string; TotalBytes, CompletedBytes, Downloaded, Uploaded int64; Done, Seeding, Paused bool; ...}`. `daemon.Dial(path) (*Client, error)`, `daemon.SocketPath()`; `(*Client)` has the engine methods + `Close()`. `ensureDaemon() (*daemon.Client, error)` (in `cmd/shoal/cli_daemon.go`) — connects, auto-starting a detached daemon; errors on Windows. In `internal/ui/model.go`: `Model{eng engine.Engine; statuses []engine.Status; lastTick time.Time; history history.Store; historyMisses map[string]int; dlSpeed, ulSpeed map[string]int64; daemonDown ...}`, `tickMsg time.Time`, `tickCmd() tea.Cmd` (700ms), `computeRates`, `maxHistoryReloads`, `New(src, eng)`/`NewWithConfig(src, eng, cfg)`. The ui-test `fakeEngine{statuses []engine.Status; ...}` and `ready(m)`/`update(m, msg)` helpers are in `internal/ui/model_test.go`.

---

### Task 1: `Client.StatusesErr` — error-returning status

**Files:**
- Modify: `internal/daemon/client.go`
- Test: `internal/daemon/lifecycle_test.go` (append)

**Interfaces:**
- Produces: `(*Client).StatusesErr() ([]engine.Status, error)`.

- [ ] **Step 1: Write the failing test**

Append to `internal/daemon/lifecycle_test.go`:

```go
func TestStatusesErrSurfacesError(t *testing.T) {
	fake := &fakeEngine{statuses: []engine.Status{{InfoHash: "a"}}}
	c := serveTest(t, fake)
	ss, err := c.StatusesErr()
	if err != nil || len(ss) != 1 || ss[0].InfoHash != "a" {
		t.Fatalf("StatusesErr on a live daemon = (%v, %v), want ([a], nil)", ss, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/daemon/ -run TestStatusesErrSurfacesError -v`
Expected: FAIL — `c.StatusesErr` undefined.

- [ ] **Step 3: Write minimal implementation**

Add to `internal/daemon/client.go` (near `Statuses`):

```go
// StatusesErr is Statuses with the RPC error exposed, for callers (the TUI poll)
// that must distinguish a dead daemon from an empty engine.
func (c *Client) StatusesErr() ([]engine.Status, error) {
	var r StatusesReply
	err := c.rpc.Call("Engine.Statuses", Empty{}, &r)
	return r.Statuses, err
}
```

(`Statuses()` is unchanged.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/daemon/ -run TestStatusesErr -v` then `go build ./...`, `gofmt -l internal/daemon/` (empty).
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/daemon/client.go internal/daemon/lifecycle_test.go
git commit -m "daemon: add Client.StatusesErr (error-returning status)"
```

---

### Task 2: async, error-aware status poll in the TUI

**Files:**
- Modify: `internal/ui/model.go` (poll interface/msg, async tick, `statusMsg` handler, `daemonDown`, `New`)
- Modify: `internal/ui/view.go` (reconnecting indicator)
- Modify: `internal/ui/model_test.go` (`Poll` on `fakeEngine`, `tick` helper, rewrite tick tests, new tests)

**Interfaces:**
- Consumes: existing `Model`, `tickCmd`, `computeRates`, `maxHistoryReloads`.
- Produces: `statusPoller interface{ Poll() ([]engine.Status, error) }`, `statusMsg{at time.Time; statuses []engine.Status; err error}`, `pollCmd(p statusPoller, at time.Time) tea.Cmd`, `Model.poll`/`Model.daemonDown`.

- [ ] **Step 1: Add `Poll` to the test engine and the `tick` helper (test scaffolding first)**

In `internal/ui/model_test.go`, add a `pollErr` field to `fakeEngine` and a `Poll` method, and a `tick` helper. First find the `fakeEngine` struct and add the field:

```go
	// add to the fakeEngine struct:
	pollErr error
```

Add the method (next to the other `fakeEngine` methods):

```go
func (e *fakeEngine) Poll() ([]engine.Status, error) { return e.statuses, e.pollErr }
```

Add the helper (near `ready`/`update`):

```go
// tick advances one poll cycle: it fires the tick, then delivers the async poll
// result as the model would, carrying the tick time so speed sampling is exact.
func tick(m Model, t time.Time) Model {
	out, _ := update(m, tickMsg(t))
	m = out
	ss, err := m.poll.Poll()
	out, _ = update(m, statusMsg{at: t, statuses: ss, err: err})
	return out
}
```

- [ ] **Step 2: Write the failing tests**

Add these new tests to `internal/ui/model_test.go`:

```go
func TestTickFiresAsyncPoll(t *testing.T) {
	eng := &fakeEngine{statuses: []engine.Status{{Name: "X", TotalBytes: 100, CompletedBytes: 50}}}
	m := ready(New(&fakeSource{}, eng))
	out, cmd := update(m, tickMsg(time.Now()))
	m = out
	if cmd == nil {
		t.Fatal("tick should return a Cmd (reschedule + poll)")
	}
	if len(m.statuses) != 0 {
		t.Fatalf("tick must NOT update statuses synchronously (poll is async), got %+v", m.statuses)
	}
}

func TestPollErrorSetsDaemonDownAndKeepsView(t *testing.T) {
	eng := &fakeEngine{statuses: []engine.Status{{Name: "X", InfoHash: "x", TotalBytes: 100, CompletedBytes: 50}}}
	m := ready(New(&fakeSource{}, eng))
	m = tick(m, time.Unix(1000, 0)) // healthy poll seeds the view
	if len(m.statuses) != 1 || m.daemonDown {
		t.Fatalf("after a healthy poll: statuses=%+v daemonDown=%v", m.statuses, m.daemonDown)
	}
	eng.pollErr = errors.New("daemon gone")
	m = tick(m, time.Unix(1001, 0)) // failing poll
	if !m.daemonDown {
		t.Fatal("a failed poll should set daemonDown")
	}
	if len(m.statuses) != 1 || m.statuses[0].Name != "X" {
		t.Fatalf("a failed poll must keep the last view, got %+v", m.statuses)
	}
}

func TestPollRecoveryClearsDaemonDown(t *testing.T) {
	eng := &fakeEngine{statuses: []engine.Status{{Name: "X", InfoHash: "x"}}, pollErr: errors.New("gone")}
	m := ready(New(&fakeSource{}, eng))
	m = tick(m, time.Unix(1000, 0)) // fails
	if !m.daemonDown {
		t.Fatal("expected daemonDown after the failing poll")
	}
	eng.pollErr = nil
	eng.statuses = []engine.Status{{Name: "Y", InfoHash: "y"}}
	m = tick(m, time.Unix(1001, 0)) // recovers
	if m.daemonDown {
		t.Fatal("a successful poll should clear daemonDown")
	}
	if len(m.statuses) != 1 || m.statuses[0].Name != "Y" {
		t.Fatalf("recovered poll should update the view, got %+v", m.statuses)
	}
}

func TestDaemonDownShowsReconnecting(t *testing.T) {
	m := ready(New(&fakeSource{}, &fakeEngine{}))
	m.section = sectionDownloads
	m.daemonDown = true
	if !strings.Contains(m.View(), "reconnecting") {
		t.Fatalf("daemonDown should render a reconnecting indicator:\n%s", m.View())
	}
}
```

(`errors` and `strings` are already imported in `model_test.go`.)

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/ui/ -run 'TestTickFiresAsyncPoll|TestPoll|TestDaemonDown' -v`
Expected: FAIL — `statusMsg`, `m.poll`, `m.daemonDown` undefined (compile error).

- [ ] **Step 4: Implement the async poll in `internal/ui/model.go`**

(a) Add the interface, message, command, and struct fields. Near `tickMsg`/`tickCmd`:

```go
type statusPoller interface {
	Poll() ([]engine.Status, error)
}

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

Add fields to the `Model` struct (next to `eng engine.Engine`):

```go
	poll       statusPoller
	daemonDown bool
```

In `NewWithConfig`, after `eng: eng,` is set (in the returned `Model{…}` literal), the model can't type-assert inside the literal, so set `poll` right after constructing. Change the tail of `NewWithConfig` from:

```go
	return Model{
		...
		eng:      eng,
		...
	}
}
```

to build the model, attach the poller, and return it:

```go
	m := Model{
		section:  sectionSearch,
		editing:  false,
		input:    ti,
		setInput: si,
		spin:     sp,
		prog:     pr,
		src:      src,
		eng:      eng,
		cfg:      cfg,
		sortDesc: true,
		booting:  true,
	}
	if p, ok := eng.(statusPoller); ok {
		m.poll = p // production daemonPoller and the test fakeEngine both implement it
	}
	return m
}
```

(b) Replace the `tickMsg` handler. The current handler runs the whole poll body inside `if m.eng != nil { … }` and ends with the notice-expiry + `return m, tickCmd()`. Change it so the tick only expires notices and fires the poll + reschedule (no synchronous `Statuses()`), and MOVE the poll body to a new `statusMsg` case.

Replace the entire `case tickMsg:` block with:

```go
	case tickMsg:
		now := time.Time(msg)
		if m.notice != "" && time.Now().After(m.noticeUntil) {
			m.notice = ""
			m.noticeErr = false
		}
		cmds := []tea.Cmd{tickCmd()}
		if m.poll != nil {
			cmds = append(cmds, pollCmd(m.poll, now))
		} else if m.eng != nil { // no poller (minimal engine): synchronous fallback
			eng := m.eng
			cmds = append(cmds, func() tea.Msg { return statusMsg{at: now, statuses: eng.Statuses()} })
		}
		return m, tea.Batch(cmds...)

	case statusMsg:
		if msg.err != nil {
			m.daemonDown = true
			return m, nil
		}
		m.daemonDown = false
		now := msg.at
		next := msg.statuses
```

Then paste — unchanged — the poll body that currently lives inside the old `tickMsg` handler's `if m.eng != nil { … }` block, STARTING right after its original first two lines (`now := time.Time(msg)` and `next := m.eng.Statuses()`, which the new code above replaces) and ENDING at the close of that block (i.e. everything from `dt := now.Sub(m.lastTick)` through the `if m.stopConfirm { … }` block). Finish the case with:

```go
		return m, nil
```

The net effect: `dt := now.Sub(m.lastTick)`, the two `computeRates` calls, the bounded history-reload, `m.statuses = next`, `m.lastTick = now`, the cursor clamps, and the cancel/stop-confirm clearing all run in `statusMsg` exactly as before — only `now`/`next` now come from `msg` instead of `time.Time(msg)`/`m.eng.Statuses()`.

- [ ] **Step 5: Add the reconnecting indicator in `internal/ui/view.go`**

In the view, where the header/status line composes the transient `m.notice` (search for `m.notice` in `view.go`), render a reconnecting indicator when `m.daemonDown` — e.g. prefer it over an empty notice line:

```go
	if m.daemonDown {
		// styled like meta/error text; keep it short
		<render "⟳ reconnecting to daemon…" into the same status/notice slot>
	}
```

Implement it so `m.View()` contains the substring `reconnecting` whenever `m.daemonDown` is true (that is exactly what `TestDaemonDownShowsReconnecting` asserts). Use the existing meta/subtle style; don't add a new layout row if the notice slot already exists.

- [ ] **Step 6: Convert the existing tick-driven tests to the `tick` helper**

In `internal/ui/model_test.go`, every existing test that loads/advances statuses via `m, _ = update(m, tickMsg(X))` (and the `m3, _ = update(m3, tickMsg(X))` variant) must now go through the two-step poll. Replace each such call with `m = tick(m, X)` (resp. `m3 = tick(m3, X)`). These are in: `TestDownloadsAndSeedingSplit`, `TestDownloadsCursorMoves`, `TestPausedDownloadRendersPaused`, `TestCancelConfirmClearsWhenTargetCompletes` (two calls), `TestTickReloadsHistoryOnCompletion` (two calls), `TestTickReloadsHistoryUntilRecorded` (two calls), `TestTickComputesTransferSpeeds` (two calls), and any other test using `update(m, tickMsg(...))` to load statuses.

Do NOT convert `TestTickPollsEngineAndReschedules` — rewrite it to assert the new async contract instead:

```go
func TestTickPollsEngineAndReschedules(t *testing.T) {
	eng := &fakeEngine{statuses: []engine.Status{{Name: "X", TotalBytes: 100, CompletedBytes: 50}}}
	m := ready(New(&fakeSource{}, eng))
	out, cmd := update(m, tickMsg(time.Now()))
	if cmd == nil {
		t.Error("tick should reschedule itself (and fire a poll)")
	}
	m = tick(out, time.Now()) // deliver a poll cycle
	if len(m.statuses) != 1 || m.statuses[0].Name != "X" {
		t.Errorf("poll did not load statuses: %+v", m.statuses)
	}
}
```

- [ ] **Step 7: Run the tests**

Run: `go test ./internal/ui/ -v` then `go test -race ./internal/ui/`, `go build ./...`, `go vet ./internal/ui/`, `gofmt -l internal/ui/` (empty).
Expected: PASS — the new poll tests, the converted tick tests (speeds/history/cursors), and the view test all green; `-race` clean.

- [ ] **Step 8: Commit**

```bash
git add internal/ui/model.go internal/ui/view.go internal/ui/model_test.go
git commit -m "TUI: poll the daemon off the UI thread; show a reconnecting indicator"
```

---

### Task 3: self-healing `daemonPoller` + main.go wiring

**Files:**
- Create: `cmd/shoal/daemon_poller.go`
- Modify: `cmd/shoal/main.go` (TUI path builds the poller)
- Test: `cmd/shoal/daemon_poller_test.go`

**Interfaces:**
- Consumes: `ensureDaemon()`, `(*daemon.Client)` (incl. `StatusesErr` from Task 1), `engine.Engine`, the `serveFakeDaemon` harness (`cmd/shoal/fake_engine_test.go`).
- Produces: `newDaemonPoller() *daemonPoller` implementing `engine.Engine` + `Poll() ([]engine.Status, error)`.

- [ ] **Step 1: Write the failing test**

Create `cmd/shoal/daemon_poller_test.go`:

```go
package main

import (
	"strings"
	"testing"

	"github.com/StrangeNoob/shoal/internal/engine"
)

func TestDaemonPollerPollsAndReconnects(t *testing.T) {
	fake := &fakeEngine{statuses: []engine.Status{{InfoHash: "a"}}}
	serveFakeDaemon(t, fake) // sets SHOAL_DAEMON_SOCK to a served socket

	p := newDaemonPoller()
	ss, err := p.Poll()
	if err != nil || len(ss) != 1 || ss[0].InfoHash != "a" {
		t.Fatalf("Poll on a live daemon = (%v, %v), want ([a], nil)", ss, err)
	}
	// A mutation goes through the same connection.
	if err := p.AddMagnet("magnet:?xt=urn:btih:" + strings.Repeat("0", 40)); err != nil {
		t.Fatalf("AddMagnet: %v", err)
	}
	if got := fake.gotMagnets(); len(got) != 1 {
		t.Fatalf("daemon did not receive the magnet: %v", got)
	}
	p.Close()
}
```

(`serveFakeDaemon` and `fakeEngine` with `gotMagnets()` are in `cmd/shoal/fake_engine_test.go`.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/shoal/ -run TestDaemonPoller -v`
Expected: FAIL — `newDaemonPoller`/`daemonPoller` undefined.

- [ ] **Step 3: Write the implementation**

Create `cmd/shoal/daemon_poller.go`:

```go
package main

import (
	"sync"

	"github.com/StrangeNoob/shoal/internal/daemon"
	"github.com/StrangeNoob/shoal/internal/engine"
)

// daemonPoller is a self-healing engine.Engine for the TUI: it holds a daemon
// client, (re)connecting via ensureDaemon (which auto-starts a down daemon) on
// demand, and exposes Poll() so the TUI can distinguish a dead daemon from an
// empty engine and show a reconnecting state.
type daemonPoller struct {
	mu sync.Mutex
	c  *daemon.Client // nil when disconnected
}

func newDaemonPoller() *daemonPoller { return &daemonPoller{} }

var _ engine.Engine = (*daemonPoller)(nil)

// client returns a live client, reconnecting via ensureDaemon (auto-start) if none.
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

// drop closes and forgets the client so the next call reconnects.
func (p *daemonPoller) drop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.c != nil {
		p.c.Close()
		p.c = nil
	}
}

// Poll returns the daemon's statuses and any connection error, dropping the
// client on error to force a reconnect next time.
func (p *daemonPoller) Poll() ([]engine.Status, error) {
	c, err := p.client()
	if err != nil {
		return nil, err
	}
	ss, err := c.StatusesErr()
	if err != nil {
		p.drop()
	}
	return ss, err
}

func (p *daemonPoller) Statuses() []engine.Status { ss, _ := p.Poll(); return ss }

// do runs an engine op through a live client, dropping the client on error.
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

func (p *daemonPoller) AddMagnet(m string) error {
	return p.do(func(c *daemon.Client) error { return c.AddMagnet(m) })
}
func (p *daemonPoller) AddTorrentURL(u, n string) error {
	return p.do(func(c *daemon.Client) error { return c.AddTorrentURL(u, n) })
}
func (p *daemonPoller) Remove(h string, del bool) error {
	return p.do(func(c *daemon.Client) error { return c.Remove(h, del) })
}
func (p *daemonPoller) Pause(h string) error {
	return p.do(func(c *daemon.Client) error { return c.Pause(h) })
}
func (p *daemonPoller) Resume(h string) error {
	return p.do(func(c *daemon.Client) error { return c.Resume(h) })
}
func (p *daemonPoller) Close() error { p.drop(); return nil }
```

- [ ] **Step 4: Wire it into `cmd/shoal/main.go`**

In `main()`'s TUI path, replace:

```go
	eng, err := ensureDaemon()
	if err != nil {
		fatal(err)
	}
	defer eng.Close() // closes the connection only; the daemon and its downloads persist
```

with:

```go
	eng := newDaemonPoller()
	if _, err := eng.client(); err != nil { // startup connect (auto-starts); Windows/failure → fatal
		fatal(err)
	}
	defer eng.Close() // closes the connection only; the daemon and its downloads persist
```

(`ui.NewWithConfig(src, eng, cfg)…` is unchanged — `eng` still satisfies `engine.Engine`, and now also `statusPoller`, so the TUI's `New` attaches it as the poller.)

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./cmd/shoal/ -run TestDaemonPoller -v` then the full `go test ./...`, `go build ./...`, `go vet ./cmd/shoal/`, `gofmt -l cmd/shoal/` (empty), `GOOS=windows go build ./cmd/shoal/` (clean); remove any stray `shoal.exe`.
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/shoal/daemon_poller.go cmd/shoal/main.go cmd/shoal/daemon_poller_test.go
git commit -m "TUI: self-healing daemonPoller that reconnects via ensureDaemon"
```

---

### Task 4: Docs

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Update the README**

In the daemon/TUI section, add a line about resilience:

> If the daemon stops or crashes while the TUI is open, the TUI shows a brief
> "reconnecting…" indicator and automatically restarts it, so downloads resume.
> (To fully stop the daemon, close the TUI first, then `shoal daemon stop`.)

- [ ] **Step 2: Verify + commit**

Run: `go build ./...` (sanity — no Go changed).

```bash
git add README.md
git commit -m "docs: TUI auto-reconnects/restarts the daemon"
```

---

## Self-Review

**Spec coverage:**
- `StatusesErr` (error-returning status) → Task 1. ✓
- `daemonPoller` (reconnect via `ensureDaemon`, engine.Engine + Poll, drop-on-error) + main.go wiring → Task 3. ✓
- Async poll (pollCmd/statusMsg, no `Statuses()` on the UI thread) → Task 2. ✓
- Error-aware: `daemonDown` set on poll error, last view kept; cleared on recovery → Task 2. ✓
- Reconnecting indicator in the view → Task 2. ✓
- `statusMsg` carries `at` so speed `dt` stays test-controllable → Task 2 (helper + handler). ✓
- Mutations auto-heal (drop-on-error → reconnect) → Task 3 (`do`). ✓
- Docs → Task 4. ✓
- Windows still cross-compiles (poller only calls `ensureDaemon`, which already guards Windows) → Tasks 3 verifies `GOOS=windows build`. ✓

**Placeholder scan:** the `statusMsg` body in Task 2 Step 4 is a MOVE of the existing, in-repo poll body with two named substitutions (`now`/`next` sources) — every change is concrete and verifiable against the current file, not invented. The view indicator (Step 5) is pinned by `TestDaemonDownShowsReconnecting` (View contains `reconnecting` when `daemonDown`). No TBD/TODO.

**Type consistency:** `statusPoller.Poll() ([]engine.Status, error)`, `statusMsg{at, statuses, err}`, `pollCmd(p, at)`, `Model.poll`/`Model.daemonDown`, `(*Client).StatusesErr()`, `daemonPoller` (implements `engine.Engine` + `Poll`), and `newDaemonPoller()` are used identically across tasks. The ui `fakeEngine` gains `Poll()` so it satisfies `statusPoller` (auto-attached in `New`); the production `daemonPoller` does too. `tick(m, t)` delivers `statusMsg{at: t, …}`, matching the handler's use of `msg.at`.
