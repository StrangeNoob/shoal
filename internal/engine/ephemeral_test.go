package engine

import "testing"

// A negative ListenPort must bind an OS-assigned ephemeral port, so two engines
// can run at once without a port collision (the CLI spawns one worker per
// download, and the TUI may already hold the fixed port).
func TestNegativeListenPortAvoidsCollision(t *testing.T) {
	e1, err := NewAnacrolix(Config{DataDir: t.TempDir(), ListenPort: -1})
	if err != nil {
		t.Fatalf("first ephemeral engine failed: %v", err)
	}
	defer e1.Close()

	e2, err := NewAnacrolix(Config{DataDir: t.TempDir(), ListenPort: -1})
	if err != nil {
		t.Fatalf("second ephemeral engine failed (port collision?): %v", err)
	}
	defer e2.Close()
}
