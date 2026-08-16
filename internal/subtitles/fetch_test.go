package subtitles

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func readJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

// writeVideoFile writes a video fixture of the given size to dir/name and
// returns its path.
func writeVideoFile(t *testing.T, dir, name string, size int) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFetchPrefersHashMatchAndWritesSrt(t *testing.T) {
	dir := t.TempDir()
	video := writeVideoFile(t, dir, "My.Movie_2020.mkv", 200*1024) // >128 KiB: hashable

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/subtitles":
			q := r.URL.Query()
			if q.Get("moviehash") == "" {
				t.Errorf("moviehash = %q, want non-empty for a hashable file", q.Get("moviehash"))
			}
			if q.Get("query") != "My Movie 2020" {
				t.Errorf("query = %q, want %q", q.Get("query"), "My Movie 2020")
			}
			// Non-match listed first to prove selection isn't just "first result".
			w.Write([]byte(`{"data":[
				{"attributes":{"language":"en","moviehash_match":false,"files":[{"file_id":222,"file_name":"nope.srt"}]}},
				{"attributes":{"language":"en","moviehash_match":true,"files":[{"file_id":111,"file_name":"yes.srt"}]}}
			]}`))
		case "/download":
			var body struct {
				FileID int64 `json:"file_id"`
			}
			if err := readJSON(r, &body); err != nil {
				t.Fatal(err)
			}
			if body.FileID != 111 {
				t.Errorf("downloaded file_id = %d, want 111 (the hash match)", body.FileID)
			}
			w.Write([]byte(`{"link":"` + srv.URL + `/files/yes.srt"}`))
		case "/files/yes.srt":
			w.Write([]byte("hash-match subtitle"))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "test-key", testUA)
	got, err := Fetch(c, video, "en")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	want := filepath.Join(dir, "My.Movie_2020.en.srt")
	if got != want {
		t.Errorf("Fetch path = %q, want %q", got, want)
	}
	data, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("read written srt: %v", err)
	}
	if string(data) != "hash-match subtitle" {
		t.Errorf("srt content = %q, want %q", data, "hash-match subtitle")
	}
	fi, err := os.Stat(got)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o644 {
		t.Errorf("srt perms = %o, want 0644", fi.Mode().Perm())
	}
}

func TestFetchFallsBackToQueryOnlyForSmallFile(t *testing.T) {
	dir := t.TempDir()
	video := writeVideoFile(t, dir, "tiny_clip.mp4", 1024) // <128 KiB: Hash fails

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/subtitles":
			q := r.URL.Query()
			if _, ok := q["moviehash"]; ok {
				t.Errorf("moviehash param present = %v, want omitted when hashing fails", q["moviehash"])
			}
			if q.Get("query") != "tiny clip" {
				t.Errorf("query = %q, want %q", q.Get("query"), "tiny clip")
			}
			w.Write([]byte(`{"data":[{"attributes":{"language":"en","moviehash_match":false,"files":[{"file_id":9,"file_name":"only.srt"}]}}]}`))
		case "/download":
			w.Write([]byte(`{"link":"` + srv.URL + `/files/only.srt"}`))
		case "/files/only.srt":
			w.Write([]byte("fallback subtitle"))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "test-key", testUA)
	got, err := Fetch(c, video, "en")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	want := filepath.Join(dir, "tiny_clip.en.srt")
	if got != want {
		t.Errorf("Fetch path = %q, want %q", got, want)
	}
	data, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("read written srt: %v", err)
	}
	if string(data) != "fallback subtitle" {
		t.Errorf("srt content = %q, want %q", data, "fallback subtitle")
	}
}

func TestFetchAllSeparatorFilenameOmitsQueryParam(t *testing.T) {
	dir := t.TempDir()
	video := writeVideoFile(t, dir, "____.mkv", 200*1024) // basename cleans to whitespace-only

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/subtitles":
			q := r.URL.Query()
			if _, ok := q["query"]; ok {
				t.Errorf("query param present = %v, want omitted for an all-separator filename", q["query"])
			}
			w.Write([]byte(`{"data":[{"attributes":{"language":"en","moviehash_match":true,"files":[{"file_id":1,"file_name":"x.srt"}]}}]}`))
		case "/download":
			w.Write([]byte(`{"link":"` + srv.URL + `/files/x.srt"}`))
		case "/files/x.srt":
			w.Write([]byte("ok"))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "test-key", testUA)
	if _, err := Fetch(c, video, "en"); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
}

func TestFetchNoResultsReturnsErrNotFound(t *testing.T) {
	dir := t.TempDir()
	video := writeVideoFile(t, dir, "no.matches.mkv", 200*1024)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[]}`))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "test-key", testUA)
	_, err := Fetch(c, video, "en")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "no.matches.en.srt")); !os.IsNotExist(statErr) {
		t.Errorf("srt file should not have been written on ErrNotFound")
	}
}

func TestFetchRateLimitedPropagates(t *testing.T) {
	dir := t.TempDir()
	video := writeVideoFile(t, dir, "limited.mkv", 200*1024)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "test-key", testUA)
	_, err := Fetch(c, video, "en")
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
}

func TestSrtPath(t *testing.T) {
	cases := []struct{ video, lang, want string }{
		{"/data/My.Movie.mkv", "en", "/data/My.Movie.en.srt"},
		{"/data/My.Movie.mkv", "", "/data/My.Movie.en.srt"}, // empty lang must not yield "My.Movie..srt"
		{"/data/show/ep1.mp4", "fr", "/data/show/ep1.fr.srt"},
		{"/data/noext", "en", "/data/noext.en.srt"},
	}
	for _, c := range cases {
		if got := SrtPath(c.video, c.lang); got != c.want {
			t.Errorf("SrtPath(%q, %q) = %q, want %q", c.video, c.lang, got, c.want)
		}
	}
}

// Fetch writes the .srt atomically: the finished file holds the full content
// and no temp file is left behind, so the auto-fetch existence guard never
// mistakes a half-written file for a fetched subtitle. Also pins the empty-lang
// default ("en", not a "movie..srt" path).
func TestFetchWritesAtomicallyAndDefaultsLang(t *testing.T) {
	dir := t.TempDir()
	video := writeVideoFile(t, dir, "atomic.mkv", 200*1024)

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/subtitles":
			if got := r.URL.Query().Get("languages"); got != "en" {
				t.Errorf("languages = %q, want en (empty lang defaults)", got)
			}
			w.Write([]byte(`{"data":[{"attributes":{"language":"en","moviehash_match":true,"files":[{"file_id":1,"file_name":"a.srt"}]}}]}`))
		case "/download":
			w.Write([]byte(`{"link":"` + srv.URL + `/files/a.srt"}`))
		case "/files/a.srt":
			w.Write([]byte("atomic subtitle"))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	got, err := Fetch(NewClient(srv.URL, "test-key", testUA), video, "")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if want := filepath.Join(dir, "atomic.en.srt"); got != want {
		t.Fatalf("Fetch path = %q, want %q", got, want)
	}
	data, err := os.ReadFile(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "atomic subtitle" {
		t.Errorf("srt content = %q, want %q", data, "atomic subtitle")
	}
	fi, err := os.Stat(got)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o644 {
		t.Errorf("srt perms = %o, want 0644", fi.Mode().Perm())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "atomic.mkv" && e.Name() != "atomic.en.srt" {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

func TestFetchBadKeyPropagates(t *testing.T) {
	dir := t.TempDir()
	video := writeVideoFile(t, dir, "badkey.mkv", 200*1024)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/subtitles":
			w.Write([]byte(`{"data":[{"attributes":{"language":"en","moviehash_match":true,"files":[{"file_id":1,"file_name":"x.srt"}]}}]}`))
		case "/download":
			w.WriteHeader(http.StatusUnauthorized)
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "test-key", testUA)
	_, err := Fetch(c, video, "en")
	if !errors.Is(err, ErrBadKey) {
		t.Fatalf("err = %v, want ErrBadKey", err)
	}
}
