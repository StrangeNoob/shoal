package ui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/StrangeNoob/shoal/internal/engine"
	"github.com/StrangeNoob/shoal/internal/source"
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

	removedHash   string
	removedDelete bool
	removeErr     error
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
func (e *fakeEngine) Remove(infoHash string, deleteData bool) error {
	e.removedHash = infoHash
	e.removedDelete = deleteData
	return e.removeErr
}
func (e *fakeEngine) Close() error { return nil }

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
	m.cfg.Path = ""   // never write a real config file during tests
	m.booting = false // ready() = the app is up and settled, past the splash
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

func TestBootingStartsTrueAndReadyClearsIt(t *testing.T) {
	if !New(&fakeSource{}, &fakeEngine{}).booting {
		t.Fatal("a fresh model should start booting (playing the splash)")
	}
	if ready(New(&fakeSource{}, &fakeEngine{})).booting {
		t.Fatal("ready() should represent a settled app (booting cleared)")
	}
}

func TestFrameMsgAdvancesAndSettles(t *testing.T) {
	m := New(&fakeSource{}, &fakeEngine{})
	m.frame = splashFrames - 1
	m2, _ := m.Update(frameMsg{})
	if m2.(Model).booting {
		t.Fatalf("booting should clear once frame reaches splashFrames")
	}
}

func TestAnyKeySkipsSplash(t *testing.T) {
	m := New(&fakeSource{}, &fakeEngine{})
	m.width, m.height, m.ready = 100, 30, true // ready but still booting
	if !m.booting {
		t.Fatal("precondition: still booting")
	}
	m2, _ := update(m, key("j"))
	if m2.booting {
		t.Fatal("any key should skip the splash (clear booting)")
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
	home := m.renderHome(80, 40)
	for _, want := range []string{"A calm BitTorrent client", "HOW IT WORKS", "START HERE"} {
		if !strings.Contains(home, want) {
			t.Errorf("first-run home screen should show %q", want)
		}
	}
	// Branding lives in the banner header now; the home body no longer repeats
	// the compact logo (it was floating centered across the pane).
	if strings.Contains(home, "s  h  o  a  l") {
		t.Error("home body should not render the compact logo")
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

func TestDownloadsCursorMoves(t *testing.T) {
	eng := &fakeEngine{statuses: []engine.Status{
		{Name: "A", InfoHash: "aa", TotalBytes: 100, CompletedBytes: 10},
		{Name: "B", InfoHash: "bb", TotalBytes: 100, CompletedBytes: 20},
	}}
	m := ready(New(&fakeSource{}, eng))
	m, _ = update(m, tickMsg(time.Now())) // load statuses
	m.editing = false
	m.section = sectionDownloads
	m, _ = update(m, key("down"))
	if m.dlCursor != 1 {
		t.Fatalf("dlCursor after down = %d, want 1", m.dlCursor)
	}
	m, _ = update(m, key("up"))
	if m.dlCursor != 0 {
		t.Fatalf("dlCursor after up = %d, want 0", m.dlCursor)
	}
}

func TestCancelConfirmDeletePath(t *testing.T) {
	eng := &fakeEngine{statuses: []engine.Status{{Name: "Movie", InfoHash: "abc123", TotalBytes: 100, CompletedBytes: 10}}}
	m := ready(New(&fakeSource{}, eng))
	m, _ = update(m, tickMsg(time.Now()))
	m.editing = false
	m.section = sectionDownloads
	m.dlCursor = 0

	m, _ = update(m, key("x")) // open confirm
	if !m.cancelConfirm || m.cancelTarget.InfoHash != "abc123" {
		t.Fatalf("x did not open confirm: confirm=%v target=%+v", m.cancelConfirm, m.cancelTarget)
	}
	m, cmd := update(m, key("d")) // cancel + delete files
	if m.cancelConfirm {
		t.Fatalf("d should close the confirm")
	}
	if cmd == nil {
		t.Fatalf("d should return a remove command")
	}
	cmd()
	if eng.removedHash != "abc123" || !eng.removedDelete {
		t.Fatalf("remove got hash=%q delete=%v, want abc123/true", eng.removedHash, eng.removedDelete)
	}
}

func TestCancelKeepAndAbort(t *testing.T) {
	eng := &fakeEngine{statuses: []engine.Status{{Name: "Movie", InfoHash: "h1", TotalBytes: 100, CompletedBytes: 10}}}
	m := ready(New(&fakeSource{}, eng))
	m, _ = update(m, tickMsg(time.Now()))
	m.editing = false
	m.section = sectionDownloads

	m, _ = update(m, key("x"))
	m, cmd := update(m, key("esc")) // abort
	if m.cancelConfirm || cmd != nil {
		t.Fatalf("esc should abort with no command")
	}
	if eng.removedHash != "" {
		t.Fatalf("esc must not call Remove, got %q", eng.removedHash)
	}

	m, _ = update(m, key("x"))
	m, cmd = update(m, key("k")) // keep files
	cmd()
	if eng.removedHash != "h1" || eng.removedDelete {
		t.Fatalf("k should Remove(keep): hash=%q delete=%v", eng.removedHash, eng.removedDelete)
	}
}

func TestCancelConfirmClearsWhenTargetCompletes(t *testing.T) {
	eng := &fakeEngine{statuses: []engine.Status{{Name: "Movie", InfoHash: "h1", TotalBytes: 100, CompletedBytes: 10}}}
	m := ready(New(&fakeSource{}, eng))
	m, _ = update(m, tickMsg(time.Now()))
	m.editing = false
	m.section = sectionDownloads
	m, _ = update(m, key("x"))
	if !m.cancelConfirm {
		t.Fatal("x should open the cancel confirm")
	}
	// the download completes before the user confirms
	eng.statuses = []engine.Status{{Name: "Movie", InfoHash: "h1", TotalBytes: 100, CompletedBytes: 100, Done: true}}
	m, _ = update(m, tickMsg(time.Now().Add(time.Second)))
	if m.cancelConfirm {
		t.Fatal("cancel confirm should clear once the target is no longer downloading")
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

func TestSortModeKeys(t *testing.T) {
	m := ready(New(&fakeSource{}, &fakeEngine{}))
	m.editing = false // command mode
	m.results = []source.Result{
		{Title: "a", SizeBytes: 1, Seeders: 1},
		{Title: "b", SizeBytes: 3, Seeders: 9},
		{Title: "c", SizeBytes: 2, Seeders: 5},
	}
	m.sortDesc = true

	// S enters sort mode and sorts by the first column (Size), desc → b first
	m, _ = update(m, key("S"))
	if !m.sortMode || m.sortField != sortSize || m.results[0].Title != "b" {
		t.Fatalf("S: sortMode=%v field=%v first=%s", m.sortMode, m.sortField, m.results[0].Title)
	}

	// right moves to Seeders (still desc → a last)
	m, _ = update(m, key("right"))
	if m.sortField != sortSeeders || m.results[2].Title != "a" {
		t.Fatalf("right: field=%v last=%s", m.sortField, m.results[2].Title)
	}

	// up sets ascending → a first
	m, _ = update(m, key("up"))
	if m.sortDesc || m.results[0].Title != "a" {
		t.Fatalf("up(asc): desc=%v first=%s", m.sortDesc, m.results[0].Title)
	}

	// esc exits sort mode
	m, _ = update(m, key("esc"))
	if m.sortMode {
		t.Fatalf("esc did not exit sort mode")
	}
}

func TestDetailDownloadClearsShowDetail(t *testing.T) {
	m := ready(New(&fakeSource{}, &fakeEngine{}))
	m.editing = false
	m.showDetail = true
	m.detail = source.Result{Title: "X", TorrentURL: "u1"}
	m, _ = update(m, key("d"))
	if m.showDetail {
		t.Fatalf("d in detail should clear showDetail so it doesn't hijack the next pane")
	}
}

func TestStreamingAllErrorsShowsSearchFailed(t *testing.T) {
	m := New(&fakeSource{}, &fakeEngine{})
	m.searchGen = 1
	m.searching = true
	m.searchCh = make(chan source.SourceUpdate, 1)
	// two sources, both error, no results
	m, _ = update(m, sourceUpdateMsg{gen: 1, up: source.SourceUpdate{Err: errors.New("boom"), Done: 1, Total: 2}})
	m, _ = update(m, sourceUpdateMsg{gen: 1, up: source.SourceUpdate{Err: errors.New("boom"), Done: 2, Total: 2}})
	m, _ = update(m, searchClosedMsg{gen: 1})
	if !m.noticeErr || !strings.Contains(m.notice, "Search failed") {
		t.Fatalf("all-sources-failed should set a 'Search failed' error notice, got notice=%q err=%v", m.notice, m.noticeErr)
	}

	// mixed: one error, one empty (no error) → NOT a total failure → "No results."
	m2 := New(&fakeSource{}, &fakeEngine{})
	m2.searchGen = 1
	m2.searching = true
	m2.searchCh = make(chan source.SourceUpdate, 1)
	m2, _ = update(m2, sourceUpdateMsg{gen: 1, up: source.SourceUpdate{Err: errors.New("boom"), Done: 1, Total: 2}})
	m2, _ = update(m2, sourceUpdateMsg{gen: 1, up: source.SourceUpdate{Done: 2, Total: 2}}) // empty, no error
	m2, _ = update(m2, searchClosedMsg{gen: 1})
	if m2.noticeErr || m2.notice != "No results." {
		t.Fatalf("partial failure with no results should be a plain 'No results.' notice, got notice=%q err=%v", m2.notice, m2.noticeErr)
	}
}

func TestComputeRates(t *testing.T) {
	completed := func(s engine.Status) int64 { return s.CompletedBytes }
	uploaded := func(s engine.Status) int64 { return s.Uploaded }

	prev := []engine.Status{{Name: "A", CompletedBytes: 1000, Uploaded: 0}}
	next := []engine.Status{{Name: "A", CompletedBytes: 1000 + 2*1024*1024, Uploaded: 3 * 1024 * 1024}}
	if got := computeRates(prev, next, 2*time.Second, completed)["A"]; got != 1024*1024 {
		t.Fatalf("download rate = %d, want %d (1 MiB/s)", got, 1024*1024)
	}
	if got := computeRates(prev, next, 3*time.Second, uploaded)["A"]; got != 1024*1024 {
		t.Fatalf("upload rate = %d, want %d (1 MiB/s)", got, 1024*1024)
	}
	if s := computeRates(nil, next, time.Second, completed); len(s) != 0 {
		t.Fatalf("no prior sample → no rate, got %v", s)
	}
	if s := computeRates(prev, next, 0, completed); s != nil {
		t.Fatalf("zero dt → nil, got %v", s)
	}
	// a shrinking byte count (torrent replaced / rechecked) yields no bogus rate
	back := []engine.Status{{Name: "A", CompletedBytes: 500}}
	if s := computeRates(prev, back, time.Second, completed); len(s) != 0 {
		t.Fatalf("negative delta → no rate, got %v", s)
	}
}

func TestNewlyCompleted(t *testing.T) {
	prev := []engine.Status{{InfoHash: "a", Done: false}, {InfoHash: "b", Done: true}}
	next := []engine.Status{{InfoHash: "a", Done: true}, {InfoHash: "b", Done: true}}
	got := newlyCompleted(prev, next)
	if len(got) != 1 || got[0].InfoHash != "a" {
		t.Fatalf("newlyCompleted = %+v, want just a", got)
	}
}

func TestTickRecordsHistory(t *testing.T) {
	eng := &fakeEngine{statuses: []engine.Status{{Name: "Movie", InfoHash: "hh", TotalBytes: 2048, CompletedBytes: 0}}}
	m := ready(New(&fakeSource{}, eng))
	t0 := time.Unix(1_000_000, 0)
	m, _ = update(m, tickMsg(t0)) // not done yet → no history
	eng.statuses = []engine.Status{{Name: "Movie", InfoHash: "hh", TotalBytes: 2048, CompletedBytes: 2048, Done: true}}
	m, _ = update(m, tickMsg(t0.Add(time.Second))) // completes → recorded
	if len(m.history.Entries) != 1 || m.history.Entries[0].InfoHash != "hh" {
		t.Fatalf("history = %+v, want one entry for hh", m.history.Entries)
	}
	m, _ = update(m, tickMsg(t0.Add(2*time.Second))) // still done → no dup
	if len(m.history.Entries) != 1 {
		t.Fatalf("history duplicated: %+v", m.history.Entries)
	}
}

func TestTickComputesTransferSpeeds(t *testing.T) {
	eng := &fakeEngine{statuses: []engine.Status{{Name: "A", TotalBytes: 100_000_000, CompletedBytes: 0, Uploaded: 0}}}
	m := ready(New(&fakeSource{}, eng))
	t0 := time.Unix(1_000_000, 0)
	m, _ = update(m, tickMsg(t0)) // seeds the prev snapshot; no speed yet
	eng.statuses = []engine.Status{{Name: "A", TotalBytes: 100_000_000, CompletedBytes: 2 * 1024 * 1024, Uploaded: 4 * 1024 * 1024}}
	m, _ = update(m, tickMsg(t0.Add(2*time.Second))) // +2 MiB down, +4 MiB up over 2s
	if got := m.dlSpeed["A"]; got != 1024*1024 {
		t.Fatalf("dlSpeed[A] = %d, want %d (1 MiB/s)", got, 1024*1024)
	}
	if got := m.ulSpeed["A"]; got != 2*1024*1024 {
		t.Fatalf("ulSpeed[A] = %d, want %d (2 MiB/s)", got, 2*1024*1024)
	}
}

func TestUpdateNoticeAndAutoUpdateGate(t *testing.T) {
	// A newer version sets the header notice.
	m := ready(New(&fakeSource{}, &fakeEngine{})).WithVersion("0.2.0")
	m2, cmd := update(m, updateCheckMsg{latest: "0.3.0", newer: true})
	mm := m2
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
	if n2.updateAvail != "" || cmd != nil {
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
