package glob

import "testing"

func TestMatch(t *testing.T) {
	cases := []struct {
		patterns []string
		path     string
		want     bool
	}{
		{[]string{"*.mkv"}, "movie.mkv", true},
		{[]string{"*.mkv"}, "Season 1/ep01.mkv", true}, // basename match crosses dirs
		{[]string{"Season 1/*"}, "Season 1/ep01.mkv", true},
		{[]string{"*.mkv"}, "notes.txt", false},
		{[]string{"*.MKV"}, "movie.mkv", true},          // case-insensitive
		{[]string{"*.srt", "*.mkv"}, "movie.mkv", true}, // OR of patterns
		{[]string{}, "movie.mkv", false},                // no patterns
		{[]string{""}, "movie.mkv", false},              // empty pattern ignored
	}
	for _, c := range cases {
		if got := Match(c.patterns, c.path); got != c.want {
			t.Errorf("Match(%v, %q) = %v, want %v", c.patterns, c.path, got, c.want)
		}
	}
}
