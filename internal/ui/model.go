// Package ui is shoal's fullscreen terminal interface, built with Bubble Tea.
// It renders a calm, fullscreen layout with four panes — Search, Downloads,
// Seeding and Settings — in one of two selectable themes (see theme.go).
package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/StrangeNoob/shoal/internal/config"
	"github.com/StrangeNoob/shoal/internal/engine"
	"github.com/StrangeNoob/shoal/internal/history"
	"github.com/StrangeNoob/shoal/internal/opener"
	"github.com/StrangeNoob/shoal/internal/source"
	upd "github.com/StrangeNoob/shoal/internal/update"
)

const sidebarWidth = 20

// maxHistoryReloads bounds how many ticks we reload history for a still-unrecorded
// finished torrent (~14s at the tick interval; the daemon normally records within ~1s).
const maxHistoryReloads = 20

// copyToClipboard is a package var so tests can stub the system clipboard.
var copyToClipboard = clipboard.WriteAll

// filterCat maps a UI filter chip to an Internet Archive mediatype. The empty
// mediatype ("All") matches everything.
type filterCat struct {
	Label     string
	Mediatype string
}

var filterCats = []filterCat{
	{"All", ""},
	{"Games", "games"},
	{"Movies", "movies"},
	{"TV", "tv"},
	{"Anime", "anime"},
	{"Audio", "audio"},
	{"Software", "software"},
	{"Texts", "texts"},
	{"Images", "image"},
}

// Model is the whole application state.
type Model struct {
	width, height int
	ready         bool
	booting       bool // playing the animated startup splash
	frame         int  // splash animation frame counter

	section        section
	editing        bool // search box focused?
	editingSetting bool // a Settings text field focused?
	editingFilter  bool // in-results filter box focused?
	hideZeroSeed   bool // hide search results with 0 seeders
	showHelp       bool
	showDetail     bool
	detail         source.Result

	input       textinput.Model // search box
	setInput    textinput.Model // settings inline editor
	filterInput textinput.Model // in-results fuzzy filter (narrows loaded results)
	spin        spinner.Model
	prog        progress.Model

	src source.Source
	eng engine.Engine
	cfg config.Config

	poll       statusPoller
	daemonDown bool

	searching   bool
	hasSearched bool
	results     []source.Result
	cursor      int
	filter      int // index into filterCats

	searchCh       chan source.SourceUpdate
	sourcesDone    int
	sourcesTotal   int
	searchGen      int
	searchCancel   context.CancelFunc
	searchErrCount int

	sortMode  bool
	sortCol   int
	sortField sortField
	sortDesc  bool

	statuses      []engine.Status
	dlSpeed       map[string]int64 // download byte/sec per Status.Name, sampled between ticks
	ulSpeed       map[string]int64 // upload (seeding) byte/sec per Status.Name
	lastTick      time.Time        // timestamp of the previous tick, for the rate delta
	history       history.Store    // completed-download record; injected via WithHistory
	historyMisses map[string]int   // per-InfoHash count of history reloads while awaiting the daemon's write
	setCursor     int              // index into settingItems()
	version       string           // build version (ldflags), "" or "dev" for local builds
	updateAvail   string           // latest version when a newer release is available

	dlCursor      int // selection in the Downloads pane
	seedCursor    int // selection in the Seeding pane
	cancelConfirm bool
	cancelTarget  engine.Status
	stopConfirm   bool          // Seeding pane: confirm "stop seeding"
	stopTarget    engine.Status // the torrent being stopped
	histConfirm   bool          // Seeding pane: confirm deleting a HISTORY entry
	histTarget    history.Entry // the history entry being deleted

	notice      string
	noticeErr   bool
	noticeUntil time.Time
	err         error
}

// New builds a model with default configuration (used by tests).
func New(src source.Source, eng engine.Engine) Model {
	return NewWithConfig(src, eng, config.Default())
}

// NewWithConfig builds the initial model, applying the persisted theme and
// colour mode before any rendering happens.
func NewWithConfig(src source.Source, eng engine.Engine, cfg config.Config) Model {
	applyColorMode(cfg.ColorMode)
	setPalette(paletteByName(cfg.Theme))

	ti := textinput.New()
	ti.Placeholder = "Search the Internet Archive…"
	ti.Prompt = ""
	ti.CharLimit = 120
	// Not focused on launch: the home screen must be navigable (tab/arrows/q
	// work immediately); "/" focuses the search box when the user wants it.

	si := textinput.New()
	si.Prompt = ""
	si.CharLimit = 200

	fi := textinput.New()
	fi.Prompt = ""
	fi.Placeholder = "filter results…"
	fi.CharLimit = 80

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = st.SearchLabel

	pr := progress.New(progress.WithSolidFill(activePalette.Accent.TrueColor))
	pr.ShowPercentage = false

	m := Model{
		section:     sectionSearch,
		editing:     false,
		input:       ti,
		setInput:    si,
		filterInput: fi,
		spin:        sp,
		prog:        pr,
		src:         src,
		eng:         eng,
		cfg:         cfg,
		sortDesc:    true,
		booting:     true,
	}
	if p, ok := eng.(statusPoller); ok {
		m.poll = p // production daemonPoller and the test fakeEngine both implement it
	}
	return m
}

// WithHistory attaches a loaded history store (main wires history.Load(); tests
// leave it empty so Save is a no-op).
func (m Model) WithHistory(h history.Store) Model {
	m.history = h
	return m
}

// WithVersion attaches the build version (main injects it via ldflags).
func (m Model) WithVersion(v string) Model {
	m.version = v
	return m
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{textinput.Blink, m.spin.Tick, tickCmd(), frameCmd()}
	if m.version != "" && m.version != "dev" {
		cmds = append(cmds, checkUpdateCmd(m.version))
	}
	return tea.Batch(cmds...)
}

// --- messages & commands ---------------------------------------------------

type searchDoneMsg struct {
	results []source.Result
	err     error
}

type sourceUpdateMsg struct {
	gen int
	up  source.SourceUpdate
}

type searchClosedMsg struct{ gen int }

type addedMsg struct {
	title string
	err   error
}

type removedMsg struct {
	name    string
	deleted bool
	err     error
}

type tickMsg time.Time

func tickCmd() tea.Cmd {
	return tea.Tick(700*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// notifyDoneCmd rings the terminal bell and posts an OSC 9 desktop notification
// (honoured by iTerm2, kitty, WezTerm, …) when a download finishes. Written to
// stderr so it doesn't disturb Bubble Tea's stdout render; both share the tty,
// so the terminal still interprets the sequence. Returns no follow-up message.
func notifyDoneCmd(name string) tea.Cmd {
	return func() tea.Msg {
		// stripControl again here so the raw write is safe regardless of caller.
		fmt.Fprintf(os.Stderr, "\a\x1b]9;shoal — finished: %s\x07", stripControl(name))
		return nil
	}
}

// statusPoller is implemented by engines that can report status off the UI
// thread (the production daemon client and the test fake); pollCmd runs it
// inside a tea.Cmd goroutine instead of blocking Update.
type statusPoller interface {
	Poll() ([]engine.Status, error)
}

type statusMsg struct {
	at       time.Time // the tick time that fired this poll (drives the speed dt)
	statuses []engine.Status
	err      error
}

func pollCmd(p statusPoller, at time.Time) tea.Cmd {
	return func() tea.Msg {
		ss, err := p.Poll()
		return statusMsg{at: at, statuses: ss, err: err}
	}
}

type updateCheckMsg struct {
	latest string
	newer  bool
}

type selfUpdatedMsg struct {
	version  string
	upToDate bool
	err      error
}

func checkUpdateCmd(current string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		rel, err := upd.CheckLatest(ctx)
		if err != nil {
			return updateCheckMsg{} // silent on failure
		}
		return updateCheckMsg{latest: rel.Version, newer: upd.Newer(current, rel.Version)}
	}
}

func autoUpdateCmd(current string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		to, up, err := upd.Apply(ctx, current, nil)
		return selfUpdatedMsg{version: to, upToDate: up, err: err}
	}
}

const (
	frameInterval = 55 * time.Millisecond // ~18fps splash
	splashFrames  = 36                    // ≈ 2s
)

func (m Model) splashT() float64 { return float64(m.frame) * frameInterval.Seconds() }

type frameMsg struct{}

func frameCmd() tea.Cmd {
	return tea.Tick(frameInterval, func(time.Time) tea.Msg { return frameMsg{} })
}

func searchCmd(src source.Source, query string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		res, err := src.Search(ctx, query)
		return searchDoneMsg{results: res, err: err}
	}
}

// streamSearcher is implemented by sources that can report per-source
// progress (e.g. source.MultiSource) instead of blocking for the full result.
type streamSearcher interface {
	SearchStream(ctx context.Context, query string, ch chan<- source.SourceUpdate)
}

// startSearch begins a new (generation-tagged) search, streaming per-source
// updates when the source supports it, else falling back to a blocking search.
func (m *Model) startSearch(query string) tea.Cmd {
	if m.searchCancel != nil {
		m.searchCancel()
		m.searchCancel = nil
	}
	m.searchGen++
	m.results = nil
	m.cursor = 0
	m.sourcesDone = 0
	m.sourcesTotal = 0
	m.searchErrCount = 0
	m.searching = true
	m.hasSearched = true
	// Clear the in-results filters so a fresh query isn't silently narrowed by a
	// leftover text filter or hide-0-seed toggle from the previous search.
	m.hideZeroSeed = false
	m.editingFilter = false
	m.filterInput.Blur()
	m.filterInput.SetValue("")

	// ponytail: keyed to the concrete *MultiSource that main.go injects. If the
	// production wiring ever wraps or replaces that source, live toggles silently
	// stop — re-thread the config into the source there instead.
	if _, ok := m.src.(*source.MultiSource); ok {
		m.src = m.enabledSource() // honor live source toggles; leave injected (test) sources untouched
	}
	ss, ok := m.src.(streamSearcher)
	if !ok {
		return searchCmd(m.src, query)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	m.searchCancel = cancel
	ch := make(chan source.SourceUpdate)
	m.searchCh = ch
	gen := m.searchGen
	go ss.SearchStream(ctx, query, ch)
	return waitForUpdate(gen, ch)
}

// enabledSource builds the search source set from the current config, so source
// toggles take effect on the next search without a restart.
func (m *Model) enabledSource() source.Source {
	return source.NewMulti(source.EnabledSources(m.cfg.DisabledSources)...)
}

func waitForUpdate(gen int, ch chan source.SourceUpdate) tea.Cmd {
	return func() tea.Msg {
		up, ok := <-ch
		if !ok {
			return searchClosedMsg{gen: gen}
		}
		return sourceUpdateMsg{gen: gen, up: up}
	}
}

func addCmd(eng engine.Engine, r source.Result) tea.Cmd {
	return func() tea.Msg {
		// Prefer a .torrent URL; fall back to a magnet (curated/open-media
		// results are magnet-only).
		var err error
		switch {
		case r.TorrentURL != "":
			err = eng.AddTorrentURL(r.TorrentURL, r.Title)
		case r.Magnet != "":
			err = eng.AddMagnet(r.Magnet)
		default:
			err = fmt.Errorf("%q has no torrent URL or magnet", r.Title)
		}
		return addedMsg{title: r.Title, err: err}
	}
}

func addMagnetCmd(eng engine.Engine, magnet string) tea.Cmd {
	return func() tea.Msg {
		err := eng.AddMagnet(magnet)
		return addedMsg{title: "magnet link", err: err}
	}
}

type folderOpenedMsg struct{ err error }

// openFolderCmd opens dir in the OS file manager, off the UI goroutine.
func openFolderCmd(dir string) tea.Cmd {
	return func() tea.Msg { return folderOpenedMsg{err: opener.Open(dir)} }
}

// openSelected opens the selected torrent's folder, or sets a notice when it
// isn't ready or is missing. dir is Path if it's a directory, else its parent
// (single-file torrents live directly in the save dir).
func (m *Model) openSelected(s engine.Status) tea.Cmd {
	if s.Path == "" {
		m.setNotice("download folder isn't ready yet")
		return nil
	}
	fi, err := os.Stat(s.Path)
	if err != nil {
		m.setError("folder not found — it may have been deleted")
		return nil
	}
	dir := s.Path
	if !fi.IsDir() {
		dir = filepath.Dir(s.Path)
	}
	return openFolderCmd(dir)
}

// pauseToggleCmd pauses a running torrent or resumes a paused one.
func pauseToggleCmd(eng engine.Engine, s engine.Status) tea.Cmd {
	return func() tea.Msg {
		if s.Paused {
			eng.Resume(s.InfoHash)
		} else {
			eng.Pause(s.InfoHash)
		}
		return nil
	}
}

func removeCmd(eng engine.Engine, infoHash, name string, deleteData bool) tea.Cmd {
	return func() tea.Msg {
		err := eng.Remove(infoHash, deleteData)
		return removedMsg{name: name, deleted: deleteData, err: err}
	}
}

// --- update ----------------------------------------------------------------

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true
		m.input.Width = max(10, m.mainWidth()-2)
		m.setInput.Width = max(10, m.mainWidth()-22)
		m.filterInput.Width = max(10, m.mainWidth()-12)
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case searchDoneMsg:
		m.searching = false
		if msg.err != nil {
			m.err = msg.err
			m.setError("Search failed: " + msg.err.Error())
		} else {
			m.results = msg.results
			m.cursor = 0
			m.err = nil
			if len(msg.results) == 0 {
				m.setNotice("No results.")
			}
		}
		return m, nil

	case sourceUpdateMsg:
		if msg.gen != m.searchGen {
			return m, nil
		}
		if len(msg.up.Results) > 0 {
			m.results = append(m.results, msg.up.Results...)
			applySort(m.results, m.sortField, m.sortDesc)
		}
		m.sourcesDone = msg.up.Done
		m.sourcesTotal = msg.up.Total
		if msg.up.Err != nil {
			m.searchErrCount++
		}
		if n := len(m.filteredResults()); m.cursor >= n {
			m.cursor = max(0, n-1)
		}
		return m, waitForUpdate(msg.gen, m.searchCh)

	case searchClosedMsg:
		if msg.gen != m.searchGen {
			return m, nil
		}
		m.searching = false
		if len(m.results) == 0 {
			if m.sourcesTotal > 0 && m.searchErrCount >= m.sourcesTotal {
				m.setError("Search failed.")
			} else {
				m.setNotice("No results.")
			}
		}
		return m, nil

	case addedMsg:
		if msg.err != nil {
			m.setError("Couldn't add: " + msg.err.Error())
		} else {
			m.setNotice("Added: " + truncate(msg.title, 48))
			m.section = sectionDownloads
		}
		return m, nil

	case removedMsg:
		switch {
		case msg.err != nil:
			m.setError("Couldn't remove: " + msg.err.Error())
		case msg.deleted:
			m.setNotice("Deleted: " + truncate(msg.name, 48))
		default:
			m.setNotice("Cancelled: " + truncate(msg.name, 48))
		}
		if n := len(m.downloading()); m.dlCursor >= n {
			m.dlCursor = max(0, n-1)
		}
		return m, nil

	case histDeletedMsg:
		if msg.err != nil {
			m.setError("Couldn't delete files: " + msg.err.Error())
			return m, nil
		}
		m.history.Remove(msg.infoHash) // drop the row only after the delete succeeds
		m.history = history.Load()
		m.setNotice("Deleted: " + truncate(msg.name, 40))
		return m, nil

	case folderOpenedMsg:
		if msg.err != nil {
			m.setError("couldn't open the folder")
		}
		return m, nil

	case tickMsg:
		now := time.Time(msg)
		if m.notice != "" && time.Now().After(m.noticeUntil) {
			m.notice = ""
			m.noticeErr = false
		}
		cmds := []tea.Cmd{tickCmd()}
		if m.poll != nil {
			cmds = append(cmds, pollCmd(m.poll, now))
		} else if m.eng != nil { // no poller (minimal engine): synchronous fallback
			eng := m.eng
			cmds = append(cmds, func() tea.Msg { return statusMsg{at: now, statuses: eng.Statuses()} })
		}
		return m, tea.Batch(cmds...)

	case statusMsg:
		if msg.at.Before(m.lastTick) {
			return m, nil // stale/out-of-order poll — ignore, even an error
		}
		if msg.err != nil {
			m.daemonDown = true
			return m, nil
		}
		m.daemonDown = false
		now := msg.at
		next := msg.statuses
		dt := now.Sub(m.lastTick)
		m.dlSpeed = computeRates(m.statuses, next, dt, func(s engine.Status) int64 { return s.Downloaded })
		m.ulSpeed = computeRates(m.statuses, next, dt, func(s engine.Status) int64 { return s.Uploaded })
		// Detect downloads that just finished (before m.statuses is overwritten) and
		// notify, if the user hasn't turned it off.
		var notifyCmds []tea.Cmd
		if m.cfg.NotifyOnComplete {
			if done := newlyCompleted(m.statuses, next); len(done) > 0 {
				name := stripControl(done[0]) // torrent names are untrusted metadata
				m.setNotice("✓ Finished: " + truncate(name, 40))
				notifyCmds = append(notifyCmds, notifyDoneCmd(name))
			}
		}
		// Reload history while a finished torrent isn't in it yet — the daemon
		// records completions on its own ticker, so the flip-to-Done edge can
		// precede its write. Bounded per torrent so a completion the daemon never
		// persists (e.g. its history write failed) can't reload forever.
		if m.historyMisses == nil {
			m.historyMisses = map[string]int{}
		}
		recorded := make(map[string]bool, len(m.history.Entries))
		for _, e := range m.history.Entries {
			recorded[e.InfoHash] = true
		}
		// Also reload on the very first poll if history was never loaded (e.g. a
		// model built via New() without WithHistory, as tests do) so the Seeding
		// pane's HISTORY rows are populated without waiting for a completion.
		reload := false
		for _, s := range next {
			if s.Done && !recorded[s.InfoHash] && m.historyMisses[s.InfoHash] < maxHistoryReloads {
				m.historyMisses[s.InfoHash]++
				reload = true
			}
		}
		if reload {
			m.history = history.Load()
		}
		m.statuses = next
		m.lastTick = now
		if n := len(m.downloading()); m.dlCursor >= n {
			m.dlCursor = max(0, n-1)
		}
		if n := len(m.seeding()) + len(m.seedHistory()); m.seedCursor >= n {
			m.seedCursor = max(0, n-1)
		}
		// If the torrent we're asking to cancel finished (or vanished) while the
		// confirm prompt was open, drop the prompt — it only applies to in-progress downloads.
		if m.cancelConfirm {
			stillDownloading := false
			for _, s := range m.downloading() {
				if s.InfoHash == m.cancelTarget.InfoHash {
					stillDownloading = true
					break
				}
			}
			if !stillDownloading {
				m.cancelConfirm = false
			}
		}
		// Likewise, if the torrent we're asking to stop is no longer seeding,
		// drop the stop prompt.
		if m.stopConfirm {
			stillSeeding := false
			for _, s := range m.seeding() {
				if s.InfoHash == m.stopTarget.InfoHash {
					stillSeeding = true
					break
				}
			}
			if !stillSeeding {
				m.stopConfirm = false
			}
		}
		return m, tea.Batch(notifyCmds...)

	case updateCheckMsg:
		if msg.newer {
			if m.cfg.AutoUpdate {
				return m, autoUpdateCmd(m.version)
			}
			m.updateAvail = msg.latest
		}
		return m, nil

	case selfUpdatedMsg:
		if msg.err == nil && !msg.upToDate && msg.version != "" {
			m.updateAvail = ""
			m.setNotice("↑ v" + msg.version + " installed — restart shoal")
		}
		return m, nil

	case frameMsg:
		if !m.booting {
			return m, nil
		}
		m.frame++
		if m.frame >= splashFrames {
			m.booting = false
			return m, nil
		}
		return m, frameCmd()

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	}

	// Forward anything else (e.g. cursor blink) to whichever input is focused.
	if m.editing {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	if m.editingSetting {
		var cmd tea.Cmd
		m.setInput, cmd = m.setInput.Update(msg)
		return m, cmd
	}
	if m.editingFilter {
		var cmd tea.Cmd
		m.filterInput, cmd = m.filterInput.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}

	if m.booting {
		m.booting = false
		return m, nil
	}

	if m.showHelp {
		switch msg.String() {
		case "?", "esc", "q":
			m.showHelp = false
		}
		return m, nil
	}

	if m.editing {
		return m.handleSearchEdit(msg)
	}
	if m.editingSetting {
		return m.handleSettingEdit(msg)
	}
	if m.editingFilter {
		return m.handleFilterEdit(msg)
	}

	if m.showDetail {
		switch msg.String() {
		case "esc":
			m.showDetail = false
		case "d":
			m.showDetail = false
			return m, addCmd(m.eng, m.detail)
		case "y":
			if err := copyToClipboard(m.detail.Magnet); err != nil {
				m.setError("Copy failed: " + err.Error())
			} else {
				m.setNotice("Magnet copied.")
			}
		case "q", "ctrl+c":
			return m, tea.Quit
		}
		return m, nil
	}

	if m.sortMode {
		return m.handleSortKey(msg)
	}

	if m.cancelConfirm {
		return m.handleCancelKey(msg)
	}

	if m.stopConfirm {
		return m.handleStopKey(msg)
	}

	if m.histConfirm {
		return m.handleHistKey(msg)
	}

	// Command mode: single keys are actions.
	switch msg.String() {
	case "q":
		return m, tea.Quit
	case "?":
		m.showHelp = true
		return m, nil
	case "/":
		m.section = sectionSearch
		m.editing = true
		m.input.Focus()
		return m, textinput.Blink
	case "f":
		if m.section == sectionSearch {
			m.editingFilter = true
			m.filterInput.Focus()
			m.cursor = 0
			return m, textinput.Blink
		}
		return m, nil
	case "z":
		if m.section == sectionSearch {
			m.hideZeroSeed = !m.hideZeroSeed
			m.cursor = 0
		}
		return m, nil
	case "tab":
		m.section = m.section.next()
		return m, nil
	case "up", "k":
		m.moveUp()
		return m, nil
	case "down", "j":
		m.moveDown()
		return m, nil
	case "left", "h":
		m.moveLeft()
		return m, nil
	case "right", "l":
		m.moveRight()
		return m, nil
	case "enter":
		cmd := m.activate()
		return m, cmd
	case "d":
		if m.section == sectionSearch {
			fr := m.filteredResults()
			if len(fr) > 0 && m.cursor < len(fr) {
				return m, addCmd(m.eng, fr[m.cursor])
			}
		}
		return m, nil
	case "x":
		switch m.section {
		case sectionDownloads:
			ds := m.downloading()
			if len(ds) > 0 && m.dlCursor < len(ds) {
				m.cancelConfirm = true
				m.cancelTarget = ds[m.dlCursor]
			}
		case sectionSeeding:
			ss := m.seeding()
			if len(ss) > 0 && m.seedCursor < len(ss) {
				m.stopConfirm = true
				m.stopTarget = ss[m.seedCursor]
			} else if hist := m.seedHistory(); len(hist) > 0 {
				if hi := m.seedCursor - len(ss); hi >= 0 && hi < len(hist) {
					m.histConfirm = true
					m.histTarget = hist[hi]
				}
			}
		}
		return m, nil
	case "p":
		switch m.section {
		case sectionDownloads:
			ds := m.downloading()
			if len(ds) > 0 && m.dlCursor < len(ds) {
				return m, pauseToggleCmd(m.eng, ds[m.dlCursor])
			}
		case sectionSeeding:
			ss := m.seeding()
			if len(ss) > 0 && m.seedCursor < len(ss) {
				return m, pauseToggleCmd(m.eng, ss[m.seedCursor])
			}
		}
		return m, nil
	case "o":
		// Assign the command first: openSelected has a pointer receiver and sets
		// the notice on m, so it must run before `return m` copies m.
		switch m.section {
		case sectionDownloads:
			ds := m.downloading()
			if len(ds) > 0 && m.dlCursor < len(ds) {
				cmd := m.openSelected(ds[m.dlCursor])
				return m, cmd
			}
		case sectionSeeding:
			ss := m.seeding()
			if m.seedCursor < len(ss) {
				cmd := m.openSelected(ss[m.seedCursor])
				return m, cmd
			}
			hist := m.seedHistory()
			if hi := m.seedCursor - len(ss); hi >= 0 && hi < len(hist) {
				cmd := m.openHistory(hist[hi])
				return m, cmd
			}
		}
		return m, nil
	case "S":
		if m.section == sectionSearch {
			m.sortMode = true
			m.sortField = sortableCols[m.sortCol]
			applySort(m.results, m.sortField, m.sortDesc)
		}
		return m, nil
	}
	return m, nil
}

// handleSortKey handles input while the sort-mode overlay is active: arrows
// pick the column (left/right) and direction (up=asc, down=desc); esc/enter/S
// exit back to normal navigation.
func (m Model) handleSortKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "S", "esc", "enter":
		m.sortMode = false
	case "left", "h":
		if m.sortCol > 0 {
			m.sortCol--
		}
		m.sortField = sortableCols[m.sortCol]
		applySort(m.results, m.sortField, m.sortDesc)
	case "right", "l":
		if m.sortCol < len(sortableCols)-1 {
			m.sortCol++
		}
		m.sortField = sortableCols[m.sortCol]
		applySort(m.results, m.sortField, m.sortDesc)
	case "up", "k":
		m.sortDesc = false
		applySort(m.results, m.sortField, m.sortDesc)
	case "down", "j":
		m.sortDesc = true
		applySort(m.results, m.sortField, m.sortDesc)
	case "q", "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

// handleCancelKey handles input while the cancel-download confirm modal is
// active: k keeps partial files, d deletes them, esc/n aborts.
// handleStopKey handles the Seeding "stop seeding" confirm. Files are always
// kept — this just stops sharing and forgets the torrent.
func (m Model) handleStopKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter", "y":
		m.stopConfirm = false
		return m, removeCmd(m.eng, m.stopTarget.InfoHash, m.stopTarget.Name, false)
	case "esc", "n":
		m.stopConfirm = false
	case "q", "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

// handleHistKey handles the HISTORY-row delete confirm: k = remove entry (keep
// files), d = remove entry + delete files, esc = cancel.
func (m Model) handleHistKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	e := m.histTarget
	switch msg.String() {
	case "k":
		m.histConfirm = false
		m.history.Remove(e.InfoHash)
		m.history = history.Load()
		m.setNotice("Removed from history: " + truncate(e.Name, 40))
		return m, nil
	case "d":
		m.histConfirm = false
		var cmd tea.Cmd
		live := false
		for _, s := range m.statuses {
			if s.InfoHash == e.InfoHash {
				live = true
				break
			}
		}
		if live {
			cmd = removeCmd(m.eng, e.InfoHash, e.Name, true) // daemon stops + deletes; removedMsg reports
			m.history.Remove(e.InfoHash)
			m.history = history.Load()
			return m, cmd
		}
		// Non-live: keep the row until the delete actually succeeds (histDeletedMsg
		// handler removes it), so a failed delete doesn't orphan the files.
		return m, deleteHistoryFilesCmd(e.InfoHash, e.Name, m.cfg.DataDir)
	case "esc", "n":
		m.histConfirm = false
		return m, nil
	case "q", "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

// histDeletedMsg reports the outcome of deleteHistoryFilesCmd.
type histDeletedMsg struct {
	infoHash string
	name     string
	err      error
}

// deleteHistoryFilesCmd removes a finished download's files off the UI thread.
func deleteHistoryFilesCmd(infoHash, name, dataDir string) tea.Cmd {
	return func() tea.Msg {
		var err error
		if name != "" {
			err = engine.RemoveUnderDir(dataDir, name)
		}
		return histDeletedMsg{infoHash: infoHash, name: name, err: err}
	}
}

func (m Model) handleCancelKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "k":
		m.cancelConfirm = false
		return m, removeCmd(m.eng, m.cancelTarget.InfoHash, m.cancelTarget.Name, false)
	case "d":
		m.cancelConfirm = false
		return m, removeCmd(m.eng, m.cancelTarget.InfoHash, m.cancelTarget.Name, true)
	case "esc", "n":
		m.cancelConfirm = false
	case "q", "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

func (m Model) handleSearchEdit(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		q := strings.TrimSpace(m.input.Value())
		m.editing = false
		m.input.Blur()
		if q == "" {
			return m, nil
		}
		if mag := asMagnet(q); mag != "" {
			return m, addMagnetCmd(m.eng, mag)
		}
		m.section = sectionSearch
		cmd := m.startSearch(q)
		return m, cmd
	case "esc":
		m.editing = false
		m.input.Blur()
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// handleFilterEdit drives the in-results filter box. Typing narrows the loaded
// results live (via filteredResults); enter keeps the filter and returns to the
// list; esc clears it. No source is re-queried.
func (m Model) handleFilterEdit(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.editingFilter = false
		m.filterInput.Blur()
		m.cursor = 0
		return m, nil
	case "esc":
		m.editingFilter = false
		m.filterInput.Blur()
		m.filterInput.SetValue("")
		m.cursor = 0
		return m, nil
	}
	var cmd tea.Cmd
	m.filterInput, cmd = m.filterInput.Update(msg)
	if m.cursor >= len(m.filteredResults()) { // keep the selection in range as it narrows
		m.cursor = max(0, len(m.filteredResults())-1)
	}
	return m, cmd
}

func (m Model) handleSettingEdit(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		items := settingItems()
		if m.setCursor >= 0 && m.setCursor < len(items) {
			items[m.setCursor].set(&m, strings.TrimSpace(m.setInput.Value()))
			m.persist()
		}
		m.editingSetting = false
		m.setInput.Blur()
		return m, nil
	case "esc":
		m.editingSetting = false
		m.setInput.Blur()
		return m, nil
	}
	var cmd tea.Cmd
	m.setInput, cmd = m.setInput.Update(msg)
	return m, cmd
}

// handleMouse maps the scroll wheel to selection movement in the current pane.
// Text-entry modes and prompts (which the keyboard also skips) are left alone.
// ponytail: wheel-scroll only; click-to-select needs per-row Y hit-testing
// against the box layout — deferred until the layout math is factored out.
func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.editing || m.editingSetting {
		return m, nil
	}
	switch {
	case msg.Button == tea.MouseButtonWheelUp:
		m.moveUp()
	case msg.Button == tea.MouseButtonWheelDown:
		m.moveDown()
	case msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress:
		m.clickSelect(msg.X, msg.Y)
	}
	return m, nil
}

// clickSelect moves the selection to a clicked row. Implemented for the two
// list panes with clean, single-height-row geometry (Search results and
// Downloads); Seeding's two-section mixed-height layout and Settings' group
// headers are left to the keyboard/wheel.
// ponytail: mirrors the render layout in View/renderResults/renderDownloads —
// the render-anchored tests catch any drift if that layout changes.
func (m *Model) clickSelect(x, y int) {
	if x <= sidebarWidth { // sidebar or the 1-col gutter, not a main-pane row
		return
	}
	switch m.section {
	case sectionSearch:
		if !m.hasSearched && !m.searching && len(m.results) == 0 {
			return // home screen has no rows
		}
		start, end, pre := m.resultsWindow(max(1, m.bodyHeight()-4))
		// body line 0 is at headerHeight()+1; results box sits 3 lines in
		// (search box, filter row, blank) + 1 box border + preRows header lines.
		base := m.headerHeight() + 1 + 3 + 1 + pre
		if i := start + (y - base); y >= base && i >= start && i < end {
			m.cursor = i
		}
	case sectionDownloads:
		if len(m.downloading()) == 0 {
			return
		}
		cancelLines := 0
		if m.cancelConfirm {
			cancelLines = 2
		}
		base := m.headerHeight() + 1 + cancelLines // renderDownloads is body line 0 (no box)
		visible := max(1, m.bodyHeight()/4)        // 4 screen lines per download row
		shown := min(len(m.downloading()), visible)
		if line := y - base; line >= 0 && line%4 <= 2 { // name/bar/detail, not the blank
			if i := line / 4; i < shown {
				m.dlCursor = i
			}
		}
	case sectionSeeding:
		for _, r := range m.seedingClickRows() {
			if y >= r.y && y < r.y+r.span {
				m.seedCursor = r.idx
				return
			}
		}
		// Settings is intentionally excluded: its value column (e.g. a long "Save
		// to" path) wraps, so rows have variable height and click hit-testing
		// can't be reliable. Settings stays keyboard/wheel-navigable.
	}
}

// clickRow maps a clickable pane row to its selection index and how many screen
// lines it spans.
type clickRow struct {
	y, idx, span int
}

// seedingClickRows returns the screen-Y, cursor index, and line span of each
// clickable row in the Seeding pane (seeding items span 2 lines; HISTORY entries
// 1), mirroring renderSeeding's layout so clicks land on the right row.
func (m Model) seedingClickRows() []clickRow {
	ss := m.seeding()
	hist := m.seedHistory()
	shown := min(len(ss), max(1, m.bodyHeight()/3))

	line := 0
	if m.stopConfirm {
		line += 2
	}
	if m.histConfirm {
		line += 2
	}
	base := m.headerHeight() + 1
	rows := make([]clickRow, 0, shown+len(hist))
	for i := 0; i < shown; i++ {
		rows = append(rows, clickRow{y: base + line, idx: i, span: 2}) // name + detail
		line += 2
		if i < shown-1 {
			line++ // blank separator
		}
	}
	if len(hist) > 0 {
		if len(ss) > 0 {
			line += 2 // the "\n\n" gap before HISTORY
		}
		line++ // HISTORY header
		const histMax = 50
		for j := range hist {
			if j >= histMax {
				break
			}
			rows = append(rows, clickRow{y: base + line, idx: len(ss) + j, span: 1})
			line++
		}
	}
	return rows
}

// --- selection movement ----------------------------------------------------

func (m *Model) moveUp() {
	switch m.section {
	case sectionSearch:
		if m.cursor > 0 {
			m.cursor--
		}
	case sectionSettings:
		if m.setCursor > 0 {
			m.setCursor--
		}
	case sectionDownloads:
		if m.dlCursor > 0 {
			m.dlCursor--
		}
	case sectionSeeding:
		if m.seedCursor > 0 {
			m.seedCursor--
		}
	}
}

func (m *Model) moveDown() {
	switch m.section {
	case sectionSearch:
		if m.cursor < len(m.filteredResults())-1 {
			m.cursor++
		}
	case sectionSettings:
		if m.setCursor < len(settingItems())-1 {
			m.setCursor++
		}
	case sectionDownloads:
		if m.dlCursor < len(m.downloading())-1 {
			m.dlCursor++
		}
	case sectionSeeding:
		if m.seedCursor < len(m.seeding())+len(m.seedHistory())-1 {
			m.seedCursor++
		}
	}
}

func (m *Model) moveLeft() {
	switch m.section {
	case sectionSearch:
		if m.filter > 0 {
			m.filter--
			m.cursor = 0
		}
	case sectionSettings:
		m.settingsChange(-1)
	}
}

func (m *Model) moveRight() {
	switch m.section {
	case sectionSearch:
		if m.filter < len(filterCats)-1 {
			m.filter++
			m.cursor = 0
		}
	case sectionSettings:
		m.settingsChange(1)
	}
}

// activate handles enter in command mode: download in Search, edit/cycle in
// Settings.
func (m *Model) activate() tea.Cmd {
	switch m.section {
	case sectionSearch:
		fr := m.filteredResults()
		if len(fr) > 0 && m.cursor < len(fr) {
			m.showDetail = true
			m.detail = fr[m.cursor]
		}
		return nil
	case sectionSettings:
		items := settingItems()
		if m.setCursor < 0 || m.setCursor >= len(items) {
			return nil
		}
		it := items[m.setCursor]
		if it.kind == kindEnum {
			m.settingsChange(1)
			return nil
		}
		// Text setting: open the inline editor seeded with the current value.
		m.editingSetting = true
		m.setInput.SetValue(it.get(m))
		m.setInput.CursorEnd()
		m.setInput.Focus()
		return textinput.Blink
	}
	return nil
}

// --- settings --------------------------------------------------------------

type setKind int

const (
	kindEnum setKind = iota
	kindText
)

type setItem struct {
	group   string
	label   string
	kind    setKind
	options []string
	get     func(m *Model) string
	set     func(m *Model, v string)
}

// settingItems is the ordered, navigable list of Settings rows (group headers
// are a render concern). Editing a value applies its side effect immediately
// and the change is persisted by the caller.
func settingItems() []setItem {
	items := []setItem{
		{group: "APPEARANCE", label: "Theme", kind: kindEnum, options: []string{"Twilight", "Tide"},
			get: func(m *Model) string { return m.cfg.Theme },
			set: func(m *Model, v string) { m.cfg.Theme = v; m.applyTheme() }},
		{group: "APPEARANCE", label: "Color mode", kind: kindEnum, options: []string{"auto", "truecolor", "256", "off"},
			get: func(m *Model) string { return m.cfg.ColorMode },
			set: func(m *Model, v string) { m.cfg.ColorMode = v; applyColorMode(v) }},
		{group: "DOWNLOADS", label: "Save to", kind: kindText,
			get: func(m *Model) string { return m.cfg.DataDir },
			set: func(m *Model, v string) {
				if v != "" {
					m.cfg.DataDir = v
				}
			}},
		{group: "DOWNLOADS", label: "When done", kind: kindEnum, options: []string{"keep seeding", "stop"},
			get: func(m *Model) string {
				if m.cfg.Seed {
					return "keep seeding"
				}
				return "stop"
			},
			set: func(m *Model, v string) { m.cfg.Seed = v == "keep seeding" }},
		{group: "DOWNLOADS", label: "Seed ratio", kind: kindText,
			get: func(m *Model) string { return fmt.Sprintf("%.1f", m.cfg.SeedRatio) },
			set: func(m *Model, v string) {
				if f, err := strconv.ParseFloat(v, 64); err == nil {
					m.cfg.SeedRatio = f
				}
			}},
		{group: "DOWNLOADS", label: "Max peers", kind: kindText,
			get: func(m *Model) string { return strconv.Itoa(m.cfg.MaxPeers) },
			set: func(m *Model, v string) {
				if n, err := strconv.Atoi(v); err == nil {
					m.cfg.MaxPeers = n
				}
			}},
		{group: "DOWNLOADS", label: "Listen port", kind: kindText,
			get: func(m *Model) string { return strconv.Itoa(m.cfg.ListenPort) },
			set: func(m *Model, v string) {
				if n, err := strconv.Atoi(v); err == nil {
					m.cfg.ListenPort = n
				}
			}},
		{group: "DOWNLOADS", label: "Down limit KB/s (0=∞)", kind: kindText,
			get: func(m *Model) string { return strconv.Itoa(m.cfg.DownloadRateKB) },
			set: func(m *Model, v string) {
				if n, err := strconv.Atoi(v); err == nil && n >= 0 {
					m.cfg.DownloadRateKB = n
				}
			}},
		{group: "DOWNLOADS", label: "Up limit KB/s (0=∞)", kind: kindText,
			get: func(m *Model) string { return strconv.Itoa(m.cfg.UploadRateKB) },
			set: func(m *Model, v string) {
				if n, err := strconv.Atoi(v); err == nil && n >= 0 {
					m.cfg.UploadRateKB = n
				}
			}},
		{group: "DOWNLOADS", label: "Max active (0=∞)", kind: kindText,
			get: func(m *Model) string { return strconv.Itoa(m.cfg.MaxActiveDownloads) },
			set: func(m *Model, v string) {
				if n, err := strconv.Atoi(v); err == nil && n >= 0 {
					m.cfg.MaxActiveDownloads = n
				}
			}},
		{group: "DOWNLOADS", label: "Notify on done", kind: kindEnum, options: []string{"on", "off"},
			get: func(m *Model) string {
				if m.cfg.NotifyOnComplete {
					return "on"
				}
				return "off"
			},
			set: func(m *Model, v string) { m.cfg.NotifyOnComplete = v == "on" }},
		{group: "UPDATES", label: "Auto-update", kind: kindEnum, options: []string{"off", "on"},
			get: func(m *Model) string {
				if m.cfg.AutoUpdate {
					return "on"
				}
				return "off"
			},
			set: func(m *Model, v string) { m.cfg.AutoUpdate = v == "on" }},
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

func (m *Model) settingsChange(dir int) {
	items := settingItems()
	if m.setCursor < 0 || m.setCursor >= len(items) {
		return
	}
	it := items[m.setCursor]
	if it.kind != kindEnum || len(it.options) == 0 {
		return
	}
	cur := it.get(m)
	idx := 0
	for i, o := range it.options {
		if o == cur {
			idx = i
			break
		}
	}
	idx = (idx + dir + len(it.options)) % len(it.options)
	it.set(m, it.options[idx])
	m.persist()
}

// applyTheme swaps the active palette and rebuilds the colour-bearing widgets
// (spinner, progress bar) so a theme switch takes effect immediately.
func (m *Model) applyTheme() {
	setPalette(paletteByName(m.cfg.Theme))
	m.spin.Style = st.SearchLabel
	pr := progress.New(progress.WithSolidFill(activePalette.Accent.TrueColor))
	pr.ShowPercentage = false
	m.prog = pr
}

func (m *Model) persist() { _ = m.cfg.Save() }

// --- derived views over state ----------------------------------------------

// filterActive reports whether the in-results text filter is engaged (focused
// or holding a query).
func (m Model) filterActive() bool {
	return m.editingFilter || m.filterInput.Value() != ""
}

// filteredResults applies the active media filter, the hide-0-seed toggle, and
// the in-results text filter to the search results — all client-side, without
// re-querying sources.
func (m Model) filteredResults() []source.Result {
	cat := filterCats[m.filter].Mediatype
	q := strings.ToLower(strings.TrimSpace(m.filterInput.Value()))
	if cat == "" && !m.hideZeroSeed && q == "" {
		return m.results
	}
	out := make([]source.Result, 0, len(m.results))
	for _, r := range m.results {
		if cat != "" && !strings.EqualFold(r.Category, cat) {
			continue
		}
		// Hide only confirmed 0-seeder results; keep those from sources that don't
		// report seeders at all (Seeders==0 but SeedersKnown==false).
		if m.hideZeroSeed && r.SeedersKnown && r.Seeders <= 0 {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(r.Title), q) {
			continue
		}
		out = append(out, r)
	}
	return out
}

func (m Model) downloading() []engine.Status {
	out := make([]engine.Status, 0, len(m.statuses))
	for _, s := range m.statuses {
		if !s.Done {
			out = append(out, s)
		}
	}
	return out
}

func (m Model) seeding() []engine.Status {
	out := make([]engine.Status, 0, len(m.statuses))
	for _, s := range m.statuses {
		if s.Done {
			out = append(out, s)
		}
	}
	return out
}

// seedHistory returns finished torrents from history that aren't currently
// active seeders — the Seeding pane's HISTORY section, selectable for opening.
func (m Model) seedHistory() []history.Entry {
	active := make(map[string]bool, len(m.statuses))
	for _, s := range m.seeding() {
		active[s.InfoHash] = true
	}
	var hist []history.Entry
	for _, e := range m.history.Entries {
		if !active[e.InfoHash] {
			hist = append(hist, e)
		}
	}
	return hist
}

// openHistory opens a finished download's folder, preferring the live torrent's
// path (still loaded) over the path stored in history.
func (m *Model) openHistory(e history.Entry) tea.Cmd {
	// 1. Still-loaded torrent → its exact on-disk path.
	for _, s := range m.statuses {
		if s.InfoHash == e.InfoHash && s.Path != "" {
			return m.openSelected(s)
		}
	}
	// 2. Path saved at completion (v0.4.9+).
	if e.Path != "" {
		return m.openSelected(engine.Status{Path: e.Path})
	}
	// 3. Legacy entry (completed before paths were recorded): best-effort the
	//    item's folder under the download dir, else open the download dir itself
	//    so `o` always lands somewhere useful — never the misleading "not ready".
	if m.cfg.DataDir != "" {
		if guess := filepath.Join(m.cfg.DataDir, e.Name); pathExists(guess) {
			return m.openSelected(engine.Status{Path: guess})
		}
		m.setNotice("opened downloads folder — exact location wasn't recorded for this older download")
		return openFolderCmd(m.cfg.DataDir)
	}
	m.setError("couldn't locate this download's folder")
	return nil
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// mainWidth is the width of the content pane (everything right of the sidebar).
func (m Model) mainWidth() int {
	return max(20, m.width-sidebarWidth-1)
}

// bodyHeight is the number of screen rows the body occupies (between the two
// rules), mirroring View's bodyH. Body line 0 sits at screen row headerHeight()+1.
func (m Model) bodyHeight() int {
	return max(3, m.height-m.headerHeight()-3)
}

// resultsWindow returns the [start,end) slice of filtered results visible in a
// results box of content height h, plus preRows — the number of lines above the
// first data row (column header, and the optional "searching…" / sort-bar
// lines). Shared by renderResults and click hit-testing so they can't drift.
func (m Model) resultsWindow(h int) (start, end, preRows int) {
	fr := m.filteredResults()
	visible := max(1, h-3)
	if m.cursor >= visible {
		start = m.cursor - visible + 1
	}
	end = min(len(fr), start+visible)
	preRows = 1 // column header
	if m.searching {
		preRows++
	}
	if m.sortMode {
		preRows++
	}
	if m.filterActive() {
		preRows++
	}
	return start, end, preRows
}

func (m *Model) setNotice(s string) {
	m.notice = s
	m.noticeErr = false
	m.noticeUntil = time.Now().Add(4 * time.Second)
}

func (m *Model) setError(s string) {
	m.notice = s
	m.noticeErr = true
	m.noticeUntil = time.Now().Add(6 * time.Second)
}
