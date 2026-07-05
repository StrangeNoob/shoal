package ui

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/StrangeNoob/shoal/internal/engine"
	"github.com/StrangeNoob/shoal/internal/source"
)

func TestNewlyCompleted(t *testing.T) {
	prev := []engine.Status{
		{InfoHash: "a", Name: "Alpha", Done: false},
		{InfoHash: "b", Name: "Beta", Done: true}, // already done before
	}
	next := []engine.Status{
		{InfoHash: "a", Name: "Alpha", Done: true}, // just finished → notify
		{InfoHash: "b", Name: "Beta", Done: true},  // still done, no re-notify
		{InfoHash: "c", Name: "Gamma", Done: true}, // new & already done → no notify
	}
	got := newlyCompleted(prev, next)
	if len(got) != 1 || got[0] != "Alpha" {
		t.Fatalf("newlyCompleted = %v, want [Alpha]", got)
	}
	// First poll (empty prev) must not notify for pre-existing finished torrents.
	if n := newlyCompleted(nil, next); len(n) != 0 {
		t.Fatalf("first poll should not notify, got %v", n)
	}
}

func TestEtaSeconds(t *testing.T) {
	// 100 MiB left at 10 MiB/s → 10s.
	got := etaSeconds(engine.Status{TotalBytes: 200 << 20, CompletedBytes: 100 << 20}, 10<<20)
	if got != 10 {
		t.Errorf("eta = %d, want 10", got)
	}
	// Unestimatable cases → 0.
	if etaSeconds(engine.Status{TotalBytes: 100, CompletedBytes: 100}, 10) != 0 {
		t.Error("complete torrent should have eta 0")
	}
	if etaSeconds(engine.Status{TotalBytes: 0, CompletedBytes: 0}, 10) != 0 {
		t.Error("unknown total should have eta 0")
	}
	if etaSeconds(engine.Status{TotalBytes: 100, CompletedBytes: 10}, 0) != 0 {
		t.Error("zero speed should have eta 0")
	}
}

func TestFormatETA(t *testing.T) {
	cases := map[int64]string{
		0:            "",
		-5:           "",
		9:            "9s",
		75:           "1m15s",
		3600 + 125:   "1h02m",
		100*3600 + 1: "99h+",
	}
	for sec, want := range cases {
		if got := formatETA(sec); got != want {
			t.Errorf("formatETA(%d) = %q, want %q", sec, got, want)
		}
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello", 4, "hel…"},
		{"hello", 1, "h"},
		{"hello", 0, ""},
		{"héllo", 3, "hé…"}, // rune-aware
	}
	for _, c := range cases {
		if got := truncate(c.in, c.n); got != c.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
}

func TestFormatBytes(t *testing.T) {
	cases := map[int64]string{
		0:          "0 B",
		512:        "512 B",
		1024:       "1.0 KiB",
		1536:       "1.5 KiB",
		1048576:    "1.0 MiB",
		1073741824: "1.0 GiB",
	}
	for in, want := range cases {
		if got := formatBytes(in); got != want {
			t.Errorf("formatBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestAsMagnet(t *testing.T) {
	if got := asMagnet("  magnet:?xt=urn:btih:abc  "); got != "magnet:?xt=urn:btih:abc" {
		t.Errorf("asMagnet(magnet) = %q", got)
	}
	if got := asMagnet("MAGNET:?xt=abc"); got != "MAGNET:?xt=abc" {
		t.Errorf("asMagnet(upper) = %q, want preserved original", got)
	}
	if got := asMagnet("http://example/x"); got != "" {
		t.Errorf("asMagnet(http) = %q, want empty", got)
	}
}

func TestPadOrTrim(t *testing.T) {
	if got := padOrTrim("hi", 5); got != "hi   " {
		t.Errorf("padOrTrim pad = %q, want %q", got, "hi   ")
	}
	if got := padOrTrim("hello world", 5); got != "hell…" {
		t.Errorf("padOrTrim trim = %q, want %q", got, "hell…")
	}
	if got := padOrTrim("x", 0); got != "" {
		t.Errorf("padOrTrim(_, 0) = %q, want empty", got)
	}
}

func TestThousands(t *testing.T) {
	cases := map[int64]string{
		0:       "0",
		42:      "42",
		1234:    "1,234",
		1234567: "1,234,567",
		-5:      "0",
	}
	for in, want := range cases {
		if got := thousands(in); got != want {
			t.Errorf("thousands(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestSizeOrDash(t *testing.T) {
	if got := sizeOrDash(0); got != "—" {
		t.Errorf("sizeOrDash(0) = %q, want —", got)
	}
	if got := sizeOrDash(-1); got != "—" {
		t.Errorf("sizeOrDash(-1) = %q, want —", got)
	}
	if got := sizeOrDash(1024); got != "1.0 KiB" {
		t.Errorf("sizeOrDash(1024) = %q, want 1.0 KiB", got)
	}
}

func TestLeechSeedRatio(t *testing.T) {
	if got := leechSeedRatio(source.Result{Seeders: 10, Leechers: 5}); got != 0.5 {
		t.Fatalf("ratio = %v, want 0.5", got)
	}
	if got := leechSeedRatio(source.Result{Seeders: 0, Leechers: 3}); !math.IsInf(got, 1) {
		t.Fatalf("ratio with 0 seeders = %v, want +Inf", got)
	}
	if got := leechSeedRatio(source.Result{Seeders: 0, Leechers: 0}); got != 0 {
		t.Fatalf("ratio with no swarm = %v, want 0", got)
	}
}

func TestApplySort(t *testing.T) {
	rs := []source.Result{
		{Title: "a", SizeBytes: 100, Seeders: 1, Leechers: 9, Popularity: 1},
		{Title: "b", SizeBytes: 300, Seeders: 9, Leechers: 1, Popularity: 9},
		{Title: "c", SizeBytes: 200, Seeders: 5, Leechers: 0, Popularity: 5},
	}
	applySort(rs, sortSize, true) // desc
	if rs[0].Title != "b" || rs[2].Title != "a" {
		t.Fatalf("size desc order = %v", titles(rs))
	}
	applySort(rs, sortSeeders, false) // asc
	if rs[0].Title != "a" || rs[2].Title != "b" {
		t.Fatalf("seeders asc order = %v", titles(rs))
	}
	applySort(rs, sortNone, true) // by Popularity desc
	if rs[0].Title != "b" || rs[2].Title != "a" {
		t.Fatalf("default (popularity) order = %v", titles(rs))
	}
}

// NOTE: `titles([]source.Result) []string` already exists in model_test.go
// (same package `ui`) — reuse it; do NOT redeclare it here.

func TestRelTime(t *testing.T) {
	now := time.Now().Unix()
	cases := map[int64]string{
		0:               "",
		now - 30:        "just now",
		now - 3*3600:    "3h ago",
		now - 2*86400:   "2d ago",
		now - 400*86400: "1y ago",
	}
	for in, want := range cases {
		if got := relTime(in); got != want {
			t.Errorf("relTime(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestSeedLeechAndRatioStr(t *testing.T) {
	if got := seedLeech(source.Result{Seeders: 69, Leechers: 12}); got != "69:12" {
		t.Fatalf("seedLeech = %q, want 69:12", got)
	}
	if got := seedLeech(source.Result{}); got != "—" {
		t.Fatalf("seedLeech (no data) = %q, want —", got)
	}
	if got := ratioStr(source.Result{Seeders: 10, Leechers: 5}); got != "0.50" {
		t.Fatalf("ratioStr = %q, want 0.50", got)
	}
	if got := ratioStr(source.Result{}); got != "—" {
		t.Fatalf("ratioStr (no data) = %q, want —", got)
	}
}

func TestTitledBoxDimensionsAndTitle(t *testing.T) {
	body := "hello\nworld"
	out := titledBox("Results (3)", "TPB", body, 30, true)
	lines := strings.Split(out, "\n")
	if len(lines) != 4 { // top border + 2 body + bottom border
		t.Fatalf("lines = %d, want 4:\n%s", len(lines), out)
	}
	for i, ln := range lines {
		if w := lipgloss.Width(ln); w != 30 {
			t.Fatalf("line %d width = %d, want 30: %q", i, w, ln)
		}
	}
	if !strings.Contains(lines[0], "Results (3)") {
		t.Fatalf("top border missing title: %q", lines[0])
	}
	if !strings.Contains(lines[0], "TPB") {
		t.Fatalf("top border missing right label: %q", lines[0])
	}
}

func TestTitledBoxNarrowClampsWidth(t *testing.T) {
	// title + right label exceed the inner width — every line must still be exactly `width`
	out := titledBox("Results (123)", "TPB", "body", 20, true)
	for i, ln := range strings.Split(out, "\n") {
		if w := lipgloss.Width(ln); w != 20 {
			t.Fatalf("narrow line %d width = %d, want 20: %q", i, w, ln)
		}
	}
	// no right label still holds the invariant
	out = titledBox("Details", "", "x", 24, false)
	for i, ln := range strings.Split(out, "\n") {
		if w := lipgloss.Width(ln); w != 24 {
			t.Fatalf("no-right line %d width = %d, want 24: %q", i, w, ln)
		}
	}
}

func TestTitledBoxTruncatesOverlongBody(t *testing.T) {
	body := strings.Repeat("x", 100) // far wider than the inner width
	out := titledBox("T", "", body, 20, false)
	for i, ln := range strings.Split(out, "\n") {
		if w := lipgloss.Width(ln); w != 20 {
			t.Fatalf("line %d width = %d, want 20: %q", i, w, ln)
		}
	}
}

func TestPadVisual(t *testing.T) {
	if got := padVisual("hi", 5); lipgloss.Width(got) != 5 {
		t.Fatalf("padVisual width = %d, want 5", lipgloss.Width(got))
	}
	if got := padVisual("toolong", 3); got != "toolong" {
		t.Fatalf("padVisual over-width should pass through, got %q", got)
	}
}
