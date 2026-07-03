# Enable / disable search sources (shared CLI + TUI)

**Date:** 2026-07-04
**Component:** `internal/config`, `internal/source`, `cmd/shoal`, `internal/ui`
**Status:** Approved (design)

## Summary

Let the user turn individual search providers on or off. The preference lives in
one place (`config.json`), so the CLI and the TUI always agree, and a toggle from
either surface takes effect on the **next search** with no restart. Disabled
sources are skipped during search.

- **CLI:** `shoal sources` shows each provider's on/off state; `shoal sources
  enable <name>` / `disable <name>` persist the change; `shoal search` respects it.
- **TUI:** a **SOURCES** group in the Settings pane with one on/off row per
  provider, persisted immediately and applied to the next search live.

## Motivation

shoal ships twelve providers; some are noise for a given user (e.g. FitGirl for
someone who only wants anime, or SolidTorrents when it's down). A persistent,
shared on/off toggle lets the user curate their sources once and have it honored
everywhere. This also realizes the README roadmap item "More sources … each
toggleable in Settings."

## Goals

- Persist an enabled/disabled state per provider in one shared config.
- Honor it in both `shoal search` (CLI) and the TUI search.
- Toggle from either surface; the other reflects it; no restart needed.
- Sensible defaults: everything enabled; newly-added providers default to enabled.

## Non-goals (YAGNI)

- No per-category or brand grouping in v1 — toggles are per provider `Name()`
  (TPB Movies and TPB TV are independent, matching `sources` / `--source`).
- No `--all` flag; a one-shot `--source <name>` already overrides a disabled source.
- No reordering / priority of sources, no per-source result caps.
- No new sources added here (this is only the toggle mechanism).

## 1. Config — the shared store (`internal/config`)

Add one field to `Config`:

```go
// Search
DisabledSources []string `json:"disabled_sources,omitempty"` // provider Name()s the user turned off
```

- **Opt-out model:** default is all-enabled; only *disabled* names are stored, so a
  provider added in a later release defaults to on without the user re-enabling.
- Stores `Name()` strings verbatim (e.g. `"EZTV"`, `"TPB Movies"`).
- `omitempty` keeps a clean file for the common (nothing-disabled) case.
- `Default()` leaves it nil. `Load()`/`Save()` need no special handling (plain JSON
  slice); the existing over-defaults unmarshal preserves it.

Helpers on `Config` (small, testable):

```go
// SourceEnabled reports whether a provider name is currently enabled.
func (c Config) SourceEnabled(name string) bool
// SetSourceEnabled adds/removes name from DisabledSources (case-insensitive match,
// dedup, stable order). No-op if already in the desired state.
func (c *Config) SetSourceEnabled(name string, enabled bool)
```

Matching is **case-insensitive** on the stored/looked-up name, but the canonical
`Name()` string is what gets stored.

## 2. Source layer (`internal/source`)

```go
// EnabledSources returns the default provider set minus any whose Name() is in
// disabled (case-insensitive). Order is preserved.
func EnabledSources(disabled []string) []Source
```

- Built on the existing `DefaultSources()`.
- `NewDefault()` (all providers) is unchanged — used where prefs don't apply.
- Preference-aware callers build `NewMulti(EnabledSources(cfg.DisabledSources)...)`.
- `sourceNames(DefaultSources())` remains the canonical name list for validation and
  display (already exists in `cmd/shoal`).

## 3. CLI (`cmd/shoal`)

### `shoal sources` (extended)
- Default output: one row per provider with its state, e.g. `on   Internet Archive`
  / `off  EZTV`, reading `config.Load()`.
- `--json`: array of `{name, enabled}` objects (was: array of names — this is a
  small shape change, documented).

### `shoal sources enable <name>` / `shoal sources disable <name>`
- Resolve `<name>` against `sourceNames(DefaultSources())` case-insensitively to the
  canonical name; unknown → error to stderr listing valid names, exit 1.
- `cfg.SetSourceEnabled(canonical, enable)` then `cfg.Save()`.
- Print a confirmation (`EZTV disabled` / `EZTV enabled`); idempotent (already in that
  state → still exit 0 with a note).
- Routing: `runSources(args, out)` dispatches on `args[0]` — `""`/absent = list,
  `enable`/`disable` = mutate. Uses `parseArgs` so flags/positionals interleave.

### `shoal search`
- Builds its source set from `source.EnabledSources(cfg.DisabledSources)` instead of
  `DefaultSources()`.
- `--source <name>` is still a one-shot filter and **overrides** the disabled state:
  it filters the *full* `DefaultSources()` (not the enabled subset), so you can search
  a disabled provider once without re-enabling it.
- **All-disabled guard:** if the enabled set is empty (and no `--source`), print
  `all sources are disabled — enable one with 'shoal sources enable <name>'` to stderr
  and exit 0 with an empty result set (`[]` for `--json`).

## 4. TUI (`internal/ui`) — live toggles

### Settings rows
Extend `settingItems()` with a **SOURCES** group: one `kindEnum` row (`on`/`off`)
per provider from `source.DefaultSources()`:

```go
{group: "SOURCES", label: s.Name(), kind: kindEnum, options: []string{"on", "off"},
  get: func(m *Model) string { if m.cfg.SourceEnabled(name) { return "on" }; return "off" },
  set: func(m *Model, v string) { m.cfg.SetSourceEnabled(name, v == "on") }},
```

(Names captured per-iteration to avoid the loop-variable closure bug.) Toggling
persists via the existing Settings save path — same mechanism as every other row —
so the CLI sees it immediately.

### Applying it live to search
Today the TUI is injected a single pre-built `source.Source` at startup
(`main.go`: `src := source.NewDefault()`). To make toggles live, the Model builds
its source set from the current config at the moment a search starts, rather than
holding a fixed one:

- The Model already holds `m.cfg`. Change the search trigger to construct
  `source.NewMulti(source.EnabledSources(m.cfg.DisabledSources)...)` for that search
  (both the batch `Search` and the streaming `SearchStream` paths the UI uses).
- This is the smallest change that honors a toggle without a restart and without a
  broader refactor of how the UI holds its source.
- All-disabled in the TUI: the search yields no sources → show the existing
  "no results / no sources" state (a short notice in the results area).

`main.go` no longer needs to pre-build and inject the source (or may inject a
config-derived one that the Model rebuilds on search) — the plan will pick the
smaller diff after reading the exact injection site.

## 5. Testing (TDD)

- **config:** `DisabledSources` JSON round-trip; `SourceEnabled` / `SetSourceEnabled`
  (enable, disable, idempotent, case-insensitive, dedup, stable order).
- **source:** `EnabledSources` filters by name, case-insensitive, preserves order,
  unknown disabled names are ignored, empty disabled → all.
- **CLI:** `sources` list output (on/off) + `--json` `{name,enabled}` shape;
  `enable`/`disable` mutate+persist against a temp config; unknown name errors;
  `runSearch` uses the enabled set and `--source` overrides a disabled provider; the
  all-disabled guard message.
- **TUI:** the SOURCES `setItem` get/set round-trips through `cfg.DisabledSources`;
  a disabled provider is absent from the set the search builds.
- No network in tests: inject fake sources / a temp config dir (`t.TempDir()`).

## Files touched

- `internal/config/config.go` — `DisabledSources` field + `SourceEnabled` /
  `SetSourceEnabled` (+ tests).
- `internal/source/default.go` (or a small new file) — `EnabledSources` (+ tests).
- `cmd/shoal/cli_sources.go` — list with state, `enable`/`disable`, JSON shape (+ tests).
- `cmd/shoal/cli_search.go` — build from `EnabledSources`, `--source` override, guard.
- `internal/ui/model.go` — SOURCES setting rows; build sources from config at search time.
- `cmd/shoal/main.go` — adjust source injection into the UI (per plan).
- `README.md` — document the toggles under the CLI / TUI sections.

## Open questions

None. Sync model (shared config, live), storage (opt-out), granularity (per provider
name), `--source` override, and no-`--all` are all decided.
