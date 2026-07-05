# Shared-engine daemon — Phase 4c: Windows transport (AF_UNIX everywhere)

**Date:** 2026-07-05
**Component:** `cmd/shoal/cli_daemon.go`, `cmd/shoal/platform_windows.go`, docs
**Status:** Approved (design)
**Part of:** the TUI↔CLI live-sync project. Phases 1–3 + 4a + 4b are merged. **4c is the final Phase-4 piece.**

## Summary

Enable the shared daemon (and thus the daemon-backed TUI and CLI) on Windows.
Go 1.24 supports AF_UNIX on Windows 10 1803+, so the transport is **unchanged** —
the daemon listens on a unix-domain socket at `%AppData%\shoal\daemon.sock` exactly
as it does on macOS/Linux. Only two Windows-specific blockers are removed: the
explicit `GOOS == "windows"` guards, and the Windows `flockExclusive` stub (which
returned an error and would fail the stale-socket reclaim).

## Goals

- `shoal` (TUI), `shoal download`, `shoal status`, and `shoal daemon`/`stop`/`status` run on Windows 10 1803+.
- No transport change — the existing unix-socket code path serves Windows too (so the merged, tested logic is reused unchanged).
- No new dependencies (stdlib only).

## Non-goals (deferred / out of scope)

- A real cross-process advisory lock on Windows for the stale-socket reclaim — Go stdlib has none, and `golang.org/x/sys/windows` is a new dependency (disallowed). The reclaim race stays unserialized on Windows (see §2); bind-first still prevents two *live* daemons.
- Windows < 10 1803 (no AF_UNIX). Unsupported.
- Named-pipe or TCP transports (the AF_UNIX-everywhere decision from brainstorming).

## Decisions (from brainstorming)

- **AF_UNIX everywhere:** keep the unix-socket transport; Go 1.24 supports it on Windows 10 1803+. Minimal change, reuses the proven transport.
- **`flockExclusive` on Windows is best-effort** (opens the lock file, takes no OS lock) rather than adding a dependency.

## Honest limitation: unverified on real Windows

This environment has **no Windows runner**, so the Windows *runtime* path (AF_UNIX
socket actually working, detached daemon spawning) **cannot be executed or tested
here**. Verification is limited to: the change cross-compiles (`GOOS=windows go
build ./...` + `go vet`) and the unchanged transport logic still passes the unix
test suite. Windows support therefore ships **best-effort / unverified-here**, and
this caveat is stated in the spec and the README.

## 1. Remove the Windows guards — `cmd/shoal/cli_daemon.go`

- In `ensureDaemon` (currently line ~37), delete:
  ```go
  if runtime.GOOS == "windows" {
      return nil, fmt.Errorf("the shoal daemon is not yet supported on Windows")
  }
  ```
- In `runDaemon` (currently line ~181), delete:
  ```go
  if runtime.GOOS == "windows" {
      fmt.Fprintln(os.Stderr, "the shoal daemon is not yet supported on Windows")
      return 1
  }
  ```
- These are the only uses of `runtime` in the file, so remove `"runtime"` from the import block. (`fmt`/`os` remain used elsewhere.)

## 2. `flockExclusive` best-effort on Windows — `cmd/shoal/platform_windows.go`

Today (`//go:build windows`) it returns an error, which fails `listenDaemon`'s
reclaim path. Replace it with a best-effort open (no OS lock):

```go
// flockExclusive on Windows opens the lock file but does NOT take a cross-process
// lock — Go's stdlib has no advisory file lock on Windows (a real one needs
// golang.org/x/sys/windows, a new dependency). The stale-socket reclaim race is
// left unserialized here; bind-first still prevents two live daemons.
// ponytail: acceptable — the reclaim race needs a crash-leftover socket AND two
// simultaneous cold-starts; upgrade to LockFileEx if it ever bites on Windows.
func flockExclusive(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
}
```

- This returns a valid `*os.File` so `listenDaemon`'s `defer lock.Close()` works; it just doesn't lock.
- `fmt` is no longer used in this file → remove it from the imports (keep `os`; keep `syscall` — used by `detachSysProcAttr`).
- `detachSysProcAttr` (same file) already sets `DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP` — unchanged.

## 3. What already works on Windows (no change)

- **Socket path:** `daemon.SocketPath()` uses `os.UserConfigDir()` → `%AppData%` on Windows → `%AppData%\shoal\daemon.sock` (well under the AF_UNIX path limit). The uid-namespaced fallback (`os.Getuid()`, returns -1 on Windows) is only reached if `UserConfigDir` fails, which it doesn't on a normal Windows — no change needed.
- **Transport:** `daemon.Dial`/`Serve`/`listenDaemon` use `net.Dial`/`net.Listen("unix", …)`, which Go 1.24 supports on Windows 10 1803+.
- **Detach:** `detachSysProcAttr` (Windows) is already correct.

## 4. Docs — `README.md`

- State that the daemon-backed TUI and CLI now run on **Windows 10 1803+** (AF_UNIX).
- Remove the earlier "unix/macOS only" / "Windows pending" caveats added in 4a/4b (the daemon idle-shutdown note, the TUI-reconnect note, and the `daemon stop`/`status` "not running on Windows" implication).
- Add a one-line note that Windows support is best-effort (AF_UNIX requires Windows 10 1803+).

## 5. Testing (TDD-light)

This is an enablement change with no new *unix-runtime* behavior (the removed guards
and the Windows-only flock stub don't run on unix), so there is little to unit-test
beyond what the existing suite already covers:

- The full unix suite still passes (`go test ./...`, `-race` where already applied) — the transport logic is untouched.
- **`GOOS=windows go build ./...`** and **`GOOS=windows go vet ./...`** are clean (the whole module cross-compiles and vets for Windows) — this is the primary new gate, confirming the guard removal and flock change compile for Windows with no unused imports.
- A brief `grep` confirms no `GOOS == "windows"` daemon guard and no `runtime` import remain in `cli_daemon.go`.

(No new unit test is added: the Windows-specific code paths cannot be executed on the unix test host, and adding a unix test that asserts "the guard is gone" would assert nothing runnable. The cross-compile + existing suite are the honest gates.)

## Files touched

- `cmd/shoal/cli_daemon.go` — remove the two `GOOS=="windows"` guards + the `runtime` import.
- `cmd/shoal/platform_windows.go` — `flockExclusive` best-effort; drop the `fmt` import.
- `README.md` — Windows now supported (best-effort, Win10 1803+); drop the unix-only caveats.

## Known limitations (documented)

- Requires **Windows 10 1803+** (AF_UNIX). Older Windows is unsupported.
- The stale-socket reclaim race is **unserialized on Windows** (rare: needs a crash-leftover socket + two simultaneous cold-starts).
- **Unverified on real Windows** in this environment — best-effort; cross-compiles and reuses the unix-tested transport.

## Open questions

None. AF_UNIX-everywhere, the best-effort Windows flock, and the documented
unverified-Windows caveat are all decided.
