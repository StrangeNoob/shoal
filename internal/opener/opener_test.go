package opener

import "testing"

func TestCommand(t *testing.T) {
	cases := []struct {
		goos, wantName string
	}{
		{"darwin", "open"},
		{"windows", "explorer"},
		{"linux", "xdg-open"},
	}
	for _, c := range cases {
		name, args := Command(c.goos, "/some/dir")
		if name != c.wantName || len(args) != 1 || args[0] != "/some/dir" {
			t.Errorf("Command(%q) = %q %v, want %q [/some/dir]", c.goos, name, args, c.wantName)
		}
	}
}
