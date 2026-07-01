package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"shoal/internal/source"
)

func (m Model) View() string {
	if !m.ready {
		return "  starting shoal…"
	}
	if m.showHelp {
		return m.helpView()
	}

	header := m.renderHeader()
	rule := st.Rule.Render(strings.Repeat("─", max(1, m.width)))
	bodyH := max(3, m.height-4)
	body := m.renderBody(bodyH)
	footer := m.renderFooter()

	return strings.Join([]string{header, rule, body, rule, footer}, "\n")
}

func (m Model) renderHeader() string {
	left := st.Logo.Render(glyphMark+" shoal") + "  " + st.Tag.Render("torrents, calmly, from your terminal")
	right := ""
	if m.notice != "" {
		glyph, style := glyphDone, st.Notice
		if m.noticeErr {
			glyph, style = glyphErr, st.Bad // errors get a distinct treatment
		}
		right = style.Render(glyph + " " + truncate(m.notice, max(10, m.width/2)))
	}
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
		left = truncate(glyphMark+" shoal", m.width)
	}
	return left + strings.Repeat(" ", gap) + right
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
	key := func(k string) string { return st.Key.Render(k) }

	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(st.Logo.Render("Welcome to shoal") + "\n\n")
	b.WriteString(st.Meta.Render("A calm BitTorrent client for your terminal. Search the Internet") + "\n")
	b.WriteString(st.Meta.Render("Archive and download freely-shared films, music, books and") + "\n")
	b.WriteString(st.Meta.Render("software — with live progress and seeding.") + "\n\n")

	b.WriteString(st.SectionHead.Render("HOW IT WORKS") + "\n")
	b.WriteString("  " + key("/") + st.Meta.Render("    type a description, then ") + key("enter") + st.Meta.Render(" to search") + "\n")
	b.WriteString("  " + key("← →") + st.Meta.Render("  narrow results by media type") + "\n")
	b.WriteString("  " + key("d") + st.Meta.Render("    download a result · ") + key("tab") + st.Meta.Render(" for Downloads") + "\n\n")

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

	const perItem = 1
	visible := max(1, (h-3)/perItem)
	start := 0
	if m.cursor >= visible {
		start = m.cursor - visible + 1
	}
	end := min(len(fr), start+visible)

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
	shown := min(len(ds), visible)
	for i := 0; i < shown; i++ {
		s := ds[i]
		b.WriteString(st.Accent.Render(glyphDown+" ") + st.Row.Render(truncate(s.Name, max(4, w-4))) + "\n")

		p := m.prog
		p.Width = barWidth
		bar := p.ViewAs(s.Percent())

		state := fmt.Sprintf("%5.1f%%", s.Percent()*100)
		detail := fmt.Sprintf("%s / %s  ·  %d peers", formatBytes(s.CompletedBytes), sizeOrDash(s.TotalBytes), s.Peers)
		if sp := m.dlSpeed[s.Name]; sp > 0 {
			detail += fmt.Sprintf("  ·  %s/s", formatBytes(sp))
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
	if len(ss) == 0 {
		return "  " + st.Meta.Render("Nothing seeding yet. Completed downloads keep sharing here.")
	}

	const perItem = 3
	visible := max(1, h/perItem)

	var b strings.Builder
	shown := min(len(ss), visible)
	for i := 0; i < shown; i++ {
		s := ss[i]
		b.WriteString(st.Good.Render(glyphSeed+" ") + st.Row.Render(truncate(s.Name, max(4, w-4))) + "\n")

		detail := fmt.Sprintf("  ·  %d peers", s.Peers)
		if s.Uploaded > 0 {
			detail = fmt.Sprintf("  ·  ratio %.2f  ·  %s %s  ·  %d peers", s.Ratio(), glyphSeed, formatBytes(s.Uploaded), s.Peers)
		}
		if sp := m.ulSpeed[s.Name]; sp > 0 {
			detail += fmt.Sprintf("  ·  %s/s", formatBytes(sp))
		}
		b.WriteString("  " + st.Good.Render(glyphDone+" complete") + st.Meta.Render(truncate(detail, max(4, w-14))) + "\n")
		if i < shown-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// --- Settings --------------------------------------------------------------

func (m Model) renderSettings(w, h int) string {
	items := settingItems()
	var b strings.Builder
	lastGroup := ""
	for i, it := range items {
		if it.group != lastGroup {
			if lastGroup != "" {
				b.WriteString("\n")
			}
			b.WriteString(st.SectionHead.Render(it.group) + "\n")
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
		b.WriteString(cursor + label + "  " + val + "\n")
	}

	// ABOUT is informational, not navigable.
	b.WriteString("\n" + st.SectionHead.Render("ABOUT") + "\n")
	b.WriteString("  " + st.SetLabel.Render(padOrTrim("shoal", 13)) + "  " + st.Meta.Render("v0.2  ·  anacrolix engine"))
	return b.String()
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
