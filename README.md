# shoal

**A calm BitTorrent client for your terminal.** Search torrents, download with live
progress, seed, and keep a history — in a fullscreen [Bubble Tea](https://github.com/charmbracelet/bubbletea) UI.

![shoal demo](demo.gif)

Built in two layers: a BitTorrent protocol implemented **by hand** from the Go standard
library (to *understand* how torrents work), and a **real, beautiful tool** on top of a
mature engine ([anacrolix/torrent](https://github.com/anacrolix/torrent) — DHT, magnets,
web seeds, seeding).

## Install

Requires **Go 1.24+**.

**Go install (recommended):**

```sh
go install github.com/StrangeNoob/shoal/cmd/shoal@latest
```

This drops a `shoal` binary in your `GOBIN` (`~/go/bin` by default — make sure it's on
your `PATH`). Then just run `shoal`.

**From source:**

```sh
git clone https://github.com/StrangeNoob/shoal
cd shoal
make install          # go install ./cmd/shoal  → $GOBIN/shoal
# or, to build a local binary without installing:
make build            # → ./shoal
go build -o shoal ./cmd/shoal
```

`go.mod` / `go.sum` are committed, so the build needs no setup step. (`make deps` only
exists to re-pin dependencies.)

Downloads land in `~/Downloads/shoal` by default — change it in **Settings → Save to**.

## Using the TUI

`shoal` opens fullscreen on the **Search** pane. The footer always shows the keys for
the current pane, and `?` opens the full list. `tab` cycles the four panes:
**Search · Downloads · Seeding · Settings**.

**Search**

| Key | Action |
| --- | --- |
| `/` | focus the search box; type a query and press `enter` |
| `↑ ↓` / `k j` | move the selection |
| `← →` / `h l` | narrow results by media type (All / Movies / TV / Anime / …) |
| `enter` | open the selected result's **details** |
| `d` | download the selected result |
| `S` | sort results (then `← →` pick a column — Size / Seeders / Leechers / Ratio — and `↑ ↓` set direction) |
| `tab` | next pane · `q` / `ctrl+c` quit |

Results stream in live from all sources (`searching… N/M sources`) as a sortable table.
Press `enter` for a **details** screen (size, health, files, hash, magnet) where `d`
downloads, `y` copies the magnet, and `esc` goes back. You can also paste a magnet link
into the search box and press `enter` to add it directly.

**Downloads** — live progress bar, transfer size, peers, and **download speed**. Select
a download with `↑ ↓` and press `x` to **cancel** it (a prompt lets you `k` keep the
partial files or `d` delete them; `esc` aborts).

**Seeding** — completed torrents you're still sharing (ratio, uploaded, **upload speed**),
followed by a **History** of everything you've downloaded (persisted across runs).

**Settings** — theme (Twilight / Tide), color mode, save location, seed ratio, max peers,
listen port. `↑ ↓` move, `← →` change an option, `enter` edits a text field.

Default sources: the Internet Archive, a small open-media catalogue, and the
torlink-derived providers — FitGirl, YTS, The Pirate Bay, 1337x, EZTV, SolidTorrents,
Nyaa, and SubsPlease.

## The hand-written core (Phase 1)

BitTorrent has no central server holding the file. Everyone sharing a file forms a
**swarm**, and you assemble the file from pieces handed to you by many peers at once:

1. **Read the `.torrent`** — a "bencoded" dictionary. Its nested `info` dictionary has
   the name, the fixed **piece length**, and the SHA-1 of every piece. The torrent's
   **infohash** is the SHA-1 of the raw `info` bytes. → `bencode/`, `metainfo/`.
2. **Ask a tracker for peers** — the phonebook: send the infohash, get back peer
   `IP:port` addresses. → `tracker/`.
3. **Handshake with a peer** — a fixed 68-byte greeting, then a tiny message protocol
   (`bitfield`, `interested`/`unchoke`, `request`, `piece`). → `peer/`.
4. **Download and verify pieces** — request 16 KiB blocks, reassemble, check each
   piece's SHA-1, spread the work across all peers, retry failures. → `download/`.
5. **Write to disk** — single file, or a directory tree for multi-file torrents.
   → `cmd/shoal-classic/`.

Build it standalone with `make classic && ./shoal-classic some.torrent .` (HTTP-tracker
torrents only — see [Phase 1 limitations](#phase-1-limitations)).

## How this maps to torlink

| Layer | torlink (Node) | shoal |
| --- | --- | --- |
| BitTorrent engine | `webtorrent` | Phase 1: **hand-written**; Phase 2: `anacrolix/torrent` |
| Search sources | `src/sources/*` | `internal/source` (Internet Archive + torlink-derived providers) |
| UI | Ink / React | `internal/ui` (Bubble Tea) |

## Project layout

```
shoal/
├── bencode/            # Phase 1: .torrent / tracker encoding
├── metainfo/           # Phase 1: parse a .torrent, compute the infohash
├── tracker/            # Phase 1: HTTP tracker announce
├── peer/               # Phase 1: handshake, wire messages, bitfield
├── download/           # Phase 1: concurrent verified piece downloader
├── internal/
│   ├── source/         # Phase 2: Source interface + default provider set
│   ├── engine/         # Phase 2: Engine interface + anacrolix backend
│   ├── history/        # Phase 2: persisted download history
│   ├── config/         # Phase 2: persisted user settings
│   └── ui/             # Phase 2: the fullscreen Bubble Tea interface
└── cmd/
    ├── shoal/          # Phase 2: the TUI (the product)
    └── shoal-classic/  # Phase 1: the hand-written CLI downloader
```

The UI depends only on the `source.Source` and `engine.Engine` interfaces, so the engine
can be swapped without touching the UI. The Phase 1 packages have tests that run offline.

## Development

```sh
make run        # build and launch the TUI
make test       # run the unit tests (offline)
make vet        # go vet
make fmt        # gofmt -w .
make classic    # build the hand-written CLI downloader (./shoal-classic)
make help       # all targets
```

### Regenerating the demo

`demo.gif` was recorded to `demo.cast` (asciinema) and rendered with
[agg](https://github.com/asciinema/agg):

```sh
agg demo.cast demo.gif
```

`demo.tape` scripts the same walkthrough for [vhs](https://github.com/charmbracelet/vhs)
as an alternative (`vhs demo.tape` writes `demo.gif`).

## Phase 1 limitations

The hand-written engine is a teaching tool and stays simple on purpose: HTTP trackers
only (no UDP), no DHT/PEX, no magnet links, expects a peer's opening `bitfield`, no
completion timeout, and download-only (never seeds). Phase 2 (anacrolix) has none of
these limits — it's there precisely so the real tool is robust while the hand-written
code stays readable.

## A note on use

BitTorrent itself is neutral infrastructure — Linux distributions, game patches, and
large open datasets are all distributed this way. What carries legal risk is the
*content* and the *indexing sites*, not the protocol. shoal can search general torrent
indexes by default; use it only for content you have the right to download and share.

## License

MIT — do what you like.
