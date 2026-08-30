package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/StrangeNoob/shoal/internal/config"
	"github.com/StrangeNoob/shoal/internal/engine"
	"github.com/StrangeNoob/shoal/internal/subtitles"
)

// isolateConfig points HOME/XDG at a temp dir so config.Load()/Save() use a
// scratch file, same trick as cli_sources_test.go / cli_history_test.go.
func isolateConfig(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, ".config"))
	return tmp
}

// setSubsConfig saves a config with the given API key/lang so runSubs' config.Load() sees it.
func setSubsConfig(t *testing.T, apiKey, lang string) {
	t.Helper()
	cfg := config.Load()
	cfg.OpenSubsAPIKey = apiKey
	if lang != "" {
		cfg.SubsLang = lang
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
}

// captureStderr redirects os.Stderr for the duration of fn and returns what
// was written. runSubs (like the rest of cmd/shoal) writes user-facing errors
// straight to os.Stderr rather than through the io.Writer test param.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	fn()
	w.Close()
	os.Stderr = orig
	var buf bytes.Buffer
	io.Copy(&buf, r)
	return buf.String()
}

// subsFetchCall records one call made through the subsFetch seam.
type subsFetchCall struct{ apiKey, path, lang string }

// swapSubsFetch replaces the subsFetch seam with fn for the test's duration,
// recording every call. Mirrors internal/engine/anacrolix_test.go's
// swapSubsFetch, but synchronous (runSubs never fetches concurrently).
func swapSubsFetch(t *testing.T, fn func(apiKey, videoPath, lang string) (string, error)) *[]subsFetchCall {
	t.Helper()
	calls := &[]subsFetchCall{}
	orig := subsFetch
	subsFetch = func(ctx context.Context, apiKey, videoPath, lang string) (string, error) {
		*calls = append(*calls, subsFetchCall{apiKey, videoPath, lang})
		return fn(apiKey, videoPath, lang)
	}
	t.Cleanup(func() { subsFetch = orig })
	return calls
}

func TestSubsNoID(t *testing.T) {
	isolateConfig(t)
	var buf bytes.Buffer
	if code := runSubs(nil, &buf); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}

func TestSubsNoAPIKey(t *testing.T) {
	isolateConfig(t) // no key saved: config.Load() returns OpenSubsAPIKey == ""

	var buf bytes.Buffer
	var code int
	stderr := captureStderr(t, func() {
		code = runSubs([]string{"abc"}, &buf)
	})
	if code == 0 {
		t.Fatalf("exit = 0, want non-zero when no API key is configured")
	}
	if !strings.Contains(stderr, "Settings → OS API key") {
		t.Errorf("stderr missing Settings entry name:\n%s", stderr)
	}
	if !strings.Contains(stderr, "opensubs_api_key") {
		t.Errorf("stderr missing config field name:\n%s", stderr)
	}
}

// runSubs' path join now routes through engine.AbsFilePath — its three-shape
// join behavior (single-file, multi-file, folder-with-one-file) is covered
// by internal/engine's own TestAbsFilePath. TestSubsEndToEndSingleFileDefaultRule
// and TestSubsEndToEndMultiFileDefaultRule below pin the exact paths runSubs
// resolves through that call site.

// A crafted "../" FileDetail.Path (raw torrent metadata — DisplayPath does no
// sanitizing) must be skipped rather than fetched: engine.AbsFilePath returns
// "" for it, and runSubs reports the file on stderr instead of calling
// subsFetch.
func TestSubsEscapePathSkipsWithStderrMessage(t *testing.T) {
	isolateConfig(t)
	setSubsConfig(t, "test-key", "en")
	swapSubsFetch(t, func(apiKey, videoPath, lang string) (string, error) {
		t.Fatalf("subsFetch should not be called for an escaping path: %s", videoPath)
		return "", nil
	})

	dataDir := t.TempDir()
	torrentDir := filepath.Join(dataDir, "My Show")
	fake := &fakeEngine{
		statuses: []engine.Status{{InfoHash: "abc" + strings.Repeat("0", 37), Name: "My Show", Path: torrentDir}},
		detail: engine.Detail{Files: []engine.FileDetail{
			{Path: "../../evil.mkv", Length: 200 << 20, Selected: true},
			{Path: "ok.mkv", Length: 100, Selected: true}, // too small to qualify itself; keeps len(files) > 1
		}},
	}
	serveFakeDaemon(t, fake)

	var buf bytes.Buffer
	var code int
	stderr := captureStderr(t, func() { code = runSubs([]string{"abc"}, &buf) })
	if code == 0 {
		t.Fatalf("exit = 0, want non-zero: the only qualifying file escapes the torrent directory")
	}
	if !strings.Contains(stderr, "evil.mkv") {
		t.Errorf("stderr should name the escaping file, got %q", stderr)
	}
	if strings.TrimSpace(buf.String()) != "" {
		t.Errorf("stdout = %q, want empty (nothing fetched)", buf.String())
	}
}

func TestSubsTargetFilesDefaultRule(t *testing.T) {
	files := []engine.FileDetail{
		{Path: "movie.mkv", Length: 200 << 20, Selected: true},      // qualifies: video ext + big
		{Path: "sample.mkv", Length: 1 << 20, Selected: true},       // too small
		{Path: "movie.srt", Length: 200 << 20, Selected: true},      // wrong ext
		{Path: "extra/clip.mp4", Length: 150 << 20, Selected: true}, // qualifies
	}
	got := subsTargetFiles(files, nil)
	if len(got) != 2 || got[0].Path != "movie.mkv" || got[1].Path != "extra/clip.mp4" {
		t.Fatalf("subsTargetFiles(default) = %+v", got)
	}
}

func TestSubsTargetFilesGlobOverridesSizeRule(t *testing.T) {
	files := []engine.FileDetail{
		{Path: "movie.mkv", Length: 1 << 10, Selected: true}, // small — would fail the default rule
		{Path: "readme.txt", Length: 10, Selected: true},
	}
	got := subsTargetFiles(files, []string{"*.mkv"})
	if len(got) != 1 || got[0].Path != "movie.mkv" {
		t.Fatalf("subsTargetFiles(glob) = %+v, want just movie.mkv (size rule bypassed)", got)
	}
}

// A deselected file (never downloaded) must never be targeted, even when it
// otherwise qualifies — fetching a subtitle for it would write an orphaned
// .srt next to a file that doesn't exist on disk.
func TestSubsTargetFilesSkipsDeselectedDefaultRule(t *testing.T) {
	files := []engine.FileDetail{
		{Path: "movie.mkv", Length: 200 << 20, Selected: true},
		{Path: "deselected.mkv", Length: 200 << 20, Selected: false},
	}
	got := subsTargetFiles(files, nil)
	if len(got) != 1 || got[0].Path != "movie.mkv" {
		t.Fatalf("subsTargetFiles(default) = %+v, want only the selected file", got)
	}
}

func TestSubsTargetFilesSkipsDeselectedGlob(t *testing.T) {
	files := []engine.FileDetail{
		{Path: "movie.mkv", Length: 200 << 20, Selected: false},
	}
	got := subsTargetFiles(files, []string{"*.mkv"})
	if len(got) != 0 {
		t.Fatalf("subsTargetFiles(glob) = %+v, want none (the only match is deselected)", got)
	}
}

// End-to-end: single-file-shaped torrent, default rule, default lang.
func TestSubsEndToEndSingleFileDefaultRule(t *testing.T) {
	isolateConfig(t)
	setSubsConfig(t, "test-key", "fr")

	calls := swapSubsFetch(t, func(apiKey, videoPath, lang string) (string, error) {
		return videoPath + ".fr.srt", nil
	})

	dataDir := t.TempDir()
	moviePath := filepath.Join(dataDir, "movie.mkv")
	fake := &fakeEngine{
		statuses: []engine.Status{{InfoHash: "abc" + strings.Repeat("0", 37), Name: "movie", Path: moviePath}},
		detail: engine.Detail{Files: []engine.FileDetail{
			{Path: "movie.mkv", Length: 200 << 20, Selected: true},
		}},
	}
	serveFakeDaemon(t, fake)

	var buf bytes.Buffer
	if code := runSubs([]string{"abc"}, &buf); code != 0 {
		t.Fatalf("exit = %d: %s", code, buf.String())
	}
	if len(*calls) != 1 {
		t.Fatalf("subsFetch calls = %d, want 1", len(*calls))
	}
	got := (*calls)[0]
	if got.apiKey != "test-key" {
		t.Errorf("apiKey = %q, want test-key", got.apiKey)
	}
	if got.lang != "fr" {
		t.Errorf("lang = %q, want fr (from config)", got.lang)
	}
	if got.path != moviePath {
		t.Errorf("path = %q, want %q", got.path, moviePath)
	}
	if strings.TrimSpace(buf.String()) != moviePath+".fr.srt" {
		t.Errorf("stdout = %q, want the written srt path", buf.String())
	}
}

// End-to-end: multi-file-shaped torrent, default rule filters by ext+size.
func TestSubsEndToEndMultiFileDefaultRule(t *testing.T) {
	isolateConfig(t)
	setSubsConfig(t, "test-key", "en")

	calls := swapSubsFetch(t, func(apiKey, videoPath, lang string) (string, error) {
		return videoPath + ".en.srt", nil
	})

	dataDir := t.TempDir()
	torrentDir := filepath.Join(dataDir, "My Show")
	fake := &fakeEngine{
		statuses: []engine.Status{{InfoHash: "abc" + strings.Repeat("0", 37), Name: "My Show", Path: torrentDir}},
		detail: engine.Detail{Files: []engine.FileDetail{
			{Path: "Season 1/ep01.mkv", Length: 200 << 20, Selected: true},
			{Path: "Season 1/sample.mkv", Length: 1 << 20, Selected: true}, // too small
			{Path: "Season 1/ep01.nfo", Length: 100, Selected: true},       // wrong ext
		}},
	}
	serveFakeDaemon(t, fake)

	var buf bytes.Buffer
	if code := runSubs([]string{"abc"}, &buf); code != 0 {
		t.Fatalf("exit = %d: %s", code, buf.String())
	}
	if len(*calls) != 1 {
		t.Fatalf("subsFetch calls = %d, want 1: %+v", len(*calls), *calls)
	}
	wantPath := filepath.Join(torrentDir, "Season 1", "ep01.mkv")
	if (*calls)[0].path != wantPath {
		t.Errorf("path = %q, want %q", (*calls)[0].path, wantPath)
	}
}

func TestSubsFilesFlagSelectsByGlob(t *testing.T) {
	isolateConfig(t)
	setSubsConfig(t, "test-key", "en")

	calls := swapSubsFetch(t, func(apiKey, videoPath, lang string) (string, error) {
		return videoPath + ".en.srt", nil
	})

	dataDir := t.TempDir()
	torrentDir := filepath.Join(dataDir, "My Show")
	fake := &fakeEngine{
		statuses: []engine.Status{{InfoHash: "abc" + strings.Repeat("0", 37), Name: "My Show", Path: torrentDir}},
		detail: engine.Detail{Files: []engine.FileDetail{
			{Path: "Season 1/ep01.mkv", Length: 1 << 10, Selected: true}, // small, but glob overrides
			{Path: "Season 1/ep02.mkv", Length: 200 << 20, Selected: true},
			{Path: "Season 1/ep02.nfo", Length: 100, Selected: true},
		}},
	}
	serveFakeDaemon(t, fake)

	var buf bytes.Buffer
	if code := runSubs([]string{"abc", "--files", "ep01*"}, &buf); code != 0 {
		t.Fatalf("exit = %d: %s", code, buf.String())
	}
	if len(*calls) != 1 {
		t.Fatalf("subsFetch calls = %d, want 1: %+v", len(*calls), *calls)
	}
	wantPath := filepath.Join(torrentDir, "Season 1", "ep01.mkv")
	if (*calls)[0].path != wantPath {
		t.Errorf("path = %q, want %q (glob should bypass the size rule)", (*calls)[0].path, wantPath)
	}
}

func TestSubsLangFlagOverridesConfig(t *testing.T) {
	isolateConfig(t)
	setSubsConfig(t, "test-key", "fr")

	calls := swapSubsFetch(t, func(apiKey, videoPath, lang string) (string, error) {
		return videoPath + "." + lang + ".srt", nil
	})

	dataDir := t.TempDir()
	moviePath := filepath.Join(dataDir, "movie.mkv")
	fake := &fakeEngine{
		statuses: []engine.Status{{InfoHash: "abc" + strings.Repeat("0", 37), Name: "movie", Path: moviePath}},
		detail: engine.Detail{Files: []engine.FileDetail{
			{Path: "movie.mkv", Length: 200 << 20, Selected: true},
		}},
	}
	serveFakeDaemon(t, fake)

	var buf bytes.Buffer
	if code := runSubs([]string{"abc", "--lang", "de"}, &buf); code != 0 {
		t.Fatalf("exit = %d: %s", code, buf.String())
	}
	if len(*calls) != 1 || (*calls)[0].lang != "de" {
		t.Fatalf("calls = %+v, want lang=de", *calls)
	}
}

func TestSubsPerFileFailureContinuesAndPartialSuccessExitsZero(t *testing.T) {
	isolateConfig(t)
	setSubsConfig(t, "test-key", "en")

	calls := swapSubsFetch(t, func(apiKey, videoPath, lang string) (string, error) {
		if strings.Contains(videoPath, "bad") {
			return "", io.ErrUnexpectedEOF
		}
		return videoPath + ".en.srt", nil
	})

	dataDir := t.TempDir()
	torrentDir := filepath.Join(dataDir, "My Show")
	fake := &fakeEngine{
		statuses: []engine.Status{{InfoHash: "abc" + strings.Repeat("0", 37), Name: "My Show", Path: torrentDir}},
		detail: engine.Detail{Files: []engine.FileDetail{
			{Path: "bad.mkv", Length: 200 << 20, Selected: true},
			{Path: "good.mkv", Length: 200 << 20, Selected: true},
		}},
	}
	serveFakeDaemon(t, fake)

	var buf bytes.Buffer
	var code int
	stderr := captureStderr(t, func() {
		code = runSubs([]string{"abc"}, &buf)
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (one file succeeded): %s / %s", code, buf.String(), stderr)
	}
	if len(*calls) != 2 {
		t.Fatalf("subsFetch calls = %d, want 2", len(*calls))
	}
	out := buf.String()
	if strings.Contains(out, "bad.mkv") {
		t.Errorf("stdout should not mention the failed file's srt path:\n%s", out)
	}
	if !strings.Contains(out, filepath.Join(torrentDir, "good.mkv")) {
		t.Errorf("stdout missing the successful srt path:\n%s", out)
	}
	if !strings.Contains(stderr, "bad.mkv") {
		t.Errorf("stderr should mention the failed file:\n%s", stderr)
	}
}

// A rate-limit or bad-key error means every remaining fetch will fail the
// same way, so runSubs must stop the loop instead of burning through the
// rest of the targets one failed request at a time.
func TestSubsRateLimitedStopsLoop(t *testing.T) {
	isolateConfig(t)
	setSubsConfig(t, "test-key", "en")

	calls := swapSubsFetch(t, func(apiKey, videoPath, lang string) (string, error) {
		return "", subtitles.ErrRateLimited
	})

	dataDir := t.TempDir()
	torrentDir := filepath.Join(dataDir, "My Show")
	fake := &fakeEngine{
		statuses: []engine.Status{{InfoHash: "abc" + strings.Repeat("0", 37), Name: "My Show", Path: torrentDir}},
		detail: engine.Detail{Files: []engine.FileDetail{
			{Path: "ep01.mkv", Length: 200 << 20, Selected: true},
			{Path: "ep02.mkv", Length: 200 << 20, Selected: true},
			{Path: "ep03.mkv", Length: 200 << 20, Selected: true},
		}},
	}
	serveFakeDaemon(t, fake)

	var buf bytes.Buffer
	var code int
	_ = captureStderr(t, func() {
		code = runSubs([]string{"abc"}, &buf)
	})
	if code == 0 {
		t.Fatalf("exit = 0, want non-zero when rate limited")
	}
	if len(*calls) != 1 {
		t.Fatalf("subsFetch calls = %d, want 1 (loop should stop after ErrRateLimited)", len(*calls))
	}
}

func TestSubsAllFilesFailExitsNonZero(t *testing.T) {
	isolateConfig(t)
	setSubsConfig(t, "test-key", "en")

	swapSubsFetch(t, func(apiKey, videoPath, lang string) (string, error) {
		return "", io.ErrUnexpectedEOF
	})

	dataDir := t.TempDir()
	moviePath := filepath.Join(dataDir, "movie.mkv")
	fake := &fakeEngine{
		statuses: []engine.Status{{InfoHash: "abc" + strings.Repeat("0", 37), Name: "movie", Path: moviePath}},
		detail: engine.Detail{Files: []engine.FileDetail{
			{Path: "movie.mkv", Length: 200 << 20, Selected: true},
		}},
	}
	serveFakeDaemon(t, fake)

	var buf bytes.Buffer
	var code int
	_ = captureStderr(t, func() {
		code = runSubs([]string{"abc"}, &buf)
	})
	if code == 0 {
		t.Fatalf("exit = 0, want non-zero when every file failed")
	}
	if strings.TrimSpace(buf.String()) != "" {
		t.Errorf("stdout should be empty on total failure, got %q", buf.String())
	}
}

func TestSubsNoQualifyingFilesExitsNonZero(t *testing.T) {
	isolateConfig(t)
	setSubsConfig(t, "test-key", "en")
	swapSubsFetch(t, func(apiKey, videoPath, lang string) (string, error) {
		t.Fatal("subsFetch should not be called when no file qualifies")
		return "", nil
	})

	dataDir := t.TempDir()
	moviePath := filepath.Join(dataDir, "readme.txt")
	fake := &fakeEngine{
		statuses: []engine.Status{{InfoHash: "abc" + strings.Repeat("0", 37), Name: "readme", Path: moviePath}},
		detail: engine.Detail{Files: []engine.FileDetail{
			{Path: "readme.txt", Length: 10, Selected: true},
		}},
	}
	serveFakeDaemon(t, fake)

	var buf bytes.Buffer
	var code int
	stderr := captureStderr(t, func() { code = runSubs([]string{"abc"}, &buf) })
	if code == 0 {
		t.Fatalf("exit = 0, want non-zero when nothing qualifies")
	}
	// "nothing qualified" is a different problem from "every fetch failed".
	if !strings.Contains(stderr, "no matching files") {
		t.Errorf("stderr = %q, want it to say no matching files", stderr)
	}
}

func TestSubsFilesGlobMatchesOnlyDeselectedFileExitsNonZero(t *testing.T) {
	isolateConfig(t)
	setSubsConfig(t, "test-key", "en")
	swapSubsFetch(t, func(apiKey, videoPath, lang string) (string, error) {
		t.Fatal("subsFetch should not be called for a deselected file")
		return "", nil
	})

	dataDir := t.TempDir()
	moviePath := filepath.Join(dataDir, "movie.mkv")
	fake := &fakeEngine{
		statuses: []engine.Status{{InfoHash: "abc" + strings.Repeat("0", 37), Name: "movie", Path: moviePath}},
		detail: engine.Detail{Files: []engine.FileDetail{
			{Path: "movie.mkv", Length: 200 << 20, Selected: false},
		}},
	}
	serveFakeDaemon(t, fake)

	var buf bytes.Buffer
	if code := runSubs([]string{"abc", "--files", "*.mkv"}, &buf); code == 0 {
		t.Fatalf("exit = 0, want non-zero when the only glob match is deselected")
	}
}
