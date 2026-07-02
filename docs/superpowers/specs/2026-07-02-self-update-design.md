# Self-update

**Date:** 2026-07-02
**Component:** `internal/update` (new), `cmd/shoal`, `internal/config`, `internal/ui`, `.goreleaser.yaml`
**Status:** Approved (design)

## Summary

Let shoal update its own binary from GitHub Releases: a `shoal update` CLI command, a
`shoal version` command, a passive in-app "update available" notice, and an opt-in
**Auto-update** setting that applies the update automatically on launch. Built on
`github.com/minio/selfupdate` for the safe cross-platform binary swap; the release
lookup, download, checksum verification, and archive extraction use the stdlib.

## Goals

- `shoal update` — check the latest GitHub Release, and if newer, download the asset for
  the current OS/arch, verify its SHA-256, and replace the running binary.
- `shoal version` — print the build version.
- In-app notice — on launch, a non-blocking check; if a newer version exists, show a dim
  indicator in the header.
- **Auto-update setting** (opt-in, default off) — when enabled, the launch check applies
  the update automatically instead of only showing the notice.

## Non-goals (YAGNI)

- No auto-apply without the setting; no periodic/background re-checks beyond launch.
- No code-signature verification (checksum only).
- No package-manager-install detection; no hot-reload (updates take effect on next launch).

## 1. Version (build-injected)

`cmd/shoal/main.go` gains `var version = "dev"`. GoReleaser injects the tag via ldflags
(`-X main.version={{ .Version }}`, e.g. `0.2.0`). The version is threaded into the UI so
the About row shows it instead of the hardcoded `"v0.2"`:

- `internal/ui`: `Model` gains `version string`; `func (m Model) WithVersion(v string) Model`
  (mirrors `WithHistory`). The About line in `renderSettings` renders `m.version` (falling
  back to `dev` when empty). `cmd/shoal/main.go` calls `…WithVersion(version)`.

## 2. `internal/update` package

Repo constants: `repoOwner = "StrangeNoob"`, `repoName = "shoal"`.

**Pure, unit-tested helpers:**
- `isNewer(current, latest string) bool` — compare `vMAJOR.MINOR.PATCH` numerically (strip a leading `v`); a `current` of `"dev"` (or unparseable) is treated as older than any real release.
- `matchAsset(names []string, goos, goarch string) (string, bool)` — pick the release asset whose name matches `_<goos>_<goarch>.` with a `.tar.gz`/`.zip` suffix.
- `checksumFor(checksums, filename string) (string, error)` — parse a `checksums.txt` (`<sha256>␠␠<name>` lines) and return the hex SHA-256 for `filename`.
- `extractBinary(archive io.Reader, zipped bool, binName string) ([]byte, error)` — read the `shoal`/`shoal.exe` entry from a `.tar.gz` (compress/gzip + archive/tar) or `.zip` (archive/zip).

**Orchestration:**
- `type Release struct { Version string; Assets []Asset }`, `Asset{ Name, URL string }`.
- `CheckLatest(ctx context.Context) (Release, error)` — `GET https://api.github.com/repos/StrangeNoob/shoal/releases/latest`, decode `tag_name` + `assets[].{name,browser_download_url}`. Sets `Authorization: Bearer <token>` when `GITHUB_TOKEN` (or `GH_TOKEN`) is set — required while the repo is private, ignored once public. A short timeout (~10s).
- `Apply(ctx, current string, applyFn func(io.Reader) error) (updatedTo string, upToDate bool, err error)`:
  1. `CheckLatest`; if `!isNewer(current, latest.Version)` → return `upToDate=true`.
  2. `matchAsset` for `runtime.GOOS`/`runtime.GOARCH`; download it and `checksums.txt`.
  3. Compute SHA-256 of the downloaded archive; compare to `checksumFor` (mismatch → error, no apply).
  4. `extractBinary` the `shoal` binary; call `applyFn(bytes.NewReader(bin))`.
  - `applyFn` defaults to `func(r io.Reader) error { return selfupdate.Apply(r, selfupdate.Options{}) }`; tests inject a capturing stub so no real binary is swapped.

## 3. CLI dispatch (`cmd/shoal/main.go`)

Before launching the TUI, dispatch on `os.Args[1]`:
- `version` / `--version` / `-v` → print `shoal <version>`.
- `update` → run the update with plain stdout progress:
  `Checking for updates…` → `Already on the latest version (vX).` **or** `Updating vX → vY…` → `Updated to vY — restart shoal to use it.` (errors go to stderr, exit 1).
- `help` / `--help` / `-h` → a short usage blurb.
- no args (or anything else) → launch the TUI (unchanged).

Keep `main` thin — the update flow lives in `internal/update` so it is testable.

## 4. Auto-update setting (`internal/config`)

- `Config` gains `AutoUpdate bool` (`json:"auto_update"`), default **false** in `Default()`.
- `internal/ui` `settingItems()` gains an entry under a new group **`UPDATES`**:
  `Auto-update` (kind enum, options `on`/`off`), `get` maps `cfg.AutoUpdate`→`on`/`off`,
  `set` maps back and persists (same immediate-save path as the other settings).

## 5. In-app notice + auto-update on launch (`internal/ui`)

- `Model` gains `updateAvail string` (latest version when newer).
- `Init` adds `checkUpdateCmd(m.version)` to its batch **only when** `m.version != "" && m.version != "dev"` (release builds only).
- `checkUpdateCmd(cur)` runs `update.CheckLatest` (with a timeout) off the UI goroutine and returns `updateCheckMsg{latest string, newer bool}` (silent on error → `newer=false`).
- `Update`:
  - `updateCheckMsg`: if `newer`, set `updateAvail = latest`; then, **iff `m.cfg.AutoUpdate`**, return `autoUpdateCmd(m.version)`.
  - `autoUpdateCmd` runs `update.Apply` and returns `selfUpdatedMsg{version string, err error}`.
  - `selfUpdatedMsg`: on success set a notice `↑ vX installed — restart shoal`; on error, silent (or a dim error notice).
- **Header indicator** (`branding.go`): when `updateAvail != ""` and not auto-updating, the
  banner header shows a dim line `↑ v<updateAvail> available · run 'shoal update'` (beneath
  the tagline). When auto-update installed it, the notice/toast conveys "installed — restart".
  Compact (small-terminal) header omits the indicator to stay one line.
  The version is threaded to `branding.go` via the existing `Model` (it already renders the
  header), so no new plumbing beyond the `updateAvail`/`version` fields.

## 6. GoReleaser

Add `-X main.version={{ .Version }}` to the `shoal` build's `ldflags` (keep `-s -w`).

## Error handling

- All network/check/update failures in the UI path are non-blocking and silent (the app is
  fully usable offline); `shoal update` surfaces errors on stderr with a non-zero exit.
- Checksum mismatch → hard error, binary is **not** replaced.
- Private repo without a token → `CheckLatest` returns an auth error; the in-app check
  swallows it (no notice), `shoal update` prints a clear "could not reach releases (set
  GITHUB_TOKEN while the repo is private)" message.
- `dev` builds never check or auto-update.

## Testing (TDD)

**`internal/update`**
- `isNewer`: table (older/newer/equal, missing patch, `dev`, leading `v`).
- `matchAsset`: picks the right `_<goos>_<goarch>.` asset; returns `false` when absent.
- `checksumFor`: returns the sha for a name; error when absent.
- `extractBinary`: build a `.tar.gz` and a `.zip` in-test containing a `shoal` file; assert the extracted bytes.
- `Apply`: integration test against an `httptest` server serving a fake `releases/latest` JSON, an archive asset, and `checksums.txt`; inject a capturing `applyFn`; assert it applies the right bytes, is a no-op when not newer, and errors on checksum mismatch.

**`internal/config`**
- Round-trip/default test extended: `AutoUpdate` defaults false and survives save/load.

**`internal/ui`**
- `WithVersion` → About row shows the version.
- `updateCheckMsg{newer:true}` sets `updateAvail`; header renders `↑ v… available`.
- With `cfg.AutoUpdate=true`, a `newer` `updateCheckMsg` returns a non-nil auto-update command; with it false, it does not.
- `Auto-update` appears in `settingItems()` and toggling it flips `cfg.AutoUpdate`.

## Files touched

- Create: `internal/update/update.go` + `internal/update/update_test.go`.
- Modify: `cmd/shoal/main.go` (version var, arg dispatch, `WithVersion`).
- Modify: `internal/config/config.go` (+`AutoUpdate`) and `config_test.go`.
- Modify: `internal/ui/model.go` (version/updateAvail fields, `WithVersion`, `checkUpdateCmd`/`updateCheckMsg`/`autoUpdateCmd`/`selfUpdatedMsg`, `Init`, `Update`, `settingItems` UPDATES group), `internal/ui/view.go` (About version), `internal/ui/branding.go` (header indicator), plus `model_test.go`/`view_test.go`.
- Modify: `.goreleaser.yaml` (version ldflag).
- Modify: `go.mod`/`go.sum` (add `github.com/minio/selfupdate`).
