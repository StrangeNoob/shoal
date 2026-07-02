# Self-update Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** shoal updates its own binary from GitHub Releases — a `shoal update` CLI command, `shoal version`, an in-app "update available" notice, and an opt-in Auto-update setting that applies on launch.

**Architecture:** A new `internal/update` package does the release lookup (GitHub API), download, SHA-256 verification, and archive extraction with the stdlib, then hands the new binary to `github.com/minio/selfupdate` for the safe swap. `cmd/shoal` gets a build-injected `version` and subcommand dispatch. `internal/ui` gets a version field, an Auto-update setting, and a non-blocking launch check that shows a header notice or (opt-in) auto-applies.

**Tech Stack:** Go 1.24, `github.com/minio/selfupdate`, stdlib (net/http, archive/tar, archive/zip, compress/gzip, crypto/sha256), Bubble Tea, GoReleaser.

## Global Constraints

- TDD: write the failing test first for every behavior.
- No Claude attribution in commits (no `Co-Authored-By` / "Generated with" trailer).
- Git repo on branch `feature/self-update`; each task's gate is its `go test`; commit when green.
- Repo constants: owner `StrangeNoob`, name `shoal`, binary `shoal`.
- Version comparison is `vMAJOR.MINOR.PATCH` numeric; `dev`/unparseable current → treated as older than any release.
- The in-app check and auto-update run on **release builds only** (`version != "" && version != "dev"`), are non-blocking, and silent on failure.
- Auth: send `Authorization: Bearer <token>` from `GITHUB_TOKEN` or `GH_TOKEN` when set (needed while the repo is private).
- Full gate before a task is done: `go build ./...`, `go vet ./...`, `gofmt -l .` (empty), `go test -race ./...`.

---

### Task 1: `internal/update` package

**Files:**
- Create: `internal/update/update.go`, `internal/update/update_test.go`
- Modify: `go.mod` / `go.sum` (add `github.com/minio/selfupdate`)

**Interfaces:**
- Produces (consumed by Tasks 3–5):
  - `func CheckLatest(ctx context.Context) (Release, error)`; `type Release struct { Version string; Assets []Asset }`, `type Asset struct { Name, URL string }`
  - `func Apply(ctx context.Context, current string, applyFn func(io.Reader) error) (updatedTo string, upToDate bool, err error)`
  - `func DisplayVersion(v string) string`
  - `var apiBase = "https://api.github.com"` (overridable in tests)

- [ ] **Step 1: Add the dependency.**

```sh
go get github.com/minio/selfupdate@latest
```

- [ ] **Step 2: Write the failing tests.**

Create `internal/update/update_test.go`:
```go
package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsNewer(t *testing.T) {
	cases := []struct {
		cur, latest string
		want        bool
	}{
		{"0.2.0", "0.3.0", true},
		{"0.2.0", "0.2.1", true},
		{"1.0.0", "0.9.9", false},
		{"0.2.0", "0.2.0", false},
		{"v0.2.0", "0.3.0", true},
		{"dev", "0.1.0", true},
		{"", "0.1.0", true},
	}
	for _, c := range cases {
		if got := isNewer(c.cur, c.latest); got != c.want {
			t.Errorf("isNewer(%q,%q)=%v want %v", c.cur, c.latest, got, c.want)
		}
	}
}

func TestMatchAsset(t *testing.T) {
	names := []string{"checksums.txt", "shoal_0.3.0_linux_amd64.tar.gz", "shoal_0.3.0_darwin_arm64.tar.gz", "shoal_0.3.0_windows_amd64.zip"}
	if got, ok := matchAsset(names, "darwin", "arm64"); !ok || got != "shoal_0.3.0_darwin_arm64.tar.gz" {
		t.Fatalf("darwin/arm64 = %q,%v", got, ok)
	}
	if got, ok := matchAsset(names, "windows", "amd64"); !ok || got != "shoal_0.3.0_windows_amd64.zip" {
		t.Fatalf("windows/amd64 = %q,%v", got, ok)
	}
	if _, ok := matchAsset(names, "plan9", "mips"); ok {
		t.Fatal("plan9/mips should not match")
	}
}

func TestChecksumFor(t *testing.T) {
	body := "aaa  shoal_0.3.0_linux_amd64.tar.gz\nbbb  shoal_0.3.0_darwin_arm64.tar.gz\n"
	if got, err := checksumFor(body, "shoal_0.3.0_darwin_arm64.tar.gz"); err != nil || got != "bbb" {
		t.Fatalf("checksumFor = %q,%v", got, err)
	}
	if _, err := checksumFor(body, "nope"); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func tarGz(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(data))}); err != nil {
		t.Fatal(err)
	}
	tw.Write(data)
	tw.Close()
	gw.Close()
	return buf.Bytes()
}

func zipArc(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create(name)
	w.Write(data)
	zw.Close()
	return buf.Bytes()
}

func TestExtractBinary(t *testing.T) {
	want := []byte("BINARY-BYTES")
	got, err := extractBinary(bytes.NewReader(tarGz(t, "shoal", want)), false, "shoal")
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("tar.gz extract = %q,%v", got, err)
	}
	got, err = extractBinary(bytes.NewReader(zipArc(t, "shoal.exe", want)), true, "shoal")
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("zip extract = %q,%v", got, err)
	}
}

func TestApply(t *testing.T) {
	bin := []byte("NEW-SHOAL-BINARY")
	archive := tarGz(t, "shoal", bin)
	sum := sha256.Sum256(archive)
	assetName := "shoal_0.3.0_" + goosArch() + ".tar.gz"

	mux := http.NewServeMux()
	var base string
	mux.HandleFunc("/repos/StrangeNoob/shoal/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"tag_name":"v0.3.0","assets":[
			{"name":%q,"browser_download_url":%q},
			{"name":"checksums.txt","browser_download_url":%q}]}`,
			assetName, base+"/dl/"+assetName, base+"/dl/checksums.txt")
	})
	mux.HandleFunc("/dl/"+assetName, func(w http.ResponseWriter, r *http.Request) { w.Write(archive) })
	mux.HandleFunc("/dl/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), assetName)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	base = srv.URL

	oldBase := apiBase
	apiBase = srv.URL
	t.Cleanup(func() { apiBase = oldBase })

	var applied []byte
	to, up, err := Apply(context.Background(), "0.2.0", func(r io.Reader) error {
		applied, _ = io.ReadAll(r)
		return nil
	})
	if err != nil || up || to != "0.3.0" {
		t.Fatalf("Apply = %q,%v,%v", to, up, err)
	}
	if !bytes.Equal(applied, bin) {
		t.Fatalf("applied bytes = %q, want the extracted binary", applied)
	}

	// already up to date
	_, up, err = Apply(context.Background(), "9.9.9", func(io.Reader) error { return nil })
	if err != nil || !up {
		t.Fatalf("up-to-date Apply = up:%v err:%v", up, err)
	}
}

// goosArch mirrors the runtime target so the fake asset name matches matchAsset.
func goosArch() string { return runtimeGOOS + "_" + runtimeGOARCH }
```
(Note: the test references `runtimeGOOS`/`runtimeGOARCH` — Step 3 defines them as package vars aliasing `runtime.GOOS`/`runtime.GOARCH` so the test's expected asset name matches `Apply`'s lookup.)

- [ ] **Step 3: Run tests to verify they fail.**

Run: `go test ./internal/update/ -v`
Expected: FAIL — undefined identifiers.

- [ ] **Step 4: Implement `internal/update/update.go`.**

```go
// Package update self-updates the shoal binary from GitHub Releases.
package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/minio/selfupdate"
)

const (
	repoOwner = "StrangeNoob"
	repoName  = "shoal"
	binName   = "shoal"
)

// apiBase is the GitHub API root; overridable in tests.
var apiBase = "https://api.github.com"

// runtimeGOOS/GOARCH are indirections so tests can match the fake asset name.
var (
	runtimeGOOS   = runtime.GOOS
	runtimeGOARCH = runtime.GOARCH
)

type Asset struct{ Name, URL string }

type Release struct {
	Version string // tag without a leading "v", e.g. "0.3.0"
	Assets  []Asset
}

func httpClient() *http.Client { return &http.Client{Timeout: 30 * time.Second} }

func authHeader(req *http.Request) {
	tok := os.Getenv("GITHUB_TOKEN")
	if tok == "" {
		tok = os.Getenv("GH_TOKEN")
	}
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
}

// DisplayVersion formats a build version for humans: "dev" or "v1.2.3".
func DisplayVersion(v string) string {
	if v == "" || v == "dev" {
		return "dev"
	}
	return "v" + strings.TrimPrefix(v, "v")
}

// CheckLatest fetches the latest published release.
func CheckLatest(ctx context.Context) (Release, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases/latest", apiBase, repoOwner, repoName)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	authHeader(req)
	resp, err := httpClient().Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("github releases API: %s", resp.Status)
	}
	var p struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		return Release{}, err
	}
	rel := Release{Version: strings.TrimPrefix(p.TagName, "v")}
	for _, a := range p.Assets {
		rel.Assets = append(rel.Assets, Asset{Name: a.Name, URL: a.URL})
	}
	return rel, nil
}

func parseVer(v string) []int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if v == "" || v == "dev" {
		return nil
	}
	v = strings.SplitN(v, "-", 2)[0] // drop any prerelease suffix
	fields := strings.Split(v, ".")
	out := make([]int, 3)
	for i := 0; i < 3; i++ {
		if i < len(fields) {
			n, err := strconv.Atoi(fields[i])
			if err != nil {
				return nil
			}
			out[i] = n
		}
	}
	return out
}

func isNewer(current, latest string) bool {
	c, l := parseVer(current), parseVer(latest)
	if c == nil {
		return true
	}
	if l == nil {
		return false
	}
	for i := 0; i < 3; i++ {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

// Newer reports whether latest is a newer version than current (exported for
// the UI's launch check).
func Newer(current, latest string) bool { return isNewer(current, latest) }

func matchAsset(names []string, goos, goarch string) (string, bool) {
	suffix := "_" + goos + "_" + goarch + "."
	for _, n := range names {
		if strings.Contains(n, suffix) && (strings.HasSuffix(n, ".tar.gz") || strings.HasSuffix(n, ".zip")) {
			return n, true
		}
	}
	return "", false
}

func checksumFor(checksums, filename string) (string, error) {
	for _, line := range strings.Split(checksums, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[1] == filename {
			return f[0], nil
		}
	}
	return "", fmt.Errorf("no checksum for %q", filename)
}

func extractBinary(archive io.Reader, zipped bool, name string) ([]byte, error) {
	want := map[string]bool{name: true, name + ".exe": true}
	if zipped {
		data, err := io.ReadAll(archive)
		if err != nil {
			return nil, err
		}
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if want[f.Name] {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(rc)
			}
		}
		return nil, fmt.Errorf("%s not found in zip", name)
	}
	gz, err := gzip.NewReader(archive)
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("%s not found in archive", name)
		}
		if err != nil {
			return nil, err
		}
		if want[h.Name] {
			return io.ReadAll(tr)
		}
	}
}

func download(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	authHeader(req)
	resp, err := httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// Apply updates the running binary to the latest release. applyFn performs the
// swap (defaults to selfupdate.Apply; tests inject a stub). Returns upToDate
// when already current.
func Apply(ctx context.Context, current string, applyFn func(io.Reader) error) (updatedTo string, upToDate bool, err error) {
	if applyFn == nil {
		applyFn = func(r io.Reader) error { return selfupdate.Apply(r, selfupdate.Options{}) }
	}
	rel, err := CheckLatest(ctx)
	if err != nil {
		return "", false, err
	}
	if !isNewer(current, rel.Version) {
		return rel.Version, true, nil
	}
	names := make([]string, len(rel.Assets))
	byName := make(map[string]string, len(rel.Assets))
	for i, a := range rel.Assets {
		names[i] = a.Name
		byName[a.Name] = a.URL
	}
	assetName, ok := matchAsset(names, runtimeGOOS, runtimeGOARCH)
	if !ok {
		return "", false, fmt.Errorf("no release asset for %s/%s", runtimeGOOS, runtimeGOARCH)
	}
	sumsURL, ok := byName["checksums.txt"]
	if !ok {
		return "", false, fmt.Errorf("release has no checksums.txt")
	}
	archive, err := download(ctx, byName[assetName])
	if err != nil {
		return "", false, err
	}
	sums, err := download(ctx, sumsURL)
	if err != nil {
		return "", false, err
	}
	want, err := checksumFor(string(sums), assetName)
	if err != nil {
		return "", false, err
	}
	if got := sha256.Sum256(archive); hex.EncodeToString(got[:]) != want {
		return "", false, fmt.Errorf("checksum mismatch for %s", assetName)
	}
	bin, err := extractBinary(bytes.NewReader(archive), strings.HasSuffix(assetName, ".zip"), binName)
	if err != nil {
		return "", false, err
	}
	if err := applyFn(bytes.NewReader(bin)); err != nil {
		return "", false, err
	}
	return rel.Version, false, nil
}
```

- [ ] **Step 5: Run tests to verify they pass.**

Run: `go test ./internal/update/ -v && go vet ./internal/update/`
Expected: PASS; vet clean.

- [ ] **Step 6: Checkpoint.**

Run: `go build ./... && gofmt -l internal/update/`
Expected: build clean, gofmt clean. (Commit.)

---

### Task 2: `config.AutoUpdate`

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces (consumed by Task 5): `Config.AutoUpdate bool` (`json:"auto_update"`), default `false`.

- [ ] **Step 1: Write the failing test.**

Add to `internal/config/config_test.go`:
```go
func TestAutoUpdateDefaultsFalseAndRoundTrips(t *testing.T) {
	isolate(t)
	if Default().AutoUpdate {
		t.Fatal("AutoUpdate should default to false (opt-in)")
	}
	c := Default()
	c.AutoUpdate = true
	if err := c.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if !Load().AutoUpdate {
		t.Fatal("AutoUpdate did not survive save/load")
	}
}
```

- [ ] **Step 2: Run it to verify it fails.**

Run: `go test ./internal/config/ -run TestAutoUpdate -v`
Expected: FAIL — `c.AutoUpdate` undefined.

- [ ] **Step 3: Add the field.**

In `internal/config/config.go`, add to `Config` (after `ListenPort`):
```go

	// Updates
	AutoUpdate bool `json:"auto_update"` // apply the latest release automatically on launch
```
`Default()` needs no change — `false` is the zero value and the desired default.

- [ ] **Step 4: Run it to verify it passes.**

Run: `go test ./internal/config/ -v`
Expected: PASS (all config tests).

- [ ] **Step 5: Checkpoint.**

Run: `go build ./... && go vet ./internal/config/`
Expected: clean. (Commit.)

---

### Task 3: Build-injected version + About + GoReleaser

**Files:**
- Modify: `cmd/shoal/main.go` (version var, `WithVersion` wiring)
- Modify: `internal/ui/model.go` (`version` field, `WithVersion`)
- Modify: `internal/ui/view.go` (About shows the version)
- Modify: `.goreleaser.yaml` (version ldflag)
- Test: `internal/ui/view_test.go`

**Interfaces:**
- Consumes: `update.DisplayVersion` (Task 1).
- Produces (consumed by Tasks 4–5): `Model.version string`; `func (m Model) WithVersion(v string) Model`; `main.version`.

- [ ] **Step 1: Write the failing test.**

Add to `internal/ui/view_test.go`:
```go
func TestAboutShowsVersion(t *testing.T) {
	m := ready(New(&fakeSource{}, &fakeEngine{})).WithVersion("0.3.0")
	m.section = sectionSettings
	if !strings.Contains(m.View(), "v0.3.0") {
		t.Fatalf("About should show the build version:\n%s", m.View())
	}
	// no version → "dev"
	d := ready(New(&fakeSource{}, &fakeEngine{}))
	d.section = sectionSettings
	if !strings.Contains(d.View(), "dev") {
		t.Fatalf("About should show 'dev' for an unversioned build:\n%s", d.View())
	}
}
```

- [ ] **Step 2: Run it to verify it fails.**

Run: `go test ./internal/ui/ -run TestAboutShowsVersion -v`
Expected: FAIL — `WithVersion` undefined / About shows hardcoded "v0.2".

- [ ] **Step 3: Add the `version` field + `WithVersion`.**

In `internal/ui/model.go`, add to the `Model` struct (near `history`):
```go
	version string // build version (ldflags), "" or "dev" for local builds
```
Add the injector near `WithHistory`:
```go
// WithVersion attaches the build version (main injects it via ldflags).
func (m Model) WithVersion(v string) Model {
	m.version = v
	return m
}
```
Add `"github.com/StrangeNoob/shoal/internal/update"` to `model.go` imports (used here and in Task 5).

- [ ] **Step 4: Show it in the About row.**

In `internal/ui/view.go`, replace the About line:
```go
	b.WriteString("  " + st.SetLabel.Render(padOrTrim("shoal", 13)) + "  " + st.Meta.Render("v0.2  ·  anacrolix engine"))
```
with:
```go
	b.WriteString("  " + st.SetLabel.Render(padOrTrim("shoal", 13)) + "  " + st.Meta.Render(update.DisplayVersion(m.version)+"  ·  anacrolix engine"))
```
Add `"github.com/StrangeNoob/shoal/internal/update"` to `view.go` imports.

- [ ] **Step 5: Wire main + GoReleaser.**

In `cmd/shoal/main.go`, add a package var and pass it through:
```go
// version is set at build time via -ldflags "-X main.version=...". "dev" locally.
var version = "dev"
```
Change the model construction to append `.WithVersion(version)`:
```go
		ui.NewWithConfig(src, eng, cfg).WithHistory(history.Load()).WithVersion(version),
```
In `.goreleaser.yaml`, change the `shoal` build's `ldflags` from:
```yaml
    ldflags:
      - -s -w
```
to:
```yaml
    ldflags:
      - -s -w -X main.version={{ .Version }}
```
(Leave the `shoal-classic` build's ldflags as `-s -w`.)

- [ ] **Step 6: Run tests + validate GoReleaser.**

Run: `go test ./internal/ui/ -run TestAboutShowsVersion -v && go build ./... && goreleaser check`
Expected: PASS; build clean; `goreleaser check` says configuration is valid.

- [ ] **Step 7: Checkpoint.**

Run: `go test ./internal/ui/ && gofmt -l internal/ cmd/`
Expected: `ok`; gofmt clean. (Commit.)

---

### Task 4: CLI dispatch (`shoal update` / `shoal version`)

**Files:**
- Modify: `cmd/shoal/main.go`
- Test: `cmd/shoal/main_test.go` (new)

**Interfaces:**
- Consumes: `update.Apply`, `update.DisplayVersion` (Task 1); `main.version` (Task 3).
- Produces: `func cli(args []string, version string, out io.Writer) (handled bool, code int)`.

- [ ] **Step 1: Write the failing test.**

Create `cmd/shoal/main_test.go`:
```go
package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestCLIVersionAndHelp(t *testing.T) {
	var out bytes.Buffer
	if handled, code := cli([]string{"shoal", "version"}, "0.3.0", &out); !handled || code != 0 {
		t.Fatalf("version: handled=%v code=%d", handled, code)
	}
	if !strings.Contains(out.String(), "shoal v0.3.0") {
		t.Fatalf("version output = %q", out.String())
	}

	out.Reset()
	if handled, _ := cli([]string{"shoal", "help"}, "0.3.0", &out); !handled || !strings.Contains(out.String(), "update") {
		t.Fatalf("help output = %q", out.String())
	}

	// no subcommand → not handled (caller launches the TUI)
	if handled, _ := cli([]string{"shoal"}, "0.3.0", &out); handled {
		t.Fatal("no-args should not be handled by cli()")
	}
}
```

- [ ] **Step 2: Run it to verify it fails.**

Run: `go test ./cmd/shoal/ -v`
Expected: FAIL — `cli` undefined.

- [ ] **Step 3: Implement the dispatch.**

In `cmd/shoal/main.go`, add imports `"context"`, `"io"`, `"time"`, and `"github.com/StrangeNoob/shoal/internal/update"`. Add:
```go
const usage = `shoal — a calm BitTorrent client for your terminal

Usage:
  shoal            launch the fullscreen TUI
  shoal update     update shoal to the latest release
  shoal version    print the version
  shoal help       show this help
`

// cli handles subcommands. Returns handled=true (with an exit code) when it
// consumed the invocation; handled=false means "launch the TUI".
func cli(args []string, version string, out io.Writer) (handled bool, code int) {
	if len(args) < 2 {
		return false, 0
	}
	switch args[1] {
	case "version", "--version", "-v":
		fmt.Fprintln(out, "shoal", update.DisplayVersion(version))
		return true, 0
	case "help", "--help", "-h":
		fmt.Fprint(out, usage)
		return true, 0
	case "update":
		return true, runUpdate(out, version)
	default:
		return false, 0
	}
}

func runUpdate(out io.Writer, version string) int {
	fmt.Fprintln(out, "Checking for updates…")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	to, upToDate, err := update.Apply(ctx, version, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "shoal: update failed:", err)
		return 1
	}
	if upToDate {
		fmt.Fprintf(out, "Already on the latest version (%s).\n", update.DisplayVersion(to))
		return 0
	}
	fmt.Fprintf(out, "Updated to %s — restart shoal to use it.\n", update.DisplayVersion(to))
	return 0
}
```
Change `main()` to dispatch first:
```go
func main() {
	if handled, code := cli(os.Args, version, os.Stdout); handled {
		os.Exit(code)
	}

	cfg := config.Load()
	// … existing body unchanged …
}
```

- [ ] **Step 4: Run it to verify it passes.**

Run: `go test ./cmd/shoal/ -v && go build ./...`
Expected: PASS; build clean.

- [ ] **Step 5: Checkpoint.**

Run: `go vet ./cmd/shoal/ && gofmt -l cmd/`
Expected: clean. (Commit.)

---

### Task 5: In-app notice, Auto-update setting, auto-apply on launch

**Files:**
- Modify: `internal/ui/model.go` (messages/commands, `Init`, `Update`, `settingItems`)
- Modify: `internal/ui/branding.go` (header indicator)
- Test: `internal/ui/model_test.go`

**Interfaces:**
- Consumes: `update.CheckLatest`/`update.Apply` (Task 1); `Model.version` (Task 3); `cfg.AutoUpdate` (Task 2).
- Produces: `Model.updateAvail string`; `updateCheckMsg`/`selfUpdatedMsg`; `checkUpdateCmd`/`autoUpdateCmd`.

- [ ] **Step 1: Write the failing tests.**

Add to `internal/ui/model_test.go`:
```go
func TestUpdateNoticeAndAutoUpdateGate(t *testing.T) {
	// A newer version sets the header notice.
	m := ready(New(&fakeSource{}, &fakeEngine{})).WithVersion("0.2.0")
	m2, cmd := update(m, updateCheckMsg{latest: "0.3.0", newer: true})
	mm := m2.(Model)
	if mm.updateAvail != "0.3.0" {
		t.Fatalf("updateAvail = %q, want 0.3.0", mm.updateAvail)
	}
	if cmd != nil { // auto-update off by default → no follow-up command
		t.Fatal("with AutoUpdate off, updateCheckMsg should not trigger an update command")
	}
	if !strings.Contains(mm.View(), "0.3.0") {
		t.Fatalf("header should advertise the available update:\n%s", mm.View())
	}

	// With AutoUpdate on, a newer version returns an auto-update command.
	a := ready(New(&fakeSource{}, &fakeEngine{})).WithVersion("0.2.0")
	a.cfg.AutoUpdate = true
	_, cmd = update(a, updateCheckMsg{latest: "0.3.0", newer: true})
	if cmd == nil {
		t.Fatal("with AutoUpdate on, a newer version should return an update command")
	}

	// Not newer → nothing.
	n := ready(New(&fakeSource{}, &fakeEngine{})).WithVersion("0.3.0")
	n2, cmd := update(n, updateCheckMsg{latest: "0.3.0", newer: false})
	if n2.(Model).updateAvail != "" || cmd != nil {
		t.Fatal("a not-newer check should do nothing")
	}
}

func TestAutoUpdateSettingTogglesConfig(t *testing.T) {
	var found *setItem
	for i := range settingItems() {
		if settingItems()[i].label == "Auto-update" {
			it := settingItems()[i]
			found = &it
			break
		}
	}
	if found == nil {
		t.Fatal("Settings should include an 'Auto-update' item")
	}
	m := New(&fakeSource{}, &fakeEngine{})
	found.set(&m, "on")
	if !m.cfg.AutoUpdate {
		t.Fatal("setting Auto-update to 'on' should enable cfg.AutoUpdate")
	}
	found.set(&m, "off")
	if m.cfg.AutoUpdate {
		t.Fatal("setting Auto-update to 'off' should disable cfg.AutoUpdate")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail.**

Run: `go test ./internal/ui/ -run 'UpdateNotice|AutoUpdateSetting' -v`
Expected: FAIL — undefined `updateCheckMsg`, `updateAvail`, no Auto-update item.

- [ ] **Step 3: Add the field, messages, and commands.**

In `internal/ui/model.go`, add to the `Model` struct (near `version`):
```go
	updateAvail string // latest version when a newer release is available
```
Add the messages + commands near `tickCmd` (imports `context`, `time` already present; add `"github.com/StrangeNoob/shoal/internal/update"` — added in Task 3):
```go
type updateCheckMsg struct {
	latest string
	newer  bool
}

type selfUpdatedMsg struct {
	version string
	err     error
}

func checkUpdateCmd(current string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		rel, err := update.CheckLatest(ctx)
		if err != nil {
			return updateCheckMsg{} // silent on failure
		}
		return updateCheckMsg{latest: rel.Version, newer: updateNewer(current, rel.Version)}
	}
}

func autoUpdateCmd(current string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		to, _, err := update.Apply(ctx, current, nil)
		return selfUpdatedMsg{version: to, err: err}
	}
}
```
`checkUpdateCmd` uses `update.Newer` (exported by Task 1) via a file-local alias in `model.go` for brevity:
```go
var updateNewer = update.Newer
```

- [ ] **Step 4: Fire the check from `Init` (release builds only).**

Change `Init()`:
```go
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{textinput.Blink, m.spin.Tick, tickCmd(), frameCmd()}
	if m.version != "" && m.version != "dev" {
		cmds = append(cmds, checkUpdateCmd(m.version))
	}
	return tea.Batch(cmds...)
}
```

- [ ] **Step 5: Handle the messages in `Update`.**

Add cases (next to `tickMsg`):
```go
	case updateCheckMsg:
		if msg.newer {
			m.updateAvail = msg.latest
			if m.cfg.AutoUpdate {
				return m, autoUpdateCmd(m.version)
			}
		}
		return m, nil

	case selfUpdatedMsg:
		if msg.err == nil && msg.version != "" {
			m.updateAvail = ""
			m.setNotice("↑ v" + msg.version + " installed — restart shoal")
		}
		return m, nil
```

- [ ] **Step 6: Add the Auto-update setting.**

In `settingItems()`, append a new group after the DOWNLOADS entries (before the closing `}` of the returned slice):
```go
		{group: "UPDATES", label: "Auto-update", kind: kindEnum, options: []string{"off", "on"},
			get: func(m *Model) string {
				if m.cfg.AutoUpdate {
					return "on"
				}
				return "off"
			},
			set: func(m *Model, v string) { m.cfg.AutoUpdate = v == "on" }},
```

- [ ] **Step 7: Show the header indicator.**

In `internal/ui/branding.go` `renderHeader`, in the banner branch, replace the tagline append:
```go
	lines = append(lines, strings.Repeat(" ", 2+headerIconWidth+4)+
		st.Tag.Render("torrents, calmly, from your terminal"))
```
with:
```go
	tagline := st.Tag.Render("torrents, calmly, from your terminal")
	if m.updateAvail != "" {
		tagline += "   " + st.Faint.Render("· ↑ v"+m.updateAvail+" available, run 'shoal update'")
	}
	lines = append(lines, strings.Repeat(" ", 2+headerIconWidth+4)+tagline)
```
(This keeps the header at 6 rows, so `headerHeight()` and the body math are unchanged.)

- [ ] **Step 8: Run tests to verify they pass.**

Run: `go test ./internal/ui/ -run 'UpdateNotice|AutoUpdateSetting' -v && go test ./internal/ui/`
Expected: the new tests PASS and the full ui suite stays green.

- [ ] **Step 9: Full checkpoint.**

Run: `go build ./... && go vet ./... && go test ./... -race && gofmt -l internal/ cmd/`
Expected: build/vet clean, all tests `ok`, gofmt clean. (Commit.)

---

## Self-Review Notes

- **Spec coverage:** `internal/update` core (Task 1); `config.AutoUpdate` (Task 2); version injection + About + goreleaser (Task 3); CLI `update`/`version`/`help` (Task 4); in-app notice + Auto-update setting + auto-apply + header indicator (Task 5). All spec sections mapped.
- **Type consistency:** `Apply(ctx, current, applyFn)`, `CheckLatest(ctx)`, `DisplayVersion`, `Newer` (Task 1) used verbatim in Tasks 3–5; `WithVersion`/`version` (Task 3) consumed by 4–5; `updateCheckMsg{latest,newer}`/`selfUpdatedMsg{version,err}` defined and used within Task 5; `cli(args,version,out)` (Task 4) matches its test.
- **Green-at-each-task:** the `Newer` export is defined in Task 1 (noted in Task 5 in case of out-of-order work); `update` import is added in Task 3 and reused in Tasks 4–5; the header indicator keeps the header at 6 rows so no layout math changes.
- **Note:** the in-app check/auto-update only fire on release builds (`version != "" && != "dev"`), so tests (which use `New(...)` with empty version, and drive `updateCheckMsg` directly) never touch the network.
