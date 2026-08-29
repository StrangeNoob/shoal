# OpenSubtitles Subtitle Download Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fetch a matching `.srt` beside a downloaded video via the OpenSubtitles.com REST API, using a user-supplied API key — auto on completion (opt-in), `shoal subs` CLI, Settings entries.

**Architecture:** New pure-ish `internal/subtitles` package (moviehash + REST client + Fetch orchestration, all httptest-testable); three config fields; an engine completion hook behind a small fetcher interface; a CLI command mirroring `shoal files`; a Settings group.

**Tech Stack:** Go stdlib only (net/http, encoding/json); no new dependencies.

**Spec:** docs/superpowers/specs/2026-08-16-opensubtitles-design.md

## Global Constraints

- TDD: failing test first with RED evidence (command + output), then GREEN, in every task.
- `go test ./...`, `go vet ./...` clean, `gofmt -l .` prints nothing before each commit; conventional commit messages, NO attribution trailers.
- No bundled/hardcoded API key anywhere — not in code, not in tests' committed fixtures (tests use obviously-fake keys like `"test-key"`).
- No new dependencies; mirror named existing patterns; minimal diffs.
- All HTTP in tests goes through `httptest` — no real network in any test.
- Video-extension set, single source of truth: `.mkv .mp4 .avi .webm .mov .m4v`; auto/default file rule: video extension AND ≥ 100 MiB (`subsMinVideoBytes = int64(100) << 20`).

---

### Task 1: moviehash

**Files:**
- Create: `internal/subtitles/hash.go`
- Test: `internal/subtitles/hash_test.go`

**Interfaces:**
- Produces: `func Hash(path string) (string, error)` in package `subtitles`; `const hashChunkSize = 65536`; `var ErrTooSmall = errors.New("subtitles: file smaller than 128 KiB has no moviehash")`.

- [ ] **Step 1: Write the failing tests**

```go
package subtitles

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// writeFixture writes size bytes where each 8-byte word is its little-endian
// word index, so expected hashes are computable in the test itself.
func writeFixture(t *testing.T, size int64) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "v.mkv")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	buf := make([]byte, 8)
	for i := int64(0); i < size/8; i++ {
		binary.LittleEndian.PutUint64(buf, uint64(i))
		if _, err := f.Write(buf); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// expectedHash mirrors the spec: size + sum of LE words of first and last 64 KiB.
func expectedHash(size int64) uint64 {
	h := uint64(size)
	for i := int64(0); i < hashChunkSize/8; i++ { // first 64 KiB: words 0..8191
		h += uint64(i)
	}
	tailStart := (size - hashChunkSize) / 8
	for i := tailStart; i < tailStart+hashChunkSize/8; i++ { // last 64 KiB
		h += uint64(i)
	}
	return h
}

func TestHashKnownContent(t *testing.T) {
	size := int64(256 * 1024)
	path := writeFixture(t, size)
	got, err := Hash(path)
	if err != nil {
		t.Fatal(err)
	}
	want := expectedHash(size)
	if got != fmtHash(want) {
		t.Fatalf("Hash = %s, want %016x", got, want)
	}
	if len(got) != 16 {
		t.Fatalf("hash must be 16 hex chars, got %d", len(got))
	}
}

func TestHashOverlappingHeadTail(t *testing.T) {
	// 128 KiB exactly: head and tail are the same bytes, both still summed.
	size := int64(128 * 1024)
	path := writeFixture(t, size)
	got, err := Hash(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != fmtHash(expectedHash(size)) {
		t.Fatalf("Hash = %s, want %016x", got, expectedHash(size))
	}
}

func TestHashTooSmall(t *testing.T) {
	path := writeFixture(t, 64*1024)
	if _, err := Hash(path); err != ErrTooSmall {
		t.Fatalf("err = %v, want ErrTooSmall", err)
	}
}

func TestHashMissingFile(t *testing.T) {
	if _, err := Hash(filepath.Join(t.TempDir(), "nope.mkv")); err == nil {
		t.Fatal("missing file must error")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/subtitles/ -v`
Expected: FAIL — `undefined: Hash` / `hashChunkSize` / `ErrTooSmall` / `fmtHash`.

- [ ] **Step 3: Implement `internal/subtitles/hash.go`**

```go
// Package subtitles finds and downloads subtitles for downloaded videos via
// the OpenSubtitles.com REST API, matching by moviehash first (the file is on
// disk) with a filename query as fallback.
package subtitles

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

// hashChunkSize is the head/tail window the OpenSubtitles moviehash sums.
const hashChunkSize = 65536

// ErrTooSmall is returned for files under 128 KiB, which the protocol cannot hash.
var ErrTooSmall = errors.New("subtitles: file smaller than 128 KiB has no moviehash")

func fmtHash(h uint64) string { return fmt.Sprintf("%016x", h) }

// Hash computes the OpenSubtitles moviehash: file size plus the little-endian
// uint64 words of the first and last 64 KiB, summed with uint64 wraparound.
func Hash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return "", err
	}
	size := fi.Size()
	if size < 2*hashChunkSize {
		return "", ErrTooSmall
	}
	h := uint64(size)
	sum := func(off int64) error {
		buf := make([]byte, hashChunkSize)
		if _, err := f.Seek(off, io.SeekStart); err != nil {
			return err
		}
		if _, err := io.ReadFull(f, buf); err != nil {
			return err
		}
		for i := 0; i < hashChunkSize; i += 8 {
			h += binary.LittleEndian.Uint64(buf[i:])
		}
		return nil
	}
	if err := sum(0); err != nil {
		return "", err
	}
	if err := sum(size - hashChunkSize); err != nil {
		return "", err
	}
	return fmtHash(h), nil
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/subtitles/ -v` → PASS (4/4).

- [ ] **Step 5: Commit**

`git add internal/subtitles/ && git commit -m "feat: OpenSubtitles moviehash (head+tail+size, 128 KiB minimum)"`

---

### Task 2: REST client

**Files:**
- Create: `internal/subtitles/client.go`
- Test: `internal/subtitles/client_test.go`

**Interfaces:**
- Produces (Task 3 consumes):
  ```go
  type Result struct {
      FileID    int64
      FileName  string
      Language  string
      HashMatch bool
  }
  var ErrRateLimited = errors.New("subtitles: rate limited (429)")
  var ErrBadKey = errors.New("subtitles: API key rejected (401/403)")
  // NewClient(baseURL, apiKey, userAgent string) *Client; zero http.Client override via Client.HTTP.
  func NewClient(baseURL, apiKey, userAgent string) *Client
  func (c *Client) Search(hash, query, lang string) ([]Result, error)
  func (c *Client) Download(fileID int64) ([]byte, error)
  ```
- Wire contract the tests pin (verified against the OpenSubtitles REST docs at implementation time — if the live API shape differs, update BOTH client and fixtures and note it in the report):
  - `Search`: GET `{base}/subtitles?moviehash=<hash>&languages=<lang>&query=<url-escaped query>` (omit empty params); headers `Api-Key`, `User-Agent`. 200 body: `{"data":[{"attributes":{"language":"en","moviehash_match":true,"files":[{"file_id":123,"file_name":"X.srt"}]}}]}` — one Result per attributes.files[0] (skip entries with empty files).
  - `Download`: POST `{base}/download`, JSON body `{"file_id":123}`, same headers + `Content-Type: application/json`. 200 body: `{"link":"<absolute url>","file_name":"X.srt"}`; then GET link (no auth headers needed) and return its bytes.
  - Any 429 → `ErrRateLimited`; 401/403 → `ErrBadKey`; other non-200 → error containing the status.

- [ ] **Step 1: Failing tests** — httptest server asserting: request path/query/headers for Search (hash+lang+query, and hash-only when query is empty); parsing of the fixture JSON above into `[]Result` (HashMatch true/false); Download's POST body + follow-the-link flow returning the .srt bytes; 429 → `ErrRateLimited` (both endpoints); 401 → `ErrBadKey`; malformed JSON → error. Use `Api-Key: test-key`.
- [ ] **Step 2: Verify failure** (`go test ./internal/subtitles/`): undefined symbols.
- [ ] **Step 3: Implement** with stdlib only; `Client{baseURL, apiKey, userAgent string; HTTP *http.Client}` (nil HTTP → http.DefaultClient inside, so tests inject the httptest client or just use the test server URL).
- [ ] **Step 4: Verify pass**, then full `go test ./...`.
- [ ] **Step 5: Commit** — `feat: OpenSubtitles REST client (hash/query search, download, typed 429/401 errors)`.

---

### Task 3: Fetch orchestration + config fields

**Files:**
- Create: `internal/subtitles/fetch.go`
- Modify: `internal/config/config.go` (read it first; mirror existing field/default/round-trip patterns)
- Test: `internal/subtitles/fetch_test.go`, `internal/config/config_test.go`

**Interfaces:**
- Consumes: Task 1 `Hash`/`ErrTooSmall`; Task 2 `Client`/`Result`/typed errors.
- Produces (Tasks 4-5 consume):
  ```go
  var ErrNotFound = errors.New("subtitles: no matching subtitles found")
  // Fetch finds and writes the subtitle for videoPath, returning the .srt path.
  func Fetch(c *Client, videoPath, lang string) (string, error)
  ```
  Config fields: `OpenSubsAPIKey string` (json `opensubs_api_key`, default ""), `SubsLang string` (json `subs_lang`, default "en"), `SubsAuto bool` (json `subs_auto`, default false).
- Fetch behavior the tests pin: query = base name without extension, `.`/`_` separators replaced by spaces; hash the file — on `ErrTooSmall` (or any hash error) search query-only, otherwise hash+query; pick first `HashMatch` result, else first result; empty results → `ErrNotFound`; write bytes to `<videoPath minus ext>.<lang>.srt` (0644) and return that path; propagate `ErrRateLimited`/`ErrBadKey` unchanged.

- [ ] **Step 1: Failing tests.** Fetch: httptest-backed table — hash-match preferred over first; query-only fallback for a <128 KiB video; ErrNotFound on empty data; srt written beside video with `.en.srt` suffix and returned path correct; 429 propagates. Config: extend the existing defaults + round-trip tests for the three fields (read config_test.go first and match its style, including the XDG_CONFIG_HOME isolation the suite already does).
- [ ] **Step 2: Verify failures.**
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Verify pass** + full suite.
- [ ] **Step 5: Commit** — `feat: subtitle Fetch orchestration; OpenSubs config fields (key, lang, auto)`.

---

### Task 4: Engine auto-fetch on completion

**Files:**
- Modify: `internal/engine/engine.go` (Config), `internal/engine/anacrolix.go`
- Test: `internal/engine/anacrolix_test.go`

**Interfaces:**
- Consumes: Task 3 `subtitles.Fetch`/`NewClient` — but ONLY through a seam so tests never do HTTP:
  ```go
  // in anacrolix.go: subsFetch is swapped in tests.
  var subsFetch = func(apiKey, videoPath, lang string) (string, error) {
      c := subtitles.NewClient("https://api.opensubtitles.com/api/v1", apiKey, "shoal")
      return subtitles.Fetch(c, videoPath, lang)
  }
  ```
- Produces: `engine.Config` gains `OpenSubsAPIKey string`, `SubsLang string`, `SubsAuto bool` (one-line comments); main.go plumbs them from the user config where other engine.Config fields are filled (read main.go to find it).
- Behavior: in the existing download-completion hook (where history recording triggers — read anacrolix.go to locate it), if `SubsAuto && OpenSubsAPIKey != ""`: for each file of the completed torrent with a video extension and length ≥ `subsMinVideoBytes` (`const subsMinVideoBytes = int64(100) << 20`, define beside the hook; extension set from the Global Constraints), call `subsFetch` with the file's absolute on-disk path in a goroutine (one per completion, iterating files serially inside it); errors are logged with the engine's existing logging idiom and never affect torrent state. Fetch at most once per torrent per daemon run (guard map, engine-mutex-protected).

- [ ] **Step 1: Failing tests.** Swap `subsFetch` for a recorder in tests (restore with t.Cleanup). Completing a fake/synthetic torrent with (a) auto off → no calls; (b) auto on + key + one qualifying video file → exactly one call with the right absolute path and configured lang; (c) auto on, no key → no calls; (d) second completion signal for the same torrent → no second call. Follow the existing completion-hook test style in anacrolix_test.go (read how completion is simulated there — the history-reload tests do this).
- [ ] **Step 2: Verify failures.**
- [ ] **Step 3: Implement** (minimal: seam var, const, guard map, hook body, Config fields, main.go plumbing).
- [ ] **Step 4: Verify pass** + full suite + vet + gofmt.
- [ ] **Step 5: Commit** — `feat: opt-in auto subtitle fetch on download completion`.

---

### Task 5: CLI `shoal subs`

**Files:**
- Create: `cmd/shoal/cli_subs.go`
- Test: `cmd/shoal/cli_subs_test.go`
- Modify: `cmd/shoal/main.go` (registration — mirror how `files` registers)

**Interfaces:**
- Consumes: Task 3 `subtitles.Fetch`/`NewClient`/typed errors + config fields; the existing id/infohash-prefix resolution helper and daemon client acquisition used by `cli_files.go` (read it first); `internal/glob` for `--files`; Detail/Status for file paths.
- Produces: `shoal subs <id|prefix> [--lang <code>] [--files <glob>]`.
- Behavior the tests pin: no API key in config → exit non-zero, stderr mentions `Settings → OS API key` and `opensubs_api_key`; target files = `--files` glob matches, else video-extension + ≥100 MiB rule (reuse the same const/extension helper — export or duplicate minimally per package convention, note which in the report); each written path printed on its own stdout line; per-file failures print to stderr but the command continues (exit 0 if ≥1 succeeded, else 1); `--lang` overrides config `SubsLang`. Absolute path resolution: join from `Status.Path` and `FileDetail.Path` — **verification point:** read how Status.Path is built in anacrolix.go and pin the join in a test.
- Like Task 4, route the actual fetch through a package-level seam var in cli_subs.go so CLI tests use a recorder, not HTTP.

- [ ] **Step 1: Failing tests** (fake engine harness from fake_engine_test.go — read it first).
- [ ] **Step 2: Verify failures.**
- [ ] **Step 3: Implement** following cli_files.go structure.
- [ ] **Step 4: Verify pass** + full suite.
- [ ] **Step 5: Commit** — `feat: shoal subs — fetch subtitles for a downloaded torrent`.

---

### Task 6: TUI Settings group + docs

**Files:**
- Modify: `internal/ui/model.go` and/or wherever `settingItems()` lives (locate it; PR #38-era code has it near renderSettings), `README.md`
- Test: `internal/ui/view_test.go` or `model_test.go` (match where settings tests live)

**Interfaces:**
- Consumes: Task 3 config fields.
- Produces: Settings rows in a new `SUBTITLES` group: "OS API key" (kindText, rendered masked: all but the last 4 chars as `•`, empty shows `unset`), "Subs lang" (kindText), "Auto subs" (on/off enum) — mirror an existing text setting and an existing enum setting exactly (read settingItems and renderSettingValue first).

- [ ] **Step 1: Failing tests:** settings pane renders the SUBTITLES group and the three labels; editing the key round-trips into config; the rendered key value is masked (assert the literal `•` masking and absence of the full key string).
- [ ] **Step 2: Verify failures.**
- [ ] **Step 3: Implement** rows + masking helper.
- [ ] **Step 4: README:** document `shoal subs`, the three settings (where `files`/`download` are documented), a Shipped-roadmap bullet; note the user-supplied-key model explicitly ("bring your own free OpenSubtitles API key").
- [ ] **Step 5: Verify** full suite + vet + gofmt.
- [ ] **Step 6: Commit** — `feat: SUBTITLES settings group (masked key, lang, auto); document shoal subs`.
