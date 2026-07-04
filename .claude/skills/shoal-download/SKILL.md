---
name: shoal-download
description: Use when the user wants to find and download a movie, show, or other torrent by name using shoal. Searches sources, picks the best match, downloads in the background, and reports progress.
---

# Downloading with shoal

Shoal exposes three non-interactive commands. Use them; never launch the bare
`shoal` TUI (it is interactive and will hang a scripted session).

## Sources

`shoal search` queries **twelve** torrent providers **concurrently** and merges
the results (each row carries its own `source` label) — it is not limited to any
one site. A slow or failing provider is skipped; you only get an error if *every*
source fails.

Integrated sources: Internet Archive, Open Media (curated), YTS, The Pirate Bay
(Movies + TV), 1337x (Movies + TV), EZTV, SolidTorrents, Nyaa, SubsPlease, FitGirl.
Get the exact list at runtime with `shoal sources` (or `shoal sources --json` for
a JSON array of {name, enabled} objects).

Results are sorted best-first (seeders, then popularity), so the top hit can come
from any provider. To restrict to one, use `--source <name>` (case-insensitive
substring), e.g. `--source archive`, `--source yts`, `--source nyaa`; an unknown
name prints the available list.

**EZTV is browse-only** — the EZTV API has no keyword search, so it returns
nothing for `shoal search "<query>"` and never appears in results. That is by
design, not an outage; don't treat its absence as a broken source. (For anime,
rely on Nyaa and SubsPlease; for TV keyword search, The Pirate Bay, 1337x, and
SolidTorrents cover it.)

## Flow

1. **Search** — always with `--json`:
   `shoal search --json "<what the user asked for>"`
   Parse the JSON array. Each element: `{id, title, size_bytes, seeders, magnet, source, ...}`.

2. **No results** → tell the user nothing matched and suggest a more specific or
   more general query. Stop.

3. **Pick** the best match: prefer high `seeders`, then a sensible `size_bytes`
   for the requested quality. If several results are plausibly *different* titles
   (not just quality variants), show the user the top few and ask which one.

4. **Download** using the chosen result's **`magnet`** (not the short id — a fresh
   run has no search cache):
   `shoal download '<magnet>'`
   It prints `started: <name> (<handle>)` and returns immediately. Capture `<handle>`.

5. **Poll** progress every few seconds:
   `shoal status <handle> --json`
   Each poll returns `{state, percent, completed, total, peers, ...}` where
   `state` is one of `downloading | done | seeding | paused`.
   - `state == "done" || state == "seeding"` → report the final path, stop.
   - otherwise → keep polling. Give up after a reasonable number of polls with no
     progress and tell the user it is stuck (check the daemon log —
     `~/Library/Application Support/shoal/logs/daemon.log` on macOS,
     `~/.config/shoal/logs/daemon.log` on Linux).

## Options

- Restrict to one provider: `shoal search --json "<query>" --source <name>`.
- Cap results: `shoal search --json "<query>" --limit <N>` (default 30; `0` = no limit).
- Clear finished entries from status: `shoal status --clear` (removes finished/done torrents, keeping files).

## Notes

All downloads run in a shared background `shoal daemon` (auto-started on the first `download` command). This means multiple downloads and all `shoal status` queries share a single engine and download folder. All downloads land in shoal's configured folder (Settings → Save to, or in `config.json`).
