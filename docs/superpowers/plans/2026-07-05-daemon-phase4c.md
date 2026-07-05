# Shared-engine daemon Phase 4c — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enable the shared daemon (and the daemon-backed TUI/CLI) on Windows 10 1803+ by unblocking the two Windows-specific barriers — the `GOOS=="windows"` guards and the `flockExclusive` stub — while keeping the AF_UNIX transport unchanged.

**Architecture:** Go 1.24 supports AF_UNIX on Windows 10 1803+, so the unix-socket transport serves Windows too. Remove the `runtime.GOOS == "windows"` early-returns and make the Windows `flockExclusive` best-effort (no OS lock — stdlib has none on Windows without a new dep). Nothing else changes.

**Tech Stack:** Go 1.24, stdlib (`net` AF_UNIX, `syscall`).

## Global Constraints

- Go; stdlib + already-vendored deps only — **no new module dependencies** (this rules out `golang.org/x/sys/windows` for a real Windows lock).
- Commits carry **no Claude attribution**.
- The transport is **unchanged** (AF_UNIX socket); only the Windows guards + flock stub change.
- **No Windows runner here:** the Windows *runtime* is untestable in this environment. The gates are: the full unix suite still passes (transport logic untouched), `GOOS=windows go build ./...` and `GOOS=windows go vet ./...` are clean, and `gofmt -l` is clean. Windows ships best-effort/unverified-here (documented).
- Bind-first still prevents two *live* daemons everywhere; the stale-socket reclaim race is left unserialized on Windows (documented).

**Interfaces already present:** `ensureDaemon() (*daemon.Client, error)` and `runDaemon(args []string, out io.Writer) int` in `cmd/shoal/cli_daemon.go` (each currently opens with a `runtime.GOOS == "windows"` guard); `listenDaemon(sock string) (net.Listener, error)` calls `flockExclusive(path string) (*os.File, error)` on its reclaim path. `flockExclusive` has a `//go:build !windows` real implementation in `platform_unix.go` (uses `syscall.Flock`) and a `//go:build windows` stub in `platform_windows.go`. `detachSysProcAttr` (Windows) already sets `DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP`.

---

### Task 1: Unblock the daemon on Windows

**Files:**
- Modify: `cmd/shoal/cli_daemon.go` (remove two `GOOS=="windows"` guards + the `runtime` import)
- Modify: `cmd/shoal/platform_windows.go` (`flockExclusive` best-effort; drop `fmt` import)

**Interfaces:**
- Produces: `ensureDaemon`/`runDaemon` that no longer refuse Windows; a Windows `flockExclusive` that returns an open (unlocked) file.

> **Note (no new unit test):** this is an enablement change with no new *unix-runtime* behavior — the removed guards and the Windows-only flock stub never execute on the unix test host. A unix test asserting "the guard is gone" would assert nothing runnable. Per the spec, the gates are the cross-compile + `go vet` for Windows, the unchanged unix suite, and a `grep` check — all in Step 3.

- [ ] **Step 1: Remove the two Windows guards in `cmd/shoal/cli_daemon.go`**

In `ensureDaemon`, delete these three lines (currently the first statement of the function):

```go
	if runtime.GOOS == "windows" {
		return nil, fmt.Errorf("the shoal daemon is not yet supported on Windows")
	}
```

so `ensureDaemon`'s body now begins with `sock := daemon.SocketPath()`.

In `runDaemon`, delete these four lines (currently the first statement of the function):

```go
	if runtime.GOOS == "windows" {
		fmt.Fprintln(os.Stderr, "the shoal daemon is not yet supported on Windows")
		return 1
	}
```

so `runDaemon`'s body now begins with `cfg := config.Load()`.

Those were the only two uses of `runtime` in the file, so remove `"runtime"` from `cli_daemon.go`'s import block. (`fmt` and `os` stay — they're used throughout the rest of the file.)

- [ ] **Step 2: Make the Windows `flockExclusive` best-effort in `cmd/shoal/platform_windows.go`**

Replace the current stub:

```go
// flockExclusive is unsupported on Windows; runDaemon guards GOOS=="windows"
// before any path that would reach it.
func flockExclusive(path string) (*os.File, error) {
	return nil, fmt.Errorf("file locking is not supported on windows")
}
```

with a best-effort open (no OS lock):

```go
// flockExclusive on Windows opens the lock file but does NOT take a cross-process
// lock — Go's stdlib has no advisory file lock on Windows (a real one needs
// golang.org/x/sys/windows, a new dependency). The stale-socket reclaim race is
// left unserialized here; bind-first still prevents two live daemons.
// ponytail: acceptable — the race needs a crash-leftover socket AND two
// simultaneous cold-starts; upgrade to LockFileEx if it ever bites on Windows.
func flockExclusive(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
}
```

`fmt` is now unused in this file → remove `"fmt"` from its import block. Keep `"os"` (used by `flockExclusive`) and `"syscall"` (used by `detachSysProcAttr`).

- [ ] **Step 3: Verify (cross-compile is the primary gate)**

Run each and confirm the expected result:

```bash
grep -n 'GOOS == "windows"' cmd/shoal/cli_daemon.go   # expected: no matches
grep -n '"runtime"' cmd/shoal/cli_daemon.go            # expected: no matches
go build ./...                                          # clean (unix)
go vet ./...                                             # clean (unix)
go test ./...                                            # all pass (transport unchanged)
gofmt -l cmd/shoal/                                      # empty
GOOS=windows go build ./...                              # clean — the whole module cross-compiles
GOOS=windows go vet ./cmd/shoal/                         # clean — no unused imports (runtime/fmt) on Windows
```

Remove any stray `shoal.exe` left by the Windows build (`rm -f shoal.exe`).
Expected: all clean/pass; the two greps return nothing.

- [ ] **Step 4: Commit**

```bash
git add cmd/shoal/cli_daemon.go cmd/shoal/platform_windows.go
git commit -m "Run the daemon on Windows (AF_UNIX): drop the GOOS guards and the flock stub"
```

---

### Task 2: Docs

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Update the README for Windows support**

Update the daemon/TUI/CLI documentation so it no longer says the daemon is unix/macOS-only:

- Where the daemon or its idle-shutdown is described (4a), and where the TUI auto-reconnect is described (4b), drop any "unix/macOS only" / "Windows pending" / "Windows support is planned" phrasing.
- Add a short note in the daemon section:

> The shared daemon (and the TUI/CLI that use it) run on Linux, macOS, and
> **Windows 10 1803+** (which is where Windows gained AF_UNIX support). Windows
> support is best-effort.

- [ ] **Step 2: Verify + commit**

Run: `go build ./...` (sanity — no Go changed).

```bash
git add README.md
git commit -m "docs: the daemon now runs on Windows 10 1803+"
```

---

## Self-Review

**Spec coverage:**
- Remove the two `GOOS=="windows"` guards + `runtime` import → Task 1 Step 1. ✓
- `flockExclusive` best-effort on Windows + drop `fmt` → Task 1 Step 2. ✓
- Transport unchanged; detach + socket path already work → no task needed (spec §3), verified by cross-compile. ✓
- Docs: Windows 10 1803+, drop unix-only caveats, best-effort note → Task 2. ✓
- Gates: unix suite passes, `GOOS=windows go build/vet` clean, `gofmt` clean, grep confirms guards/runtime gone → Task 1 Step 3. ✓
- No-new-deps, no-Claude-attribution → Global Constraints. ✓

**Placeholder scan:** none — the two removals quote the exact current lines; the flock replacement is complete code; the doc edit names the exact phrases to drop and the note to add. The "no new unit test" is explicitly justified (Windows-only code is unrunnable on the unix host), not a skipped step.

**Type consistency:** `flockExclusive(path string) (*os.File, error)` keeps its exact signature (only the body changes), so `listenDaemon`'s call site and the `platform_unix.go` counterpart are unaffected. `ensureDaemon`/`runDaemon` signatures are unchanged. No new symbols introduced.
