package engine

import (
	"reflect"
	"testing"
)

func TestResolveDeselected(t *testing.T) {
	paths := []string{"movie.mkv", "extras/sample.mkv", "readme.txt"}
	// --files *.mkv keeps the two .mkv files, deselects readme.txt.
	got := resolveDeselected(paths, []string{"*.mkv"})
	if !reflect.DeepEqual(got, []string{"readme.txt"}) {
		t.Fatalf("resolveDeselected = %v, want [readme.txt]", got)
	}
	// No globs → nothing deselected.
	if got := resolveDeselected(paths, nil); got != nil {
		t.Fatalf("no globs should deselect nothing, got %v", got)
	}
}
