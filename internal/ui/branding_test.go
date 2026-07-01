package ui

import (
	"strings"
	"testing"
)

func TestBannerHeaderAndHeight(t *testing.T) {
	m := ready(New(&fakeSource{}, &fakeEngine{})) // 100x30 → full banner
	if m.headerHeight() != 6 {
		t.Fatalf("headerHeight at 100x30 = %d, want 6", m.headerHeight())
	}
	h := m.renderHeader()
	if !strings.Contains(h, "█") {
		t.Fatalf("banner header should contain the block wordmark:\n%s", h)
	}
	if !strings.Contains(h, "torrents, calmly, from your terminal") {
		t.Fatalf("banner header should contain the tagline:\n%s", h)
	}
}

func TestCompactHeaderOnSmallTerminal(t *testing.T) {
	m := New(&fakeSource{}, &fakeEngine{})
	m.width, m.height, m.ready = 40, 12, true
	if m.headerHeight() != 1 {
		t.Fatalf("headerHeight at 40x12 = %d, want 1", m.headerHeight())
	}
	h := m.renderHeader()
	if strings.Contains(h, "\n") {
		t.Fatalf("compact header should be a single line:\n%s", h)
	}
	if !strings.Contains(h, "shoal") {
		t.Fatalf("compact header should contain 'shoal':\n%s", h)
	}
}

func TestRenderLogoWordmark(t *testing.T) {
	for _, got := range []string{renderLogo(60), renderLogoCompact(60)} {
		if !strings.Contains(got, "s  h  o  a  l") {
			t.Fatalf("logo should contain the wordmark:\n%s", got)
		}
	}
}

func TestRenderSplashStaticAndScene(t *testing.T) {
	m := New(&fakeSource{}, &fakeEngine{})
	still := m.renderSplash(80, 24, 0, false)
	if !strings.Contains(still, "starting shoal…") {
		t.Fatalf("static splash should contain the starting message:\n%s", still)
	}
	if !strings.Contains(still, "s  h  o  a  l") {
		t.Fatalf("static splash should contain the wordmark:\n%s", still)
	}
	scene := renderScene(60, 3, 0.5)
	if lines := strings.Count(scene, "\n") + 1; lines != 3 {
		t.Fatalf("renderScene(60,3,_) produced %d lines, want 3", lines)
	}
}
