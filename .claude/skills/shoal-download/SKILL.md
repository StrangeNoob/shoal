---
name: shoal-download
description: Use when the user wants to find and download a movie, show, or other torrent by name using shoal. Searches sources, picks the best match, downloads in the background, and reports progress.
---

# Downloading with shoal

Shoal exposes several non-interactive commands (search, download, status,
history, pause/resume/remove/open, ...). Use them; never launch the bare
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
- Download only some files of a multi-file torrent: add `--files '<glob>'` to the download
  command (see **Selecting files** below).

## History & Management

**History** — completed downloads are persisted and accessible via the CLI:

- `shoal history [--json]` — list all completed downloads (JSON format for scripts).
- `shoal history rm <id> [--delete-files]` — remove a history entry. By default keeps files;
  add `--delete-files` to also delete them from disk.
- `shoal history clear [--delete-files]` — wipe the history log. By default keeps all files;
  add `--delete-files` to also delete all downloaded files.

**Live Download Control** (requires a running daemon; doesn't auto-start it):

- `shoal pause <id>` — pause a download or active seeder.
- `shoal resume <id>` — resume a paused download.
- `shoal remove <id> [--delete-files]` — cancel/remove a download or seeder. By default
  keeps files; add `--delete-files` to also delete them.
- `shoal open <id>` — reveal the download's folder in your file manager (or print the path).

In all cases, `<id>` is an **infohash prefix** from `shoal status` or `shoal history`
(you can use a unique short prefix; the daemon resolves it). History deletion is also
available in the TUI (Seeding pane, press `x` on a history row, then `k` to keep files or
`d` to delete them).

## Selecting files

For a multi-file torrent (e.g. a season pack, or a release with sample/extras files) you
can download only some of the files. Globs are comma-separated, case-insensitive, and
matched against both the full path and the basename (so `*.mkv` catches nested files;
`Season 1/*` matches by path). There is no `**`.

- **At download time:** `shoal download '<magnet>' --files '<glob>'`
  — e.g. `--files '*.mkv'` for just the videos, `--files '*.mkv,*.srt'` for videos + subs.
  Applied once metadata arrives (works for magnets). Not supported for `.torrent`-URL
  downloads yet.
- **List a download's files:** `shoal files <id> [--json]` — a table of `# / ✓·✗ (selected) /
  SIZE / PATH`, one row per file (needs the torrent's metadata to have arrived).
- **Change the selection later:** `shoal files <id> --only '<glob>'` — download only the
  files matching the glob and stop the rest. Files stop/resume live and the selection
  persists across restarts. An empty pattern or one that matches nothing is rejected (it
  won't silently deselect everything).

## Notes

All downloads run in a shared background `shoal daemon` (auto-started on the first `download` command). This means multiple downloads and all `shoal status` queries share a single engine and download folder. All downloads land in shoal's configured folder (Settings → Save to, or in `config.json`).
