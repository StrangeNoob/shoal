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
	"github.com/StrangeNoob/shoal/internal/source"
	upd "github.com/StrangeNoob/shoal/internal/update"
)

const sidebarWidth = 20

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
	showHelp       bool
	showDetail     bool
	detail         source.Result

	input    textinput.Model // search box
	setInput textinput.Model // settings inline editor
	spin     spinner.Model
	prog     progress.Model

	src source.Source
	eng engine.Engine
	cfg config.Config

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

	statuses    []engine.Status
	dlSpeed     map[string]int64 // download byte/sec per Status.Name, sampled between ticks
	ulSpeed     map[string]int64 // upload (seeding) byte/sec per Status.Name
	lastTick    time.Time        // timestamp of the previous tick, for the rate delta
	history     history.Store    // completed-download record; injected via WithHistory
	setCursor   int              // index into settingItems()
	version     string           // build version (ldflags), "" or "dev" for local builds
	updateAvail string           // latest version when a newer release is available

	dlCursor      int // selection in the Downloads pane
	seedCursor    int // selection in the Seeding pane
	cancelConfirm bool
	cancelTarget  engine.Status
	stopConfirm   bool          // Seeding pane: confirm "stop seeding"
	stopTarget    engine.Status // the torrent being stopped

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

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = st.SearchLabel

	pr := progress.New(progress.WithSolidFill(activePalette.Accent.TrueColor))
	pr.ShowPercentage = false

	return Model{
		section:  sectionSearch,
		editing:  false,
		input:    ti,
		setInput: si,
		spin:     sp,
		prog:     pr,
		src:      src,
		eng:      eng,
		cfg:      cfg,
		sortDesc: true,
		booting:  true,
	}
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
	return func() tea.Msg { return folderOpenedMsg{err: openInFileManager(dir)} }
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

// newlyCompleted returns torrents that flipped Done false→true (or first appeared
// already Done) between two snapshots, keyed by InfoHash.
func newlyCompleted(prev, next []engine.Status) []engine.Status {
	was := make(map[string]bool, len(prev))
	for _, s := range prev {
		if s.Done {
			was[s.InfoHash] = true
		}
	}
	var out []engine.Status
	for _, s := range next {
		if s.Done && s.InfoHash != "" && !was[s.InfoHash] {
			out = append(out, s)
		}
	}
	return out
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
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

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

	case folderOpenedMsg:
		if msg.err != nil {
			m.setError("couldn't open the folder")
		}
		return m, nil

	case tickMsg:
		if m.eng != nil {
			now := time.Time(msg)
			next := m.eng.Statuses()
			dt := now.Sub(m.lastTick)
			m.dlSpeed = computeRates(m.statuses, next, dt, func(s engine.Status) int64 { return s.Downloaded })
			m.ulSpeed = computeRates(m.statuses, next, dt, func(s engine.Status) int64 { return s.Uploaded })
			if len(newlyCompleted(m.statuses, next)) > 0 {
				m.history = history.Load() // the daemon records completions; refresh the History pane
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
		}
		if m.notice != "" && time.Now().After(m.noticeUntil) {
			m.notice = ""
			m.noticeErr = false
		}
		return m, tickCmd()

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

// filteredResults applies the active media filter to the search results.
func (m Model) filteredResults() []source.Result {
	cat := filterCats[m.filter].Mediatype
	if cat == "" {
		return m.results
	}
	out := make([]source.Result, 0, len(m.results))
	for _, r := range m.results {
		if strings.EqualFold(r.Category, cat) {
			out = append(out, r)
		}
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
