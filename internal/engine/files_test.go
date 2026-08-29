package engine

import (
	"path/filepath"
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

func TestAbsFilePath(t *testing.T) {
	single := []FileDetail{{Path: "movie.mkv", Length: 200 << 20}}
	if got, want := AbsFilePath("/data/movie.mkv", single, single[0]), "/data/movie.mkv"; got != want {
		t.Errorf("single-file join = %q, want %q", got, want)
	}

	multi := []FileDetail{
		{Path: "Season 1/ep01.mkv", Length: 200 << 20},
		{Path: "Season 1/ep01.nfo", Length: 100},
	}
	got := AbsFilePath("/data/My Show", multi, multi[0])
	want := filepath.Join("/data/My Show", "Season 1", "ep01.mkv")
	if got != want {
		t.Errorf("multi-file join = %q, want %q", got, want)
	}

	// Third shape: a directory-mode torrent that happens to contain exactly
	// one file. len(files)==1 alone must not be treated as single-file
	// format — f.Path ("movie.mkv") differs from the torrent's own name
	// (base(statusPath) == "ReleaseFolder"), so this must still Join.
	folderOneFile := []FileDetail{{Path: "movie.mkv", Length: 200 << 20}}
	folderPath := filepath.Join("/data", "ReleaseFolder")
	gotFolder := AbsFilePath(folderPath, folderOneFile, folderOneFile[0])
	wantFolder := filepath.Join(folderPath, "movie.mkv")
	if gotFolder != wantFolder {
		t.Errorf("folder-with-one-file join = %q, want %q", gotFolder, wantFolder)
	}
}
