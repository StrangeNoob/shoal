# Pause / Resume downloads + persisted queue

**Date:** 2026-07-02
**Component:** `internal/engine`, `internal/queue` (new), `internal/ui`, `cmd/shoal`
**Status:** Approved (design)

## Summary

Let the user pause and resume individual downloads, and persist the full set of added
torrents so they are restored on the next launch (downloads resume from on-disk pieces,
seeding resumes, paused stays paused). Today nothing is restored — the torrent list is
forgotten each launch, so seeding silently stops and downloads must be re-added by hand.

## Goals

- **Pause / resume** an in-progress download from the Downloads pane (`p`).
- **Persist all added torrents** (downloads + seeding + paused state) and re-add them on
  startup, so a restart restores the session.

## Non-goals (YAGNI)

- No concurrency limit / scheduler — "queue" here means the persisted *set* of torrents,
  not a throttle; all downloads still run at once.
- No pause control in the Seeding pane (pause targets in-progress downloads).
- No caching of `.torrent` bytes for URL-added torrents (see Known limitation).

## 1. Engine: pause / resume

`Engine` interface gains:
```go
// Pause halts the torrent with the given hex infohash (stops downloading and
// uploading). An unknown hash is a no-op (nil error).
Pause(infoHash string) error
// Resume restarts a paused torrent. An unknown hash is a no-op (nil error).
Resume(infoHash string) error
```

`Status` gains `Paused bool`.

**anacrolix backend** (`internal/engine/anacrolix.go`):
- `Anacrolix` gains `paused map[metainfo.Hash]bool` (guarded by the existing `mu`) and
  stores the configured max-conns (`maxConns int`, from `Config.MaxPeers`, defaulting to
  the anacrolix default `50` when `MaxPeers == 0`) so resume can restore it.
- `Pause(hex)`: find the torrent; `t.DisallowDataDownload()` + `t.SetMaxEstablishedConns(0)`;
  set `paused[h] = true`; persist (see §3).
- `Resume(hex)`: `t.AllowDataDownload()` + `t.SetMaxEstablishedConns(a.maxConns)`;
  set `paused[h] = false`; persist.
- `Statuses()` sets `Status.Paused = a.paused[h]`.
- The seed-ratio loop is unchanged; a paused torrent already has conns at 0, so ratio
  enforcement is a no-op on it.

## 2. Persisted store: `internal/queue`

New package mirroring `internal/history` (same file-permission and load/save shape):
```go
type Entry struct {
    InfoHash   string // lowercase hex; the identity
    Magnet     string // set if added via magnet
    TorrentURL string // set if added via a .torrent URL
    Name       string // display hint
    Paused     bool
}
type Store struct {
    Path    string
    Entries []Entry
}
func Load() *Store                     // default path = <config dir>/queue.json
func LoadFrom(path string) *Store
func (s *Store) Save() error           // 0o600 file, 0o700 dir
func (s *Store) Upsert(e Entry)        // replace-by-InfoHash or append
func (s *Store) Remove(infoHash string)
func (s *Store) SetPaused(infoHash string, paused bool)
```
`queue.json` lives in the OS config dir (like `history.json`/`config.json`). Exactly one
of `Magnet`/`TorrentURL` is set per entry.

## 3. Engine owns persistence (self-restoring)

`engine.Config` gains `QueuePath string` (empty = persistence disabled, e.g. in tests).

- `NewAnacrolix`: if `QueuePath != ""`, `queue.LoadFrom(QueuePath)` and re-add every entry
  via the internal add path with `persist=false` (they're already stored); after add,
  apply `Pause` for entries with `Paused == true`. anacrolix re-verifies on-disk pieces on
  add, so completed torrents resume seeding and partial ones resume downloading.
- The add helpers split into an internal `addTorrent(mi, name, persist)` /
  `addMagnetURI(uri, name, persist)`; the public `AddTorrentURL`/`AddMagnet` call them with
  `persist=true`, writing an `Entry` (with `Magnet` or `TorrentURL`) via `store.Upsert` + `Save`.
- `Remove` → `store.Remove(hex)` + `Save`. `Pause`/`Resume` → `store.SetPaused(hex, …)` + `Save`.
- The engine holds `store *queue.Store` (nil when `QueuePath == ""`); all store calls are
  nil-guarded. Store access happens under `mu`.

**Restore ordering:** re-add is best-effort — a failed entry (e.g. a dead `.torrent` URL)
is skipped and left in the store; magnet entries always re-add. Restore runs synchronously
in `NewAnacrolix` before it returns, so `Statuses()` reflects the restored set immediately.

## 4. UI

Downloads pane (`internal/ui`):
- `p` toggles pause/resume on the selected download (`m.downloading()[m.dlCursor]`): if the
  target's `Status.Paused`, call `engine.Resume(infoHash)`, else `engine.Pause(infoHash)`.
  No local row mutation is needed — the existing periodic tick re-reads `Statuses()`, which
  reflects the new `Paused` state on the next refresh.
- Paused rows render `⏸ paused` in place of the speed/`↓/s` readout; the progress bar stays
  (frozen). The rate sampler (`computeRates`) naturally shows 0/s for a paused torrent.
- The Downloads footer hint gains `p pause`.
- `fakeEngine` in `model_test.go` implements `Pause`/`Resume` (record calls).

## 5. main

`cmd/shoal/main.go` sets `engine.Config.QueuePath` to `<config dir>/queue.json` (reuse the
same dir resolution `internal/queue` uses; a small exported helper `queue.DefaultPath()`).
No other change — restore is automatic inside `NewAnacrolix`.

## Error handling

- Unknown infohash to `Pause`/`Resume`/`Remove` → no-op, nil error.
- Restore re-add failure → skip that entry, keep it in the store, continue with the rest.
- Store save failure → surfaced as the mutating call's error where practical; a failed
  `Save` during Add/Pause must not crash the engine (log-and-continue; the in-memory state
  is still correct for this session).
- Nil store (`QueuePath == ""`) → all persistence calls are no-ops.

## Known limitation

Magnet-added torrents restore robustly. A torrent added via a `.torrent` **URL** re-fetches
that URL on restart; if the URL is dead the torrent isn't restored (its files remain on
disk). Search results are magnets, so this rarely bites. Caching the `.torrent` bytes for
offline URL restore is a possible follow-up.

## Testing (TDD)

**`internal/queue`**
- Round-trip: Upsert two entries, Save, Load → same entries.
- `Upsert` replaces by `InfoHash` (no duplicates); `Remove` drops by hash; `SetPaused` flips.
- `Save` writes an owner-only (0o600) file in an owner-only (0o700) dir.

**`internal/engine`**
- `Pause`/`Resume` flip `Status.Paused` for a torrent built from an offline `.torrent`
  (existing engine tests already construct one via anacrolix `metainfo`).
- With a `QueuePath` pointing at a pre-seeded `queue.json` (one magnet or an httptest-served
  `.torrent` URL entry), `NewAnacrolix` restores it — `Statuses()` includes the torrent, and
  a `Paused:true` entry comes back paused.
- Adding a torrent writes an `Entry` to `queue.json`; `Remove` deletes it.

**`internal/ui`**
- `p` on a selected download calls `engine.Pause` (then `Resume` when already paused) — assert via `fakeEngine` recorded calls.
- A `Status{Paused:true}` download renders `paused` (assert `View()` contains it) and no speed.

## Files touched

- Create: `internal/queue/queue.go` + `internal/queue/queue_test.go`.
- Modify: `internal/engine/engine.go` (interface + `Status.Paused`), `internal/engine/anacrolix.go`
  (paused map, maxConns, Pause/Resume, store wiring, restore, internal add helpers) + `anacrolix_test.go`.
- Modify: `internal/ui/model.go` (`p` key handling, pause command), `internal/ui/view.go`
  (paused render + footer hint), `internal/ui/model_test.go` (`fakeEngine` Pause/Resume) + view/model tests.
- Modify: `cmd/shoal/main.go` (`QueuePath` in `engine.Config`).
