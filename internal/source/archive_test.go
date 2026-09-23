package source

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// redirectTransport rewrites every request to point at a test server while
// preserving the original path and query, so we can exercise Archive.Search
// (which hard-codes the archive.org host) without touching production code.
type redirectTransport struct{ scheme, host string }

func (rt redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = rt.scheme
	req.URL.Host = rt.host
	return http.DefaultTransport.RoundTrip(req)
}

func archivePointedAt(srv *httptest.Server) *Archive {
	u, _ := url.Parse(srv.URL)
	return &Archive{Client: &http.Client{
		Transport: redirectTransport{u.Scheme, u.Host},
		Timeout:   5 * time.Second,
	}}
}

func TestArchiveName(t *testing.T) {
	if NewArchive().Name() != "Internet Archive" {
		t.Errorf("Name = %q", NewArchive().Name())
	}
}

func TestArchiveSearchMapsDocs(t *testing.T) {
	const body = `{"response":{"docs":[
		{"identifier":"id1","title":"Title One","item_size":12345,"downloads":678},
		{"identifier":"id2","title":["First","Second"],"downloads":5},
		{"identifier":"id3","title":""},
		{"identifier":"id4","title":42},
		{"identifier":"","title":"skip me"}
	]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if want := `title:("linux")`; r.URL.Query().Get("q") != want {
			t.Errorf("query q = %q, want %s", r.URL.Query().Get("q"), want)
		}
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	res, err := archivePointedAt(srv).Search(context.Background(), "linux")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res) != 4 {
		t.Fatalf("got %d results, want 4 (empty identifier dropped)", len(res))
	}

	if res[0].Title != "Title One" || res[0].SizeBytes != 12345 || res[0].Popularity != 678 {
		t.Errorf("res[0] = %+v", res[0])
	}
	if res[0].Source != "Internet Archive" {
		t.Errorf("res[0].Source = %q", res[0].Source)
	}
	if want := "https://archive.org/download/id1/id1_archive.torrent"; res[0].TorrentURL != want {
		t.Errorf("res[0].TorrentURL = %q, want %q", res[0].TorrentURL, want)
	}
	if res[1].Title != "First" || res[1].Popularity != 5 || res[1].SizeBytes != 0 {
		t.Errorf("res[1] (array title) = %+v", res[1])
	}
	if res[2].Title != "id3" { // empty title falls back to identifier
		t.Errorf("res[2].Title = %q, want id3", res[2].Title)
	}
	if res[3].Title != "42" { // numeric title via flexString default branch
		t.Errorf("res[3].Title = %q, want 42", res[3].Title)
	}
}

func TestArchiveSearchNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)
	if _, err := archivePointedAt(srv).Search(context.Background(), "x"); err == nil {
		t.Fatal("Search expected error on non-200 status")
	}
}

func TestArchiveSearchBadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("{not json"))
	}))
	t.Cleanup(srv.Close)
	if _, err := archivePointedAt(srv).Search(context.Background(), "x"); err == nil {
		t.Fatal("Search expected JSON decode error")
	}
}

// Searches must match titles only: an unscoped archive.org query also matches
// descriptions, which surfaced unrelated items ("system design course" returned
// CIA Reading Room memos). Lucene syntax is stripped, not escaped, and every
// term is quoted: archive.org fails on a bare leading apostrophe.
func TestTitleQuery(t *testing.T) {
	cases := map[string]string{
		"system design course":   `title:("system" "design" "course")`,
		"Spider-Man: Homecoming": `title:("Spider" "Man" "Homecoming")`,
		`"AC/DC" (live)`:         `title:("AC" "DC" "live")`,
		`"rock 'n' roll"`:        `title:("rock" "'n'" "roll")`,
		"'round midnight":        `title:("'round" "midnight")`,
		"tom AND jerry":          `title:("tom" "AND" "jerry")`,
		"a<b <c>":                `title:("a" "b" "c")`,
		"  ?? ** ":               "",
	}
	for in, want := range cases {
		if got := titleQuery(in); got != want {
			t.Errorf("titleQuery(%q) = %q, want %q", in, got, want)
		}
	}
}

// archive.org reports query errors as HTTP 200 with an "error" field and no
// docs. That must surface as an error, not pass for an empty result list.
func TestArchiveSearchErrorEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"error":"[BACKEND_ERROR] Invalid or no response from Elasticsearch"}`))
	}))
	t.Cleanup(srv.Close)
	if res, err := archivePointedAt(srv).Search(context.Background(), "x"); err == nil {
		t.Fatalf("Search = %v, nil; want an error", res)
	}
}

// A query with no searchable terms must not hit the network or error.
func TestArchiveSearchSkipsEmptyTitleQuery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s", r.URL)
	}))
	t.Cleanup(srv.Close)
	res, err := archivePointedAt(srv).Search(context.Background(), "???")
	if err != nil || res != nil {
		t.Fatalf("Search(???) = %v, %v; want nil, nil", res, err)
	}
}
