package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/StrangeNoob/shoal/internal/engine"
)

// menuItem is one row of the context menu: a label, the keyboard shortcut
// shown as a right-aligned hint, and the action to run — the exact code the
// hinted key already runs elsewhere (see downloadSelected/openDetail/
// copyMagnet), so a menu pick can never drift from its keyboard equivalent.
type menuItem struct {
	label string
	key   string
	do    func(*Model) (tea.Model, tea.Cmd)
}

// searchRowMenuItems is the context menu opened by right-clicking a Search
// result row.
func searchRowMenuItems() []menuItem {
	return []menuItem{
		{label: "Download", key: "d", do: func(m *Model) (tea.Model, tea.Cmd) {
			return *m, m.downloadSelected()
		}},
		{label: "Details", key: "enter", do: func(m *Model) (tea.Model, tea.Cmd) {
			m.openDetail()
			return *m, nil
		}},
		{label: "Copy magnet", key: "y", do: func(m *Model) (tea.Model, tea.Cmd) {
			fr := m.filteredResults()
			if len(fr) > 0 && m.cursor < len(fr) {
				m.copyMagnet(fr[m.cursor].Magnet)
			}
			return *m, nil
		}},
	}
}

// downloadsRowMenuItems is the context menu opened by right-clicking a
// Downloads row. Every action re-reads the current selection from the model
// at run time (dlCursor into downloading()) rather than closing over s, so it
// can't act on a stale row if the list changes while the menu is open — s is
// used only to label the state-dependent items as they stood at open time.
func downloadsRowMenuItems(s engine.Status) []menuItem {
	pauseLabel := "Pause"
	if s.Paused {
		pauseLabel = "Resume"
	}
	seqLabel := "Sequential on"
	if s.Sequential {
		seqLabel = "Sequential off"
	}
	return []menuItem{
		{label: pauseLabel, key: "p", do: func(m *Model) (tea.Model, tea.Cmd) {
			return *m, m.pauseSelected()
		}},
		{label: "Details", key: "enter", do: func(m *Model) (tea.Model, tea.Cmd) {
			return *m, m.activate()
		}},
		{label: "Open folder", key: "o", do: func(m *Model) (tea.Model, tea.Cmd) {
			return *m, m.openCurrentSelection()
		}},
		{label: seqLabel, key: "s", do: func(m *Model) (tea.Model, tea.Cmd) {
			return *m, m.toggleSequential()
		}},
		{label: "Move up", key: "[", do: func(m *Model) (tea.Model, tea.Cmd) {
			return *m, m.reorderSelected(-1)
		}},
		{label: "Move down", key: "]", do: func(m *Model) (tea.Model, tea.Cmd) {
			return *m, m.reorderSelected(1)
		}},
		{label: "Cancel...", key: "x", do: func(m *Model) (tea.Model, tea.Cmd) {
			m.openRemoveConfirm()
			return *m, nil
		}},
	}
}

// seedingRowMenuItems is the context menu opened by right-clicking an active
// Seeding row (a HISTORY row gets historyRowMenuItems instead).
func seedingRowMenuItems(s engine.Status) []menuItem {
	pauseLabel := "Pause"
	if s.Paused {
		pauseLabel = "Resume"
	}
	return []menuItem{
		{label: pauseLabel, key: "p", do: func(m *Model) (tea.Model, tea.Cmd) {
			return *m, m.pauseSelected()
		}},
		{label: "Open", key: "o", do: func(m *Model) (tea.Model, tea.Cmd) {
			return *m, m.openCurrentSelection()
		}},
		{label: "Stop seeding...", key: "x", do: func(m *Model) (tea.Model, tea.Cmd) {
			m.openRemoveConfirm()
			return *m, nil
		}},
	}
}

// historyRowMenuItems is the context menu opened by right-clicking a
// Seeding-pane HISTORY row.
func historyRowMenuItems() []menuItem {
	return []menuItem{
		{label: "Open", key: "o", do: func(m *Model) (tea.Model, tea.Cmd) {
			return *m, m.openCurrentSelection()
		}},
		{label: "Remove...", key: "x", do: func(m *Model) (tea.Model, tea.Cmd) {
			m.openRemoveConfirm()
			return *m, nil
		}},
	}
}

// menuGap is the minimum number of spaces between an item's label and its
// right-aligned key hint.
const menuGap = 2

// menuContentWidth is the widest "Label" + gap + "key" span across items —
// the box's usable width, before the 1-space pad on each side and the border.
func menuContentWidth(items []menuItem) int {
	w := 0
	for _, it := range items {
		if s := len(it.label) + menuGap + len(it.key); s > w {
			w = s
		}
	}
	return w
}

// menuGeometry is the on-screen rectangle the context-menu overlay occupies —
// row/col of its top-left corner (border included) and its width/height.
// Shared by renderMenu (which draws exactly this box) and menuItemAt (which
// hit-tests clicks against it), so a click target can't drift from what's
// drawn — the same shared-geometry pattern clickSelect uses for the panes.
func (m Model) menuGeometry() (row, col, width, height int) {
	width = menuContentWidth(m.menuItems) + 4 // 1-space pad each side + 2 border cols
	height = len(m.menuItems) + 2             // top + bottom border
	row, col = m.menuRow, m.menuCol
	if col+width > m.width {
		col = max(0, m.width-width)
	}
	if row+height > m.height {
		row = max(0, m.height-height)
	}
	return max(0, row), max(0, col), width, height
}

// renderMenu draws the compact bordered overlay: one "Label      key" line
// per item, the key faint and right-aligned, the cursor row highlighted.
func (m Model) renderMenu() string {
	contentW := menuContentWidth(m.menuItems)
	var b strings.Builder
	for i, it := range m.menuItems {
		labelStyle := st.Row
		if i == m.menuCursor {
			labelStyle = st.RowSel
		}
		gap := contentW - len(it.label) - len(it.key)
		line := " " + labelStyle.Render(it.label) + strings.Repeat(" ", gap) + st.Faint.Render(it.key) + " "
		b.WriteString(line)
		if i < len(m.menuItems)-1 {
			b.WriteString("\n")
		}
	}
	_, _, width, _ := m.menuGeometry()
	return titledBox("", "", b.String(), width, true)
}

// menuItemAt reports which item, if any, sits under (x, y), using the exact
// rectangle renderMenu drew (see menuGeometry).
func (m Model) menuItemAt(x, y int) (int, bool) {
	row, col, width, _ := m.menuGeometry()
	i := y - row - 1 // row 0 is the top border
	if i < 0 || i >= len(m.menuItems) || x < col || x >= col+width {
		return -1, false
	}
	return i, true
}

// openResultMenu opens (or re-anchors) the Search result menu at (x, y): it
// shares clickSelect's row hit-testing, so a click target can't drift from
// what's drawn, and selects the row the same way a left-click would. A miss
// (no row under the click) is inert — the menu, if already open, is left
// exactly as it was.
func (m Model) openResultMenu(x, y int) (tea.Model, tea.Cmd) {
	if m.section != sectionSearch || !m.clickSelect(x, y) {
		return m, nil
	}
	m.menuOpen = true
	m.menuCursor = 0
	m.menuRow, m.menuCol = y, x
	m.menuItems = searchRowMenuItems()
	return m, nil
}

// openDownloadsRowMenu opens (or re-anchors) the Downloads row menu at
// (x, y): it shares clickSelect's row hit-testing, so a click target can't
// drift from what's drawn, and selects the row the same way a left-click
// would. A miss is inert — the menu, if already open, is left exactly as it
// was.
func (m Model) openDownloadsRowMenu(x, y int) (tea.Model, tea.Cmd) {
	if m.section != sectionDownloads || !m.clickSelect(x, y) {
		return m, nil
	}
	m.menuOpen = true
	m.menuCursor = 0
	m.menuRow, m.menuCol = y, x
	m.menuItems = downloadsRowMenuItems(m.downloading()[m.dlCursor])
	return m, nil
}

// openSeedingRowMenu opens (or re-anchors) the Seeding-pane row menu at
// (x, y) — an active seeder gets seedingRowMenuItems, a HISTORY row gets
// historyRowMenuItems. Shares clickSelect the same way openDownloadsRowMenu
// does; a miss is inert.
func (m Model) openSeedingRowMenu(x, y int) (tea.Model, tea.Cmd) {
	if m.section != sectionSeeding || !m.clickSelect(x, y) {
		return m, nil
	}
	ss := m.seeding()
	if m.seedCursor < len(ss) {
		m.menuItems = seedingRowMenuItems(ss[m.seedCursor])
	} else {
		m.menuItems = historyRowMenuItems()
	}
	m.menuOpen = true
	m.menuCursor = 0
	m.menuRow, m.menuCol = y, x
	return m, nil
}

// openRowMenu opens (or re-anchors) the right-click context menu for
// whichever pane is active — Search results, Downloads, or Seeding
// (including its HISTORY rows). Any other pane, or a click that doesn't land
// on a row, is inert.
func (m Model) openRowMenu(x, y int) (tea.Model, tea.Cmd) {
	switch m.section {
	case sectionSearch:
		return m.openResultMenu(x, y)
	case sectionDownloads:
		return m.openDownloadsRowMenu(x, y)
	case sectionSeeding:
		return m.openSeedingRowMenu(x, y)
	}
	return m, nil
}

// handleMenuKey handles input while the context menu is open: ↑/↓ move the
// highlighted item, enter dispatches into it (closing the menu first), esc
// closes without running anything. Every other key is suspended — the menu
// owns the keyboard while open, like the app's other modal overlays.
func (m Model) handleMenuKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up":
		if m.menuCursor > 0 {
			m.menuCursor--
		}
	case "down":
		if m.menuCursor < len(m.menuItems)-1 {
			m.menuCursor++
		}
	case "enter":
		it := m.menuItems[m.menuCursor]
		m.menuOpen = false
		return it.do(&m)
	case "esc":
		m.menuOpen = false
	}
	return m, nil
}

// handleMenuMouse handles a mouse event while the context menu is open: a
// left-click on an item dispatches into it, elsewhere closes the menu; a
// right-click re-anchors the menu at whatever row (if any) it lands on,
// sharing openRowMenu with the path that first opens it. Anything else
// (wheel, motion) is inert.
func (m Model) handleMenuMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.Button == tea.MouseButtonRight && msg.Action == tea.MouseActionPress:
		return m.openRowMenu(msg.X, msg.Y)
	case msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress:
		if i, ok := m.menuItemAt(msg.X, msg.Y); ok {
			it := m.menuItems[i]
			m.menuOpen = false
			return it.do(&m)
		}
		m.menuOpen = false
		return m, nil
	}
	return m, nil
}

// spliceMenu overlays the context menu onto an already-composed frame by
// overwriting the screen rectangle it occupies (menuGeometry) — it never adds
// or removes lines, so TestViewFitsHeight's line-count budget is unaffected.
func (m Model) spliceMenu(view string) string {
	if !m.menuOpen || len(m.menuItems) == 0 {
		return view
	}
	row, col, width, height := m.menuGeometry()
	lines := strings.Split(view, "\n")
	overlay := strings.Split(m.renderMenu(), "\n")
	for i := 0; i < height && i < len(overlay) && row+i < len(lines); i++ {
		lines[row+i] = overwriteRect(lines[row+i], col, width, overlay[i])
	}
	return strings.Join(lines, "\n")
}

// overwriteRect replaces the visible column range [col, col+width) of an
// ANSI-styled line with patch (already exactly `width` cells wide). Uses
// ansi.Truncate/TruncateLeft so the splice can't land inside an escape
// sequence or split a wide character.
func overwriteRect(line string, col, width int, patch string) string {
	left := ansi.Truncate(line, col, "")
	if w := ansi.StringWidth(left); w < col {
		left += strings.Repeat(" ", col-w)
	}
	right := ansi.TruncateLeft(line, col+width, "")
	return left + patch + right
}
