# Seeding-pane actions + open download folder

**Date:** 2026-07-03
**Component:** `internal/engine`, `internal/ui`
**Status:** Approved (design)

## Summary

Two per-torrent actions in the download panes:

1. **Stop / pause seeding** — from the Seeding pane, `p` pauses/resumes seeding a
   torrent (stop/resume uploading, keep files, resumable) and `x` stops it
   entirely (removes from the client, keeps files).
2. **Open download folder** — from the Downloads or Seeding pane, `o` opens the
   selected torrent's folder in the OS file manager, with clear notices when the
   folder isn't ready yet or has been deleted.

Both add a selection cursor + key handling to the Seeding pane and reuse the
existing `Pause`/`Resume`/`Remove` engine methods and the Downloads pane's
cursor/confirm patterns.

## Goals

- Let the user stop an individual torrent from seeding (pause = resumable; stop = permanent, files kept).
- Let the user open a torrent's folder on disk, and be told when it isn't ready or is gone.

## Non-goals (YAGNI)

- No keep/delete choice on **stop seeding** — files are always kept (they're completed downloads).
- No bulk stop-all / open-all.
- No "reveal/select the file" — `o` opens the containing folder only.
- `o` is not wired into the detail screen (only the pane selection).

## 1. Seeding-pane cursor + actions (`internal/ui`)

- `Model` gains `seedCursor int` (mirrors `dlCursor`) and `stopConfirm bool` +
  `stopTarget engine.Status`.
- **Navigation:** in `sectionSeeding`, `↑ ↓` (and `k`/`j`) move `seedCursor` over
  `m.seeding()`, clamped to `[0, len(seeding)-1]`; on each tick the cursor is
  re-clamped if the list shrank (same guard as `dlCursor`). The selected row is
  highlighted (`glyphCursor` + `st.RowSel`, like Downloads).
- **`p` (pause/resume):** on the selected seeder, if `Status.Paused` →
  `m.eng.Resume(infoHash)`, else `m.eng.Pause(infoHash)` (returned as a `tea.Cmd`
  closure, exactly like the Downloads `p` handler). A paused complete torrent
  stops uploading (`SetMaxEstablishedConns(0)` drops peers); the paused state
  already persists via `queue.json`.
- **`x` (stop):** opens a confirm — `m.stopConfirm = true; m.stopTarget = sel`.
  While `stopConfirm`, `enter` (or `y`) → `removeCmd(m.eng, stopTarget.InfoHash,
  stopTarget.Name, false)` (keep files) then `m.stopConfirm = false`; `esc`
  cancels. On the tick, if `stopTarget` is no longer seeding, clear `stopConfirm`
  (same pattern the cancel-confirm uses).
- **Render (`renderSeeding`):** the selected row uses the cursor glyph; a paused
  seeder renders `⏸ paused (not sharing)` in place of the
  `complete · ratio · ↑… · N peers` line. During `stopConfirm`, a prompt line
  renders above the list: `Stop seeding "<name>"?  enter stop (keep files) · esc back`.

## 2. Open download folder

### Engine (`internal/engine`)
`Status` gains `Path string` — the torrent's top-level on-disk path:
`filepath.Join(a.dataDir, t.Name())` when metadata is available (`t.Info() != nil`),
else `""`. Uses `t.Name()` (the real on-disk name), not the display name.

### UI (`internal/ui`)
- **`o` (open):** in `sectionDownloads`/`sectionSeeding`, on the selected torrent
  (`m.downloading()[m.dlCursor]` / `m.seeding()[m.seedCursor]`):
  - `Path == ""` → `setNotice("download folder isn't ready yet")`.
  - else `os.Stat(Path)`: on not-exist → `setNotice("folder not found — it may have been deleted")` (`noticeErr`).
  - else compute the folder to open: `Path` if it's a directory, else
    `filepath.Dir(Path)` (single-file torrent → its containing dir); return
    `openFolderCmd(dir)`.
- `openFolderCmd(dir) tea.Cmd` runs the opener off the UI goroutine and returns
  `folderOpenedMsg{err error}`; on `err != nil` → `setNotice("couldn't open the folder")`.
- Helpers (in `internal/ui/helpers.go`):
  - `openCommand(goos, path string) (name string, args []string)` — pure,
    testable: `darwin`→`("open", [path])`, `windows`→`("explorer", [path])`,
    else→`("xdg-open", [path])`.
  - `openInFileManager(path string) error` — `name, args := openCommand(runtime.GOOS, path); exec.Command(name, args...).Start()`.

## Footer (`renderFooter`)

- Downloads: `↑↓ move · o open · p pause/resume · x cancel · tab panes · ? help · q quit`.
- Seeding: `↑↓ move · o open · p pause/resume · x stop · tab panes · ? help · q quit`.
- During Seeding `stopConfirm`: `enter stop · esc back`.
- The `?` help screen gains rows for `o` (open folder) and notes `p`/`x` apply to Seeding too.

## Error handling

- Unknown/absent selection (empty pane) → the key is a no-op.
- `o` on a not-ready or deleted path → a notice, never a crash; the opener runs
  detached (`Start()`, not `Run()`), so a slow/failed file manager can't block the TUI.
- `x` stop always keeps files (`deleteData=false`); it removes the torrent from
  the client and `queue.json` (won't re-seed on restart).

## Testing

**`internal/engine`**
- `Status.Path == filepath.Join(dataDir, "<name>")` after adding an offline
  `.torrent` (metadata present); `""` for a magnet with no metadata yet.

**`internal/ui`**
- `openCommand`: table (darwin/linux/windows → right exec + args).
- `o` on a `Status{Path: ""}` → notice "isn't ready"; on `Path` pointing at a
  non-existent path → notice "deleted" (`noticeErr` true); on an existing temp
  dir → returns a non-nil command (folder-open).
- Seeding `p` on the selected seeder calls `fakeEngine.Pause`/`Resume`.
- Seeding `x` opens `stopConfirm`; `enter` calls `fakeEngine.Remove(hash, false)`
  and clears the confirm; `esc` cancels.
- `seedCursor` clamps/navigates with `↑↓`; a `Status{Paused:true}` seeder renders "paused".

## Files touched

- Modify: `internal/engine/engine.go` (`Status.Path`), `internal/engine/anacrolix.go`
  (set `Path` in `Statuses`) + `anacrolix_test.go`.
- Modify: `internal/ui/model.go` (`seedCursor`, `stopConfirm`/`stopTarget`, key
  handling for `o`/`p`/`x`/`↑↓` in Seeding, tick clamps, `openFolderCmd`/`folderOpenedMsg`),
  `internal/ui/view.go` (`renderSeeding` cursor + paused + stop-confirm, footer,
  help), `internal/ui/helpers.go` (`openCommand`/`openInFileManager`), plus
  `model_test.go`/`view_test.go`/`helpers_test.go`.
