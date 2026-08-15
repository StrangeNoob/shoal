package subtitles

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

const testUA = "shoal vtest"

// searchFixture is the wire shape pinned by the design spec: one Result per
// attributes.files[0], entries with no files skipped.
const searchFixture = `{"data":[
	{"attributes":{"language":"en","moviehash_match":true,"files":[{"file_id":123,"file_name":"X.srt"}]}},
	{"attributes":{"language":"fr","moviehash_match":false,"files":[{"file_id":456,"file_name":"Y.srt"}]}},
	{"attributes":{"language":"de","moviehash_match":false,"files":[]}}
]}`

func TestSearchRequestAndParsing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/subtitles" {
			t.Errorf("method/path = %s %s, want GET /subtitles", r.Method, r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("moviehash") != "abc123" || q.Get("languages") != "en" || q.Get("query") != "my movie" {
			t.Errorf("query = %v, want moviehash=abc123 languages=en query=%q", q, "my movie")
		}
		if got := r.Header.Get("Api-Key"); got != "test-key" {
			t.Errorf("Api-Key = %q, want test-key", got)
		}
		if got := r.Header.Get("User-Agent"); got != testUA {
			t.Errorf("User-Agent = %q, want %q", got, testUA)
		}
		io.WriteString(w, searchFixture)
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "test-key", testUA)
	got, err := c.Search("abc123", "my movie", "en")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	want := []Result{
		{FileID: 123, FileName: "X.srt", Language: "en", HashMatch: true},
		{FileID: 456, FileName: "Y.srt", Language: "fr", HashMatch: false},
	}
	if len(got) != len(want) {
		t.Fatalf("results = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("result[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestSearchHashOnlyOmitsQueryParam(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("moviehash") != "abc123" {
			t.Errorf("moviehash = %q, want abc123", q.Get("moviehash"))
		}
		if _, ok := q["query"]; ok {
			t.Errorf("query param present = %v, want omitted when empty", q["query"])
		}
		io.WriteString(w, `{"data":[]}`)
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "test-key", testUA)
	if _, err := c.Search("abc123", "", "en"); err != nil {
		t.Fatalf("Search: %v", err)
	}
}

func TestDownloadFlow(t *testing.T) {
	const body = "1\n00:00:01,000 --> 00:00:02,000\nHello\n"
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/download":
			if r.Method != http.MethodPost {
				t.Errorf("method = %s, want POST", r.Method)
			}
			if got := r.Header.Get("Api-Key"); got != "test-key" {
				t.Errorf("Api-Key = %q, want test-key", got)
			}
			if got := r.Header.Get("User-Agent"); got != testUA {
				t.Errorf("User-Agent = %q, want %q", got, testUA)
			}
			if got := r.Header.Get("Content-Type"); got != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", got)
			}
			var reqBody struct {
				FileID int64 `json:"file_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
				t.Fatalf("decode request body: %v", err)
			}
			if reqBody.FileID != 123 {
				t.Errorf("file_id = %d, want 123", reqBody.FileID)
			}
			w.Write([]byte(`{"link":"` + srv.URL + `/files/X.srt","file_name":"X.srt"}`))
		case "/files/X.srt":
			io.WriteString(w, body)
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "test-key", testUA)
	got, err := c.Download(123)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if string(got) != body {
		t.Fatalf("Download body = %q, want %q", got, body)
	}
}

func TestSearchRateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "test-key", testUA)
	if _, err := c.Search("abc123", "", "en"); err != ErrRateLimited {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
}

func TestDownloadRateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "test-key", testUA)
	if _, err := c.Download(123); err != ErrRateLimited {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
}

func TestSearchBadKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "test-key", testUA)
	if _, err := c.Search("abc123", "", "en"); err != ErrBadKey {
		t.Fatalf("err = %v, want ErrBadKey", err)
	}
}

func TestDownloadBadKeyForbidden(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "test-key", testUA)
	if _, err := c.Download(123); err != ErrBadKey {
		t.Fatalf("err = %v, want ErrBadKey", err)
	}
}

func TestSearchOtherStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "test-key", testUA)
	_, err := c.Search("abc123", "", "en")
	if err == nil {
		t.Fatal("err = nil, want error containing status")
	}
	if err == ErrRateLimited || err == ErrBadKey {
		t.Fatalf("err = %v, want a plain status error, not a typed sentinel", err)
	}
}

func TestSearchMalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `not json`)
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "test-key", testUA)
	if _, err := c.Search("abc123", "", "en"); err == nil {
		t.Fatal("err = nil, want a decode error for malformed JSON")
	}
}

// countingTransport proves Client.HTTP is actually used for requests instead
// of being silently ignored in favor of http.DefaultClient.
type countingTransport struct {
	calls int
	rt    http.RoundTripper
}

func (t *countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.calls++
	return t.rt.RoundTrip(req)
}

func TestClientHTTPOverrideIsUsed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":[]}`)
	}))
	t.Cleanup(srv.Close)

	ct := &countingTransport{rt: http.DefaultTransport}
	c := NewClient(srv.URL, "test-key", testUA)
	c.HTTP = &http.Client{Transport: ct}
	if _, err := c.Search("abc123", "", "en"); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if ct.calls != 1 {
		t.Fatalf("override transport calls = %d, want 1", ct.calls)
	}
}
