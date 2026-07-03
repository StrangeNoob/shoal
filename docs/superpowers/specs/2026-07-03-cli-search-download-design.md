# Non-interactive CLI: `shoal search` + `shoal download` (+ Claude skill)

**Date:** 2026-07-03
**Component:** `cmd/shoal`, `internal/source`, `internal/engine`, `.claude/skills`
**Status:** Approved (design)

## Summary

Add two non-interactive subcommands to the existing `shoal` binary so a script —
specifically a Claude Code / Codex skill — can search for and download a single
title without driving the fullscreen TUI:

1. `shoal search "<query>" [--json] [--source <name>] [--limit N]` — searches the
   same multi-source catalogue the TUI uses and prints results, each with a short
   id. `--json` emits machine-readable rows (including the full magnet) for a skill
   to parse.
2. `shoal download <magnet|torrent-url|infohash|short-id> [--out <dir>]` —
   downloads one torrent in the foreground, prints progress, and exits 0 when the
   files are fully downloaded and hash-verified.

The default (no args) still launches the TUI. A bundled skill
(`.claude/skills/shoal-download/SKILL.md`) teaches Claude the search → pick →
download flow.

## Motivation

The TUI is interactive-only; a skill can't press keys in a Bubble Tea program. A
skill can only run a command and read its output. The two-step split (search
returns a list, download takes one id) is intentionally chosen over a single
auto-pick command because **the skill is the intelligence** — let Claude read the
list (title, size, seeders) and choose, rather than baking "most seeders wins"
into Go. This keeps the binary dumb and composable.

## Goals

- A scriptable `search` that returns clean, parseable results from the existing sources.
- A scriptable `download` that takes a magnet, a `.torrent` URL, a bare infohash, or a short id from a prior search, and blocks until complete.
- A Claude skill that chains the two so "find and download <title>" works end to end.
- Reuse the existing engine and source layers; no new binary, no duplicated stack.

## Non-goals (YAGNI)

- No background/daemon download — the TUI remains the stay-running / seeding mode.
- No seeding from the CLI (`download` forces `Seed=false` so a one-shot exits).
- No interactive picker inside `download` — selection is the skill's / caller's job.
- No resume-by-id, no bulk download, no new persisted config keys.
- Porting features into `cmd/shoal-classic` is explicitly out of scope; classic keeps its zero-dependency hand-written core.

## 1. Command routing (`cmd/shoal/main.go`)

The existing `cli(args, version, out)` already dispatches `version`/`help`/`update`
and returns `handled=false` to fall through to the TUI. Add cases:

- `case "search":` → `runSearch(args[2:], out)`
- `case "download":` → `runDownload(args[2:], out)`

Both return `handled=true` with an exit code. Update the `usage` string. No args →
TUI as today.

Flag parsing uses stdlib `flag` (a `flag.FlagSet` per subcommand) — no new
dependency.

## 2. `shoal search`

Signature: `shoal search "<query>" [--json] [--source <name>] [--limit N]`

- Builds the source set with `source.NewDefault()` (same providers as the TUI). If
  `--source <name>` is given, filter to sources whose `Name()` matches (case-insensitive
  substring); unknown name → error listing available names.
- Runs `Search(ctx, query)` with a context timeout (e.g. 30s).
- Sorts best-first by `Seeders` then `Popularity` (reuse existing sort if present),
  caps at `--limit` (default 30).
- Derives each row's short id = first 8 hex chars of the infohash, obtained from the
  result's `Magnet` via the existing `source.ParseMagnetInfoHash`. Results with no
  magnet/infohash are skipped for id purposes but still listed (id shown as `—`) —
  they can only be downloaded via their `TorrentURL`, which `--json` still carries.
- **Human output (default):** aligned columns `id  title  size  seeders  source`,
  sizes via the existing human-bytes helper.
- **`--json`:** a JSON array of objects:
  `{id, title, size_bytes, seeders, leechers, source, category, magnet, torrent_url}`.
- Writes the displayed (post-sort, post-`--limit`) result set to the cache file
  (see §4) so short ids resolve in a later `download`.
- Diagnostics (source failures, "searching…") → **stderr**; results → **stdout**.
- Exit 0 on success even with zero results (prints an empty list / `[]`); non-zero
  only on a hard failure (e.g. every source errored).

## 3. `shoal download`

Signature: `shoal download <arg> [--out <dir>]`

**Argument resolution** (first match wins):

1. Starts with `magnet:` → use as magnet directly.
2. Starts with `http://` / `https://` → treat as a `.torrent` URL.
3. Matches `^[0-9a-fA-F]{40}$` (or base32 infohash) → build `magnet:?xt=urn:btih:<hash>`.
4. Matches `^[0-9a-fA-F]{8}$` (short id) → look up the full magnet in the cache
   (§4). Missing cache or no match → clear error ("no recent search contains id
   <x>; run `shoal search` first").
5. Otherwise → error ("unrecognized download target").

**Engine + download loop:**

- Load config, but override `Seed=false` and `SeedRatio=0` so the one-shot download
  stops at completion. `DataDir` = `--out` if given, else `cfg.DataDir`. Reuse the
  existing `engine.NewAnacrolix(engine.Config{…})` wiring factored out of `main`.
- Add via `AddMagnet` (cases 1/3/4) or `AddTorrentURL(url, "")` (case 2).
- Poll `eng.Statuses()` on a ticker (~1s). There is exactly one torrent; print a
  progress line to **stderr** (`name  pct%  downloaded/total  peers`). When its
  `Done` is true, print the final on-disk `Path` to **stdout**, `eng.Close()`, exit 0.
- Failure modes → message to stderr, non-zero exit:
  - add error (bad magnet / unreachable URL),
  - context timeout with no metadata / no peers after a grace period (e.g. warn but
    keep waiting; a hard overall `--timeout` is a possible later flag, not v1 —
    default: wait indefinitely, user Ctrl-C's),
  - disk write error surfaced by the engine.
- SIGINT (Ctrl-C) → `Close()` and exit non-zero cleanly.

## 4. Short-id cache

- File: `filepath.Join(os.UserConfigDir(), "shoal", "last-search.json")` — same
  `shoal` config dir as `queue.json` / `config.json`.
- Written by `search` (overwrites each run — "last search only"). Content: the
  displayed results as `{id → {magnet, torrent_url, title}}` (or an array; a map keyed
  by id is simplest for lookup).
- Read by `download` only for the short-id case. Absent/garbage cache is a soft
  condition — a clear error telling the user to search first, never a crash.
- ponytail: last-search-only, no history of searches; if multi-search recall is ever
  wanted, this grows into a small ring — not now.

## 5. Claude skill (`.claude/skills/shoal-download/SKILL.md`)

A skill that drives the two commands:

- **Trigger:** user asks to find / download a movie, show, or file via shoal.
- **Flow:**
  1. Run `shoal search --json "<the title the user asked for>"`.
  2. Parse the JSON. If empty → tell the user nothing was found, suggest refining.
  3. Pick the best match (prefer high seeders and a sensible size/quality); if
     several are plausibly different things, ask the user which one.
  4. Run `shoal download '<chosen .magnet>'` (magnet, not short id — a skill run is
     stateless and shouldn't depend on the cache).
  5. Report the final path from stdout, or the error from a non-zero exit.
- Emphasize: always use `--json` for search; pass the full magnet to download; handle
  "no results" and "download failed" without hanging.

## 6. Testing (TDD, per project convention)

Write failing tests first. Table-driven, in `cmd/shoal`:

- **Argument resolution** — magnet / http url / 40-hex infohash / 8-hex short-id /
  garbage each route to the expected target (magnet string, url, or cache lookup);
  unknown → error. Factor resolution into a pure function to test without a network.
- **Search output shaping** — given a fixed `[]source.Result`, assert the JSON fields,
  the short-id derivation (first 8 of infohash), the `—` id for magnet-less results,
  and `--limit` truncation. Inject a fake `source.Source` so no network is hit.
- **Cache round-trip** — write from search, read back in download resolution; missing
  file → the expected soft error.

The live network download path is already covered by `internal/engine` tests and is
not re-mocked here.

## Files touched

- `cmd/shoal/main.go` — routing, `runSearch`, `runDownload`, arg-resolution helper,
  factored engine setup, usage text (+ `main_test.go`).
- Possibly a small new file in `cmd/shoal/` (e.g. `cli_search.go` / `cli_download.go`)
  if `main.go` grows past comfortable — keep files focused.
- `internal/source` — reuse only; add a tiny exported helper only if id derivation
  needs one not already present (`ParseMagnetInfoHash` exists).
- New: `.claude/skills/shoal-download/SKILL.md`.
- New: `…/shoal/last-search.json` at runtime (not committed).

## Open questions

None outstanding — approach (non-interactive on main binary), the id model (short id
in the table + full magnet in `--json`, download accepts both), and scope (CLI +
skill) are all decided.
