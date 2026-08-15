# OpenSubtitles subtitle download — design spec

Issue: #42 (subtask of #37; research in #41). Maintainer decisions (2026-08-16):
the API key is **entered by the user** in shoal Settings — no key ships in the
binary — and a **CLI command** is required alongside the TUI/daemon surfaces.

## Problem

After downloading a video, users want a matching `.srt` next to it. Research
(#41) picked OpenSubtitles.com's REST API (hash search + app-key auth built for
distributed clients; the old XML-RPC API is shut down) with moviehash-first
matching, since the file is on disk.

## Auth model (decided)

- v1 uses **only** a user-supplied API key (free from any OpenSubtitles.com
  account, under "API Consumers"). Stored in shoal's config like other
  settings; sent as the `Api-Key` header. No username/password fields.
- **Verification point:** if implementation reveals the `/download` endpoint
  refuses key-only (anonymous) access, surface that finding to the maintainer —
  do not silently add credential fields.

## Components

### 1. `internal/subtitles` package (new)

- `Hash(path string) (string, error)` — the OpenSubtitles moviehash: file size
  plus the little-endian uint64 words of the first and last 64 KiB, summed with
  wraparound, formatted `%016x`. Files smaller than 128 KiB return an error
  (protocol minimum).
- `Client` — REST client with injectable base URL and `http.Client` (httptest
  in tests). Required headers on every request: `Api-Key: <key>`,
  `User-Agent: shoal v<version>`. Two operations:
  - `Search(hash, query, lang string) ([]Result, error)` — GET
    `/subtitles?moviehash=<hash>&languages=<lang>&query=<query>`; a `Result`
    carries `FileID int64`, `FileName string`, `Language string`,
    `HashMatch bool` (from `attributes.moviehash_match`).
  - `Download(fileID int64) ([]byte, error)` — POST `/download` with
    `{"file_id": N}`, then GET the returned `link`.
  - HTTP 429 (either call) returns a typed `ErrRateLimited`; HTTP 401/403 a
    typed `ErrBadKey`.
- `Fetch(c *Client, videoPath, lang string) (srtPath string, err error)` —
  orchestration: hash the file (hash failure → fall back to query-only), search
  (query = the video's base name with separators cleaned), pick the best result
  (first `HashMatch`, else first result), download, write
  `<video-basename>.<lang>.srt` beside the video, return its path. No result →
  typed `ErrNotFound`.

### 2. Config (internal/config)

Three fields, following the existing pattern (JSON round-trip, defaults):
`OpenSubsAPIKey string` (default ""), `SubsLang string` (default "en"),
`SubsAuto bool` (default false).

### 3. Daemon-side auto-fetch (internal/engine)

When `SubsAuto` is on and a key is configured: on download completion (the
existing completion hook where history is recorded), fetch subtitles for every
file with a video extension (`.mkv .mp4 .avi .webm .mov .m4v`) of at least
100 MiB. Failures log and are otherwise silent — subtitles never block or fail
a download. Engine `Config` gains the three values (plumbed from main.go like
existing settings).

### 4. CLI: `shoal subs`

`shoal subs <id|prefix> [--lang <code>] [--files <glob>]` — resolves the
torrent (existing prefix helper), picks target files (glob via
`internal/glob`, else the default video-extension/size rule), fetches each,
prints each written `.srt` path on stdout. No API key configured → non-zero
exit, stderr message naming the Settings entry and config field. `--lang`
defaults to the configured `SubsLang`.

### 5. TUI Settings

A `SUBTITLES` group with three rows following existing setting-item patterns:
"OS API key" (text, rendered masked except last 4 chars), "Subs lang" (text),
"Auto subs" (on/off enum). Engine-applied note not needed: key/lang/auto are
read per fetch, so they apply live for the CLI; the daemon auto-hook picks up
changes on daemon restart like other engine settings.

## Testing

TDD. Hash: fixture files with known hashes (generate small synthetic files in
tests; 128 KiB minimum case; wraparound case). Client/Fetch: httptest servers
scripting success, hash-match preference, query fallback, 429, 401, no-result.
Engine hook: fake fetcher recording calls (interface seam so tests don't do
HTTP). CLI: fake engine + httptest. TUI: settings render/edit round-trip.

## Out of scope

Username/password login, multi-provider fallback, subtitle sync/muxing,
retry queues, TUI-side manual fetch keybind (CLI covers manual fetch),
bundled API keys anywhere.
