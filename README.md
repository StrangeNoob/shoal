# shoal

**A calm BitTorrent client for your terminal.** Search torrents, download with live
progress, seed, and keep a history — in a fullscreen [Bubble Tea](https://github.com/charmbracelet/bubbletea) UI.

![shoal demo](demo.gif)

Built on a full BitTorrent engine ([anacrolix/torrent](https://github.com/anacrolix/torrent)
— DHT, magnets, web seeds, seeding), with a multi-source search layer and a fullscreen
Bubble Tea UI.

## Install

**Prebuilt binaries (GitHub Releases) — no Go toolchain needed.** Download the archive
for your platform from the [Releases page](https://github.com/StrangeNoob/shoal/releases/latest)
— assets are named `shoal_<version>_<os>_<arch>.tar.gz` (`.zip` on Windows), where `<os>`
is `linux`/`darwin`/`windows` and `<arch>` is `amd64`/`arm64` — unpack it and put the
`shoal` binary on your `PATH`:

```sh
tar -xzf shoal_0.2.0_darwin_arm64.tar.gz     # pick the asset for your OS/arch
sudo mv shoal /usr/local/bin/                # or anywhere on your $PATH
shoal
```

Or with the GitHub CLI (edit the pattern for your OS/arch):

```sh
gh release download --repo StrangeNoob/shoal --pattern 'shoal_*_darwin_arm64.tar.gz'
```

Each release also ships a `checksums.txt` to verify the download.

**Go install** (needs Go 1.24+):

```sh
go install github.com/StrangeNoob/shoal/cmd/shoal@latest
```

This drops a `shoal` binary in your `GOBIN` (`~/go/bin` by default — make sure it's on
your `PATH`). Then just run `shoal`.

**From source** (needs Go 1.24+):

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
listen port, and auto-update. `↑ ↓` move, `← →` change an option, `enter` edits a text field.
A **SOURCES** group lists every provider with an on/off toggle (`← →` to change);
turning one off removes it from searches immediately, and the change is shared with
the `shoal sources` CLI.

Default sources: the Internet Archive, a small open-media catalogue, and public
indexes — FitGirl, YTS, The Pirate Bay, 1337x, EZTV, SolidTorrents, Nyaa, and SubsPlease.
(EZTV has no keyword-search API, so it only appears in empty-query browse, not in a
keyword search.)

## Command line (scripting)

Besides the fullscreen TUI, `shoal` has non-interactive subcommands so a script — or an
AI agent — can search and download without the UI. Run `shoal` with no arguments for the
TUI; with a subcommand for scripting:

```sh
shoal sources                        # list providers with on/off state (add --json)
shoal sources enable  <name>         # turn a provider on
shoal sources disable <name>         # turn a provider off
shoal search "big buck bunny"        # search every source (add --json for scripts)
shoal download <magnet|url|infohash|id>   # download in the background
shoal status [id]                    # progress of background downloads (--json, --clear)
shoal daemon status                  # show daemon status, uptime, and torrent counts
shoal daemon stop                    # stop the shared daemon
```

- **`sources`** lists all available providers with their on/off state (enable `--json` for
  machines); `enable <name>` and `disable <name>` toggle a provider. Toggles are shared
  with the TUI (stored in `config.json`) and take effect on the next search — no restart.
  A one-shot `--source <name>` still searches a provider even if it's disabled.
- **`search`** queries all sources concurrently and prints a table (a short id per row),
  or a JSON array with the full magnet for each result via `--json`. Narrow to one
  provider with `--source <name>`, cap results with `--limit <N>`.
- **`download`** accepts a magnet, a `.torrent` URL, a 40-char infohash, or a short id
  from your last `search`. It starts the download **in the background** and returns
  immediately with a handle; files land in shoal's configured folder (Settings → Save to, or `config.json`).
  CLI downloads run in a shared background `shoal daemon` (started automatically on the first `download`),
  so multiple downloads and `shoal status` all share one engine and one download folder.
  The TUI runs on the same shared `shoal daemon` as the CLI, so downloads and seeding stay in sync
  between them. Engine settings (listen port, max peers, save-to, seed) configure the daemon and
  take effect when it restarts. (The daemon-backed TUI is unix/macOS only for now; Windows support
  is planned.)
- **`status`** reports each background download as `downloading | done | seeding | paused`;
  `--clear` prunes finished (done) torrents, keeping files.
- **`daemon status`** — show whether the shared daemon is running, its uptime, and torrent counts.
- **`daemon stop`** — stop the shared daemon (it also stops on its own when idle).

The daemon auto-starts on the first `download` or when the TUI launches. If the
daemon stops or crashes while the TUI is open, the TUI shows a brief
"reconnecting…" indicator and automatically restarts it, so downloads resume.
(To fully stop the daemon, close the TUI first, then `shoal daemon stop`.)

The daemon also **auto-stops when it's been empty (no torrents) with no connected
clients for `daemon_idle_minutes` (default 10; set `0` in `config.json` to
disable)**. A torrent or any connected client keeps it running.

### Claude Code skill

This repo ships a [Claude Code](https://claude.com/claude-code) skill
(`.claude/skills/shoal-download/`) that teaches Claude to drive those commands — so you
can just say *"find and download Big Buck Bunny"* and it searches, picks the best match,
downloads in the background, and reports progress.

**No registration or config is required** — Claude Code discovers skills from the
filesystem. Two ways to use it:

- **Inside this repo:** run `claude` from a shoal checkout and the project-scoped skill in
  `.claude/skills/` loads automatically — nothing to install.
- **Everywhere (recommended):** with the `shoal` binary on your `PATH` (see
  [Install](#install)), run:

  ```sh
  shoal skill install      # → ~/.claude/skills/shoal-download/SKILL.md
  ```

  The skill is embedded in the binary, so this works offline and installs the version that
  matches your `shoal`. Restart Claude Code once (a brand-new skills directory is picked up
  on next launch), then ask it to find and download something. Re-run with `--force` after a
  `shoal update` to refresh the installed skill.

The skill is plain Markdown with `name`/`description` frontmatter: Claude loads that
summary at startup and reads the full instructions on demand when your request matches.
See the [Claude Code skills docs](https://code.claude.com/docs/en/skills) for the full
mechanics.

## Updating

shoal can update itself from GitHub Releases:

```sh
shoal update     # fetch the latest release, verify its checksum, replace the binary
shoal version    # print the installed version
```

`shoal update` downloads the archive for your OS/arch, checks its SHA-256 against the
release's `checksums.txt`, and swaps the binary in place (restart to use it). On release
builds, a launch-time check also shows a `↑ vX available` hint in the header. Turn on
**Settings → Auto-update** to have shoal apply new releases automatically on launch.

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

## Roadmap

Shipped: CI on every push/PR, tag-triggered GoReleaser releases, and self-update
(`shoal update` + opt-in Auto-update). Still planned — contributions welcome:

- **Pause / resume downloads** and **persist the active download queue** across restarts.
- **More sources** behind the existing `source.Source` interface, each toggleable in Settings.
- **Homebrew tap** as an additional install channel.

## A note on use

BitTorrent itself is neutral infrastructure — Linux distributions, game patches, and
large open datasets are all distributed this way. What carries legal risk is the
*content* and the *indexing sites*, not the protocol. shoal can search general torrent
indexes by default; use it only for content you have the right to download and share.

## License

MIT — do what you like.
