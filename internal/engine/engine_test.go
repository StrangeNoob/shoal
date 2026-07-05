package engine

import (
	"testing"

	"golang.org/x/time/rate"
)

func TestRateLimiter(t *testing.T) {
	// A configured rate becomes the limiter's Limit; the burst is floored at
	// 256 KiB so large piece requests aren't stalled below the target.
	l := rateLimiter(50 * 1024)
	if l.Limit() != rate.Limit(50*1024) {
		t.Errorf("limit = %v, want %d", l.Limit(), 50*1024)
	}
	if l.Burst() < 1<<18 {
		t.Errorf("burst = %d, want >= %d", l.Burst(), 1<<18)
	}
	// A rate above the floor keeps its own value as the burst.
	if b := rateLimiter(1 << 20).Burst(); b != 1<<20 {
		t.Errorf("burst = %d, want %d", b, 1<<20)
	}
}

func TestStatusPercent(t *testing.T) {
	cases := []struct {
		name      string
		total     int64
		completed int64
		want      float64
	}{
		{"zero total", 0, 0, 0},
		{"negative total", -5, 10, 0},
		{"half", 100, 50, 0.5},
		{"complete", 100, 100, 1},
		{"over-complete clamps", 100, 150, 1},
		{"empty start", 100, 0, 0},
	}
	for _, c := range cases {
		s := Status{TotalBytes: c.total, CompletedBytes: c.completed}
		if got := s.Percent(); got != c.want {
			t.Errorf("%s: Percent() = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestReachedRatio(t *testing.T) {
	cases := []struct {
		name     string
		uploaded int64
		total    int64
		ratio    float64
		want     bool
	}{
		{"ratio disabled (0)", 1000, 100, 0, false},
		{"negative ratio", 1000, 100, -1, false},
		{"unknown total", 500, 0, 1, false},
		{"nothing uploaded", 0, 100, 1, false},
		{"exactly at ratio", 100, 100, 1, true},
		{"over ratio", 250, 100, 2, true},
		{"under ratio", 150, 100, 2, false},
	}
	for _, c := range cases {
		if got := reachedRatio(c.uploaded, c.total, c.ratio); got != c.want {
			t.Errorf("%s: reachedRatio(%d,%d,%v) = %v, want %v", c.name, c.uploaded, c.total, c.ratio, got, c.want)
		}
	}
}

func TestStatusRatio(t *testing.T) {
	cases := []struct {
		name     string
		total    int64
		uploaded int64
		want     float64
	}{
		{"zero total", 0, 100, 0},
		{"negative total", -1, 100, 0},
		{"nothing uploaded", 100, 0, 0},
		{"half", 100, 50, 0.5},
		{"two-to-one", 100, 200, 2},
	}
	for _, c := range cases {
		s := Status{TotalBytes: c.total, Uploaded: c.uploaded}
		if got := s.Ratio(); got != c.want {
			t.Errorf("%s: Ratio() = %v, want %v", c.name, got, c.want)
		}
	}
}
