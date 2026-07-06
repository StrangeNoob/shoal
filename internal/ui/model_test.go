package ui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/StrangeNoob/shoal/internal/engine"
	"github.com/StrangeNoob/shoal/internal/history"
	"github.com/StrangeNoob/shoal/internal/source"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "shoal-ui-test")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", dir)
	os.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

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

	paused  map[string]bool
	pollErr error

	detail engine.Detail // returned by Detail (details screen)
}

func (e *fakeEngine) Detail(infoHash string) (engine.Detail, error) { return e.detail, nil }

func (e *fakeEngine) AddTorrentURL(url, name string) error {
	e.addedURL, e.addedName = url, name
	return e.urlErr
}
func (e *fakeEngine) AddMagnet(magnet string) error {
	e.addedMagnet = magnet
	return e.magErr
}
func (e *fakeEngine) Statuses() []engine.Status      { return e.statuses }
func (e *fakeEngine) Poll() ([]engine.Status, error) { return e.statuses, e.pollErr }
func (e *fakeEngine) Remove(infoHash string, deleteData bool) error {
	e.removedHash = infoHash
	e.removedDelete = deleteData
	return e.removeErr
}
func (e *fakeEngine) Pause(infoHash string) error {
	if e.paused == nil {
		e.paused = map[string]bool{}
	}
	e.paused[infoHash] = true
	return nil
}
func (e *fakeEngine) Resume(infoHash string) error {
	if e.paused == nil {
		e.paused = map[string]bool{}
	}
	e.paused[infoHash] = false
	return nil
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

// tick advances one poll cycle: it fires the tick, then delivers the async poll
// result as the model would, carrying the tick time so speed sampling is exact.
func tick(m Model, t time.Time) Model {
	out, _ := update(m, tickMsg(t))
	m = out
	ss, err := m.poll.Poll()
	out, _ = update(m, statusMsg{at: t, statuses: ss, err: err})
	return out
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
	if m.editing {
		t.Error("model should start navigable (not editing) so keys move between panes instead of typing into search")
	}
	if m.cfg.Theme != "Twilight" {
		t.Errorf("default theme = %q, want Twilight", m.cfg.Theme)
	}
	if !strings.Contains(m.View(), "starting shoal") {
		t.Errorf("pre-ready View = %q, want starting message", m.View())
	}
}

// The home screen must be usable immediately: keys navigate between panes, the
// footer shows the full option set, and search is opt-in via "/". Regression
// test for the "trapped in the search box on launch" bug.
func TestFreshHomeScreenIsNavigable(t *testing.T) {
	m := ready(New(&fakeSource{}, &fakeEngine{}))
	if m.editing {
		t.Fatal("fresh model must not be in editing mode (keys have to navigate, not type)")
	}
	// tab moves to Downloads without first touching search
	m2, _ := update(m, key("tab"))
	if m2.section != sectionDownloads {
		t.Errorf("tab from home should switch to Downloads, got section %v", m2.section)
	}
	// the footer advertises navigation, not just enter/esc
	if !strings.Contains(m.View(), "panes") {
		t.Errorf("home footer should show all options incl. 'tab panes':\n%s", m.View())
	}
	// "/" focuses the search box on demand
	m3, _ := update(m, key("/"))
	if !m3.editing {
		t.Error(`"/" should enter search-editing mode`)
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
	m, _ = update(m, key("/")) // focus the search box (opt-in)
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
	m, _ = update(m, key("/"))
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
	m, _ = update(m, key("/"))
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
	m, _ = update(m, key("/"))
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
	m, _ = update(m, key("/"))
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

func wheel(b tea.MouseButton) tea.MouseMsg {
	return tea.MouseMsg{Button: b, Action: tea.MouseActionPress}
}

func clickAt(x, y int) tea.MouseMsg {
	return tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, X: x, Y: y}
}

// lineOf returns the 0-based screen row of the first rendered line containing s.
func lineOf(view, s string) int {
	for i, ln := range strings.Split(view, "\n") {
		if strings.Contains(ln, s) {
			return i
		}
	}
	return -1
}

func TestClickSelectsSearchRow(t *testing.T) {
	src := &fakeSource{results: []source.Result{{Title: "Alpha"}, {Title: "Bravo"}, {Title: "Charlie"}}}
	m := ready(New(src, &fakeEngine{}))
	m, _ = update(m, key("/"))
	m.input.SetValue("q")
	m, cmd := update(m, key("enter"))
	m, _ = update(m, cmd()) // populate results (cursor starts at 0)

	y := lineOf(m.View(), "Bravo") // find where the 2nd result actually rendered
	if y < 0 {
		t.Fatal("Bravo row not rendered")
	}
	m, _ = update(m, clickAt(sidebarWidth+3, y))
	if m.cursor != 1 {
		t.Fatalf("clicking the Bravo row selected cursor=%d, want 1", m.cursor)
	}
	// A click in the sidebar column must not move the selection.
	m, _ = update(m, clickAt(2, y))
	if m.cursor != 1 {
		t.Fatalf("sidebar click moved cursor to %d", m.cursor)
	}
}

func TestSearchHideZeroSeedAndFilter(t *testing.T) {
	src := &fakeSource{results: []source.Result{
		{Title: "Ubuntu ISO", Seeders: 50, SeedersKnown: true},
		{Title: "Ubuntu Dead", Seeders: 0, SeedersKnown: true},   // real 0 → hide
		{Title: "Archive Item", Seeders: 0, SeedersKnown: false}, // unknown → keep
		{Title: "Debian ISO", Seeders: 10, SeedersKnown: true},
	}}
	m := ready(New(src, &fakeEngine{}))
	m, _ = update(m, key("/"))
	m.input.SetValue("q")
	m, cmd := update(m, key("enter"))
	m, _ = update(m, cmd())
	if len(m.filteredResults()) != 4 {
		t.Fatalf("baseline results = %d, want 4", len(m.filteredResults()))
	}

	// z hides only the confirmed 0-seed result — the unknown-seeder one stays.
	m, _ = update(m, key("z"))
	got := m.filteredResults()
	if len(got) != 3 {
		t.Fatalf("hide-0-seed results = %d, want 3", len(got))
	}
	for _, r := range got {
		if r.Title == "Ubuntu Dead" {
			t.Fatal("hide-0-seed should have dropped the confirmed 0-seed result")
		}
	}

	// f then typing narrows to a title substring (case-insensitive).
	m, _ = update(m, key("f"))
	for _, r := range "debian" {
		m, _ = update(m, key(string(r)))
	}
	got = m.filteredResults()
	if len(got) != 1 || got[0].Title != "Debian ISO" {
		t.Fatalf("filter narrowed to %v, want [Debian ISO]", got)
	}

	// esc clears the text filter (0-seed hide persists → 3 of the 4 remain).
	m, _ = update(m, key("esc"))
	if n := len(m.filteredResults()); n != 3 {
		t.Fatalf("after clearing filter = %d, want 3", n)
	}

	// A brand-new search resets both filters, so the fresh set isn't narrowed.
	m, _ = update(m, key("/"))
	m.input.SetValue("q2")
	m, cmd = update(m, key("enter"))
	m, _ = update(m, cmd())
	if m.hideZeroSeed || m.filterInput.Value() != "" {
		t.Fatal("a new search must clear hide-0-seed and the text filter")
	}
	if n := len(m.filteredResults()); n != 4 {
		t.Fatalf("fresh search results = %d, want 4 (no leftover filter)", n)
	}
}

func TestClickSelectsDownloadRow(t *testing.T) {
	m := ready(New(&fakeSource{}, &fakeEngine{}))
	m.section = sectionDownloads
	m.statuses = []engine.Status{
		{Name: "DownloadOne", InfoHash: "a", TotalBytes: 100, CompletedBytes: 10},
		{Name: "DownloadTwo", InfoHash: "b", TotalBytes: 100, CompletedBytes: 20},
	}
	y := lineOf(m.View(), "DownloadTwo")
	if y < 0 {
		t.Fatal("second download not rendered")
	}
	m, _ = update(m, clickAt(sidebarWidth+3, y))
	if m.dlCursor != 1 {
		t.Fatalf("clicking DownloadTwo selected dlCursor=%d, want 1", m.dlCursor)
	}
}

func TestClickSelectsSeedingRow(t *testing.T) {
	m := ready(New(&fakeSource{}, &fakeEngine{}))
	m.section = sectionSeeding
	m.statuses = []engine.Status{
		{Name: "SeedOne", InfoHash: "a", TotalBytes: 100, CompletedBytes: 100, Done: true, Seeding: true},
		{Name: "SeedTwo", InfoHash: "b", TotalBytes: 100, CompletedBytes: 100, Done: true, Seeding: true},
	}
	y := lineOf(m.View(), "SeedTwo")
	if y < 0 {
		t.Fatal("second seeding row not rendered")
	}
	m, _ = update(m, clickAt(sidebarWidth+3, y))
	if m.seedCursor != 1 {
		t.Fatalf("clicking SeedTwo selected seedCursor=%d, want 1", m.seedCursor)
	}
}

func TestDownloadDetailScreen(t *testing.T) {
	eng := &fakeEngine{
		statuses: []engine.Status{{Name: "BigPack", InfoHash: "a", TotalBytes: 200, CompletedBytes: 50}},
		detail: engine.Detail{
			Files: []engine.FileDetail{
				{Path: "BigPack/movie.mkv", Length: 150, Completed: 40},
				{Path: "BigPack/readme.txt", Length: 50, Completed: 10},
			},
			Trackers: []string{"udp://tracker.example:1337"},
		},
	}
	m := ready(New(&fakeSource{}, eng))
	m.section = sectionDownloads
	m.statuses = eng.statuses

	// enter opens the details screen and fires the fetch command.
	m, cmd := update(m, key("enter"))
	if !m.showDlDetail {
		t.Fatal("enter should open the download details screen")
	}
	if cmd == nil {
		t.Fatal("enter should return a detail-fetch command")
	}
	m, _ = update(m, cmd()) // deliver dlDetailMsg

	view := m.View()
	for _, want := range []string{"download details", "movie.mkv", "readme.txt", "TRACKERS", "tracker.example"} {
		if !strings.Contains(view, want) {
			t.Errorf("details view missing %q:\n%s", want, view)
		}
	}
	// esc closes it.
	m, _ = update(m, key("esc"))
	if m.showDlDetail {
		t.Fatal("esc should close the details screen")
	}
}

func TestMouseWheelMovesSelection(t *testing.T) {
	src := &fakeSource{results: []source.Result{{Title: "A"}, {Title: "B"}, {Title: "C"}}}
	m := ready(New(src, &fakeEngine{}))
	m, _ = update(m, key("/"))
	m.input.SetValue("q")
	m, cmd := update(m, key("enter"))
	m, _ = update(m, cmd()) // populate results, leave editing mode

	m, _ = update(m, wheel(tea.MouseButtonWheelDown))
	if m.cursor != 1 {
		t.Errorf("cursor after wheel down = %d, want 1", m.cursor)
	}
	m, _ = update(m, wheel(tea.MouseButtonWheelUp))
	if m.cursor != 0 {
		t.Errorf("cursor after wheel up = %d, want 0", m.cursor)
	}

	// While the search box is focused, the wheel must not move the selection.
	m, _ = update(m, key("/")) // focus input (editing)
	m, _ = update(m, wheel(tea.MouseButtonWheelDown))
	if m.cursor != 0 {
		t.Errorf("wheel should be ignored while editing, cursor = %d", m.cursor)
	}
}

func TestFilterNarrowsResults(t *testing.T) {
	src := &fakeSource{results: []source.Result{
		{Title: "A Film", Category: "movies"},
		{Title: "A Song", Category: "audio"},
	}}
	m := ready(New(src, &fakeEngine{}))
	m, _ = update(m, key("/"))
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
	m = tick(m, time.Now())

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
	m = tick(m, time.Now()) // load statuses
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
	m = tick(m, time.Now())
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
	m = tick(m, time.Now())
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
	m = tick(m, time.Now())
	m.editing = false
	m.section = sectionDownloads
	m, _ = update(m, key("x"))
	if !m.cancelConfirm {
		t.Fatal("x should open the cancel confirm")
	}
	// the download completes before the user confirms
	eng.statuses = []engine.Status{{Name: "Movie", InfoHash: "h1", TotalBytes: 100, CompletedBytes: 100, Done: true}}
	m = tick(m, time.Now().Add(time.Second))
	if m.cancelConfirm {
		t.Fatal("cancel confirm should clear once the target is no longer downloading")
	}
}

func TestPauseKeyPausesSelectedDownload(t *testing.T) {
	fe := &fakeEngine{statuses: []engine.Status{
		{Name: "dl", InfoHash: "aaa", TotalBytes: 100, CompletedBytes: 10},
	}}
	m := ready(New(&fakeSource{}, fe))
	m = tick(m, time.Now()) // load statuses
	m.editing = false
	m.section = sectionDownloads
	m.dlCursor = 0

	m2, cmd := update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	_ = m2
	if cmd == nil {
		t.Fatal("p should return a pause command")
	}
	cmd()
	if !fe.paused["aaa"] {
		t.Fatal("p should pause the selected download")
	}

	// A paused status → p resumes.
	fe.statuses[0].Paused = true
	m3 := ready(New(&fakeSource{}, fe))
	m3 = tick(m3, time.Now()) // load statuses
	m3.editing = false
	m3.section = sectionDownloads
	m3.dlCursor = 0
	_, cmd = update(m3, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	if cmd == nil {
		t.Fatal("p should return a resume command")
	}
	cmd()
	if fe.paused["aaa"] {
		t.Fatal("p on a paused download should resume it")
	}
}

func TestPausedDownloadRendersPaused(t *testing.T) {
	fe := &fakeEngine{statuses: []engine.Status{
		{Name: "dl", InfoHash: "aaa", TotalBytes: 100, CompletedBytes: 10, Paused: true},
	}}
	m := ready(New(&fakeSource{}, fe))
	m = tick(m, time.Now()) // load statuses
	m.section = sectionDownloads
	if !strings.Contains(m.View(), "paused") {
		t.Fatalf("a paused download should render 'paused':\n%s", m.View())
	}
}

func TestTickFiresAsyncPoll(t *testing.T) {
	eng := &fakeEngine{statuses: []engine.Status{{Name: "X", TotalBytes: 100, CompletedBytes: 50}}}
	m := ready(New(&fakeSource{}, eng))
	out, cmd := update(m, tickMsg(time.Now()))
	m = out
	if cmd == nil {
		t.Fatal("tick should return a Cmd (reschedule + poll)")
	}
	if len(m.statuses) != 0 {
		t.Fatalf("tick must NOT update statuses synchronously (poll is async), got %+v", m.statuses)
	}
}

func TestPollErrorSetsDaemonDownAndKeepsView(t *testing.T) {
	eng := &fakeEngine{statuses: []engine.Status{{Name: "X", InfoHash: "x", TotalBytes: 100, CompletedBytes: 50}}}
	m := ready(New(&fakeSource{}, eng))
	m = tick(m, time.Unix(1000, 0)) // healthy poll seeds the view
	if len(m.statuses) != 1 || m.daemonDown {
		t.Fatalf("after a healthy poll: statuses=%+v daemonDown=%v", m.statuses, m.daemonDown)
	}
	eng.statuses = []engine.Status{{Name: "Y", InfoHash: "y"}} // errored poll carries a different payload
	eng.pollErr = errors.New("daemon gone")
	m = tick(m, time.Unix(1001, 0)) // failing poll returns ([Y], err)
	if !m.daemonDown {
		t.Fatal("a failed poll should set daemonDown")
	}
	if len(m.statuses) != 1 || m.statuses[0].Name != "X" {
		t.Fatalf("a failed poll must keep the last view, got %+v", m.statuses)
	}
}

func TestPollRecoveryClearsDaemonDown(t *testing.T) {
	eng := &fakeEngine{statuses: []engine.Status{{Name: "X", InfoHash: "x"}}, pollErr: errors.New("gone")}
	m := ready(New(&fakeSource{}, eng))
	m = tick(m, time.Unix(1000, 0)) // fails
	if !m.daemonDown {
		t.Fatal("expected daemonDown after the failing poll")
	}
	eng.pollErr = nil
	eng.statuses = []engine.Status{{Name: "Y", InfoHash: "y"}}
	m = tick(m, time.Unix(1001, 0)) // recovers
	if m.daemonDown {
		t.Fatal("a successful poll should clear daemonDown")
	}
	if len(m.statuses) != 1 || m.statuses[0].Name != "Y" {
		t.Fatalf("recovered poll should update the view, got %+v", m.statuses)
	}
}

func TestStalePollIgnored(t *testing.T) {
	eng := &fakeEngine{statuses: []engine.Status{{Name: "new", InfoHash: "n"}}}
	m := ready(New(&fakeSource{}, eng))
	m = tick(m, time.Unix(2000, 0)) // lastTick=2000, statuses=[new]
	out, _ := update(m, statusMsg{at: time.Unix(1000, 0), statuses: []engine.Status{{Name: "old", InfoHash: "o"}}})
	m = out
	if len(m.statuses) != 1 || m.statuses[0].Name != "new" {
		t.Fatalf("a stale (older-at) poll should be ignored, got %+v", m.statuses)
	}
}

func TestDaemonDownShowsReconnecting(t *testing.T) {
	m := ready(New(&fakeSource{}, &fakeEngine{}))
	m.section = sectionDownloads
	m.daemonDown = true
	if !strings.Contains(m.View(), "reconnecting") {
		t.Fatalf("daemonDown should render a reconnecting indicator:\n%s", m.View())
	}
}

func TestTickPollsEngineAndReschedules(t *testing.T) {
	eng := &fakeEngine{statuses: []engine.Status{{Name: "X", TotalBytes: 100, CompletedBytes: 50}}}
	m := ready(New(&fakeSource{}, eng))
	out, cmd := update(m, tickMsg(time.Now()))
	if cmd == nil {
		t.Error("tick should reschedule itself (and fire a poll)")
	}
	m = tick(out, time.Now()) // deliver a poll cycle
	if len(m.statuses) != 1 || m.statuses[0].Name != "X" {
		t.Errorf("poll did not load statuses: %+v", m.statuses)
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

func TestTickReloadsHistoryOnCompletion(t *testing.T) {
	// Isolate the history file; the daemon (simulated by the pre-write below) is the recorder.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, ".config"))
	seed := history.Load()
	seed.Append(history.Entry{InfoHash: "disk1", Name: "FromDaemon"}) // writes history.json

	eng := &fakeEngine{statuses: []engine.Status{{Name: "Movie", InfoHash: "hh", TotalBytes: 2048}}}
	m := ready(New(&fakeSource{}, eng))
	t0 := time.Unix(1_000_000, 0)
	m = tick(m, t0) // not done → no reload

	eng.statuses = []engine.Status{{Name: "Movie", InfoHash: "hh", TotalBytes: 2048, CompletedBytes: 2048, Done: true}}
	m = tick(m, t0.Add(time.Second)) // completes → TUI reloads history from disk

	// The TUI must NOT append the completed torrent ("hh"); it reloads the daemon's file ("disk1").
	if len(m.history.Entries) != 1 || m.history.Entries[0].InfoHash != "disk1" {
		t.Fatalf("expected reload from disk (disk1), got %+v", m.history.Entries)
	}
}

func TestTickReloadsHistoryUntilRecorded(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, ".config"))

	// history starts empty; a torrent completes but the daemon hasn't recorded it yet.
	eng := &fakeEngine{statuses: []engine.Status{{Name: "Movie", InfoHash: "hh", TotalBytes: 2048, CompletedBytes: 2048, Done: true}}}
	m := ready(New(&fakeSource{}, eng))
	t0 := time.Unix(1_000_000, 0)
	m = tick(m, t0) // Done but not in history → reload (disk still empty)
	if len(m.history.Entries) != 0 {
		t.Fatalf("history should still be empty (daemon hasn't recorded), got %+v", m.history.Entries)
	}
	rec := history.Load() // simulate the daemon recording it
	rec.Append(history.Entry{InfoHash: "hh", Name: "Movie"})
	m = tick(m, t0.Add(time.Second)) // still missing from m.history → reload → now present
	if len(m.history.Entries) != 1 || m.history.Entries[0].InfoHash != "hh" {
		t.Fatalf("reload should pick up the daemon's record, got %+v", m.history.Entries)
	}
}

func TestTickComputesTransferSpeeds(t *testing.T) {
	eng := &fakeEngine{statuses: []engine.Status{{Name: "A", TotalBytes: 100_000_000, CompletedBytes: 0, Uploaded: 0}}}
	m := ready(New(&fakeSource{}, eng))
	t0 := time.Unix(1_000_000, 0)
	m = tick(m, t0) // seeds the prev snapshot; no speed yet
	// download speed samples Downloaded (live payload), upload samples Uploaded
	eng.statuses = []engine.Status{{Name: "A", TotalBytes: 100_000_000, Downloaded: 2 * 1024 * 1024, Uploaded: 4 * 1024 * 1024}}
	m = tick(m, t0.Add(2*time.Second)) // +2 MiB down, +4 MiB up over 2s
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

	// With AutoUpdate on, a newer version returns an auto-update command and
	// must NOT also show the "run 'shoal update'" indicator (spec §5: the
	// indicator is only for when auto-update is not already handling it).
	a := ready(New(&fakeSource{}, &fakeEngine{})).WithVersion("0.2.0")
	a.cfg.AutoUpdate = true
	a2, cmd := update(a, updateCheckMsg{latest: "0.3.0", newer: true})
	if cmd == nil {
		t.Fatal("with AutoUpdate on, a newer version should return an update command")
	}
	if a2.updateAvail != "" {
		t.Fatalf("with AutoUpdate on, updateAvail should stay empty, got %q", a2.updateAvail)
	}

	// Not newer → nothing.
	n := ready(New(&fakeSource{}, &fakeEngine{})).WithVersion("0.3.0")
	n2, cmd := update(n, updateCheckMsg{latest: "0.3.0", newer: false})
	if n2.updateAvail != "" || cmd != nil {
		t.Fatal("a not-newer check should do nothing")
	}
}

func TestSelfUpdatedMsgOnlyNoticesWhenApplied(t *testing.T) {
	// upToDate: the auto-update ran but there was nothing to apply — this must
	// be a no-op, not a false "installed" toast.
	m := ready(New(&fakeSource{}, &fakeEngine{})).WithVersion("0.2.0")
	m.updateAvail = "0.3.0"
	m2, _ := update(m, selfUpdatedMsg{version: "0.3.0", upToDate: true})
	if m2.notice != "" {
		t.Fatalf("upToDate selfUpdatedMsg should not set a notice, got %q", m2.notice)
	}

	// Actually applied: the "installed" notice fires and updateAvail clears.
	m3, _ := update(m, selfUpdatedMsg{version: "0.3.0", upToDate: false})
	if m3.notice == "" {
		t.Fatal("an applied selfUpdatedMsg should set an 'installed' notice")
	}
	if m3.updateAvail != "" {
		t.Fatal("an applied selfUpdatedMsg should clear updateAvail")
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

func TestSeedingPauseAndCursor(t *testing.T) {
	fe := &fakeEngine{statuses: []engine.Status{
		{Name: "A", InfoHash: "a", TotalBytes: 100, CompletedBytes: 100, Done: true},
		{Name: "B", InfoHash: "b", TotalBytes: 100, CompletedBytes: 100, Done: true},
	}}
	m := ready(New(&fakeSource{}, fe))
	m.statuses = fe.statuses
	m.section = sectionSeeding

	// down moves the seed cursor
	m, _ = update(m, key("down"))
	if m.seedCursor != 1 {
		t.Fatalf("seedCursor after down = %d, want 1", m.seedCursor)
	}
	// p pauses the selected seeder (B)
	_, cmd := update(m, key("p"))
	if cmd == nil {
		t.Fatal("p should return a pause command")
	}
	cmd()
	if !fe.paused["b"] {
		t.Fatal("p should pause the selected seeding torrent")
	}
	// p again on a paused seeder resumes it
	fe.statuses[1].Paused = true
	m.seedCursor = 1
	_, cmd = update(m, key("p"))
	cmd()
	if fe.paused["b"] {
		t.Fatal("p on a paused seeder should resume it")
	}
}

func TestSeedingStopFlow(t *testing.T) {
	fe := &fakeEngine{statuses: []engine.Status{
		{Name: "Movie", InfoHash: "h1", TotalBytes: 100, CompletedBytes: 100, Done: true},
	}}
	m := ready(New(&fakeSource{}, fe))
	m.statuses = fe.statuses
	m.section = sectionSeeding

	// x opens the stop confirm
	m, _ = update(m, key("x"))
	if !m.stopConfirm || m.stopTarget.InfoHash != "h1" {
		t.Fatalf("x should open the stop confirm, got confirm=%v target=%q", m.stopConfirm, m.stopTarget.InfoHash)
	}
	// enter stops it (removeCmd with deleteData=false)
	m2, cmd := update(m, key("enter"))
	if m2.stopConfirm {
		t.Fatal("enter should close the stop confirm")
	}
	if cmd == nil {
		t.Fatal("enter should return a remove command")
	}
	cmd()
	if fe.removedHash != "h1" || fe.removedDelete {
		t.Fatalf("stop should Remove(h1, deleteData=false); got hash=%q delete=%v", fe.removedHash, fe.removedDelete)
	}
	// esc cancels the confirm without removing
	fe.removedHash = ""
	m, _ = update(m, key("x"))
	m3, _ := update(m, key("esc"))
	if m3.stopConfirm {
		t.Fatal("esc should cancel the stop confirm")
	}
	if fe.removedHash != "" {
		t.Fatalf("esc must not Remove, got %q", fe.removedHash)
	}
}

func TestOpenFolderNotices(t *testing.T) {
	// not ready: no on-disk path yet
	fe := &fakeEngine{statuses: []engine.Status{
		{Name: "A", InfoHash: "a", TotalBytes: 100, CompletedBytes: 10, Path: ""},
	}}
	m := ready(New(&fakeSource{}, fe))
	m.statuses = fe.statuses
	m.section = sectionDownloads
	m2, _ := update(m, key("o"))
	if !strings.Contains(m2.notice, "ready") {
		t.Errorf("o with no path should notice 'not ready', got %q", m2.notice)
	}

	// deleted: path set but missing
	fe.statuses[0].Path = filepath.Join(t.TempDir(), "gone")
	m3, _ := update(m, key("o"))
	if !strings.Contains(m3.notice, "deleted") || !m3.noticeErr {
		t.Errorf("o on a missing path should error 'deleted', got %q err=%v", m3.notice, m3.noticeErr)
	}

	// existing dir → returns a command (folder open)
	fe.statuses[0].Path = t.TempDir()
	_, cmd := update(m, key("o"))
	if cmd == nil {
		t.Error("o on an existing folder should return an open command")
	}
}

func TestOpenHistoryFolder(t *testing.T) {
	dir := t.TempDir() // exists → openable
	fe := &fakeEngine{statuses: []engine.Status{}}
	m := ready(New(&fakeSource{}, fe))
	m.section = sectionSeeding

	// One finished item in history with a stored Path; no active seeders, so the
	// cursor (0) selects the history row.
	m.history.Entries = []history.Entry{{InfoHash: "h1", Name: "Old Movie", Path: dir}}
	_, cmd := update(m, key("o"))
	if cmd == nil {
		t.Fatal("o on a history entry with a valid Path should open its folder")
	}

	// A history item whose folder is gone → 'deleted' notice.
	m.history.Entries = []history.Entry{{InfoHash: "h2", Name: "Gone", Path: filepath.Join(dir, "missing")}}
	m2, _ := update(m, key("o"))
	if !strings.Contains(m2.notice, "deleted") {
		t.Errorf("o on a missing history path should notice 'deleted', got %q", m2.notice)
	}

	// A still-loaded torrent's live Path wins over the stored one.
	fe.statuses = []engine.Status{{InfoHash: "h3", Name: "Loaded", Path: dir, Done: false}}
	m.statuses = fe.statuses
	m.history.Entries = []history.Entry{{InfoHash: "h3", Name: "Loaded", Path: "/stale/gone"}}
	_, cmd = update(m, key("o"))
	if cmd == nil {
		t.Fatal("o should resolve a loaded torrent's live Path and open it")
	}
}

func TestSeedCursorSpansHistory(t *testing.T) {
	fe := &fakeEngine{statuses: []engine.Status{
		{Name: "Seeder", InfoHash: "s1", TotalBytes: 100, CompletedBytes: 100, Done: true},
	}}
	m := ready(New(&fakeSource{}, fe))
	m.statuses = fe.statuses
	m.history.Entries = []history.Entry{{InfoHash: "h1", Name: "Old"}}
	m.section = sectionSeeding
	// 1 active seeder + 1 history = 2 selectable; down should reach index 1
	m, _ = update(m, key("down"))
	if m.seedCursor != 1 {
		t.Fatalf("seedCursor should span history (want 1), got %d", m.seedCursor)
	}
	// can't go past the last selectable item
	m, _ = update(m, key("down"))
	if m.seedCursor != 1 {
		t.Fatalf("seedCursor should clamp at the last item, got %d", m.seedCursor)
	}
}

func TestOpenHistoryFallsBackToDownloadDir(t *testing.T) {
	dir := t.TempDir()
	m := ready(New(&fakeSource{}, &fakeEngine{}))
	m.cfg.DataDir = dir
	m.section = sectionSeeding

	// Legacy entry: no stored Path, torrent not loaded, name matches no folder →
	// fall back to opening the downloads folder (not the misleading 'isn't ready').
	m.history.Entries = []history.Entry{{InfoHash: "old", Name: "Nonexistent Movie"}}
	m2, cmd := update(m, key("o"))
	if cmd == nil {
		t.Fatal("o on a legacy history item should open the downloads folder as a fallback")
	}
	if strings.Contains(m2.notice, "isn't ready") {
		t.Errorf("must not show the 'isn't ready yet' message for a finished item, got %q", m2.notice)
	}
	if !strings.Contains(m2.notice, "downloads folder") {
		t.Errorf("should note the downloads-folder fallback, got %q", m2.notice)
	}
}

func TestOpenHistoryGuessesFolderByName(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "Match Movie [1080p]"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := ready(New(&fakeSource{}, &fakeEngine{}))
	m.cfg.DataDir = dir
	m.section = sectionSeeding
	// legacy entry whose Name matches an on-disk folder → opens it, no fallback notice
	m.history.Entries = []history.Entry{{InfoHash: "x", Name: "Match Movie [1080p]"}}
	m2, cmd := update(m, key("o"))
	if cmd == nil {
		t.Fatal("o should open the matching on-disk folder")
	}
	if strings.Contains(m2.notice, "downloads folder") {
		t.Errorf("an exact folder match should not trigger the downloads-folder fallback, got %q", m2.notice)
	}
}

func TestHistoryRowDeleteConfirm(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, ".config"))
	seed := history.Load()
	seed.Append(history.Entry{InfoHash: "hh", Name: "Old Movie"})

	// no active torrents → the seeding pane shows only the history row
	m := ready(New(&fakeSource{}, &fakeEngine{}).WithHistory(history.Load()))
	m.section = sectionSeeding
	m.seedCursor = 0 // the single history row

	m, _ = update(m, key("x"))
	if !m.histConfirm {
		t.Fatal("x on a history row should open the delete confirm")
	}
	m, _ = update(m, key("k")) // [k] remove entry, keep files
	if m.histConfirm {
		t.Fatal("confirm should close after a choice")
	}
	if len(history.Load().Entries) != 0 {
		t.Fatalf("history entry should be removed, got %+v", history.Load().Entries)
	}
}

func TestHistDeletedMsgReportsError(t *testing.T) {
	m := ready(New(&fakeSource{}, &fakeEngine{}))
	got, _ := update(m, histDeletedMsg{name: "X", err: errors.New("boom")})
	if !got.noticeErr {
		t.Fatal("a failed file delete should surface an error, not a success notice")
	}
}

func TestHistDeletedMsgKeepsRowOnFailure(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, ".config"))
	seed := history.Load()
	seed.Append(history.Entry{InfoHash: "hh", Name: "M"})
	m := ready(New(&fakeSource{}, &fakeEngine{}).WithHistory(history.Load()))
	out, _ := update(m, histDeletedMsg{infoHash: "hh", name: "M", err: errors.New("boom")})
	if !out.noticeErr {
		t.Fatal("a failed delete should surface an error")
	}
	if len(history.Load().Entries) != 1 {
		t.Fatal("a failed delete must keep the history row")
	}
}

func TestHistDeletedMsgRemovesRowOnSuccess(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, ".config"))
	seed := history.Load()
	seed.Append(history.Entry{InfoHash: "hh", Name: "M"})
	m := ready(New(&fakeSource{}, &fakeEngine{}).WithHistory(history.Load()))
	update(m, histDeletedMsg{infoHash: "hh", name: "M"}) // no error
	if len(history.Load().Entries) != 0 {
		t.Fatal("a successful delete should remove the history row")
	}
}
