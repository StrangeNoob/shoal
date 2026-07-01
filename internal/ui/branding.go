package ui

// branding.go — shoal's logo, banner header, and animated startup splash.
//
// Self-contained so you can drop it into your existing UI package. It renders
// with lipgloss only and degrades gracefully (256-colour → NO_COLOR/Ascii).
//
// ── What this file provides ──────────────────────────────────────────────────
//   • renderLogo(w) / renderLogoCompact(w)     the fish-shoal logo (static)
//   • (Model).renderHeader() + headerHeight()  the two-block banner header
//   • (Model).renderSplash(...) + renderScene  the animated "living shoal"
//
// ── Dependencies you must already have (adapt names to your theme) ───────────
//   Styles (lipgloss.Style) on a package value `st`:
//       st.Logo    accent + BOLD   (wordmark, bright core)
//       st.Accent  accent
//       st.Dim     dim grey
//       st.Faint   faint grey      (waves, tagline, rules)
//       st.Tag     dim + italic    (tagline)
//       st.Notice  success colour  (toast ✓)
//       st.Bad     error colour    (toast ✗)
//   Glyph consts: glyphDone = "✓", glyphErr = "✗"
//   Helpers:      max(a,b int) int  (Go 1.21 builtin) and
//                 truncate(s string, n int) string   (uses lipgloss.Width)
//
// ── Model fields + wiring you must add (see INTEGRATION at the bottom) ───────
//   Fields:   booting bool ; frame int
//   Set:      booting = true when you construct the Model
//   Init:     add frameCmd() to your tea.Batch
//   Update:   handle frameMsg{} (advance/settle the splash)
//   handleKey: skip the splash on any key
//   View:     branch to the splash while booting; size body from headerHeight()

import (
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// ============================================================================
// LOGO — a shoal of ><> fish massed into a diamond
// ============================================================================
//
// The fish glyph is the identity. Depth comes from the accent→dim→faint ramp,
// but the diamond formation carries it on its own, so it still reads under
// NO_COLOR/Ascii. Colour codes: 1 faint, 2 dim, 3 accent, 4 accent+bold(core).

const fishR = "><>" // swims right

func fishStyle(code int) lipgloss.Style {
	switch code {
	case 4:
		return st.Logo // accent + bold (bright core)
	case 3:
		return st.Accent
	case 2:
		return st.Dim
	default:
		return st.Faint
	}
}

// fishRow renders n fish (5 spaces apart), coloured per codes, centered in w.
// Rows are symmetric, so centering each independently keeps the columns aligned.
func fishRow(codes []int, w int) string {
	const gap = 5
	visible := len(codes)*len(fishR) + (len(codes)-1)*gap
	var b strings.Builder
	b.WriteString(strings.Repeat(" ", max(0, (w-visible)/2)))
	for i, c := range codes {
		if i > 0 {
			b.WriteString(strings.Repeat(" ", gap))
		}
		b.WriteString(fishStyle(c).Render(fishR))
	}
	return b.String()
}

func centerText(s lipgloss.Style, text string, w int) string {
	return strings.Repeat(" ", max(0, (w-lipgloss.Width(text))/2)) + s.Render(text)
}

// renderLogo is the full 5-row shoal-in-a-diamond + wordmark, centered in w.
func renderLogo(w int) string {
	rows := [][]int{{1}, {2, 2}, {3, 4, 3}, {2, 2}, {1}}
	lines := make([]string, 0, len(rows)+1)
	for _, r := range rows {
		lines = append(lines, fishRow(r, w))
	}
	lines = append(lines, "", centerText(st.Logo, "s  h  o  a  l", w))
	return strings.Join(lines, "\n")
}

// renderLogoCompact is a 3-row shoal + wordmark for tighter spaces (e.g. home).
func renderLogoCompact(w int) string {
	rows := [][]int{{2}, {3, 4, 3}, {2}}
	lines := make([]string, 0, len(rows)+1)
	for _, r := range rows {
		lines = append(lines, fishRow(r, w))
	}
	lines = append(lines, centerText(st.Logo, "s  h  o  a  l", w))
	return strings.Join(lines, "\n")
}

// ============================================================================
// HEADER — two-block banner: fish icon (left) + block SHOAL (right)
// ============================================================================

// blockShoal renders the wordmark SHOAL in 5-row block capitals. 29 cells wide.
func blockShoal() []string {
	glyphs := map[rune][]string{
		'S': {"█████", "█    ", "█████", "    █", "█████"},
		'H': {"█   █", "█   █", "█████", "█   █", "█   █"},
		'O': {"█████", "█   █", "█   █", "█   █", "█████"},
		'A': {"█████", "█   █", "█████", "█   █", "█   █"},
		'L': {"█    ", "█    ", "█    ", "█    ", "█████"},
	}
	rows := make([]string, 5)
	for r := 0; r < 5; r++ {
		parts := make([]string, 0, 5)
		for _, c := range "SHOAL" {
			parts = append(parts, glyphs[c][r])
		}
		rows[r] = strings.Join(parts, " ")
	}
	return rows
}

const headerIconWidth = 19 // fixed column width of the fish icon in the header

// headerIconRow renders one row of the fish-shoal icon, padded to a fixed width
// so the block wordmark to its right lines up across rows.
func headerIconRow(codes []int) string {
	s := fishRow(codes, headerIconWidth)
	if w := lipgloss.Width(s); w < headerIconWidth {
		s += strings.Repeat(" ", headerIconWidth-w)
	}
	return s
}

// headerHeight is the number of rows the header occupies: the 6-row banner,
// or 1 (compact one-line header) when the terminal is too small.
func (m Model) headerHeight() int {
	if m.width < 60 || m.height < 20 {
		return 1
	}
	return 6
}

// noticeText is the right-aligned toast: green ✓ for success, error ✗ for errors.
func (m Model) noticeText() string {
	if m.notice == "" {
		return ""
	}
	glyph, style := glyphDone, st.Notice
	if m.noticeErr {
		glyph, style = glyphErr, st.Bad
	}
	return style.Render(glyph + " " + truncate(m.notice, max(10, m.width/2)))
}

func (m Model) renderHeader() string {
	// Compact one-line header on small terminals (keeps 80×24 usable and any
	// narrower layout intact).
	if m.headerHeight() == 1 {
		left := st.Logo.Render(fishR + " shoal")
		right := m.noticeText()
		gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
		if gap < 1 {
			gap = 1
			left = truncate(fishR+" shoal", m.width)
		}
		return left + strings.Repeat(" ", gap) + right
	}

	// Banner: fish icon (left) + block SHOAL (right), tagline beneath the word.
	iconCodes := [][]int{{1}, {2, 2}, {3, 4, 3}, {2, 2}, {1}}
	block := blockShoal()
	lines := make([]string, 0, 6)
	for r := 0; r < 5; r++ {
		row := "  " + headerIconRow(iconCodes[r]) + "    " + st.Logo.Render(block[r])
		if r == 0 { // pin the notice/toast to the top-right
			if note := m.noticeText(); note != "" {
				if gap := m.width - lipgloss.Width(row) - lipgloss.Width(note); gap >= 2 {
					row += strings.Repeat(" ", gap) + note
				}
			}
		}
		lines = append(lines, row)
	}
	lines = append(lines, strings.Repeat(" ", 2+headerIconWidth+4)+
		st.Tag.Render("torrents, calmly, from your terminal"))
	return strings.Join(lines, "\n")
}

// ============================================================================
// ANIMATED SPLASH — a living shoal swimming through drifting waves
// ============================================================================
//
// Rendered from a rune grid each frame (frame advances ~18fps while booting).
// Fish/wave counts scale to width; motion is deterministic from t so there's no
// per-model animation state to thread around. Everything is placed with column
// arithmetic (monospace) — never lipgloss.Width of a moving string.

type splashFish struct {
	x0, speed, amp, bob, phase float64
	row, code                  int
	dir                        int // +1 right, -1 left
}

// buildSplashFish lays out a deterministic shoal sized to the scene.
func buildSplashFish(w, sceneH int) []splashFish {
	n := clampInt(w/7, 8, 16)
	seed := uint32(7)
	rnd := func() float64 { seed = seed*1103515245 + 12345; return float64((seed>>8)&0xffff) / 65536.0 }
	fish := make([]splashFish, 0, n)
	for i := 0; i < n; i++ {
		depth := rnd() // 0 far … 1 near
		code := 1
		switch {
		case depth > 0.66:
			code = 4
		case depth > 0.33:
			code = 2
		}
		dir := 1
		if rnd() < 0.12 {
			dir = -1
		}
		fish = append(fish, splashFish{
			x0:    rnd() * float64(w),
			speed: 3 + depth*7, // cols/sec (parallax: near = faster)
			amp:   0.4 + rnd()*0.9,
			bob:   0.5 + rnd()*0.7,
			phase: rnd() * 6.28,
			row:   1 + int(rnd()*float64(max(1, sceneH-2))),
			code:  code,
			dir:   dir,
		})
	}
	return fish
}

// renderScene draws the sea (drifting waves + swimming fish) as sceneH styled
// lines of width w. t is seconds since boot.
func renderScene(w, sceneH int, t float64) string {
	if sceneH < 1 {
		sceneH = 1
	}
	span := w + 6
	runes := make([][]rune, sceneH) // grid of glyphs
	codes := make([][]int, sceneH)  // 0 empty, 1..4 fish depth, 5 wave
	for r := 0; r < sceneH; r++ {
		runes[r] = make([]rune, w)
		codes[r] = make([]int, w)
		for c := 0; c < w; c++ {
			runes[r][c] = ' '
		}
	}

	// drifting wave layers
	waves := []struct {
		row   int
		speed float64
	}{{0, 2.4}, {sceneH / 2, 1.4}, {sceneH - 1, 3.2}}
	for _, wv := range waves {
		if wv.row < 0 || wv.row >= sceneH {
			continue
		}
		shift := int(t*wv.speed) % 6
		for c := 0; c < w; c++ {
			if (c+shift)%6 < 5 && runes[wv.row][c] == ' ' {
				runes[wv.row][c] = '≈'
				codes[wv.row][c] = 5
			}
		}
	}

	// fish (drawn after waves, so they swim in front)
	for _, f := range buildSplashFish(w, sceneH) {
		x := int(f.x0+f.speed*t) % span
		if f.dir < 0 {
			x = span - x
		}
		x -= 3
		y := f.row + int(f.amp*math.Sin(t*f.bob+f.phase)+0.5)
		if y < 0 || y >= sceneH {
			continue
		}
		glyph := fishR
		if f.dir < 0 {
			glyph = "<><"
		}
		for k, ch := range glyph {
			if c := x + k; c >= 0 && c < w {
				runes[y][c] = ch
				codes[y][c] = f.code
			}
		}
	}

	// coalesce each row into runs of equal code, styled once per run
	var b strings.Builder
	for r := 0; r < sceneH; r++ {
		for c := 0; c < w; {
			code := codes[r][c]
			j := c
			for j < w && codes[r][j] == code {
				j++
			}
			seg := string(runes[r][c:j])
			switch {
			case code == 0:
				b.WriteString(seg)
			case code == 5:
				b.WriteString(st.Faint.Render(seg))
			default:
				b.WriteString(fishStyle(code).Render(seg))
			}
			c = j
		}
		if r < sceneH-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// renderSplash is the startup screen. animate=false (pre-size flash) shows the
// still logo; while booting it shows the living shoal with the wordmark and
// status pinned below. t is seconds since boot (see splashT()).
func (m Model) renderSplash(w, h int, t float64, animate bool) string {
	if !animate {
		block := renderLogo(w) + "\n\n" +
			centerText(st.Tag, "torrents, calmly, from your terminal", w) + "\n\n" +
			centerText(st.Faint, "starting shoal…", w)
		top := max(0, (h-lipgloss.Height(block))/2)
		return strings.Repeat("\n", top) + block
	}
	sceneH := max(3, h-3)
	scene := renderScene(w, sceneH, t)
	word := centerText(st.Logo, "s  h  o  a  l", w)
	status := centerText(st.Faint, "starting shoal…", w)
	return scene + "\n" + word + "\n" + status
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
