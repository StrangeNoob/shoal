package ui

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"shoal/internal/source"
)

// truncate shortens s to at most n runes, adding an ellipsis when it cuts.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:max(0, n)])
	}
	return string(r[:n-1]) + "…"
}

// formatBytes renders a byte count as a compact human string (e.g. "1.4 GiB").
func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// asMagnet returns s if it looks like a magnet link, else "".
func asMagnet(s string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(s)), "magnet:?") {
		return strings.TrimSpace(s)
	}
	return ""
}

// padOrTrim forces s to exactly w display columns (simple rune-based).
func padOrTrim(s string, w int) string {
	if w <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) > w {
		return truncate(s, w)
	}
	return s + strings.Repeat(" ", w-len(r))
}

type sortField int

const (
	sortNone sortField = iota
	sortSize
	sortSeeders
	sortLeechers
	sortRatio
)

var sortableCols = []sortField{sortSize, sortSeeders, sortLeechers, sortRatio}

func (f sortField) label() string {
	switch f {
	case sortSize:
		return "Size"
	case sortSeeders:
		return "Seeders"
	case sortLeechers:
		return "Leechers"
	case sortRatio:
		return "Ratio"
	default:
		return "Relevance"
	}
}

// leechSeedRatio is Leechers ÷ Seeders; 0 seeders with any leechers is +Inf
// (worst swarm), an empty swarm is 0.
func leechSeedRatio(r source.Result) float64 {
	if r.Seeders == 0 {
		if r.Leechers == 0 {
			return 0
		}
		return math.Inf(1)
	}
	return float64(r.Leechers) / float64(r.Seeders)
}

// applySort orders rs in place (stable). desc = largest/most first.
func applySort(rs []source.Result, f sortField, desc bool) {
	less := func(i, j int) bool {
		var a, b float64
		switch f {
		case sortSize:
			a, b = float64(rs[i].SizeBytes), float64(rs[j].SizeBytes)
		case sortSeeders:
			a, b = float64(rs[i].Seeders), float64(rs[j].Seeders)
		case sortLeechers:
			a, b = float64(rs[i].Leechers), float64(rs[j].Leechers)
		case sortRatio:
			a, b = leechSeedRatio(rs[i]), leechSeedRatio(rs[j])
		default: // sortNone → health order by Popularity
			a, b = float64(rs[i].Popularity), float64(rs[j].Popularity)
		}
		if desc {
			return a > b
		}
		return a < b
	}
	sort.SliceStable(rs, less)
}

func relTime(unix int64) string {
	if unix <= 0 {
		return ""
	}
	d := time.Since(time.Unix(unix, 0))
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dmo ago", int(d.Hours()/24/30))
	default:
		return fmt.Sprintf("%dy ago", int(d.Hours()/24/365))
	}
}

func seedLeech(r source.Result) string {
	if r.Seeders == 0 && r.Leechers == 0 {
		return "—"
	}
	return fmt.Sprintf("%d:%d", r.Seeders, r.Leechers)
}

func ratioStr(r source.Result) string {
	if r.Seeders == 0 && r.Leechers == 0 {
		return "—"
	}
	v := leechSeedRatio(r)
	if math.IsInf(v, 1) {
		return "∞"
	}
	return fmt.Sprintf("%.2f", v)
}

// padVisual right-pads s with spaces to visible width w (ANSI-aware). If s is
// already >= w it is returned unchanged.
// ponytail: callers keep body lines within the inner width; this only pads.
func padVisual(s string, w int) string {
	gap := w - lipgloss.Width(s)
	if gap <= 0 {
		return s
	}
	return s + strings.Repeat(" ", gap)
}

// titledBox draws a rounded border of total width `width`, with `title` inset in
// the top edge and an optional right-aligned `right` label. Body lines are
// padded to the inner width.
func titledBox(title, right, body string, width int, focused bool) string {
	if width < 8 {
		width = 8
	}
	border := st.Faint
	if focused {
		border = st.Accent
	}
	inner := width - 2

	var rightSeg string
	if right != "" {
		rightSeg = " " + right + " ─"
	}
	// Truncate the title so the top border never exceeds `width`. The 3 accounts
	// for the "─ " prefix and " " suffix around the title text.
	titleRoom := inner - lipgloss.Width(rightSeg) - 3
	if titleRoom < 0 {
		// No room for the right label alongside the title framing — drop it.
		rightSeg = ""
		titleRoom = inner - 3
	}
	if titleRoom < 0 {
		titleRoom = 0
	}
	titleSeg := "─ " + truncate(title, titleRoom) + " "
	fill := inner - lipgloss.Width(titleSeg) - lipgloss.Width(rightSeg)
	if fill < 0 {
		fill = 0
	}
	top := "╭" + titleSeg + strings.Repeat("─", fill) + rightSeg + "╮"

	var b strings.Builder
	b.WriteString(border.Render(top) + "\n")
	for _, ln := range strings.Split(body, "\n") {
		ln = lipgloss.NewStyle().MaxWidth(inner).Render(ln) // cap overlong lines so the border never bows
		b.WriteString(border.Render("│") + padVisual(ln, inner) + border.Render("│") + "\n")
	}
	b.WriteString(border.Render("╰" + strings.Repeat("─", inner) + "╯"))
	return b.String()
}
