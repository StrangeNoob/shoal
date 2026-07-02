# shoal

**A calm BitTorrent client for your terminal.** Search torrents, download with live
progress, seed, and keep a history — in a fullscreen [Bubble Tea](https://github.com/charmbracelet/bubbletea) UI.

![shoal demo](demo.gif)

Built on a full BitTorrent engine ([anacrolix/torrent](https://github.com/anacrolix/torrent)
— DHT, magnets, web seeds, seeding), with a multi-source search layer and a fullscreen
Bubble Tea UI.

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

Default sources: the Internet Archive, a small open-media catalogue, and public
indexes — FitGirl, YTS, The Pirate Bay, 1337x, EZTV, SolidTorrents, Nyaa, and SubsPlease.

## Project layout

```
shoal/
├── bencode/            # .torrent / tracker bencoding
├── metainfo/           # parse a .torrent, compute the infohash
├── tracker/            # HTTP tracker announce
├── peer/               # peer handshake, wire messages, bitfield
├── download/           # concurrent verified piece downloader
├── internal/
│   ├── source/         # Source interface + default provider set
│   ├── engine/         # Engine interface (anacrolix/torrent backend)
│   ├── history/        # persisted download history
│   ├── config/         # persisted user settings
│   └── ui/             # the fullscreen Bubble Tea interface
└── cmd/
    ├── shoal/          # the TUI (main binary)
    └── shoal-classic/  # a standalone CLI downloader
```

The UI depends only on the `source.Source` and `engine.Engine` interfaces, so the engine
can be swapped without touching the UI. The core packages have offline unit tests.

## Development

```sh
make run        # build and launch the TUI
make test       # run the unit tests (offline)
make vet        # go vet
make fmt        # gofmt -w .
make classic    # build the CLI downloader (./shoal-classic)
make help       # all targets
```

## A note on use

BitTorrent itself is neutral infrastructure — Linux distributions, game patches, and
large open datasets are all distributed this way. What carries legal risk is the
*content* and the *indexing sites*, not the protocol. shoal can search general torrent
indexes by default; use it only for content you have the right to download and share.

## License

MIT — do what you like.
