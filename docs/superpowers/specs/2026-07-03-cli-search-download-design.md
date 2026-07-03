# Non-interactive CLI: `shoal search` / `download` / `status` (+ Claude skill)

**Date:** 2026-07-03
**Component:** `cmd/shoal`, `internal/source`, `internal/engine`, `internal/history`, `.claude/skills`
**Status:** Approved (design)

## Summary

Add three non-interactive subcommands to the existing `shoal` binary so a script —
specifically a Claude Code / Codex skill — can search for, download, and track a
single title without driving the fullscreen TUI:

1. `shoal search "<query>" [--json] [--source <name>] [--limit N]` — searches the
   same multi-source catalogue the TUI uses and prints results, each with a short
   id. `--json` emits machine-readable rows (including the full magnet).
2. `shoal download <magnet|torrent-url|infohash|short-id> [--out <dir>]` — starts a
   download **in the background** and returns immediately with a handle id.
3. `shoal status [id] [--json] [--clear]` — reports how far along the background
   download(s) are; run anytime, from any terminal.

The default (no args) still launches the TUI. A bundled skill
(`.claude/skills/shoal-download/SKILL.md`) chains search → download → poll status.

## Motivation

The TUI is interactive-only; a skill can't press keys in a Bubble Tea program. A
skill can only run a command and read its output. The search/download split is
chosen so **the skill is the intelligence** — Claude reads the list and picks,
rather than "most seeders wins" being baked into Go. Background download + a status
command lets the skill kick off a long download without blocking on it, then poll
progress in a clean loop.

## Goals

- A scriptable `search` returning clean, parseable results from the existing sources.
- A scriptable `download` that accepts a magnet, `.torrent` URL, bare infohash, or a
  short id from a prior search, runs in the background, and returns a handle.
- A scriptable `status` that reports progress (percent, bytes, peers, done/error) for
  in-flight and recently-finished downloads.
- A Claude skill that chains the three so "find and download <title>" works end to end.
- Reuse the existing engine, source, and history layers; no new binary, no daemon.

## Non-goals (YAGNI)

- **No long-lived daemon / socket.** Each download is its own short-lived detached
  process that exits when the torrent completes; `status` is a stateless reader of
  per-download files.
- No seeding from the CLI (`download` forces `Seed=false` so a one-shot exits at 100%).
- No interactive picker inside `download` — selection is the skill's / caller's job.
- No pause/resume/cancel of a background download in v1 (kill the worker `pid`, or use
  the TUI). No bulk download. No new persisted config keys.
- Porting features into `cmd/shoal-classic` is out of scope; classic keeps its
  zero-dependency hand-written core.

## 1. Command routing (`cmd/shoal/main.go`)

The existing `cli(args, version, out)` already dispatches `version`/`help`/`update`
and returns `handled=false` to fall through to the TUI. Add cases:

- `case "search":` → `runSearch(args[2:], out)`
- `case "download":` → `runDownload(args[2:], out)`
- `case "status":` → `runStatus(args[2:], out)`

`download` also honors a **hidden `--worker` flag** (not shown in `usage`) that marks
the detached child; the parent spawns the child with it (see §3). No args → TUI as
today. Flag parsing uses stdlib `flag` (a `FlagSet` per subcommand) — no new dependency.

Subcommand handlers likely live in focused files (`cli_search.go`, `cli_download.go`,
`cli_status.go`) to keep `main.go` small.

## 2. `shoal search`

Signature: `shoal search "<query>" [--json] [--source <name>] [--limit N]`

- Builds the source set with `source.NewDefault()` (same providers as the TUI).
  `--source <name>` filters to sources whose `Name()` matches (case-insensitive
  substring); unknown name → error listing available names.
- Runs `Search(ctx, query)` with a context timeout (~30s), sorts best-first by
  `Seeders` then `Popularity`, caps at `--limit` (default 30).
- Row short id = first 8 hex chars of the infohash, from the result's `Magnet` via the
  existing `source.ParseMagnetInfoHash`. Results with no magnet/infohash list with id
  `—` (downloadable only via their `TorrentURL`, which `--json` still carries).
- **Human (default):** aligned `id  title  size  seeders  source`.
- **`--json`:** array of `{id, title, size_bytes, seeders, leechers, source, category,
  magnet, torrent_url}`.
- Writes the displayed (post-sort, post-`--limit`) results to the short-id cache (§5)
  so `download <short-id>` resolves later.
- Diagnostics → **stderr**; results → **stdout**. Exit 0 even with zero results
  (empty list / `[]`); non-zero only if every source errored.

## 3. `shoal download` (background)

Signature: `shoal download <arg> [--out <dir>]` (+ hidden `--worker --id <h> --out <d>`)

**Argument resolution** (pure function, first match wins):

1. `magnet:` prefix → magnet.
2. `http://` / `https://` → `.torrent` URL.
3. `^[0-9a-fA-F]{40}$` (or base32 infohash) → build `magnet:?xt=urn:btih:<hash>`.
4. `^[0-9a-fA-F]{8}$` (short id) → look up the full magnet in the short-id cache (§5).
   Missing/stale cache → clear error ("no recent search contains id <x>; run `shoal
   search` first").
5. Else → error ("unrecognized download target").

**Handle id** = short infohash when known at launch (cases 1/3/4 — so `search` id
`a1b2c3` → `download a1b2c3` → `status a1b2c3` all line up). For a URL (case 2) the
infohash isn't known yet, so the launcher assigns a generated 8-char handle; the
worker fills in the real infohash once metadata arrives.

**Parent (no `--worker`):**
- Resolve arg → target + handle.
- Spawn a detached copy of itself: `shoal download --worker --id <handle> --out <dir>
  <target>`, stdio redirected to `…/shoal/logs/<handle>.log`, **not waited on**. Unix:
  `SysProcAttr{Setsid: true}`; Windows: detached-process flags in a build-tagged helper.
- Print `started: <name-or-magnet-dn> (<handle>)` to stdout, exit 0.

**Worker (`--worker`):**
- Build the engine from config with `Seed=false`, `SeedRatio=0`, `DataDir` = `--out`
  or `cfg.DataDir`. Reuse the `engine.NewAnacrolix` wiring factored out of `main`.
- Add via `AddMagnet` (cases 1/3/4) or `AddTorrentURL(url, "")` (case 2).
- Loop on a ~1s ticker: read the single `Statuses()` entry, write the active state
  file `…/shoal/active/<handle>.json` (§4). Exactly one torrent per worker.
- On `Done` → write terminal state (`done:true`, final `Path`), record the completion
  via `internal/history`, `Close()`, exit 0.
- On add error / disk error → write `error:"…"`, exit non-zero. SIGINT → `Close()`, exit.
- The loop body is factored to take an `engine.Engine`, so tests drive it with a fake
  engine — no real detach or network in tests.

## 4. `shoal status`

Signature: `shoal status [id] [--json] [--clear]`

- Reads every `…/shoal/active/*.json` (or just `<id>.json` when an id is given).
- **Human (default):** aligned `id  name  pct  completed/total  peers  state`, where
  state ∈ `downloading | done | error | stalled`. **`stalled`** = `pid` no longer alive
  and not `done` (the worker died mid-download).
- **`--json`:** array of the state objects.
- **`--clear`:** delete finished/errored entries (`done` or `error`) after printing;
  live/stalled entries are kept.
- No active files → empty output / `[]`, exit 0. Unknown `<id>` → exit non-zero.

## 5. State files

All under the existing `shoal` config dir, `filepath.Join(os.UserConfigDir(), "shoal", …)`:

- **`last-search.json`** — short-id cache. Overwritten each `search` ("last search
  only"). Maps `id → {magnet, torrent_url, title}` for `download <short-id>` lookup.
  Absent/garbage is a soft error in `download`, never a crash.
- **`active/<handle>.json`** — one file **per download**, owned solely by that worker
  (no locking, no write races). Fields: `{id, infohash, name, total, completed, peers,
  seeding, done, error, out, pid, updated_at}`. Written each tick and on terminal state.
  `status` reads them; `status --clear` prunes finished ones.
- **`logs/<handle>.log`** — the detached worker's stdout/stderr, for post-mortem.

ponytail: per-file ownership avoids a lock; `last-search` keeps only the latest search.
If multi-search recall or a lock-free single-file store is ever wanted, revisit then.

## 6. Claude skill (`.claude/skills/shoal-download/SKILL.md`)

- **Trigger:** user asks to find / download a movie, show, or file via shoal.
- **Flow:**
  1. `shoal search --json "<title the user asked for>"`; parse JSON.
  2. Empty → tell the user, suggest refining the query.
  3. Pick the best match (high seeders, sensible size/quality); ask the user if several
     plausibly-different titles match.
  4. `shoal download '<chosen .magnet>'` — capture the returned handle. Use the magnet
     (not the short id): a fresh skill run is stateless and shouldn't rely on the cache.
  5. Poll `shoal status <handle> --json` every few seconds until `done:true` (report
     the path) or `error`/`stalled` (report the failure). Don't block indefinitely —
     give up after a sensible number of polls with no progress.

## 7. Testing (TDD, per project convention)

Write failing tests first. Table-driven, in `cmd/shoal`:

- **Argument resolution** — magnet / http url / 40-hex infohash / 8-hex short-id /
  garbage route to the expected target; unknown → error. Pure function, no network.
- **Handle derivation** — infohash-based when known, generated token for URL case.
- **Search output shaping** — fixed `[]source.Result` → JSON fields, short-id
  derivation, `—` id for magnet-less rows, `--limit` truncation. Fake `source.Source`.
- **Worker loop** — fake `engine.Engine` returning a scripted progression → asserts the
  active state file transitions to `done` with the final path; error path writes `error`.
- **Status formatting** — given fixture `active/*.json`, assert table + JSON output, the
  `stalled` classification (dead pid), and `--clear` pruning of finished entries.
- **Cache round-trip** — `search` writes, `download` short-id path reads back; missing
  file → the expected soft error.

The live network download is covered by `internal/engine` tests; real process detach is
not tested (the worker loop is exercised directly with a fake engine).

## Files touched

- `cmd/shoal/main.go` — routing + usage text (+ `main_test.go`).
- New `cmd/shoal/cli_search.go`, `cli_download.go`, `cli_status.go` (+ tests) — handlers,
  arg-resolution and handle-derivation helpers, factored engine setup, the worker loop.
- New `cmd/shoal/detach_unix.go` / `detach_windows.go` (build-tagged) — spawn the
  detached worker.
- New small state module (in `cmd/shoal` or `internal/`) for `active/*.json` +
  `last-search.json` read/write.
- `internal/source`, `internal/engine`, `internal/history` — reuse only.
- New `.claude/skills/shoal-download/SKILL.md`.
- Runtime (not committed): `…/shoal/last-search.json`, `…/shoal/active/*.json`,
  `…/shoal/logs/*.log`.

## Open questions

None outstanding. Approach (non-interactive on the main binary), the id model (short id
in the table, full magnet in `--json`, download accepts both), background download via
detached per-download workers + a stateless `status` reader, and scope (CLI + skill) are
all decided.
