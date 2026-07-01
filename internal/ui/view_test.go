package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"shoal/internal/engine"
	"shoal/internal/source"
)

func TestInitReturnsCommand(t *testing.T) {
	if New(&fakeSource{}, &fakeEngine{}).Init() == nil {
		t.Error("Init should return a batched command")
	}
}

func TestSectionString(t *testing.T) {
	cases := map[section]string{
		sectionSearch:    "Search",
		sectionDownloads: "Downloads",
		sectionSeeding:   "Seeding",
		sectionSettings:  "Settings",
	}
	for s, want := range cases {
		if s.String() != want {
			t.Errorf("section(%d).String() = %q, want %q", s, s.String(), want)
		}
	}
}

func TestRenderResultsListWithOverflow(t *testing.T) {
	m := ready(New(&fakeSource{}, &fakeEngine{}))
	m.hasSearched = true
	m.results = make([]source.Result, 30)
	for i := range m.results {
		m.results[i] = source.Result{Title: fmt.Sprintf("Result %d", i), Source: "IA", SizeBytes: 1024, Popularity: int64(i)}
	}
	v := m.View() // Search pane with a long list
	if !strings.Contains(v, "Result 0") {
		t.Error("results view should list the first result")
	}
	if !strings.Contains(v, glyphMore) {
		t.Error("an overflowing list should show the 'more' indicator")
	}
}

func TestRenderResultsEmptyFilter(t *testing.T) {
	src := []source.Result{{Title: "A Song", Category: "audio"}}
	m := ready(New(&fakeSource{}, &fakeEngine{}))
	m.hasSearched = true
	m.results = src
	m.filter = filterIndex("Movies") // nothing matches the lone audio result
	if !strings.Contains(m.View(), "No matches") {
		t.Error("a filter with no matches should show a 'No matches' hint")
	}
}

func TestRenderResultsTable(t *testing.T) {
	m := ready(New(&fakeSource{}, &fakeEngine{}))
	m.hasSearched = true
	m.results = []source.Result{
		{Title: "Magic Mike (2012) 1080p", Source: "TPB", SizeBytes: 1_700_000_000, Seeders: 69, Leechers: 12},
	}
	m.sourcesDone, m.sourcesTotal, m.searching = 6, 10, true
	m.sortField = sortSize
	m.sortDesc = true

	out := m.renderResults(80, 20)
	for _, want := range []string{"Results (1)", "searching… 6/10 sources", "Seed:Lch", "69:12", "TPB", "Size"} {
		if !strings.Contains(out, want) {
			t.Fatalf("renderResults missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "Size▼") {
		t.Fatalf("sort arrow not attached to Size header:\n%s", out)
	}
}

func TestRenderResultsArrowFollowsSortField(t *testing.T) {
	m := ready(New(&fakeSource{}, &fakeEngine{}))
	m.hasSearched = true
	m.results = []source.Result{
		{Title: "Magic Mike (2012) 1080p", Source: "TPB", SizeBytes: 1_700_000_000, Seeders: 69, Leechers: 12},
	}
	m.sortField = sortLeechers
	m.sortDesc = true

	out := m.renderResults(80, 20)
	if !strings.Contains(out, "Seed:Lch▼") {
		t.Fatalf("sortLeechers active sort should light the Seed:Lch column:\n%s", out)
	}
}

func TestRenderResultsSortBarInSortMode(t *testing.T) {
	m := ready(New(&fakeSource{}, &fakeEngine{}))
	m.hasSearched = true
	m.results = []source.Result{{Title: "x", Source: "TPB", Seeders: 1}}
	m.sortMode = true
	m.sortField = sortSeeders
	m.sortCol = 1

	out := m.renderResults(80, 20)
	if !strings.Contains(out, "Sort") || !strings.Contains(out, "Seeders") || !strings.Contains(out, "Leechers") {
		t.Fatalf("sort bar missing in sort mode:\n%s", out)
	}
}

func TestRenderSettingsPane(t *testing.T) {
	m := ready(New(&fakeSource{}, &fakeEngine{}))
	m.section = sectionSettings
	v := m.View()
	for _, want := range []string{"APPEARANCE", "DOWNLOADS", "Theme", "Save to", "Twilight", "ABOUT"} {
		if !strings.Contains(v, want) {
			t.Errorf("settings view missing %q", want)
		}
	}

	// Open the inline editor on a text row to render the editing branch.
	m.editing = false
	m.section = sectionSettings
	m.setCursor = 2 // "Save to"
	edit, _ := update(m, key("enter"))
	if !edit.editingSetting {
		t.Fatal("enter on a text setting should open the editor")
	}
	_ = edit.View() // exercises the sel&&editingSetting value branch
}

func TestRenderSeedingShowsRatio(t *testing.T) {
	eng := &fakeEngine{statuses: []engine.Status{
		{Name: "Done", TotalBytes: 1000, CompletedBytes: 1000, Uploaded: 2000, Peers: 2, Done: true},
	}}
	m := ready(New(&fakeSource{}, eng))
	m, _ = update(m, tickMsg(time.Now()))
	m.section = sectionSeeding
	v := m.View()
	if !strings.Contains(v, "ratio") || !strings.Contains(v, "complete") {
		t.Errorf("seeding view should show ratio + complete, got:\n%s", v)
	}
}

func TestMoveLeftFilterAndSetting(t *testing.T) {
	// Search: → then ← returns the filter to All.
	m := ready(New(&fakeSource{}, &fakeEngine{}))
	m.editing = false
	m.hasSearched = true
	m.results = []source.Result{{Title: "x", Category: "movies"}}
	m, _ = update(m, key("right"))
	if m.filter != 1 {
		t.Fatalf("filter after → = %d, want 1", m.filter)
	}
	m, _ = update(m, key("left"))
	if m.filter != 0 {
		t.Errorf("filter after ← = %d, want 0", m.filter)
	}

	// Settings: ← on the Theme enum wraps Twilight → Tide.
	s := ready(New(&fakeSource{}, &fakeEngine{}))
	s.editing = false
	s.section = sectionSettings
	s, _ = update(s, key("left"))
	if s.cfg.Theme != "Tide" {
		t.Errorf("← on Theme = %q, want Tide (wrap)", s.cfg.Theme)
	}
}

func TestDownloadMagnetOnlyResult(t *testing.T) {
	eng := &fakeEngine{}
	m := ready(New(&fakeSource{}, eng))
	m.editing = false
	m.hasSearched = true
	m.results = []source.Result{{Title: "Open Movie", Magnet: "magnet:?xt=urn:btih:abc"}}

	m, cmd := update(m, key("d"))
	if cmd == nil {
		t.Fatal("d should return a command for a magnet-only result")
	}
	cmd()
	if eng.addedMagnet != "magnet:?xt=urn:btih:abc" {
		t.Errorf("magnet-only result should call AddMagnet, got addedMagnet=%q", eng.addedMagnet)
	}
	if eng.addedURL != "" {
		t.Errorf("magnet-only result should not call AddTorrentURL, got addedURL=%q", eng.addedURL)
	}
}

func TestApplyColorModeBranches(t *testing.T) {
	for _, mode := range []string{"truecolor", "256", "off", "auto", "unknown"} {
		applyColorMode(mode) // must not panic for any value
	}
	applyColorMode("auto") // leave detection on for any later tests
}

func TestRenderDetail(t *testing.T) {
	m := ready(New(&fakeSource{}, &fakeEngine{}))
	m.showDetail = true
	m.detail = source.Result{
		Title:     "Magic Mike (2012) 1080p BrRip x264 - YIFY",
		Source:    "TPB",
		SizeBytes: 1_700_000_000,
		Seeders:   69,
		Leechers:  12,
		Files:     3,
		Added:     time.Now().Add(-48 * time.Hour).Unix(),
		Magnet:    "magnet:?xt=urn:btih:1681fba79fa80d6db6916975e8dafb637058c87b&dn=Magic",
	}

	out := m.renderDetail(80, 24)
	// Note: the seeder count and " seeders …" are separately styled spans, so
	// assert on the contiguous "seeders · N leechers" run, not "69 seeders".
	for _, want := range []string{"Details", "Magic Mike", "Size", "Health", "seeders · 12 leechers", "Files", "Added", "Hash", "1681fba7", "Magnet", "d Download", "y Copy magnet", "esc back"} {
		if !strings.Contains(out, want) {
			t.Fatalf("renderDetail missing %q:\n%s", want, out)
		}
	}
}
