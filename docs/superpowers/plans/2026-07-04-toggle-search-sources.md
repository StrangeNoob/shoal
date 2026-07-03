# Enable / disable search sources — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let users turn individual search providers on/off, persisted in one shared config so the CLI and TUI always agree and a toggle takes effect on the next search with no restart.

**Architecture:** A `DisabledSources []string` field in `config.json` is the single source of truth (opt-out: absent = enabled). `source.EnabledSources(disabled)` filters the provider set. The CLI's `sources`/`search` and the TUI's Settings + search all read/write that one field.

**Tech Stack:** Go, stdlib only, Bubble Tea TUI, existing `internal/source` + `internal/config`.

## Global Constraints

- Go; stdlib + already-vendored deps only — **no new module dependencies**.
- TDD: write the failing test first. Commits carry **no Claude attribution** (no `Co-Authored-By`, no "Generated with").
- Config is the single shared store; **opt-out** model (default all enabled; only disabled provider `Name()`s stored). Matching is **case-insensitive**; the canonical `Name()` string is what gets stored.
- Machine output → stdout / the provided `out` writer; diagnostics/errors → stderr.
- `--source <name>` overrides a disabled source (it filters the **full** `DefaultSources()`).
- Tests never hit the network or the real config path: use real `source.DefaultSources()` (constructors are offline — network only happens in `Search`), fake sources, and for anything reading the config file, isolate with `t.Setenv("HOME", tmp)` **and** `t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, ".config"))`.
- `gofmt -l .` must be clean (CI fails otherwise); run `go vet` and the full `go test ./...` before each commit.

---

### Task 1: Config field + toggle helpers

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/sources_test.go` (create)

**Interfaces:**
- Produces:
  - `Config.DisabledSources []string` (json `disabled_sources,omitempty`)
  - `func (c Config) SourceEnabled(name string) bool`
  - `func (c *Config) SetSourceEnabled(name string, enabled bool)`

- [ ] **Step 1: Write the failing test**

```go
// internal/config/sources_test.go
package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSourceEnabledToggle(t *testing.T) {
	var c Config
	if !c.SourceEnabled("EZTV") {
		t.Fatal("a provider not in DisabledSources should be enabled by default")
	}
	c.SetSourceEnabled("EZTV", false)
	if c.SourceEnabled("EZTV") {
		t.Fatal("EZTV should now be disabled")
	}
	if c.SourceEnabled("eztv") {
		t.Fatal("lookup should be case-insensitive")
	}
	c.SetSourceEnabled("EZTV", false) // idempotent, no duplicate
	if len(c.DisabledSources) != 1 {
		t.Fatalf("disable should be idempotent, got %v", c.DisabledSources)
	}
	c.SetSourceEnabled("eztv", true) // case-insensitive re-enable
	if len(c.DisabledSources) != 0 || !c.SourceEnabled("EZTV") {
		t.Fatalf("re-enable should clear the entry, got %v", c.DisabledSources)
	}
}

func TestDisabledSourcesJSONRoundTrip(t *testing.T) {
	c := Config{DisabledSources: []string{"EZTV", "FitGirl"}}
	b, _ := json.Marshal(c)
	var back Config
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.DisabledSources) != 2 || back.DisabledSources[0] != "EZTV" {
		t.Fatalf("round-trip lost data: %v", back.DisabledSources)
	}
	if empty, _ := json.Marshal(Config{}); strings.Contains(string(empty), "disabled_sources") {
		t.Fatalf("empty DisabledSources must be omitted: %s", empty)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run 'TestSourceEnabledToggle|TestDisabledSourcesJSONRoundTrip' -v`
Expected: FAIL — `c.SourceEnabled undefined` / `DisabledSources` unknown field.

- [ ] **Step 3: Write minimal implementation**

Add `"strings"` to the imports in `internal/config/config.go`. Add the field to the `Config` struct (after the `ListenPort` line, before `// Updates`):

```go
	// Search
	DisabledSources []string `json:"disabled_sources,omitempty"` // provider Name()s the user turned off
```

Add the two methods (anywhere at file scope, e.g. after `Default`):

```go
// SourceEnabled reports whether a provider name is enabled (i.e. not present in
// DisabledSources). Matching is case-insensitive.
func (c Config) SourceEnabled(name string) bool {
	for _, d := range c.DisabledSources {
		if strings.EqualFold(d, name) {
			return false
		}
	}
	return true
}

// SetSourceEnabled turns a provider on or off, updating DisabledSources
// (case-insensitive, deduped, order-stable). Storing the given name verbatim
// when disabling.
func (c *Config) SetSourceEnabled(name string, enabled bool) {
	var kept []string
	for _, d := range c.DisabledSources {
		if !strings.EqualFold(d, name) {
			kept = append(kept, d)
		}
	}
	if !enabled {
		kept = append(kept, name)
	}
	c.DisabledSources = kept
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -v` then `go build ./...`
Expected: PASS; build clean.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/sources_test.go
git commit -m "Add DisabledSources config field with SourceEnabled/SetSourceEnabled"
```

---

### Task 2: `source.EnabledSources`

**Files:**
- Modify: `internal/source/default.go`
- Test: `internal/source/enabled_test.go` (create)

**Interfaces:**
- Consumes: `DefaultSources() []Source`, `Source.Name()`.
- Produces: `func EnabledSources(disabled []string) []Source`

- [ ] **Step 1: Write the failing test**

```go
// internal/source/enabled_test.go
package source

import "testing"

func TestEnabledSources(t *testing.T) {
	all := DefaultSources()
	if len(EnabledSources(nil)) != len(all) {
		t.Fatalf("nil disabled should return all %d, got %d", len(all), len(EnabledSources(nil)))
	}
	got := EnabledSources([]string{"eztv", "does-not-exist"}) // case-insensitive; unknown ignored
	if len(got) != len(all)-1 {
		t.Fatalf("expected one fewer than %d, got %d", len(all), len(got))
	}
	for _, s := range got {
		if s.Name() == "EZTV" {
			t.Fatal("EZTV should have been filtered out")
		}
	}
	if got[0].Name() != all[0].Name() {
		t.Fatalf("order not preserved: %q vs %q", got[0].Name(), all[0].Name())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/source/ -run TestEnabledSources -v`
Expected: FAIL — `undefined: EnabledSources`.

- [ ] **Step 3: Write minimal implementation**

In `internal/source/default.go`, add an import block (the file currently has none) and the function:

```go
package source

import "strings"

// EnabledSources returns the default provider set minus any whose Name() appears
// in disabled (case-insensitive). Order is preserved. Unknown names are ignored.
func EnabledSources(disabled []string) []Source {
	if len(disabled) == 0 {
		return DefaultSources()
	}
	off := make(map[string]bool, len(disabled))
	for _, d := range disabled {
		off[strings.ToLower(strings.TrimSpace(d))] = true
	}
	var out []Source
	for _, s := range DefaultSources() {
		if !off[strings.ToLower(s.Name())] {
			out = append(out, s)
		}
	}
	return out
}
```

(Keep the existing `NewTorlinkSources`, `DefaultSources`, `NewDefault` in the file unchanged.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/source/ -run TestEnabledSources -v` then `go build ./...`
Expected: PASS; build clean.

- [ ] **Step 5: Commit**

```bash
git add internal/source/default.go internal/source/enabled_test.go
git commit -m "Add source.EnabledSources to filter providers by disabled name"
```

---

### Task 3: CLI `sources` — show state + enable/disable

**Files:**
- Modify: `cmd/shoal/cli_sources.go`
- Test: `cmd/shoal/cli_sources_test.go` (append)

**Interfaces:**
- Consumes: `config.Load`, `config.Config.{SourceEnabled,SetSourceEnabled,Save}`, `source.DefaultSources`, `sourceNames` (existing), `parseArgs` (existing).
- Produces: reworked `runSources(args []string, out io.Writer) int`; `func matchSourceName(name string) (string, bool)`.

- [ ] **Step 1: Write the failing test**

```go
// append to cmd/shoal/cli_sources_test.go
import (
	"path/filepath"
	// (bytes, encoding/json, testing already imported by the existing file)
)

func TestMatchSourceName(t *testing.T) {
	if got, ok := matchSourceName("eztv"); !ok || got != "EZTV" {
		t.Fatalf("matchSourceName(eztv) = %q,%v want EZTV,true", got, ok)
	}
	if _, ok := matchSourceName("nope"); ok {
		t.Fatal("unknown name should not match")
	}
}

func TestSourcesEnableDisableAndStatus(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, ".config"))

	var buf bytes.Buffer
	if code := runSources([]string{"disable", "eztv"}, &buf); code != 0 {
		t.Fatalf("disable exit %d: %s", code, buf.String())
	}

	buf.Reset()
	if code := runSources([]string{"--json"}, &buf); code != 0 {
		t.Fatalf("list exit %d", code)
	}
	var rows []struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
	}
	if err := json.Unmarshal(buf.Bytes(), &rows); err != nil {
		t.Fatalf("bad json: %v\n%s", err, buf.String())
	}
	sawEZTVoff := false
	for _, r := range rows {
		if r.Name == "EZTV" {
			if r.Enabled {
				t.Fatal("EZTV should be disabled after `disable`")
			}
			sawEZTVoff = true
		}
	}
	if !sawEZTVoff {
		t.Fatal("EZTV row missing from status list")
	}

	// unknown name errors
	buf.Reset()
	if code := runSources([]string{"disable", "not-a-source"}, &buf); code != 1 {
		t.Fatalf("unknown source should exit 1, got %d", code)
	}

	// re-enable
	buf.Reset()
	if code := runSources([]string{"enable", "EZTV"}, &buf); code != 0 {
		t.Fatalf("enable exit %d", code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/shoal/ -run 'TestMatchSourceName|TestSourcesEnableDisable' -v`
Expected: FAIL — `undefined: matchSourceName` and status list is plain names (no `enabled`).

- [ ] **Step 3: Write minimal implementation**

Replace the whole body of `cmd/shoal/cli_sources.go` with:

```go
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/StrangeNoob/shoal/internal/config"
	"github.com/StrangeNoob/shoal/internal/source"
)

// matchSourceName resolves a case-insensitive name to the canonical provider Name().
func matchSourceName(name string) (string, bool) {
	for _, n := range sourceNames(source.DefaultSources()) {
		if strings.EqualFold(n, name) {
			return n, true
		}
	}
	return "", false
}

// runSources lists the search providers with their on/off state, and toggles them:
//
//	shoal sources                  # list with state
//	shoal sources enable  <name>   # turn a provider on
//	shoal sources disable <name>   # turn a provider off
func runSources(args []string, out io.Writer) int {
	action := ""
	if len(args) > 0 && (args[0] == "enable" || args[0] == "disable") {
		action, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("sources", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit JSON")
	positionals, err := parseArgs(fs, args)
	if err != nil {
		return 2
	}
	cfg := config.Load()

	if action != "" {
		name := strings.Join(positionals, " ")
		if name == "" {
			fmt.Fprintf(os.Stderr, "usage: shoal sources %s <name>\n", action)
			return 2
		}
		canonical, ok := matchSourceName(name)
		if !ok {
			fmt.Fprintf(os.Stderr, "unknown source %q; valid: %s\n",
				name, strings.Join(sourceNames(source.DefaultSources()), ", "))
			return 1
		}
		enable := action == "enable"
		cfg.SetSourceEnabled(canonical, enable)
		if err := cfg.Save(); err != nil {
			fmt.Fprintln(os.Stderr, "shoal: sources:", err)
			return 1
		}
		state := "disabled"
		if enable {
			state = "enabled"
		}
		fmt.Fprintf(out, "%s %s\n", canonical, state)
		return 0
	}

	names := sourceNames(source.DefaultSources())
	if *jsonOut {
		type row struct {
			Name    string `json:"name"`
			Enabled bool   `json:"enabled"`
		}
		rows := make([]row, len(names))
		for i, n := range names {
			rows[i] = row{Name: n, Enabled: cfg.SourceEnabled(n)}
		}
		b, _ := json.MarshalIndent(rows, "", "  ")
		fmt.Fprintln(out, string(b))
		return 0
	}
	for _, n := range names {
		state := "off"
		if cfg.SourceEnabled(n) {
			state = "on"
		}
		fmt.Fprintf(out, "%-3s %s\n", state, n)
	}
	return 0
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/shoal/ -run 'TestMatchSourceName|TestSourcesEnableDisable|TestRunSources' -v` then `go build ./...`
Expected: PASS (the older `TestRunSourcesJSON`/`TestRunSourcesPlain` from the previous `sources` command still pass — they assert names are present, which the new output still contains).

Note: if `TestRunSourcesJSON` asserted the JSON was a bare `["name",…]` array, update it to the new `[{name,enabled}]` shape as part of this task (the spec documents this shape change).

- [ ] **Step 5: Commit**

```bash
git add cmd/shoal/cli_sources.go cmd/shoal/cli_sources_test.go
git commit -m "shoal sources: show on/off state and add enable/disable subcommands"
```

---

### Task 4: `shoal search` honors the enabled set

**Files:**
- Modify: `cmd/shoal/cli_search.go`
- Test: `cmd/shoal/cli_search_test.go` (append)

**Interfaces:**
- Consumes: `config.Load`, `source.EnabledSources`, `filterSources`/`sourceNames` (existing), `source.DefaultSources`.
- Produces: `func selectSearchSources(all []source.Source, srcName string, disabled []string) (srcs []source.Source, unknownSource, allDisabled bool)`; updated `runSearch`.

- [ ] **Step 1: Write the failing test**

```go
// append to cmd/shoal/cli_search_test.go
import "github.com/StrangeNoob/shoal/internal/source" // already imported

func TestSelectSearchSources(t *testing.T) {
	all := source.DefaultSources() // offline: constructors only build structs

	// --source overrides a disabled provider (searches the full set)
	srcs, unknown, allDis := selectSearchSources(all, "eztv", []string{"EZTV"})
	if unknown || allDis || len(srcs) == 0 {
		t.Fatalf("--source should override disabled: srcs=%d unknown=%v allDis=%v", len(srcs), unknown, allDis)
	}

	// default (no --source) respects the disabled set
	srcs, _, _ = selectSearchSources(all, "", []string{"EZTV"})
	for _, s := range srcs {
		if s.Name() == "EZTV" {
			t.Fatal("disabled EZTV should not be searched by default")
		}
	}

	// all disabled → allDisabled flag
	var names []string
	for _, s := range all {
		names = append(names, s.Name())
	}
	if _, _, allDis = selectSearchSources(all, "", names); !allDis {
		t.Fatal("disabling every source should report allDisabled")
	}

	// unknown --source
	if _, unknown, _ = selectSearchSources(all, "zzzz", nil); !unknown {
		t.Fatal("an unmatched --source should report unknownSource")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/shoal/ -run TestSelectSearchSources -v`
Expected: FAIL — `undefined: selectSearchSources`.

- [ ] **Step 3: Write minimal implementation**

In `cmd/shoal/cli_search.go`, add `"github.com/StrangeNoob/shoal/internal/config"` to the imports, and the selector:

```go
// selectSearchSources decides which providers a search hits. An explicit --source
// filters the full set (overriding any disabled state); otherwise the config's
// enabled set is used.
func selectSearchSources(all []source.Source, srcName string, disabled []string) (srcs []source.Source, unknownSource, allDisabled bool) {
	if srcName != "" {
		srcs = filterSources(all, srcName)
		if len(srcs) == 0 {
			return nil, true, false
		}
		return srcs, false, false
	}
	srcs = source.EnabledSources(disabled)
	if len(srcs) == 0 {
		return nil, false, true
	}
	return srcs, false, false
}
```

Replace the source-selection block in `runSearch` (currently the `all := source.DefaultSources()` … `}` block that builds `srcs`) with:

```go
	cfg := config.Load()
	all := source.DefaultSources()
	srcs, unknownSource, allDisabled := selectSearchSources(all, *srcName, cfg.DisabledSources)
	if unknownSource {
		fmt.Fprintf(os.Stderr, "no source matches %q; available: %s\n",
			*srcName, strings.Join(sourceNames(all), ", "))
		return 1
	}
	if allDisabled {
		fmt.Fprintln(os.Stderr, "all sources are disabled — enable one with 'shoal sources enable <name>'")
		printSearch(out, []searchRow{}, *jsonOut) // empty result set (\"[]\" for --json)
		return 0
	}
```

(The following `ctx … searchCore(ctx, source.NewMulti(srcs...), …)` lines stay unchanged.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/shoal/ -run 'TestSelectSearchSources|TestSearchCore|TestToRows' -v` then `go build ./...`
Expected: PASS; build clean.

- [ ] **Step 5: Commit**

```bash
git add cmd/shoal/cli_search.go cmd/shoal/cli_search_test.go
git commit -m "shoal search: honor enabled sources; --source overrides disabled"
```

---

### Task 5: TUI — SOURCES toggles + live search

**Files:**
- Modify: `internal/ui/model.go`
- Test: `internal/ui/sources_test.go` (create)

**Interfaces:**
- Consumes: `config.Config.{SourceEnabled,SetSourceEnabled,DisabledSources}`, `source.DefaultSources`, `source.EnabledSources`, `source.NewMulti`, existing `setItem`/`settingItems`/`startSearch`.
- Produces: extended `settingItems()`; `func (m *Model) enabledSource() source.Source`.

- [ ] **Step 1: Write the failing test**

```go
// internal/ui/sources_test.go
package ui

import (
	"testing"

	"github.com/StrangeNoob/shoal/internal/config"
	"github.com/StrangeNoob/shoal/internal/source"
)

func TestSourceSettingToggles(t *testing.T) {
	m := &Model{cfg: config.Config{}}
	var eztv *setItem
	items := settingItems()
	for i := range items {
		if items[i].group == "SOURCES" && items[i].label == "EZTV" {
			eztv = &items[i]
		}
	}
	if eztv == nil {
		t.Fatal("expected a SOURCES row for EZTV")
	}
	if eztv.get(m) != "on" {
		t.Fatalf("EZTV should default to on, got %q", eztv.get(m))
	}
	eztv.set(m, "off")
	if eztv.get(m) != "off" || m.cfg.SourceEnabled("EZTV") {
		t.Fatalf("toggling off failed: get=%q disabled=%v", eztv.get(m), m.cfg.DisabledSources)
	}
}

func TestEnabledSourceHonorsConfig(t *testing.T) {
	// all providers disabled → empty multi-source
	var names []string
	for _, s := range source.DefaultSources() {
		names = append(names, s.Name())
	}
	m := &Model{cfg: config.Config{DisabledSources: names}}
	if got := m.enabledSource().Name(); got != "no sources" {
		t.Fatalf("all-disabled should build an empty source set, got %q", got)
	}
	// one disabled → the rest remain
	m2 := &Model{cfg: config.Config{DisabledSources: []string{"EZTV"}}}
	if m2.enabledSource().Name() == "no sources" {
		t.Fatal("only one disabled — the set should be non-empty")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ui/ -run 'TestSourceSettingToggles|TestEnabledSourceHonorsConfig' -v`
Expected: FAIL — no SOURCES rows; `m.enabledSource undefined`.

- [ ] **Step 3: Write minimal implementation**

In `internal/ui/model.go`, change `settingItems()` to build a slice and append a SOURCES row per provider. Replace `return []setItem{ … }` so the literal is assigned to a variable, then append:

```go
func settingItems() []setItem {
	items := []setItem{
		// ... all existing rows unchanged ...
	}
	for _, s := range source.DefaultSources() {
		name := s.Name() // capture per iteration (avoid the loop-variable closure bug)
		items = append(items, setItem{
			group: "SOURCES", label: name, kind: kindEnum, options: []string{"on", "off"},
			get: func(m *Model) string {
				if m.cfg.SourceEnabled(name) {
					return "on"
				}
				return "off"
			},
			set: func(m *Model, v string) { m.cfg.SetSourceEnabled(name, v == "on") },
		})
	}
	return items
}
```

Add the helper (near `startSearch`):

```go
// enabledSource builds the search source set from the current config, so source
// toggles take effect on the next search without a restart.
func (m *Model) enabledSource() source.Source {
	return source.NewMulti(source.EnabledSources(m.cfg.DisabledSources)...)
}
```

In `startSearch`, rebuild `m.src` from config before it is used. Insert this line immediately before `ss, ok := m.src.(streamSearcher)`:

```go
	m.src = m.enabledSource() // honor live source toggles
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/ui/ -run 'TestSourceSettingToggles|TestEnabledSourceHonorsConfig' -v` then `go test ./internal/ui/` and `go build ./...`
Expected: PASS; existing UI tests still pass; build clean.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/model.go internal/ui/sources_test.go
git commit -m "TUI: SOURCES toggles in Settings, applied live to the next search"
```

---

### Task 6: Documentation

**Files:**
- Modify: `README.md`

**Interfaces:** none (docs).

- [ ] **Step 1: Update the CLI section**

In `README.md`, under **Command line (scripting)**, extend the `sources` bullet/command block to cover the toggles. Replace the `shoal sources` line in the code block with:

```sh
shoal sources                        # list providers with on/off state (add --json)
shoal sources enable  <name>         # turn a provider on
shoal sources disable <name>         # turn a provider off
```

And add, after the `search` bullet:

> Toggles are shared with the TUI (stored in `config.json`) and take effect on the
> next search — no restart. A one-shot `--source <name>` still searches a provider
> even if it's disabled.

- [ ] **Step 2: Update the TUI Settings section**

In the **Settings** paragraph under **Using the TUI**, append:

> A **SOURCES** group lists every provider with an on/off toggle (`← →` to change);
> turning one off removes it from searches immediately, and the change is shared with
> the `shoal sources` CLI.

- [ ] **Step 3: Verify + commit**

Run: `gofmt -l . ` (should be empty — README is not Go, but confirm nothing else is dirty) and `go build ./...`.

```bash
git add README.md
git commit -m "docs: document source enable/disable in CLI and TUI"
```

---

## Self-Review

**Spec coverage:**
- Config `DisabledSources` + opt-out + helpers → Task 1. ✓
- `source.EnabledSources` → Task 2. ✓
- CLI `sources` state + enable/disable + JSON shape → Task 3. ✓
- CLI `search` honors enabled set + `--source` override + all-disabled guard → Task 4. ✓
- TUI SOURCES toggles + live rebuild + all-disabled (empty set → existing no-results) → Task 5. ✓
- Shared/live via one config.json (CLI Save + TUI persist) → Tasks 1/3/5. ✓
- Docs → Task 6. ✓
- No network / no real config in tests: real `DefaultSources()` (offline) + `t.Setenv` HOME/XDG isolation → Tasks 3–5. ✓

**Placeholder scan:** none — every step carries complete code. The one narrative note (update `TestRunSourcesJSON` if it asserted the old array shape) names the exact change.

**Type consistency:** `DisabledSources`, `SourceEnabled`, `SetSourceEnabled`, `EnabledSources`, `matchSourceName`, `selectSearchSources`, `enabledSource` are used with identical signatures across tasks. Config is the single field all surfaces read/write. `MultiSource.Name()` returning `"no sources"` for an empty set (used in the Task 5 test) is existing behavior in `internal/source/multi.go`.
