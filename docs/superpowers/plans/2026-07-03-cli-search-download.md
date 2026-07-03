# Non-interactive CLI (search / download / status) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `shoal search`, `shoal download`, and `shoal status` subcommands (plus a Claude skill) so a script can find, download, and track a single title without the interactive TUI.

**Architecture:** Extend the existing `cli()` dispatcher in `cmd/shoal/main.go`. `search` reuses `internal/source`; `download` resolves its argument then spawns a **detached copy of itself** (`download --worker`) that drives `internal/engine` and writes progress to a per-download JSON file; `status` is a stateless reader of those files. No daemon.

**Tech Stack:** Go, stdlib `flag`/`os/exec`/`encoding/json`, `anacrolix/torrent` (via the existing `internal/engine`), Bubble Tea untouched.

## Global Constraints

- Language: Go; only stdlib + already-vendored deps — **no new module dependencies**.
- TDD: write the failing test first (project rule: [[use-tdd-on-shoal]]).
- Commits: **no Claude attribution** — no `Co-Authored-By`, no "Generated with" trailer.
- All state lives under the existing shoal config dir: `filepath.Join(os.UserConfigDir(), "shoal", …)`.
- The CLI download engine uses `Seed=false`, `SeedRatio=0`, `QueuePath=""` (never touches the TUI's queue and always exits at 100%).
- Diagnostics → stderr; machine output (results/JSON) → stdout.
- `cmd/shoal-classic` is **not** modified.
- Tests never hit the network or spawn real processes: inject a fake `source.Source` / `engine.Engine`, and point state paths at `t.TempDir()`.

---

### Task 1: Download-target resolution

Pure function that turns a user argument (magnet / URL / infohash / short id / garbage) into a resolved target with an 8-char handle. No I/O, no network.

**Files:**
- Create: `cmd/shoal/cli_download.go`
- Test: `cmd/shoal/cli_download_test.go`

**Interfaces:**
- Consumes: `source.ParseMagnetInfoHash(magnet string) string` (existing).
- Produces:
  - `type dlTarget struct { Magnet string; URL string; Handle string }`
  - `func resolveTarget(arg string, lookup func(id string) (string, bool)) (dlTarget, error)`

- [ ] **Step 1: Write the failing test**

```go
// cmd/shoal/cli_download_test.go
package main

import "testing"

func TestResolveTarget(t *testing.T) {
	lookup := func(id string) (string, bool) {
		if id == "a1b2c3d4" {
			return "magnet:?xt=urn:btih:a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4&dn=Cached", true
		}
		return "", false
	}
	const ih = "0123456789abcdef0123456789abcdef01234567"
	cases := []struct {
		name       string
		arg        string
		wantMagnet string
		wantURL    string
		wantHandle string
		wantErr    bool
	}{
		{"magnet", "magnet:?xt=urn:btih:" + ih + "&dn=X", "magnet:?xt=urn:btih:" + ih + "&dn=X", "", "01234567", false},
		{"http url", "https://example.com/x.torrent", "", "https://example.com/x.torrent", "", false},
		{"infohash40", ih, "magnet:?xt=urn:btih:" + ih, "", "01234567", false},
		{"short id hit", "a1b2c3d4", "magnet:?xt=urn:btih:a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4&dn=Cached", "", "a1b2c3d4", false},
		{"short id miss", "deadbeef", "", "", "", true},
		{"garbage", "not a torrent", "", "", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := resolveTarget(c.arg, lookup)
			if c.wantErr {
				if err == nil {
					t.Fatalf("want error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Magnet != c.wantMagnet || got.URL != c.wantURL {
				t.Errorf("target = %+v, want magnet=%q url=%q", got, c.wantMagnet, c.wantURL)
			}
			if c.name == "http url" {
				if len(got.Handle) != 8 {
					t.Errorf("url handle = %q, want 8 hex chars", got.Handle)
				}
			} else if got.Handle != c.wantHandle {
				t.Errorf("handle = %q, want %q", got.Handle, c.wantHandle)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/shoal/ -run TestResolveTarget -v`
Expected: FAIL — `undefined: resolveTarget` (compile error).

- [ ] **Step 3: Write minimal implementation**

```go
// cmd/shoal/cli_download.go
package main

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"github.com/StrangeNoob/shoal/internal/source"
)

var (
	hex40RE = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
	hex8RE  = regexp.MustCompile(`^[0-9a-fA-F]{8}$`)
)

// dlTarget is a resolved download: a magnet or a .torrent URL, plus the handle
// used as the state-file id.
type dlTarget struct {
	Magnet string // set for magnet / hash / short-id
	URL    string // set for a .torrent URL
	Handle string // 8-hex state-file id
}

// resolveTarget turns a user argument into a download target. lookup resolves a
// short id to a magnet; pass nil to disable short-id resolution.
func resolveTarget(arg string, lookup func(id string) (string, bool)) (dlTarget, error) {
	s := strings.TrimSpace(arg)
	switch {
	case strings.HasPrefix(strings.ToLower(s), "magnet:"):
		ih := source.ParseMagnetInfoHash(s)
		if ih == "" {
			return dlTarget{}, fmt.Errorf("magnet has no infohash: %s", s)
		}
		return dlTarget{Magnet: s, Handle: ih[:8]}, nil
	case strings.HasPrefix(s, "http://"), strings.HasPrefix(s, "https://"):
		sum := sha1.Sum([]byte(s))
		return dlTarget{URL: s, Handle: hex.EncodeToString(sum[:])[:8]}, nil
	case hex40RE.MatchString(s):
		ih := strings.ToLower(s)
		return dlTarget{Magnet: "magnet:?xt=urn:btih:" + ih, Handle: ih[:8]}, nil
	case hex8RE.MatchString(s):
		id := strings.ToLower(s)
		if lookup != nil {
			if magnet, ok := lookup(id); ok {
				return dlTarget{Magnet: magnet, Handle: id}, nil
			}
		}
		return dlTarget{}, fmt.Errorf("no recent search contains id %s; run `shoal search` first", id)
	default:
		return dlTarget{}, fmt.Errorf("unrecognized download target: %s", s)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/shoal/ -run TestResolveTarget -v`
Expected: PASS (all subtests).

- [ ] **Step 5: Commit**

```bash
git add cmd/shoal/cli_download.go cmd/shoal/cli_download_test.go
git commit -m "Add download-target resolution (magnet/url/hash/short-id)"
```

---

### Task 2: State + short-id cache files

The per-download `active/<id>.json` store and the `last-search.json` short-id cache. All functions take a `base` dir so tests point at a temp dir.

**Files:**
- Create: `cmd/shoal/state.go`
- Test: `cmd/shoal/state_test.go`

**Interfaces:**
- Produces:
  - `type Active struct { … }` (fields below)
  - `type cacheEntry struct { Magnet, TorrentURL, Title string }`
  - `func writeActive(base string, a Active) error`
  - `func readActive(base, id string) (Active, error)`
  - `func listActive(base string) ([]Active, error)`
  - `func clearFinished(base string) (int, error)`
  - `func writeCache(base string, m map[string]cacheEntry) error`
  - `func lookupCache(base, id string) (cacheEntry, bool)`
  - `func configDir() string`

- [ ] **Step 1: Write the failing test**

```go
// cmd/shoal/state_test.go
package main

import "testing"

func TestActiveRoundTripAndClear(t *testing.T) {
	base := t.TempDir()
	if err := writeActive(base, Active{ID: "aaaa1111", Name: "One", Total: 100, Completed: 50}); err != nil {
		t.Fatal(err)
	}
	if err := writeActive(base, Active{ID: "bbbb2222", Name: "Two", Total: 100, Completed: 100, Done: true}); err != nil {
		t.Fatal(err)
	}
	got, err := readActive(base, "aaaa1111")
	if err != nil || got.Name != "One" || got.Completed != 50 {
		t.Fatalf("readActive = %+v, err %v", got, err)
	}
	items, err := listActive(base)
	if err != nil || len(items) != 2 {
		t.Fatalf("listActive len = %d, err %v", len(items), err)
	}
	n, err := clearFinished(base)
	if err != nil || n != 1 {
		t.Fatalf("clearFinished removed %d, err %v", n, err)
	}
	items, _ = listActive(base)
	if len(items) != 1 || items[0].ID != "aaaa1111" {
		t.Fatalf("after clear = %+v", items)
	}
}

func TestCacheRoundTripAndMiss(t *testing.T) {
	base := t.TempDir()
	if _, ok := lookupCache(base, "nope0000"); ok {
		t.Fatal("expected miss on empty cache")
	}
	m := map[string]cacheEntry{"01234567": {Magnet: "magnet:?xt=urn:btih:x", Title: "Movie"}}
	if err := writeCache(base, m); err != nil {
		t.Fatal(err)
	}
	e, ok := lookupCache(base, "01234567")
	if !ok || e.Magnet != "magnet:?xt=urn:btih:x" || e.Title != "Movie" {
		t.Fatalf("lookupCache = %+v, ok %v", e, ok)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/shoal/ -run 'TestActiveRoundTripAndClear|TestCacheRoundTripAndMiss' -v`
Expected: FAIL — `undefined: writeActive` (compile error).

- [ ] **Step 3: Write minimal implementation**

```go
// cmd/shoal/state.go
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Active is one background download's live state — one JSON file per download,
// written only by that download's worker (no locking).
type Active struct {
	ID        string    `json:"id"`
	InfoHash  string    `json:"info_hash"`
	Name      string    `json:"name"`
	Total     int64     `json:"total"`
	Completed int64     `json:"completed"`
	Peers     int       `json:"peers"`
	Seeding   bool      `json:"seeding"`
	Done      bool      `json:"done"`
	Error     string    `json:"error,omitempty"`
	Path      string    `json:"path,omitempty"`
	Out       string    `json:"out"`
	Pid       int       `json:"pid"`
	UpdatedAt time.Time `json:"updated_at"`
}

// cacheEntry is what `download <short-id>` needs to start a prior search hit.
type cacheEntry struct {
	Magnet     string `json:"magnet"`
	TorrentURL string `json:"torrent_url"`
	Title      string `json:"title"`
}

func activeDir(base string) string { return filepath.Join(base, "active") }
func cachePath(base string) string { return filepath.Join(base, "last-search.json") }

// configDir is the shoal config dir (…/shoal), base for all state files.
func configDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "shoal")
}

func writeActive(base string, a Active) error {
	dir := activeDir(base)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, a.ID+".json"), b, 0o600)
}

func readActive(base, id string) (Active, error) {
	var a Active
	b, err := os.ReadFile(filepath.Join(activeDir(base), id+".json"))
	if err != nil {
		return a, err
	}
	err = json.Unmarshal(b, &a)
	return a, err
}

func listActive(base string) ([]Active, error) {
	ents, err := os.ReadDir(activeDir(base))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Active
	for _, e := range ents {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(activeDir(base), e.Name()))
		if err != nil {
			continue
		}
		var a Active
		if json.Unmarshal(b, &a) == nil {
			out = append(out, a)
		}
	}
	return out, nil
}

// clearFinished deletes done/errored entries; returns how many were removed.
func clearFinished(base string) (int, error) {
	items, err := listActive(base)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, a := range items {
		if a.Done || a.Error != "" {
			if os.Remove(filepath.Join(activeDir(base), a.ID+".json")) == nil {
				n++
			}
		}
	}
	return n, nil
}

func writeCache(base string, m map[string]cacheEntry) error {
	if err := os.MkdirAll(base, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(cachePath(base), b, 0o600)
}

func lookupCache(base, id string) (cacheEntry, bool) {
	b, err := os.ReadFile(cachePath(base))
	if err != nil {
		return cacheEntry{}, false
	}
	var m map[string]cacheEntry
	if json.Unmarshal(b, &m) != nil {
		return cacheEntry{}, false
	}
	e, ok := m[id]
	return e, ok
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/shoal/ -run 'TestActiveRoundTripAndClear|TestCacheRoundTripAndMiss' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/shoal/state.go cmd/shoal/state_test.go
git commit -m "Add per-download state files and short-id search cache"
```

---

### Task 3: `shoal search`

Search core (testable with a fake source) + row shaping + cache write + output. Also exposes `source.DefaultSources()` so `--source` can filter.

**Files:**
- Modify: `internal/source/default.go` (add `DefaultSources`, keep `NewDefault`)
- Create: `cmd/shoal/cli_search.go`
- Create: `cmd/shoal/cli_util.go` (shared `humanBytes`)
- Test: `cmd/shoal/cli_search_test.go`

**Interfaces:**
- Consumes: `source.Source` interface, `source.NewMulti(...Source) *MultiSource`, `source.Result`, `source.ParseMagnetInfoHash`; `writeCache` (Task 2).
- Produces:
  - `type searchRow struct { … }` (JSON-tagged, below)
  - `func toRows(results []source.Result, limit int) []searchRow`
  - `func rowsToCache(rows []searchRow) map[string]cacheEntry`
  - `func searchCore(ctx context.Context, src source.Source, query string, limit int, base string) ([]searchRow, error)`
  - `func humanBytes(n int64) string`
  - `source.DefaultSources() []Source`

- [ ] **Step 1: Write the failing test**

```go
// cmd/shoal/cli_search_test.go
package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/StrangeNoob/shoal/internal/source"
)

type fakeSource struct{ res []source.Result }

func (f fakeSource) Name() string { return "fake" }
func (f fakeSource) Search(context.Context, string) ([]source.Result, error) { return f.res, nil }

func TestToRowsSortsAndDerivesID(t *testing.T) {
	const ih = "0123456789abcdef0123456789abcdef01234567"
	in := []source.Result{
		{Title: "Low", Seeders: 2, Magnet: "magnet:?xt=urn:btih:" + ih},
		{Title: "High", Seeders: 40, Magnet: "magnet:?xt=urn:btih:" + ih},
		{Title: "NoMagnet", Seeders: 99}, // no magnet -> id "—"
	}
	rows := toRows(in, 30)
	if rows[0].Title != "High" {
		t.Errorf("expected most-seeded first, got %q", rows[0].Title)
	}
	if rows[0].ID != "01234567" {
		t.Errorf("id = %q, want 01234567", rows[0].ID)
	}
	var noMag searchRow
	for _, r := range rows {
		if r.Title == "NoMagnet" {
			noMag = r
		}
	}
	if noMag.ID != "—" {
		t.Errorf("magnet-less id = %q, want —", noMag.ID)
	}
	if got := toRows(in, 1); len(got) != 1 {
		t.Errorf("limit not applied: len %d", len(got))
	}
}

func TestSearchCoreWritesCacheAndJSON(t *testing.T) {
	base := t.TempDir()
	const ih = "abcdef0123456789abcdef0123456789abcdef01"
	src := fakeSource{res: []source.Result{
		{Title: "Movie", Seeders: 10, SizeBytes: 1024, Source: "fake", Magnet: "magnet:?xt=urn:btih:" + ih},
	}}
	rows, err := searchCore(context.Background(), src, "movie", 30, base)
	if err != nil || len(rows) != 1 {
		t.Fatalf("searchCore rows %d err %v", len(rows), err)
	}
	// cache resolvable by the derived id
	e, ok := lookupCache(base, "abcdef01")
	if !ok || e.Title != "Movie" {
		t.Fatalf("cache miss: %+v ok %v", e, ok)
	}
	// JSON shape round-trips
	b, _ := json.Marshal(rows)
	var back []map[string]any
	if json.Unmarshal(b, &back) != nil || back[0]["magnet"] == "" {
		t.Fatalf("bad json: %s", b)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/shoal/ -run 'TestToRows|TestSearchCore' -v`
Expected: FAIL — `undefined: toRows` / `searchCore`.

- [ ] **Step 3: Write minimal implementation**

First expose the default source slice:

```go
// internal/source/default.go — replace NewDefault with this pair
package source

// NewTorlinkSources returns the full source set ported from torlink.
func NewTorlinkSources() []Source {
	return []Source{
		NewFitGirl(),
		NewYTS(),
		NewPirateBayMovies(),
		New1337xMovies(),
		NewEZTV(),
		NewSolidTorrents(),
		NewPirateBayTV(),
		New1337xTV(),
		NewNyaa(),
		NewSubsPlease(),
	}
}

// DefaultSources returns shoal's default provider set (archive + curated + torlink).
func DefaultSources() []Source {
	sources := []Source{NewArchive(), NewCurated()}
	return append(sources, NewTorlinkSources()...)
}

// NewDefault returns shoal's default multi-source catalogue.
func NewDefault() *MultiSource { return NewMulti(DefaultSources()...) }
```

Shared formatter:

```go
// cmd/shoal/cli_util.go
package main

import "fmt"

// humanBytes renders a byte count like "723.0 MiB".
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
```

Search command:

```go
// cmd/shoal/cli_search.go
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/StrangeNoob/shoal/internal/source"
)

type searchRow struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	SizeBytes  int64  `json:"size_bytes"`
	Seeders    int64  `json:"seeders"`
	Leechers   int64  `json:"leechers"`
	Source     string `json:"source"`
	Category   string `json:"category"`
	Magnet     string `json:"magnet"`
	TorrentURL string `json:"torrent_url"`
}

// toRows sorts best-first (seeders, then popularity), caps at limit, and derives
// each row's short id from its magnet infohash ("—" when there is none).
func toRows(results []source.Result, limit int) []searchRow {
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Seeders != results[j].Seeders {
			return results[i].Seeders > results[j].Seeders
		}
		return results[i].Popularity > results[j].Popularity
	})
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	rows := make([]searchRow, 0, len(results))
	for _, r := range results {
		id := "—"
		if ih := source.ParseMagnetInfoHash(r.Magnet); ih != "" {
			id = ih[:8]
		}
		rows = append(rows, searchRow{
			ID: id, Title: r.Title, SizeBytes: r.SizeBytes, Seeders: r.Seeders,
			Leechers: r.Leechers, Source: r.Source, Category: r.Category,
			Magnet: r.Magnet, TorrentURL: r.TorrentURL,
		})
	}
	return rows
}

// rowsToCache builds the short-id cache (skips magnet-less "—" rows).
func rowsToCache(rows []searchRow) map[string]cacheEntry {
	m := map[string]cacheEntry{}
	for _, r := range rows {
		if r.ID == "—" {
			continue
		}
		m[r.ID] = cacheEntry{Magnet: r.Magnet, TorrentURL: r.TorrentURL, Title: r.Title}
	}
	return m
}

// searchCore runs a search, shapes rows, and writes the short-id cache.
func searchCore(ctx context.Context, src source.Source, query string, limit int, base string) ([]searchRow, error) {
	results, err := src.Search(ctx, query)
	if err != nil {
		return nil, err
	}
	rows := toRows(results, limit)
	_ = writeCache(base, rowsToCache(rows))
	return rows, nil
}

func filterSources(srcs []source.Source, name string) []source.Source {
	var out []source.Source
	for _, s := range srcs {
		if strings.Contains(strings.ToLower(s.Name()), strings.ToLower(name)) {
			out = append(out, s)
		}
	}
	return out
}

func sourceNames(srcs []source.Source) []string {
	names := make([]string, len(srcs))
	for i, s := range srcs {
		names[i] = s.Name()
	}
	return names
}

func runSearch(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit JSON")
	srcName := fs.String("source", "", "limit to sources matching name")
	limit := fs.Int("limit", 30, "max results")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	query := strings.Join(fs.Args(), " ")
	if query == "" {
		fmt.Fprintln(os.Stderr, `usage: shoal search "<query>" [--json] [--source name] [--limit N]`)
		return 2
	}

	srcs := source.DefaultSources()
	if *srcName != "" {
		srcs = filterSources(srcs, *srcName)
		if len(srcs) == 0 {
			fmt.Fprintf(os.Stderr, "no source matches %q; available: %s\n",
				*srcName, strings.Join(sourceNames(source.DefaultSources()), ", "))
			return 1
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rows, err := searchCore(ctx, source.NewMulti(srcs...), query, *limit, configDir())
	if err != nil {
		fmt.Fprintln(os.Stderr, "search failed:", err)
		return 1
	}
	printSearch(out, rows, *jsonOut)
	return 0
}

func printSearch(out io.Writer, rows []searchRow, asJSON bool) {
	if asJSON {
		b, _ := json.MarshalIndent(rows, "", "  ")
		fmt.Fprintln(out, string(b))
		return
	}
	if len(rows) == 0 {
		fmt.Fprintln(out, "no results")
		return
	}
	for _, r := range rows {
		fmt.Fprintf(out, "%-8s  %-50.50s  %10s  s:%-5d  %s\n",
			r.ID, r.Title, humanBytes(r.SizeBytes), r.Seeders, r.Source)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/shoal/ ./internal/source/ -run 'TestToRows|TestSearchCore' -v` then `go build ./...`
Expected: PASS; build clean.

- [ ] **Step 5: Commit**

```bash
git add internal/source/default.go cmd/shoal/cli_search.go cmd/shoal/cli_util.go cmd/shoal/cli_search_test.go
git commit -m "Add shoal search subcommand (table + --json + short-id cache)"
```

---

### Task 4: Download worker loop

The engine-driving loop the detached worker runs: poll `Statuses()`, write the active file each step, record history on completion. Tested with a fake `engine.Engine` — deterministic, no network.

**Files:**
- Modify: `cmd/shoal/cli_download.go` (append worker functions)
- Test: `cmd/shoal/cli_download_test.go` (append)

**Interfaces:**
- Consumes: `engine.Engine` (interface), `engine.Status`, `history.Store.Append`, `writeActive` (Task 2).
- Produces:
  - `func firstStatus(ss []engine.Status) *engine.Status`
  - `func stepWorker(eng engine.Engine, base string, a *Active) (done bool)`
  - `func runWorker(eng engine.Engine, base string, a Active, hist *history.Store, interval time.Duration)`

- [ ] **Step 1: Write the failing test**

```go
// append to cmd/shoal/cli_download_test.go
import (
	"testing"
	"time"

	"github.com/StrangeNoob/shoal/internal/engine"
	"github.com/StrangeNoob/shoal/internal/history"
)

type fakeEngine struct {
	frames [][]engine.Status
	i      int
}

func (f *fakeEngine) AddTorrentURL(string, string) error { return nil }
func (f *fakeEngine) AddMagnet(string) error             { return nil }
func (f *fakeEngine) Remove(string, bool) error          { return nil }
func (f *fakeEngine) Pause(string) error                 { return nil }
func (f *fakeEngine) Resume(string) error                { return nil }
func (f *fakeEngine) Close() error                       { return nil }
func (f *fakeEngine) Statuses() []engine.Status {
	if f.i < len(f.frames) {
		s := f.frames[f.i]
		f.i++
		return s
	}
	if len(f.frames) == 0 {
		return nil
	}
	return f.frames[len(f.frames)-1]
}

func TestStepWorkerProgressAndDone(t *testing.T) {
	base := t.TempDir()
	eng := &fakeEngine{frames: [][]engine.Status{
		{{InfoHash: "ffff0000", Name: "Movie", TotalBytes: 100, CompletedBytes: 40, Peers: 3}},
		{{InfoHash: "ffff0000", Name: "Movie", TotalBytes: 100, CompletedBytes: 100, Done: true, Path: "/data/Movie"}},
	}}
	a := Active{ID: "ffff0000"}
	if done := stepWorker(eng, base, &a); done {
		t.Fatal("frame 1 should not be done")
	}
	got, _ := readActive(base, "ffff0000")
	if got.Completed != 40 || got.Name != "Movie" || got.Peers != 3 {
		t.Fatalf("mid-progress not written: %+v", got)
	}
	if done := stepWorker(eng, base, &a); !done {
		t.Fatal("frame 2 should be done")
	}
	got, _ = readActive(base, "ffff0000")
	if !got.Done || got.Path != "/data/Movie" {
		t.Fatalf("done state not written: %+v", got)
	}
}

func TestRunWorkerRecordsHistory(t *testing.T) {
	base := t.TempDir()
	eng := &fakeEngine{frames: [][]engine.Status{
		{{InfoHash: "eeee1111", Name: "Done", TotalBytes: 10, CompletedBytes: 10, Done: true, Path: "/d/Done"}},
	}}
	hist := history.Store{Path: base + "/history.json"}
	runWorker(eng, base, Active{ID: "eeee1111"}, &hist, time.Millisecond)
	if len(hist.Entries) != 1 || hist.Entries[0].Name != "Done" || hist.Entries[0].Path != "/d/Done" {
		t.Fatalf("history not recorded: %+v", hist.Entries)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/shoal/ -run 'TestStepWorker|TestRunWorker' -v`
Expected: FAIL — `undefined: stepWorker`.

- [ ] **Step 3: Write minimal implementation**

```go
// append to cmd/shoal/cli_download.go
import (
	"time"

	"github.com/StrangeNoob/shoal/internal/engine"
	"github.com/StrangeNoob/shoal/internal/history"
)

func firstStatus(ss []engine.Status) *engine.Status {
	if len(ss) == 0 {
		return nil
	}
	return &ss[0]
}

// stepWorker performs one poll: refreshes a from the engine and writes the state
// file. Returns true once the (single) torrent is done.
func stepWorker(eng engine.Engine, base string, a *Active) (done bool) {
	st := firstStatus(eng.Statuses())
	if st == nil {
		return false
	}
	a.InfoHash = st.InfoHash
	if st.Name != "" {
		a.Name = st.Name
	}
	a.Total = st.TotalBytes
	a.Completed = st.CompletedBytes
	a.Peers = st.Peers
	a.Seeding = st.Seeding
	a.Path = st.Path
	a.Done = st.Done
	a.UpdatedAt = time.Now()
	_ = writeActive(base, *a)
	return st.Done
}

// runWorker polls until the download completes, then records it to history.
func runWorker(eng engine.Engine, base string, a Active, hist *history.Store, interval time.Duration) {
	_ = writeActive(base, a) // show up in `status` immediately
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for range tick.C {
		if stepWorker(eng, base, &a) {
			hist.Append(history.Entry{
				InfoHash:    a.InfoHash,
				Name:        a.Name,
				Size:        a.Total,
				CompletedAt: time.Now(),
				Path:        a.Path,
			})
			return
		}
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/shoal/ -run 'TestStepWorker|TestRunWorker' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/shoal/cli_download.go cmd/shoal/cli_download_test.go
git commit -m "Add download worker loop (progress state + history on completion)"
```

---

### Task 5: `shoal status` + platform helpers

Status reader with state classification (`downloading|done|error|stalled`), plus the build-tagged `pidAlive`/`detachSysProcAttr` helpers (the latter used in Task 6).

**Files:**
- Create: `cmd/shoal/cli_status.go`
- Create: `cmd/shoal/platform_unix.go` (`//go:build !windows`)
- Create: `cmd/shoal/platform_windows.go` (`//go:build windows`)
- Test: `cmd/shoal/cli_status_test.go`

**Interfaces:**
- Consumes: `listActive`, `readActive`, `clearFinished` (Task 2), `humanBytes` (Task 3).
- Produces:
  - `func stateOf(a Active, alive func(pid int) bool) string`
  - `func printStatus(out io.Writer, items []Active, asJSON bool, alive func(int) bool)`
  - `func runStatus(args []string, out io.Writer) int`
  - `func pidAlive(pid int) bool`
  - `func detachSysProcAttr() *syscall.SysProcAttr`

- [ ] **Step 1: Write the failing test**

```go
// cmd/shoal/cli_status_test.go
package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestStateOf(t *testing.T) {
	dead := func(int) bool { return false }
	alive := func(int) bool { return true }
	cases := []struct {
		a    Active
		f    func(int) bool
		want string
	}{
		{Active{Error: "boom"}, alive, "error"},
		{Active{Done: true}, alive, "done"},
		{Active{Pid: 42}, dead, "stalled"},
		{Active{Pid: 42}, alive, "downloading"},
	}
	for _, c := range cases {
		if got := stateOf(c.a, c.f); got != c.want {
			t.Errorf("stateOf(%+v) = %q, want %q", c.a, got, c.want)
		}
	}
}

func TestPrintStatusJSON(t *testing.T) {
	var buf bytes.Buffer
	items := []Active{{ID: "aaaa0000", Name: "M", Total: 200, Completed: 100, Pid: 1}}
	printStatus(&buf, items, true, func(int) bool { return true })
	var rows []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rows); err != nil {
		t.Fatalf("bad json: %v\n%s", err, buf.String())
	}
	if rows[0]["state"] != "downloading" || rows[0]["percent"].(float64) != 0.5 {
		t.Fatalf("row = %+v", rows[0])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/shoal/ -run 'TestStateOf|TestPrintStatusJSON' -v`
Expected: FAIL — `undefined: stateOf`.

- [ ] **Step 3: Write minimal implementation**

```go
// cmd/shoal/cli_status.go
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
)

// stateOf classifies an active entry for display.
func stateOf(a Active, alive func(pid int) bool) string {
	switch {
	case a.Error != "":
		return "error"
	case a.Done:
		return "done"
	case a.Pid > 0 && alive != nil && !alive(a.Pid):
		return "stalled"
	default:
		return "downloading"
	}
}

type statusRow struct {
	Active
	State   string  `json:"state"`
	Percent float64 `json:"percent"`
}

func printStatus(out io.Writer, items []Active, asJSON bool, alive func(int) bool) {
	rows := make([]statusRow, 0, len(items))
	for _, a := range items {
		pct := 0.0
		if a.Total > 0 {
			pct = float64(a.Completed) / float64(a.Total)
		}
		rows = append(rows, statusRow{Active: a, State: stateOf(a, alive), Percent: pct})
	}
	if asJSON {
		b, _ := json.MarshalIndent(rows, "", "  ")
		fmt.Fprintln(out, string(b))
		return
	}
	if len(rows) == 0 {
		fmt.Fprintln(out, "no active downloads")
		return
	}
	for _, r := range rows {
		fmt.Fprintf(out, "%-8s  %-40.40s  %5.1f%%  %10s/%-10s  %d peers  %s\n",
			r.ID, r.Name, r.Percent*100, humanBytes(r.Completed), humanBytes(r.Total), r.Peers, r.State)
	}
}

func runStatus(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit JSON")
	clear := fs.Bool("clear", false, "remove finished/errored entries after printing")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	base := configDir()
	var items []Active
	if id := fs.Arg(0); id != "" {
		a, err := readActive(base, id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "no such download: %s\n", id)
			return 1
		}
		items = []Active{a}
	} else {
		items, _ = listActive(base)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].UpdatedAt.After(items[j].UpdatedAt) })
	printStatus(out, items, *jsonOut, pidAlive)
	if *clear {
		_, _ = clearFinished(base)
	}
	return 0
}
```

```go
// cmd/shoal/platform_unix.go
//go:build !windows

package main

import "syscall"

// pidAlive reports whether a process with pid exists (signal 0 probe).
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

// detachSysProcAttr detaches a spawned worker into its own session so it
// survives the parent process and terminal closing.
func detachSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
```

```go
// cmd/shoal/platform_windows.go
//go:build windows

package main

import "syscall"

// pidAlive is best-effort on Windows (no cheap liveness probe): treat any
// positive pid as alive, so "stalled" detection is simply unavailable here.
func pidAlive(pid int) bool { return pid > 0 }

// detachSysProcAttr detaches the worker from the console.
// 0x00000008 = DETACHED_PROCESS, 0x00000200 = CREATE_NEW_PROCESS_GROUP.
func detachSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: 0x00000008 | 0x00000200}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/shoal/ -run 'TestStateOf|TestPrintStatusJSON' -v` then `go vet ./cmd/shoal/`
Expected: PASS; vet clean.

- [ ] **Step 5: Commit**

```bash
git add cmd/shoal/cli_status.go cmd/shoal/platform_unix.go cmd/shoal/platform_windows.go cmd/shoal/cli_status_test.go
git commit -m "Add shoal status subcommand and platform detach/liveness helpers"
```

---

### Task 6: Routing, detached spawn, and worker entrypoint

Wire the three subcommands into `cli()`, implement the `download` parent (resolve → spawn detached worker → return) and the `--worker` child (build engine → add → runWorker), and update the usage text.

**Files:**
- Modify: `cmd/shoal/main.go` (usage text + `cli()` switch)
- Modify: `cmd/shoal/cli_download.go` (append `runDownload`, `downloadWorker`, `spawnWorker`, `displayName`)
- Modify: `cmd/shoal/main_test.go` (routing assertions)

**Interfaces:**
- Consumes: `resolveTarget` (Task 1), `lookupCache`/`configDir`/`writeActive` (Task 2), `runWorker` (Task 4), `detachSysProcAttr` (Task 5), `config.Load`, `engine.NewAnacrolix`, `engine.Config`, `history.Load`.
- Produces: `runDownload`, `downloadWorker`, `spawnWorker`, `displayName`; extended `cli()`.

- [ ] **Step 1: Write the failing test**

```go
// append to cmd/shoal/main_test.go
func TestCLIRoutesNewSubcommands(t *testing.T) {
	var buf bytes.Buffer
	for _, name := range []string{"search", "download", "status"} {
		// Missing operands make these exit non-zero, but they must be *handled*
		// (not fall through to launching the TUI).
		handled, _ := cli([]string{"shoal", name}, "1.0.0", &buf)
		if !handled {
			t.Errorf("cli did not handle %q", name)
		}
	}
	if handled, _ := cli([]string{"shoal"}, "1.0.0", &buf); handled {
		t.Error("no-arg invocation should fall through to the TUI")
	}
}

func TestDisplayName(t *testing.T) {
	if got := displayName(dlTarget{URL: "https://x/y.torrent"}); got != "https://x/y.torrent" {
		t.Errorf("url name = %q", got)
	}
	if got := displayName(dlTarget{Magnet: "magnet:?xt=urn:btih:abc&dn=Big+Buck"}); got != "Big Buck" {
		t.Errorf("magnet name = %q, want 'Big Buck'", got)
	}
}
```

Ensure `main_test.go` imports `bytes` (add if missing).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/shoal/ -run 'TestCLIRoutes|TestDisplayName' -v`
Expected: FAIL — `cli did not handle "search"` and `undefined: displayName`.

- [ ] **Step 3: Write minimal implementation**

In `cmd/shoal/main.go`, extend the `switch` in `cli()` (before `default:`):

```go
	case "search":
		return true, runSearch(args[2:], out)
	case "download":
		return true, runDownload(args[2:], out)
	case "status":
		return true, runStatus(args[2:], out)
```

Replace the `usage` const:

```go
const usage = `shoal — a calm BitTorrent client for your terminal

Usage:
  shoal                         launch the fullscreen TUI
  shoal search "<query>"        search torrent sources (add --json for scripts)
  shoal download <id|magnet>    download in the background (add --out <dir>)
  shoal status [id]             show download progress (add --json, --clear)
  shoal update                  update shoal to the latest release
  shoal version                 print the version
  shoal help                    show this help
`
```

Append to `cmd/shoal/cli_download.go`:

```go
import (
	"net/url"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/StrangeNoob/shoal/internal/config"
)

// runDownload is the `download` entrypoint. The parent resolves the target and
// spawns a detached worker; --worker runs the actual download loop.
func runDownload(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("download", flag.ContinueOnError)
	worker := fs.Bool("worker", false, "") // hidden: marks the detached child
	id := fs.String("id", "", "")          // hidden: worker handle
	outDir := fs.String("out", "", "download directory")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	arg := fs.Arg(0)
	if arg == "" {
		fmt.Fprintln(os.Stderr, "usage: shoal download <magnet|url|infohash|id> [--out dir]")
		return 2
	}

	cfg := config.Load()
	base := configDir()
	dir := cfg.DataDir
	if *outDir != "" {
		dir = *outDir
	}

	if *worker {
		return downloadWorker(*id, arg, dir, base)
	}

	tgt, err := resolveTarget(arg, func(sid string) (string, bool) {
		e, ok := lookupCache(base, sid)
		return e.Magnet, ok
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	target := tgt.Magnet
	if target == "" {
		target = tgt.URL
	}
	if err := spawnWorker(tgt.Handle, target, dir, base); err != nil {
		fmt.Fprintln(os.Stderr, "failed to start download:", err)
		return 1
	}
	fmt.Fprintf(out, "started: %s (%s)\n", displayName(tgt), tgt.Handle)
	return 0
}

// downloadWorker is the detached child: build the engine, add the one torrent,
// and run the progress loop to completion.
func downloadWorker(id, target, dir, base string) int {
	cfg := config.Load()
	eng, err := engine.NewAnacrolix(engine.Config{
		DataDir:    dir,
		ListenPort: cfg.ListenPort,
		MaxPeers:   cfg.MaxPeers,
		Seed:       false, // one-shot: stop at 100%, don't seed forever
		SeedRatio:  0,
		QueuePath:  "", // never touch the TUI's persistent queue
	})
	a := Active{ID: id, Out: dir, Pid: os.Getpid(), UpdatedAt: time.Now()}
	if err != nil {
		a.Error = err.Error()
		_ = writeActive(base, a)
		return 1
	}
	defer eng.Close()

	if strings.HasPrefix(strings.ToLower(target), "magnet:") {
		err = eng.AddMagnet(target)
	} else {
		err = eng.AddTorrentURL(target, "")
	}
	if err != nil {
		a.Error = err.Error()
		_ = writeActive(base, a)
		return 1
	}

	hist := history.Load()
	runWorker(eng, base, a, &hist, time.Second)
	return 0
}

// spawnWorker launches a detached copy of this binary to run the download.
func spawnWorker(handle, target, dir, base string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(base, "logs"), 0o700); err != nil {
		return err
	}
	logf, err := os.OpenFile(filepath.Join(base, "logs", handle+".log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(exe, "download", "--worker", "--id", handle, "--out", dir, target)
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.SysProcAttr = detachSysProcAttr()
	return cmd.Start() // do not Wait: the child outlives us
}

// displayName is a friendly label for the "started:" line.
func displayName(t dlTarget) string {
	if t.URL != "" {
		return t.URL
	}
	if u, err := url.Parse(t.Magnet); err == nil {
		if dn := u.Query().Get("dn"); dn != "" {
			return dn
		}
	}
	return "download"
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/shoal/ -run 'TestCLIRoutes|TestDisplayName' -v` then `go build ./... && go test ./...`
Expected: PASS; full build + suite green.

- [ ] **Step 5: Commit**

```bash
git add cmd/shoal/main.go cmd/shoal/cli_download.go cmd/shoal/main_test.go
git commit -m "Wire search/download/status routing and detached download worker"
```

---

### Task 7: Claude skill

Author the skill that drives the CLI, and do one real end-to-end check.

**Files:**
- Create: `.claude/skills/shoal-download/SKILL.md`

**Interfaces:**
- Consumes: the `shoal search --json` / `shoal download` / `shoal status --json` commands from Tasks 3–6.

- [ ] **Step 1: Write the skill**

```markdown
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
```

- [ ] **Step 2: Verify the commands the skill relies on exist**

Run: `go build -o /tmp/shoal ./cmd/shoal && /tmp/shoal help`
Expected: usage text lists `search`, `download`, and `status`.

- [ ] **Step 3: End-to-end smoke test (real network, a well-seeded public domain title)**

Run:
```bash
/tmp/shoal search --json "Big Buck Bunny" | head -c 400
/tmp/shoal download "$(/tmp/shoal search --json 'Big Buck Bunny' | \
  python3 -c 'import sys,json;print(json.load(sys.stdin)[0]["magnet"])')" --out /tmp/shoaltest
sleep 5 && /tmp/shoal status --json
```
Expected: search prints a JSON array; download prints `started: … (<handle>)`; status shows the handle with a growing `percent`/`peers`. (Full completion depends on swarm health — a rising percent is enough to confirm the pipeline.)

- [ ] **Step 4: Commit**

```bash
git add .claude/skills/shoal-download/SKILL.md
git commit -m "Add shoal-download Claude skill"
```

---

## Self-Review

**Spec coverage:**
- `search` (JSON, --source, --limit, cache write) → Task 3. ✓
- `download` background + detached worker + handle model → Tasks 1, 4, 6. ✓
- `status` (id, --json, --clear, stalled) → Task 5. ✓
- State files (`active/*.json`, `last-search.json`, `logs/*.log`) → Tasks 2, 6. ✓
- History recording on completion → Task 4. ✓
- Claude skill → Task 7. ✓
- `Seed=false`/`QueuePath=""` one-shot → Task 6 (`downloadWorker`). ✓
- No `shoal-classic` changes, no new deps → honored throughout. ✓

**Placeholder scan:** none — every code step is complete; the two hidden flags carry empty usage strings intentionally.

**Type consistency:** `Active`, `dlTarget`, `searchRow`, `cacheEntry` and the function signatures (`resolveTarget`, `stepWorker`, `runWorker`, `stateOf`, `printStatus`, `runSearch/Download/Status`, `spawnWorker`, `downloadWorker`) are used identically across tasks. `configDir()` is the single base-path source. The `download` worker only ever sees a `magnet:`/`http` target (parent pre-resolves hash/short-id), matching `downloadWorker`'s prefix check.
