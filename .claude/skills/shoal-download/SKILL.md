---
name: shoal-download
description: Use when the user wants to find and download a movie, show, or other torrent by name using shoal. Searches sources, picks the best match, downloads in the background, and reports progress.
---

# Downloading with shoal

Shoal exposes three non-interactive commands. Use them; never launch the bare
`shoal` TUI (it is interactive and will hang a scripted session).

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
   Each poll returns `{state, percent, completed, total, peers, ...}`.
   - `state == "done"` → report the final path, stop.
   - `state == "error"` or `"stalled"` → report the failure (check
     `~/.config/shoal/logs/<handle>.log` if the user wants detail), stop.
   - otherwise → keep polling. Give up after a reasonable number of polls with no
     progress and tell the user it is stuck.

## Options

- Save elsewhere: `shoal download '<magnet>' --out <dir>`.
- Clear finished entries from status: `shoal status --clear`.
