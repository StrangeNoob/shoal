package ui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// shoal's visual system. Two palettes ("Twilight" is the default; "Tide" is the
// quieter, near-monochrome alternative) selectable at runtime from Settings.
// Every colour is declared as a CompleteColor so lipgloss/termenv can degrade
// gracefully: truecolor when available, a 256-colour fallback otherwise, a
// 16-colour fallback below that, and — when NO_COLOR is set or the profile is
// Ascii — no colour at all. Because of that last case we NEVER lean on colour
// alone for state: every coloured signal is paired with a glyph, label, weight
// or reverse-video treatment (see the glyph set below).

// --- iconography: one small, consistent, cell-safe unicode set ---------------
const (
	glyphMark   = "\u25c6" // ◆ wordmark
	glyphNavOn  = "\u25cf" // ● active nav / active enum option
	glyphNavOff = "\u25cb" // ○ inactive nav / inactive enum option
	glyphCursor = "\u276f" // ❯ selection cursor
	glyphDown   = "\u2193" // ↓ downloading
	glyphSeed   = "\u2191" // ↑ seeding / uploaded
	glyphDone   = "\u2713" // ✓ complete / success
	glyphErr    = "\u2717" // ✗ error
	glyphFill   = "\u2588" // █ progress fill
	glyphTrack  = "\u2591" // ░ progress track
	glyphMore   = "\u22ef" // ⋯ overflow / more
)

// Palette is a named colour set with truecolor hex + 256/16-colour fallbacks.
type Palette struct {
	Name                                               string
	Fg, Dim, Faint, Accent, Good, Warn, Error, Surface lipgloss.CompleteColor
}

func col(hex, c256, c16 string) lipgloss.CompleteColor {
	return lipgloss.CompleteColor{TrueColor: hex, ANSI256: c256, ANSI: c16}
}

// Twilight — airy indigo-charcoal dusk, one confident periwinkle accent.
var twilight = Palette{
	Name:    "Twilight",
	Fg:      col("#d7dbe8", "253", "7"),
	Dim:     col("#8b91a7", "245", "7"),
	Faint:   col("#565d75", "240", "8"),
	Accent:  col("#9db4ff", "111", "12"),
	Good:    col("#8fd6b4", "115", "10"),
	Warn:    col("#e6c489", "179", "11"),
	Error:   col("#f0909e", "210", "9"),
	Surface: col("#262a37", "236", "0"),
}

// Tide — near-monochrome slate, a single seafoam-teal accent reserved for state.
var tide = Palette{
	Name:    "Tide",
	Fg:      col("#d6dee0", "253", "7"),
	Dim:     col("#899699", "245", "7"),
	Faint:   col("#515f62", "240", "8"),
	Accent:  col("#6fd0c0", "79", "14"),
	Good:    col("#9fd6a8", "150", "10"),
	Warn:    col("#e3c79b", "180", "11"),
	Error:   col("#e89aa0", "174", "9"),
	Surface: col("#1f2728", "235", "0"),
}

var palettes = []Palette{twilight, tide}

func paletteByName(name string) Palette {
	for _, p := range palettes {
		if p.Name == name {
			return p
		}
	}
	return twilight
}

// Styles is the full set of lipgloss styles the view draws with, derived from a
// Palette. Hierarchy without font sizes:
//
//	L1  accent + bold      wordmark, overlay titles
//	L2  fg + bold          active nav, selected rows
//	L3  fg                 body text, result/torrent names
//	L4  dim                meta lines
//	L5  faint              rules, counts, key hints, section headers
//
// Emphasis comes from weight (bold), the dim/faint colour ramp, italic (only the
// tagline), and reverse-video (Surface background) for selection — all of which
// survive NO_COLOR.
type Styles struct {
	Logo, Tag                               lipgloss.Style
	SideActive, SideInactive, Count         lipgloss.Style
	Rule                                    lipgloss.Style
	SearchLabel                             lipgloss.Style
	Row, RowSel, Meta                       lipgloss.Style
	FilterOn, FilterOff                     lipgloss.Style
	SectionHead                             lipgloss.Style
	SetLabel, SetLabelSel, SetVal, SetValOn lipgloss.Style
	Good, Bad, Notice                       lipgloss.Style
	Accent, Faint, Dim                      lipgloss.Style
	Footer, Key, KeyDesc, FooterSep         lipgloss.Style
}

func newStyles(p Palette) Styles {
	s := lipgloss.NewStyle
	return Styles{
		Logo:         s().Foreground(p.Accent).Bold(true),
		Tag:          s().Foreground(p.Dim).Italic(true),
		SideActive:   s().Foreground(p.Accent).Bold(true),
		SideInactive: s().Foreground(p.Dim),
		Count:        s().Foreground(p.Dim),
		Rule:         s().Foreground(p.Faint),
		SearchLabel:  s().Foreground(p.Accent).Bold(true),
		Row:          s().Foreground(p.Fg),
		RowSel:       s().Foreground(p.Fg).Bold(true).Background(p.Surface),
		Meta:         s().Foreground(p.Dim),
		FilterOn:     s().Foreground(p.Accent).Bold(true).Background(p.Surface),
		FilterOff:    s().Foreground(p.Dim),
		SectionHead:  s().Foreground(p.Faint),
		SetLabel:     s().Foreground(p.Fg),
		SetLabelSel:  s().Foreground(p.Fg).Bold(true).Background(p.Surface),
		SetVal:       s().Foreground(p.Fg),
		SetValOn:     s().Foreground(p.Accent),
		Good:         s().Foreground(p.Good),
		Bad:          s().Foreground(p.Error), // errors get their OWN treatment now
		Notice:       s().Foreground(p.Good),
		Accent:       s().Foreground(p.Accent),
		Faint:        s().Foreground(p.Faint),
		Dim:          s().Foreground(p.Dim),
		Footer:       s().Foreground(p.Dim),
		Key:          s().Foreground(p.Accent).Bold(true),
		KeyDesc:      s().Foreground(p.Faint),
		FooterSep:    s().Foreground(p.Faint),
	}
}

// active palette + its styles, rebuilt whenever the theme is switched.
var (
	activePalette = twilight
	st            = newStyles(twilight)
)

func setPalette(p Palette) {
	activePalette = p
	st = newStyles(p)
}

// applyColorMode overrides lipgloss's colour profile. "auto" detects from the
// environment (and so honours NO_COLOR); the others force a profile.
//
// TODO(handoff): termenv's detection entry point has changed across versions.
// On older termenv this is termenv.ColorProfile(); on newer it is
// termenv.NewOutput(os.Stdout).EnvColorProfile(). Pick whichever your pinned
// version exposes — both return a termenv.Profile.
func applyColorMode(mode string) {
	switch mode {
	case "truecolor":
		lipgloss.SetColorProfile(termenv.TrueColor)
	case "256":
		lipgloss.SetColorProfile(termenv.ANSI256)
	case "off":
		lipgloss.SetColorProfile(termenv.Ascii)
	default: // "auto"
		lipgloss.SetColorProfile(termenv.ColorProfile())
	}
}
