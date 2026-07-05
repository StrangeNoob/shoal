package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/StrangeNoob/shoal/internal/source"
	upd "github.com/StrangeNoob/shoal/internal/update"
)

func (m Model) View() string {
	if !m.ready {
		return m.renderSplash(80, 24, 0, false) // pre-size flash: still logo
	}
	if m.booting {
		return m.renderSplash(m.width, m.height, m.splashT(), true)
	}
	if m.showHelp {
		return m.helpView()
	}

	header := m.renderHeader()
	rule := st.Rule.Render(strings.Repeat("─", max(1, m.width)))
	bodyH := m.bodyHeight() // header (up to 6 rows) + 2 rules + footer
	body := m.renderBody(bodyH)
	footer := m.renderFooter()

	return strings.Join([]string{header, rule, body, rule, footer}, "\n")
}

func (m Model) renderBody(h int) string {
	sidebar := lipgloss.NewStyle().Width(sidebarWidth).Height(h).Render(m.renderSidebar())
	main := lipgloss.NewStyle().Width(m.mainWidth()).Height(h).Render(m.renderMain(m.mainWidth(), h))
	return lipgloss.JoinHorizontal(lipgloss.Top, sidebar, " ", main)
}

func (m Model) renderSidebar() string {
	var b strings.Builder
	b.WriteString("\n")

	item := func(sec section, label string, count int, hasCount bool) string {
		on := m.section == sec
		nav, navStyle, labStyle := glyphNavOff, st.Faint, st.SideInactive
		if on {
			nav, navStyle, labStyle = glyphNavOn, st.SideActive, st.SideActive
		}
		line := navStyle.Render(nav) + " " + labStyle.Render(label)
		if hasCount && count > 0 {
			line += "  " + st.Count.Render(fmt.Sprintf("%d", count))
		}
		return line
	}

	b.WriteString(item(sectionSearch, "Search", len(m.filteredResults()), true))
	b.WriteString("\n")
	b.WriteString(item(sectionDownloads, "Downloads", len(m.downloading()), true))
	b.WriteString("\n")
	b.WriteString(item(sectionSeeding, "Seeding", len(m.seeding()), true))
	b.WriteString("\n\n")
	b.WriteString(item(sectionSettings, "Settings", 0, false))
	b.WriteString("\n\n")
	b.WriteString(st.Faint.Render(m.src.Name()))
	return b.String()
}

func (m Model) renderMain(w, h int) string {
	switch m.section {
	case sectionDownloads:
		return m.renderDownloads(w, h)
	case sectionSeeding:
		return m.renderSeeding(w, h)
	case sectionSettings:
		return m.renderSettings(w, h)
	default:
		if m.showDetail {
			return m.renderDetail(w, h)
		}
		return m.renderSearch(w, h)
	}
}

// --- Search ----------------------------------------------------------------

func (m Model) renderSearch(w, h int) string {
	// First run, before any search: the welcome / home screen.
	if !m.hasSearched && !m.searching && len(m.results) == 0 {
		return m.renderHome(w, h)
	}

	box := st.SearchLabel.Render("/ ") + m.input.View()
	if m.searching {
		box += "  " + m.spin.View() + st.Meta.Render(" searching…")
	}

	var b strings.Builder
	b.WriteString(box)
	b.WriteString("\n")
	b.WriteString(m.renderFilterRow(w))
	b.WriteString("\n\n")
	b.WriteString(m.renderResults(w, max(1, h-4)))
	return b.String()
}

func (m Model) renderFilterRow(w int) string {
	parts := make([]string, 0, len(filterCats))
	for i, fc := range filterCats {
		if i == m.filter {
			parts = append(parts, st.FilterOn.Render(" "+fc.Label+" "))
		} else {
			parts = append(parts, st.FilterOff.Render(fc.Label))
		}
	}
	// Don't truncate here: the parts carry ANSI escapes, so a rune-count
	// truncate would miscount width. The main pane's lipgloss box (Width set in
	// renderBody) soft-wraps this row on very narrow terminals instead.
	_ = w
	return "  " + strings.Join(parts, "  ")
}

func (m Model) renderHome(w, h int) string {
	cmd := func(k, desc string) string {
		pad := 9 - lipgloss.Width(k)
		if pad < 1 {
			pad = 1
		}
		return "  " + st.Key.Render(k) + strings.Repeat(" ", pad) + st.Meta.Render(desc) + "\n"
	}

	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(st.Meta.Render("A calm BitTorrent client for your terminal. Search the Internet") + "\n")
	b.WriteString(st.Meta.Render("Archive and download freely-shared films, music, books and") + "\n")
	b.WriteString(st.Meta.Render("software — with live progress and seeding.") + "\n\n")

	b.WriteString(st.SectionHead.Render("HOW IT WORKS") + "\n")
	b.WriteString(cmd("/", "type a query, then enter to search"))
	b.WriteString(cmd("↑ ↓", "move the selection"))
	b.WriteString(cmd("← →", "narrow by media type · change a setting"))
	b.WriteString(cmd("enter", "open a result's details"))
	b.WriteString(cmd("d", "download the selected result"))
	b.WriteString(cmd("S", "sort results  (← → column · ↑ ↓ direction)"))
	b.WriteString(cmd("p", "pause / resume a download"))
	b.WriteString(cmd("x", "cancel a download  (keep or delete files)"))
	b.WriteString(cmd("y", "copy magnet  (in a result's details)"))
	b.WriteString(cmd("tab", "cycle Search · Downloads · Seeding · Settings"))
	b.WriteString(cmd("esc", "leave search · close details · cancel"))
	b.WriteString(cmd("? / q", "show all keys · quit") + "\n")

	b.WriteString(st.SectionHead.Render("START HERE") + "\n")
	b.WriteString(st.SearchLabel.Render("/ ") + m.input.View() + "\n")
	b.WriteString(m.renderFilterRow(w) + "\n\n")
	b.WriteString("  " + st.Faint.Render("try  ") + st.Row.Render("ubuntu 24.04   ·   blender   ·   jazz   ·   public-domain films") + "\n")
	b.WriteString("  " + st.Meta.Render("or paste a magnet link to add it directly."))
	return b.String()
}

func (m Model) renderResults(w, h int) string {
	fr := m.filteredResults()
	if len(fr) == 0 && !m.searching {
		if m.filter != 0 {
			return "  " + st.Meta.Render("No matches in ") + st.Accent.Render(filterCats[m.filter].Label) +
				st.Meta.Render(". Try ") + st.Key.Render("← →") + st.Meta.Render(" for another filter.")
		}
		return "  " + st.Meta.Render("No matches. Try fewer or different words — or paste a magnet link.")
	}

	boxW := max(24, w)
	inner := boxW - 2

	// Column widths (right-aligned numeric columns; Name flexes). Leave a couple
	// columns of slack so no assembled row exceeds `inner` — titledBox pads short
	// lines but does not truncate long ones, so an over-long row would bow out the
	// right border.
	numW := max(2, len(strconv.Itoa(len(fr))))
	const sizeW, slW, srcW = 8, 9, 5
	nameW := max(6, inner-(numW+sizeW+slW+srcW+12))

	arrowFor := func(fields ...sortField) string {
		for _, f := range fields {
			if m.sortField == f {
				if m.sortDesc {
					return "▼"
				}
				return "▲"
			}
		}
		return ""
	}
	colHead := func(label string, fields ...sortField) string {
		return label + arrowFor(fields...)
	}

	var body strings.Builder

	if m.searching {
		body.WriteString(st.Meta.Render(fmt.Sprintf("searching… %d/%d sources", m.sourcesDone, m.sourcesTotal)) + "\n")
	}
	if m.sortMode {
		body.WriteString(m.renderSortBar() + "\n")
	}

	// Header row (prefix "  " matches the row's marker+space so Name aligns).
	head := "  " + st.Faint.Render(leftPad("#", numW)) + " " +
		st.Faint.Render(padRight("Name", nameW)) + " " +
		st.Faint.Render(leftPad(colHead("Size", sortSize), sizeW)) + "  " +
		st.Faint.Render(leftPad(colHead("Seed:Lch", sortSeeders, sortLeechers, sortRatio), slW)) + "  " +
		st.Faint.Render(leftPad("Src", srcW))
	body.WriteString(head + "\n")

	start, end, _ := m.resultsWindow(h)

	for i := start; i < end; i++ {
		r := fr[i]
		selected := i == m.cursor
		marker, nameStyle := " ", st.Row
		if selected {
			marker, nameStyle = st.Accent.Render(glyphCursor), st.RowSel
		}
		num := leftPad(strconv.Itoa(i+1), numW)
		name := padRight(truncate(r.Title, nameW), nameW)
		row := marker + " " + st.Faint.Render(num) + " " +
			nameStyle.Render(name) + " " +
			st.Meta.Render(leftPad(shortSize(r.SizeBytes), sizeW)) + "  " +
			st.Meta.Render(leftPad(seedLeech(r), slW)) + "  " +
			st.Meta.Render(leftPad(r.Source, srcW))
		body.WriteString(row)
		if i < end-1 {
			body.WriteString("\n")
		}
	}
	if end < len(fr) {
		// keep glyphMore — the existing TestRenderResultsListWithOverflow asserts it
		body.WriteString("\n" + st.Faint.Render(fmt.Sprintf("%s %d more %s", glyphMore, len(fr)-end, glyphDown)))
	}

	title := fmt.Sprintf("Results (%d)", len(fr))
	return titledBox(title, "", body.String(), boxW, m.section == sectionSearch)
}

func (m Model) renderSortBar() string {
	parts := make([]string, 0, len(sortableCols))
	for i, f := range sortableCols {
		lbl := f.label()
		if i == m.sortCol {
			dir := "▼"
			if !m.sortDesc {
				dir = "▲"
			}
			parts = append(parts, st.Accent.Render("[ "+lbl+" "+dir+" ]"))
		} else {
			parts = append(parts, st.Faint.Render(lbl))
		}
	}
	return st.SectionHead.Render("Sort ▸") + " " + strings.Join(parts, "   ")
}

func (m Model) renderDetail(w, h int) string {
	r := m.detail
	boxW := max(24, w)

	var b strings.Builder
	b.WriteString(st.Row.Render(truncate(r.Title, boxW-4)) + "\n\n")

	row := func(label, val string) {
		// val may be a styled span; an empty val (e.g. relTime(0)=="" → Render("")=="") omits the row.
		if val == "" {
			return
		}
		b.WriteString(st.Faint.Render(padRight(label, 8)) + " " + val + "\n")
	}

	row("Size", st.Row.Render(sizeOrDash(r.SizeBytes)))
	row("Health", detailHealth(r))
	if r.Files > 0 {
		row("Files", st.Row.Render(fmt.Sprintf("%d", r.Files)))
	}
	row("Added", st.Meta.Render(relTime(r.Added)))
	if pm := source.ParseMagnetInfoHash(r.Magnet); pm != "" {
		row("Hash", st.Faint.Render(pm))
	}
	if r.Magnet != "" {
		row("Magnet", st.Faint.Render(truncate(r.Magnet, boxW-14)))
	} else if r.TorrentURL != "" {
		row("Torrent", st.Faint.Render(truncate(r.TorrentURL, boxW-14)))
	}

	b.WriteString("\n")
	b.WriteString(st.Key.Render("d") + " " + st.KeyDesc.Render("Download") + "   " + st.FooterSep.Render("·") + "   ")
	b.WriteString(st.Key.Render("y") + " " + st.KeyDesc.Render("Copy magnet") + "   " + st.FooterSep.Render("·") + "   ")
	b.WriteString(st.Key.Render("esc") + " " + st.KeyDesc.Render("back"))

	query := st.SearchLabel.Render("❯ ") + truncate(m.input.Value(), boxW-6)
	search := titledBox("Search", "", query, boxW, false)
	details := titledBox("Details", r.Source, b.String(), boxW, true)
	return search + "\n" + details
}

func detailHealth(r source.Result) string {
	if r.Seeders == 0 && r.Leechers == 0 {
		if r.Popularity > 0 {
			return st.Meta.Render(fmt.Sprintf("%s downloads", thousands(r.Popularity)))
		}
		return st.Meta.Render("—")
	}
	return st.Good.Render(fmt.Sprintf("%d", r.Seeders)) + st.Meta.Render(fmt.Sprintf(" seeders · %d leechers", r.Leechers))
}

// --- Downloads (in progress) -----------------------------------------------

func (m Model) renderDownloads(w, h int) string {
	ds := m.downloading()
	if len(ds) == 0 {
		return "  " + st.Meta.Render("No active downloads. Find something in ") +
			st.Accent.Render("Search") + st.Meta.Render(" and press ") + st.Key.Render("d") + st.Meta.Render(".")
	}

	const perItem = 4
	visible := max(1, h/perItem)
	barWidth := max(10, min(48, w-24))

	var b strings.Builder
	if m.cancelConfirm {
		b.WriteString("  " + st.Bad.Render("Cancel ") +
			st.Row.Render("\""+truncate(m.cancelTarget.Name, max(8, w-32))+"\"") + st.Meta.Render("?   ") +
			st.Key.Render("k") + st.Meta.Render(" keep files   ·   ") +
			st.Key.Render("d") + st.Meta.Render(" delete files   ·   ") +
			st.Key.Render("esc") + st.Meta.Render(" back") + "\n\n")
	}

	shown := min(len(ds), visible)
	for i := 0; i < shown; i++ {
		s := ds[i]
		head, nameStyle := st.Accent.Render(glyphDown+" "), st.Row
		if i == m.dlCursor {
			head, nameStyle = st.Accent.Render(glyphCursor+" "), st.RowSel
		}
		b.WriteString(head + nameStyle.Render(truncate(s.Name, max(4, w-4))) + "\n")

		p := m.prog
		p.Width = barWidth
		bar := p.ViewAs(s.Percent())

		state := fmt.Sprintf("%5.1f%%", s.Percent()*100)
		detail := fmt.Sprintf("%s / %s  ·  %d peers", formatBytes(s.CompletedBytes), sizeOrDash(s.TotalBytes), s.Peers)
		switch {
		case s.Paused:
			detail += "  ·  ⏸ paused"
		case m.dlSpeed[s.Name] > 0:
			detail += fmt.Sprintf("  ·  %s/s", formatBytes(m.dlSpeed[s.Name]))
			if eta := formatETA(etaSeconds(s, m.dlSpeed[s.Name])); eta != "" {
				detail += "  ·  ETA " + eta
			}
		}

		b.WriteString("  " + bar + "  " + st.Row.Render(state) + "\n")
		b.WriteString("  " + st.Meta.Render(detail) + "\n")
		if i < shown-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// --- Seeding (complete) ----------------------------------------------------

func (m Model) renderSeeding(w, h int) string {
	ss := m.seeding()
	hist := m.seedHistory()

	if len(ss) == 0 && len(hist) == 0 {
		return "  " + st.Meta.Render("Nothing seeding yet. Completed downloads keep sharing here.")
	}

	const perItem = 3
	visible := max(1, h/perItem)

	var b strings.Builder
	if m.stopConfirm {
		b.WriteString("  " + st.Bad.Render("Stop seeding ") +
			st.Row.Render("\""+truncate(m.stopTarget.Name, max(8, w-32))+"\"") + st.Meta.Render("?   ") +
			st.Key.Render("enter") + st.Meta.Render(" stop (keep files)   ·   ") +
			st.Key.Render("esc") + st.Meta.Render(" back") + "\n\n")
	}
	if m.histConfirm {
		b.WriteString("  " + st.Bad.Render("Delete ") +
			st.Row.Render("\""+truncate(m.histTarget.Name, max(8, w-32))+"\"") + st.Meta.Render(" from history?   ") +
			st.Key.Render("k") + st.Meta.Render(" remove   ·   ") +
			st.Key.Render("d") + st.Meta.Render(" +delete files   ·   ") +
			st.Key.Render("esc") + st.Meta.Render(" back") + "\n\n")
	}
	shown := min(len(ss), visible)
	for i := 0; i < shown; i++ {
		s := ss[i]
		head, nameStyle := st.Good.Render(glyphSeed+" "), st.Row
		if i == m.seedCursor {
			head, nameStyle = st.Accent.Render(glyphCursor+" "), st.RowSel
		}
		b.WriteString(head + nameStyle.Render(truncate(s.Name, max(4, w-4))) + "\n")

		if s.Paused {
			b.WriteString("  " + st.Meta.Render("⏸ paused (not sharing)") + "\n")
		} else {
			label := "complete"
			if s.Seeding {
				label = "seeding"
			}
			// Always show the upload total (↑ 0 B when nothing's been shared yet);
			// add the ratio once there's been any upload.
			up := fmt.Sprintf("  ·  %s %s", glyphSeed, formatBytes(s.Uploaded))
			if s.Uploaded > 0 {
				up = fmt.Sprintf("  ·  ratio %.2f  ·  %s %s", s.Ratio(), glyphSeed, formatBytes(s.Uploaded))
			}
			// Active downloaders; if none are connected but announce found peers,
			// show how many are known so 'seeding, no demand' reads differently
			// from 'not announcing'.
			peers := fmt.Sprintf("  ·  %d peers", s.Peers)
			if s.Peers == 0 && s.TotalPeers > 0 {
				peers = fmt.Sprintf("  ·  0 peers (%d known)", s.TotalPeers)
			}
			detail := up + peers
			if sp := m.ulSpeed[s.Name]; sp > 0 {
				detail += fmt.Sprintf("  ·  %s/s", formatBytes(sp))
			}
			b.WriteString("  " + st.Good.Render(glyphDone+" "+label) + st.Meta.Render(truncate(detail, max(4, w-14))) + "\n")
		}
		if i < shown-1 {
			b.WriteString("\n")
		}
	}

	if len(hist) > 0 {
		if len(ss) > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(st.SectionHead.Render("HISTORY") + "\n")
		const histMax = 50
		for i, e := range hist {
			if i >= histMax {
				b.WriteString("  " + st.Faint.Render(fmt.Sprintf("%s %d more %s", glyphMore, len(hist)-histMax, glyphDown)) + "\n")
				break
			}
			meta := "  ·  " + sizeOrDash(e.Size) + "  ·  " + relTime(e.CompletedAt.Unix())
			marker, nameStyle := st.Good.Render(glyphDone+" "), st.Row
			if len(ss)+i == m.seedCursor {
				marker, nameStyle = st.Accent.Render(glyphCursor+" "), st.RowSel
			}
			b.WriteString("  " + marker + nameStyle.Render(truncate(e.Name, max(4, w-24))) + st.Meta.Render(meta) + "\n")
		}
	}
	return b.String()
}

// --- Settings --------------------------------------------------------------

func (m Model) renderSettings(w, h int) string {
	items := settingItems()

	// Render each row into its own block of lines (a group header, preceded by a
	// blank separator, leads the first row of each group).
	blocks := make([][]string, len(items))
	counts := make([]int, len(items))
	lastGroup := ""
	for i, it := range items {
		var ls []string
		if it.group != lastGroup {
			if lastGroup != "" {
				ls = append(ls, "")
			}
			ls = append(ls, st.SectionHead.Render(it.group))
			lastGroup = it.group
		}
		sel := i == m.setCursor
		cursor, labStyle := "  ", st.SetLabel
		if sel {
			cursor, labStyle = st.Accent.Render(glyphCursor+" "), st.SetLabelSel
		}
		label := labStyle.Render(padOrTrim(it.label, 13))
		var val string
		if sel && m.editingSetting {
			val = m.setInput.View()
		} else {
			val = m.renderSettingValue(it)
		}
		ls = append(ls, cursor+label+"  "+val)
		blocks[i], counts[i] = ls, len(ls)
	}

	// ABOUT is informational, not navigable — always pinned at the bottom.
	about := []string{
		"",
		st.SectionHead.Render("ABOUT"),
		"  " + st.SetLabel.Render(padOrTrim("shoal", 13)) + "  " + st.Meta.Render(upd.DisplayVersion(m.version)+"  ·  anacrolix engine"),
		"  " + st.Meta.Render("engine settings (port, peers, save-to, seed) apply when the daemon restarts"),
	}

	// Window the navigable rows around the cursor so the selection is always
	// visible even when the list is taller than the pane (e.g. many SOURCES rows).
	avail := h - len(about)
	if avail < 1 {
		avail = 1
	}
	start, end := settingsWindow(counts, m.setCursor, avail)

	var b strings.Builder
	for i := start; i < end; i++ {
		for _, ln := range blocks[i] {
			b.WriteString(ln + "\n")
		}
	}
	for _, ln := range about {
		b.WriteString(ln + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// settingsWindow returns the [start,end) range of setting rows to render so that
// the cursor is visible and the total line count fits in avail, growing outward
// from the cursor (downward first, then upward).
func settingsWindow(counts []int, cursor, avail int) (start, end int) {
	n := len(counts)
	if n == 0 {
		return 0, 0
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= n {
		cursor = n - 1
	}
	used := counts[cursor]
	start, end = cursor, cursor+1
	for {
		grew := false
		if end < n && used+counts[end] <= avail {
			used += counts[end]
			end++
			grew = true
		}
		if start > 0 && used+counts[start-1] <= avail {
			used += counts[start-1]
			start--
			grew = true
		}
		if !grew {
			break
		}
	}
	return start, end
}

func (m Model) renderSettingValue(it setItem) string {
	if it.kind == kindEnum {
		cur := it.get(&m)
		parts := make([]string, 0, len(it.options))
		for _, o := range it.options {
			if o == cur {
				parts = append(parts, st.SetValOn.Render(glyphNavOn+" "+o))
			} else {
				parts = append(parts, st.Meta.Render(glyphNavOff+" "+o))
			}
		}
		return strings.Join(parts, "   ")
	}
	style := st.SetVal
	if it.label == "Save to" {
		style = st.Accent
	}
	return style.Render(it.get(&m))
}

// --- footer & help ---------------------------------------------------------

func (m Model) renderFooter() string {
	hint := func(key, desc string) string {
		return st.Key.Render(key) + " " + st.KeyDesc.Render(desc)
	}
	sep := st.FooterSep.Render("   ")

	var parts []string
	switch {
	case m.editing:
		parts = []string{hint("enter", "search"), hint("esc", "cancel")}
	case m.editingSetting:
		parts = []string{hint("enter", "save"), hint("esc", "cancel")}
	case m.showDetail:
		parts = []string{hint("d", "download"), hint("y", "copy magnet"), hint("esc", "back")}
	case m.sortMode:
		parts = []string{hint("←→", "column"), hint("↑↓", "direction"), hint("esc", "done")}
	case m.cancelConfirm:
		parts = []string{hint("k", "keep files"), hint("d", "delete files"), hint("esc", "back")}
	case m.stopConfirm:
		parts = []string{hint("enter", "stop"), hint("esc", "back")}
	case m.histConfirm:
		parts = []string{hint("k", "remove"), hint("d", "+delete files"), hint("esc", "back")}
	case m.section == sectionDownloads:
		parts = []string{hint("↑↓", "move"), hint("o", "open"), hint("p", "pause/resume"), hint("x", "cancel"), hint("tab", "panes"), hint("?", "help"), hint("q", "quit")}
	case m.section == sectionSearch:
		parts = []string{
			hint("/", "search"), hint("↑↓", "move"), hint("←→", "filter"),
			hint("enter", "details"), hint("d", "download"), hint("S", "sort"),
			hint("tab", "panes"), hint("?", "help"), hint("q", "quit"),
		}
	case m.section == sectionSettings:
		parts = []string{
			hint("↑↓", "move"), hint("←→", "change"), hint("enter", "edit"),
			hint("tab", "panes"), hint("?", "help"), hint("q", "quit"),
		}
	case m.section == sectionSeeding:
		parts = []string{hint("↑↓", "move"), hint("o", "open"), hint("p", "pause/resume"), hint("x", "stop/delete"), hint("tab", "panes"), hint("?", "help"), hint("q", "quit")}
	default:
		parts = []string{hint("tab", "panes"), hint("?", "help"), hint("q", "quit")}
	}
	return st.Footer.Render(strings.Join(parts, sep))
}

func (m Model) helpView() string {
	rows := [][2]string{
		{"/", "focus the search box and start typing"},
		{"enter", "run the search · open a result's details"},
		{"esc", "leave the search box / close details / cancel"},
		{"↑ ↓ / k j", "move the selection"},
		{"← → / h l", "switch the media filter · change a setting"},
		{"d", "download the selected result"},
		{"p", "pause / resume the selected download or seed"},
		{"x", "cancel a download · stop seeding · delete a HISTORY entry"},
		{"o", "open the download's folder"},
		{"S", "sort results (←→ column · ↑↓ direction)"},
		{"y", "copy magnet (in details)"},
		{"tab", "cycle Search · Downloads · Seeding · Settings"},
		{"?", "toggle this help"},
		{"q / ctrl+c", "quit"},
	}

	var b strings.Builder
	b.WriteString("\n  " + st.Logo.Render("shoal — keys") + "\n\n")
	keyCol := st.Key.Width(16)
	for _, r := range rows {
		b.WriteString("  " + keyCol.Render(r[0]) + st.KeyDesc.Render(r[1]) + "\n")
	}
	b.WriteString("\n  " + st.Meta.Render("press any of ? · esc · q to close"))

	// Pad to full height so the alt-screen doesn't show stale rows.
	content := b.String()
	lines := strings.Count(content, "\n")
	if pad := m.height - lines - 1; pad > 0 {
		content += strings.Repeat("\n", pad)
	}
	return content
}

// --- small formatting helpers used only by the view ---

func sizeOrDash(n int64) string {
	if n <= 0 {
		return "—"
	}
	return formatBytes(n)
}

func padRight(s string, w int) string {
	if len(s) >= w {
		return s
	}
	return s + strings.Repeat(" ", w-len(s))
}

func leftPad(s string, w int) string {
	if len(s) >= w {
		return s
	}
	return strings.Repeat(" ", w-len(s)) + s
}

// shortSize is a compact size for the table column (e.g. "1.7G", "751M").
func shortSize(n int64) string {
	if n <= 0 {
		return "—"
	}
	const u = 1024.0
	f := float64(n)
	switch {
	case f >= u*u*u*u:
		return fmt.Sprintf("%.1fT", f/(u*u*u*u))
	case f >= u*u*u:
		return fmt.Sprintf("%.1fG", f/(u*u*u))
	case f >= u*u:
		return fmt.Sprintf("%.0fM", f/(u*u))
	case f >= u:
		return fmt.Sprintf("%.0fK", f/u)
	default:
		return fmt.Sprintf("%dB", n)
	}
}

func thousands(n int64) string {
	if n < 0 {
		n = 0
	}
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	return string(out)
}
