package ui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"shoal/internal/engine"
	"shoal/internal/source"
)

// --- fakes implementing the two interfaces the UI depends on ---------------

type fakeSource struct {
	results []source.Result
	err     error
}

func (f *fakeSource) Name() string { return "Fake Source" }
func (f *fakeSource) Search(ctx context.Context, query string) ([]source.Result, error) {
	return f.results, f.err
}

type fakeEngine struct {
	statuses    []engine.Status
	urlErr      error
	magErr      error
	addedURL    string
	addedName   string
	addedMagnet string
}

func (e *fakeEngine) AddTorrentURL(url, name string) error {
	e.addedURL, e.addedName = url, name
	return e.urlErr
}
func (e *fakeEngine) AddMagnet(magnet string) error {
	e.addedMagnet = magnet
	return e.magErr
}
func (e *fakeEngine) Statuses() []engine.Status { return e.statuses }
func (e *fakeEngine) Close() error              { return nil }

// --- helpers ---------------------------------------------------------------

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

func ready(m Model) Model {
	out, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = out.(Model)
	m.cfg.Path = "" // never write a real config file during tests
	return m
}

func update(m Model, msg tea.Msg) (Model, tea.Cmd) {
	out, cmd := m.Update(msg)
	return out.(Model), cmd
}

func titles(rs []source.Result) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Title
	}
	return out
}

func filterIndex(label string) int {
	for i, fc := range filterCats {
		if fc.Label == label {
			return i
		}
	}
	return 0
}

// --- tests -----------------------------------------------------------------

func TestNewModelDefaults(t *testing.T) {
	m := New(&fakeSource{}, &fakeEngine{})
	if m.section != sectionSearch {
		t.Error("default section is not Search")
	}
	if !m.editing {
		t.Error("model should start in editing mode")
	}
	if m.cfg.Theme != "Twilight" {
		t.Errorf("default theme = %q, want Twilight", m.cfg.Theme)
	}
	if !strings.Contains(m.View(), "starting shoal") {
		t.Errorf("pre-ready View = %q, want starting message", m.View())
	}
}

func TestWindowSizeMakesReady(t *testing.T) {
	m := ready(New(&fakeSource{}, &fakeEngine{}))
	if !m.ready || m.width != 100 || m.height != 30 {
		t.Errorf("after WindowSizeMsg: ready=%v width=%d height=%d", m.ready, m.width, m.height)
	}
}

func TestHomeShownBeforeFirstSearch(t *testing.T) {
	m := ready(New(&fakeSource{}, &fakeEngine{}))
	if !strings.Contains(m.View(), "Welcome to shoal") {
		t.Error("first-run Search pane should show the welcome screen")
	}
}

func TestSearchFlowPopulatesResults(t *testing.T) {
	src := &fakeSource{results: []source.Result{{Title: "A"}, {Title: "B"}}}
	m := ready(New(src, &fakeEngine{}))
	m.input.SetValue("linux")

	m, cmd := update(m, key("enter"))
	if !m.searching || m.editing {
		t.Errorf("after enter: searching=%v editing=%v, want true/false", m.searching, m.editing)
	}
	if cmd == nil {
		t.Fatal("enter in search box should return a search command")
	}
	msg := cmd()
	done, ok := msg.(searchDoneMsg)
	if !ok {
		t.Fatalf("command msg = %T, want searchDoneMsg", msg)
	}
	if len(done.results) != 2 {
		t.Fatalf("search returned %d results, want 2", len(done.results))
	}

	m, _ = update(m, done)
	if m.searching {
		t.Error("searching should be false after searchDoneMsg")
	}
	if len(m.results) != 2 || m.cursor != 0 {
		t.Errorf("results=%d cursor=%d, want 2/0", len(m.results), m.cursor)
	}
}

func TestSearchErrorSetsNotice(t *testing.T) {
	m := ready(New(&fakeSource{err: errors.New("boom")}, &fakeEngine{}))
	m.input.SetValue("q")
	m, cmd := update(m, key("enter"))
	m, _ = update(m, cmd())
	if m.err == nil {
		t.Error("expected m.err to be set on search failure")
	}
	if !strings.Contains(m.notice, "Search failed") {
		t.Errorf("notice = %q, want a 'Search failed' message", m.notice)
	}
	if !m.noticeErr {
		t.Error("a search failure should mark the notice as an error")
	}
}

func TestSearchEmptyResultsNotice(t *testing.T) {
	m := ready(New(&fakeSource{results: nil}, &fakeEngine{}))
	m.input.SetValue("q")
	m, cmd := update(m, key("enter"))
	m, _ = update(m, cmd())
	if m.notice != "No results." {
		t.Errorf("notice = %q, want 'No results.'", m.notice)
	}
	if m.noticeErr {
		t.Error("an empty-results notice is not an error")
	}
}

func TestMagnetEnterAddsDirectly(t *testing.T) {
	eng := &fakeEngine{}
	m := ready(New(&fakeSource{}, eng))
	const magnet = "magnet:?xt=urn:btih:deadbeef"
	m.input.SetValue(magnet)

	m, cmd := update(m, key("enter"))
	if cmd == nil {
		t.Fatal("enter on a magnet should return an add command")
	}
	added, ok := cmd().(addedMsg)
	if !ok {
		t.Fatalf("command msg = %T, want addedMsg", cmd())
	}
	if eng.addedMagnet != magnet {
		t.Errorf("engine got magnet %q, want %q", eng.addedMagnet, magnet)
	}
	m, _ = update(m, added)
	if m.section != sectionDownloads {
		t.Error("a successful add should switch to the Downloads pane")
	}
}

func TestNavigationAndDownloadSelection(t *testing.T) {
	eng := &fakeEngine{}
	src := &fakeSource{results: []source.Result{
		{Title: "A", TorrentURL: "u1"},
		{Title: "B", TorrentURL: "u2"},
	}}
	m := ready(New(src, eng))
	m.input.SetValue("q")
	m, cmd := update(m, key("enter"))
	m, _ = update(m, cmd()) // populate results, leaves editing mode

	m, _ = update(m, key("down"))
	if m.cursor != 1 {
		t.Errorf("cursor after down = %d, want 1", m.cursor)
	}
	m, _ = update(m, key("up"))
	if m.cursor != 0 {
		t.Errorf("cursor after up = %d, want 0", m.cursor)
	}

	m, cmd = update(m, key("d"))
	if cmd == nil {
		t.Fatal("d should return a download command")
	}
	cmd()
	if eng.addedURL != "u1" || eng.addedName != "A" {
		t.Errorf("download passed %q/%q, want u1/A", eng.addedURL, eng.addedName)
	}
}

func TestFilterNarrowsResults(t *testing.T) {
	src := &fakeSource{results: []source.Result{
		{Title: "A Film", Category: "movies"},
		{Title: "A Song", Category: "audio"},
	}}
	m := ready(New(src, &fakeEngine{}))
	m.input.SetValue("q")
	m, cmd := update(m, key("enter"))
	m, _ = update(m, cmd())

	if got := len(m.filteredResults()); got != 2 {
		t.Fatalf("All filter = %d results, want 2", got)
	}

	m.filter = filterIndex("Movies")
	fr := m.filteredResults()
	if len(fr) != 1 || fr[0].Title != "A Film" {
		t.Errorf("Movies filter = %v, want [A Film]", titles(fr))
	}
	if m.cursor != 0 {
		t.Errorf("changing filter should reset cursor, got %d", m.cursor)
	}
}

func TestFilterIncludesTorlinkCategories(t *testing.T) {
	want := map[string]string{
		"Games":  "games",
		"Movies": "movies",
		"TV":     "tv",
		"Anime":  "anime",
	}
	for _, fc := range filterCats {
		delete(want, fc.Label)
	}
	if len(want) != 0 {
		t.Fatalf("missing torlink category filters: %v", want)
	}
}

func TestTabCyclesFourPanes(t *testing.T) {
	m := ready(New(&fakeSource{}, &fakeEngine{}))
	m.editing = false
	want := []section{sectionDownloads, sectionSeeding, sectionSettings, sectionSearch}
	for _, w := range want {
		m, _ = update(m, key("tab"))
		if m.section != w {
			t.Fatalf("tab → %v, want %v", m.section, w)
		}
	}
}

func TestSettingsThemeToggle(t *testing.T) {
	m := ready(New(&fakeSource{}, &fakeEngine{}))
	m.editing = false
	for m.section != sectionSettings {
		m, _ = update(m, key("tab"))
	}
	if m.setCursor != 0 {
		t.Fatalf("settings cursor = %d, want 0 (Theme)", m.setCursor)
	}
	if m.cfg.Theme != "Twilight" {
		t.Fatalf("default theme = %q, want Twilight", m.cfg.Theme)
	}

	m, _ = update(m, key("right")) // cycle the Theme enum
	if m.cfg.Theme != "Tide" {
		t.Errorf("after →, theme = %q, want Tide", m.cfg.Theme)
	}
	if activePalette.Name != "Tide" {
		t.Errorf("active palette = %q, want Tide", activePalette.Name)
	}
}

func TestSettingsTextEdit(t *testing.T) {
	m := ready(New(&fakeSource{}, &fakeEngine{}))
	m.editing = false
	for m.section != sectionSettings {
		m, _ = update(m, key("tab"))
	}
	m.setCursor = 2 // "Save to"
	m, _ = update(m, key("enter"))
	if !m.editingSetting {
		t.Fatal("enter on a text setting should open the inline editor")
	}
	m.setInput.SetValue("/tmp/shoal")
	m, _ = update(m, key("enter"))
	if m.editingSetting {
		t.Error("enter should commit and close the editor")
	}
	if m.cfg.DataDir != "/tmp/shoal" {
		t.Errorf("Save to = %q, want /tmp/shoal", m.cfg.DataDir)
	}
}

func TestDownloadsAndSeedingSplit(t *testing.T) {
	eng := &fakeEngine{statuses: []engine.Status{
		{Name: "Downloading", TotalBytes: 1000, CompletedBytes: 500, Peers: 3},
		{Name: "Finished", TotalBytes: 1000, CompletedBytes: 1000, Done: true},
	}}
	m := ready(New(&fakeSource{}, eng))
	m, _ = update(m, tickMsg(time.Now()))

	if len(m.downloading()) != 1 || m.downloading()[0].Name != "Downloading" {
		t.Errorf("downloading() = %v, want [Downloading]", m.downloading())
	}
	if len(m.seeding()) != 1 || m.seeding()[0].Name != "Finished" {
		t.Errorf("seeding() = %v, want [Finished]", m.seeding())
	}

	m.section = sectionDownloads
	if dv := m.View(); !strings.Contains(dv, "Downloading") || strings.Contains(dv, "Finished") {
		t.Error("Downloads pane should show only in-progress torrents")
	}
	m.section = sectionSeeding
	if sv := m.View(); !strings.Contains(sv, "Finished") || !strings.Contains(sv, "complete") {
		t.Error("Seeding pane should show completed torrents as 'complete'")
	}
}

func TestTickPollsEngineAndReschedules(t *testing.T) {
	eng := &fakeEngine{statuses: []engine.Status{{Name: "X", TotalBytes: 100, CompletedBytes: 50}}}
	m := ready(New(&fakeSource{}, eng))
	m, cmd := update(m, tickMsg(time.Now()))
	if len(m.statuses) != 1 || m.statuses[0].Name != "X" {
		t.Errorf("tick did not poll statuses: %+v", m.statuses)
	}
	if cmd == nil {
		t.Error("tick should reschedule itself")
	}
}

func TestSlashEntersAndEscLeavesEditing(t *testing.T) {
	m := ready(New(&fakeSource{}, &fakeEngine{}))
	m.editing = false
	m, _ = update(m, key("/"))
	if !m.editing {
		t.Error("/ should focus the search box")
	}
	m, _ = update(m, key("esc"))
	if m.editing {
		t.Error("esc should leave the search box")
	}
}

func TestHelpToggle(t *testing.T) {
	m := ready(New(&fakeSource{}, &fakeEngine{}))
	m.editing = false
	m, _ = update(m, key("?"))
	if !m.showHelp {
		t.Error("? should open help")
	}
	if !strings.Contains(m.View(), "keys") {
		t.Error("help view should mention keys")
	}
	m, _ = update(m, key("?"))
	if m.showHelp {
		t.Error("? should close help")
	}
}

func TestQuitKeys(t *testing.T) {
	m := ready(New(&fakeSource{}, &fakeEngine{}))
	m.editing = false
	_, cmd := update(m, key("q"))
	if cmd == nil {
		t.Fatal("q should return a command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("q (command mode) should quit")
	}

	// ctrl+c quits even while editing.
	editing := ready(New(&fakeSource{}, &fakeEngine{}))
	_, cmd = update(editing, key("ctrl+c"))
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("ctrl+c should quit even while editing")
	}
}

func TestStreamingUpdatesMergeAndCount(t *testing.T) {
	m := New(source.NewCurated(), nil)
	m.searchGen = 1
	m.searching = true
	m.searchCh = make(chan source.SourceUpdate, 1)

	// current-generation update: merges, sorts, records counts
	upd := sourceUpdateMsg{gen: 1, up: source.SourceUpdate{
		Results: []source.Result{{Title: "x", Popularity: 3}, {Title: "y", Popularity: 8}},
		Done:    1, Total: 2,
	}}
	m2, _ := m.Update(upd)
	mm := m2.(Model)
	if len(mm.results) != 2 || mm.sourcesDone != 1 || mm.sourcesTotal != 2 {
		t.Fatalf("after update: results=%d done=%d total=%d", len(mm.results), mm.sourcesDone, mm.sourcesTotal)
	}
	if mm.results[0].Title != "y" { // default sort = Popularity desc
		t.Fatalf("results not health-sorted: %s first", mm.results[0].Title)
	}

	// stale-generation update is ignored
	stale := sourceUpdateMsg{gen: 0, up: source.SourceUpdate{Results: []source.Result{{Title: "z"}}, Done: 9, Total: 9}}
	m3, _ := mm.Update(stale)
	if mmm := m3.(Model); len(mmm.results) != 2 || mmm.sourcesDone != 1 {
		t.Fatalf("stale update should be ignored, got results=%d done=%d", len(mmm.results), mmm.sourcesDone)
	}

	// close ends the spinner
	m4, _ := mm.Update(searchClosedMsg{gen: 1})
	if m4.(Model).searching {
		t.Fatalf("searchClosedMsg should stop searching")
	}
}

func TestDetailOpenCopyAndBack(t *testing.T) {
	orig := copyToClipboard
	defer func() { copyToClipboard = orig }()

	const magnet = "magnet:?xt=urn:btih:abcdef0123456789abcdef0123456789abcdef01&dn=Movie"
	m := ready(New(&fakeSource{}, &fakeEngine{}))
	m.editing = false // command mode (ready model starts focused on the search box)
	m.hasSearched = true
	m.results = []source.Result{{Title: "Movie", Magnet: magnet}}
	m.cursor = 0

	// enter opens the detail screen
	m, _ = update(m, key("enter"))
	if !m.showDetail || m.detail.Title != "Movie" {
		t.Fatalf("enter did not open detail: showDetail=%v title=%q", m.showDetail, m.detail.Title)
	}

	// y copies the magnet via the injected clipboard func
	var copied string
	copyToClipboard = func(s string) error { copied = s; return nil }
	m, _ = update(m, key("y"))
	if copied != magnet {
		t.Fatalf("y did not copy magnet, copied=%q", copied)
	}
	if m.notice == "" {
		t.Fatalf("y should set a 'copied' notice")
	}

	// esc returns to the list
	m, _ = update(m, key("esc"))
	if m.showDetail {
		t.Fatalf("esc did not close detail")
	}
}
