package source

import "testing"

func TestEnabledSources(t *testing.T) {
	all := DefaultSources()
	if len(EnabledSources(nil)) != len(all) {
		t.Fatalf("nil disabled should return all %d, got %d", len(all), len(EnabledSources(nil)))
	}
	got := EnabledSources([]string{"eztv", "does-not-exist"}) // case-insensitive; unknown ignored
	if len(got) != len(all)-1 {
		t.Fatalf("expected one fewer than %d, got %d", len(all), len(got))
	}
	for _, s := range got {
		if s.Name() == "EZTV" {
			t.Fatal("EZTV should have been filtered out")
		}
	}
	if got[0].Name() != all[0].Name() {
		t.Fatalf("order not preserved: %q vs %q", got[0].Name(), all[0].Name())
	}
}
