package subtitles

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// NewDefaultClient is the single place the production base URL, request
// timeout and versioned User-Agent live. OpenSubtitles requires a
// "AppName vX.Y.Z"-shaped User-Agent, so AppVersion is normalized into that
// form regardless of whether the caller already included a "v" prefix.
func TestNewDefaultClientHasTimeoutAndVersionedUA(t *testing.T) {
	orig := AppVersion
	t.Cleanup(func() { AppVersion = orig })

	cases := []struct{ version, wantUA string }{
		{"1.2.3", "shoal v1.2.3"},
		{"v1.2.3", "shoal v1.2.3"},
		{"dev", "shoal v0.0.0-dev"},
		{"", "shoal v0.0.0-dev"},
	}
	for _, tc := range cases {
		AppVersion = tc.version
		c := NewDefaultClient("key")
		if c.userAgent != tc.wantUA {
			t.Errorf("AppVersion = %q: userAgent = %q, want %q", tc.version, c.userAgent, tc.wantUA)
		}
	}

	AppVersion = "v1.2.3"
	c := NewDefaultClient("key")
	if c.HTTP == nil || c.HTTP.Timeout <= 0 {
		t.Fatalf("HTTP = %+v, want a non-nil client with a timeout", c.HTTP)
	}
	if !strings.HasPrefix(c.baseURL, "https://api.opensubtitles.com") {
		t.Errorf("baseURL = %q, want the public API", c.baseURL)
	}
	if c.apiKey != "key" {
		t.Errorf("apiKey = %q, want key", c.apiKey)
	}
}

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
		_, _ = io.WriteString(w, searchFixture)
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "test-key", testUA)
	got, err := c.Search(context.Background(), "abc123", "my movie", "en")
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
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "test-key", testUA)
	if _, err := c.Search(context.Background(), "abc123", "", "en"); err != nil {
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
			// The live gateway 503s requests without an Accept header.
			if got := r.Header.Get("Accept"); got != "application/json" {
				t.Errorf("Accept = %q, want application/json", got)
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
			_, _ = w.Write([]byte(`{"link":"` + srv.URL + `/files/X.srt","file_name":"X.srt"}`))
		case "/files/X.srt":
			_, _ = io.WriteString(w, body)
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "test-key", testUA)
	got, err := c.Download(context.Background(), 123)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if string(got) != body {
		t.Fatalf("Download body = %q, want %q", got, body)
	}
}

// Download follows the CDN link the API hands back unauthenticated, so it
// must validate that link before making a second network request — otherwise
// a compromised or misbehaving API response could redirect the client
// anywhere. Real-world links must be https; the http:// loopback exception
// exists only so these httptest-backed tests keep working.
func TestDownloadRejectsEmptyLink(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"link":""}`)
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "test-key", testUA)
	if _, err := c.Download(context.Background(), 123); err == nil {
		t.Fatal("err = nil, want error for an empty download link")
	}
}

func TestDownloadRejectsNonHTTPSNonLoopbackLink(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"link":"http://evil.example/x.srt"}`)
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "test-key", testUA)
	_, err := c.Download(context.Background(), 123)
	if err == nil {
		t.Fatal("err = nil, want error for a non-https, non-loopback download link")
	}
	if !strings.Contains(err.Error(), "evil.example") {
		t.Errorf("err = %v, want it to mention the rejected link", err)
	}
}

// A real (https) download link must never reach a private, loopback, or
// otherwise non-public address — the check runs on the actual dial target
// (post-DNS), so a rebinding hostname can't dodge it either.
func TestRejectNonPublicAddr(t *testing.T) {
	bad := []string{
		"127.0.0.1:443", "[::1]:443", "10.0.0.8:443", "192.168.1.5:443",
		"172.16.3.2:443", "169.254.1.1:443", "[fe80::1]:443", "0.0.0.0:443",
		"224.0.0.1:443",
	}
	for _, a := range bad {
		if rejectNonPublicAddr("tcp", a, nil) == nil {
			t.Errorf("rejectNonPublicAddr(%q) = nil, want error", a)
		}
	}
	good := []string{"93.184.216.34:443", "[2606:2800:220:1:248:1893:25c8:1946]:443"}
	for _, a := range good {
		if err := rejectNonPublicAddr("tcp", a, nil); err != nil {
			t.Errorf("rejectNonPublicAddr(%q) = %v, want nil", a, err)
		}
	}
}

func TestDownloadRejectsPrivateHTTPSTarget(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"link":"https://127.0.0.1:1/x.srt"}`)
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "test-key", testUA)
	_, err := c.Download(context.Background(), 123)
	if err == nil || !strings.Contains(err.Error(), "non-public") {
		t.Fatalf("err = %v, want a non-public-address policy error", err)
	}
}

// The link policy must also hold across redirects: a validated loopback/https
// link that redirects to an unsafe target must be rejected before the
// redirected request is sent.
func TestDownloadRejectsUnsafeRedirect(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/download":
			_, _ = w.Write([]byte(`{"link":"` + srv.URL + `/redir","file_name":"X.srt"}`))
		case "/redir":
			http.Redirect(w, r, "http://evil.example/x.srt", http.StatusFound)
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "test-key", testUA)
	_, err := c.Download(context.Background(), 123)
	if err == nil {
		t.Fatal("err = nil, want error for a redirect to a non-https, non-loopback target")
	}
	if !strings.Contains(err.Error(), "evil.example") {
		t.Errorf("err = %v, want it to mention the rejected redirect target", err)
	}
}

func TestDownloadFollowsSafeRedirect(t *testing.T) {
	const body = "1\n00:00:01,000 --> 00:00:02,000\nHello\n"
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/download":
			_, _ = w.Write([]byte(`{"link":"` + srv.URL + `/redir","file_name":"X.srt"}`))
		case "/redir":
			http.Redirect(w, r, srv.URL+"/files/X.srt", http.StatusFound)
		case "/files/X.srt":
			_, _ = io.WriteString(w, body)
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "test-key", testUA)
	got, err := c.Download(context.Background(), 123)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if string(got) != body {
		t.Fatalf("Download body = %q, want %q", got, body)
	}
}

// An oversized response must be an error, not silently truncated data written
// out as a "valid" .srt (which the auto-fetch existence guard would then trust).
func TestDownloadRejectsOversizedBody(t *testing.T) {
	big := bytes.Repeat([]byte("a"), maxDownloadBytes+2)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/download":
			_, _ = w.Write([]byte(`{"link":"` + srv.URL + `/files/big.srt","file_name":"big.srt"}`))
		case "/files/big.srt":
			_, _ = w.Write(big)
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "test-key", testUA)
	if _, err := c.Download(context.Background(), 123); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("err = %v, want an exceeds-limit error", err)
	}
}

// do()'s non-200 default branch must include a bounded snippet of the
// response body so callers (and their error logs) see why the API rejected
// the request, not just the bare status line.
func TestSearchOtherStatusErrorIncludesBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"message":"invalid moviehash format"}`)
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "test-key", testUA)
	_, err := c.Search(context.Background(), "abc123", "", "en")
	if err == nil {
		t.Fatal("err = nil, want error")
	}
	if !strings.Contains(err.Error(), "invalid moviehash format") {
		t.Errorf("err = %v, want it to include the response body", err)
	}
}

func TestSearchRateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "test-key", testUA)
	if _, err := c.Search(context.Background(), "abc123", "", "en"); err != ErrRateLimited {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
}

func TestDownloadRateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "test-key", testUA)
	if _, err := c.Download(context.Background(), 123); err != ErrRateLimited {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
}

func TestSearchBadKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "test-key", testUA)
	if _, err := c.Search(context.Background(), "abc123", "", "en"); err != ErrBadKey {
		t.Fatalf("err = %v, want ErrBadKey", err)
	}
}

func TestDownloadBadKeyForbidden(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "test-key", testUA)
	if _, err := c.Download(context.Background(), 123); err != ErrBadKey {
		t.Fatalf("err = %v, want ErrBadKey", err)
	}
}

func TestSearchOtherStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "test-key", testUA)
	_, err := c.Search(context.Background(), "abc123", "", "en")
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
	if _, err := c.Search(context.Background(), "abc123", "", "en"); err == nil {
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
	if _, err := c.Search(context.Background(), "abc123", "", "en"); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if ct.calls != 1 {
		t.Fatalf("override transport calls = %d, want 1", ct.calls)
	}
}

// Search must build its request with the caller's context (issue #47): an
// already-canceled context aborts before the request ever reaches the wire,
// so the server sees zero requests.
func TestSearchAbortsOnCanceledContext(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	c := NewClient(srv.URL, "test-key", testUA)
	if _, err := c.Search(ctx, "abc123", "", "en"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if called {
		t.Error("server was called though the context was already canceled")
	}
}

// Download's second hop — following the CDN link the API hands back — must
// also honor the caller's context, not just the initial POST /download. A
// client that dropped ctx on that hop would hang until the CDN responds
// (potentially the full 30s HTTP timeout) instead of aborting immediately,
// which is exactly what issue #47 needs for daemon shutdown.
func TestDownloadCDNLinkRespectsContextCancellation(t *testing.T) {
	unblock := make(chan struct{})
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/download":
			_, _ = w.Write([]byte(`{"link":"` + srv.URL + `/files/slow.srt"}`))
		case "/files/slow.srt":
			<-unblock // hangs until the test releases it or the request is canceled first
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	t.Cleanup(func() {
		close(unblock)
		srv.Close()
	})

	ctx, cancel := context.WithCancel(context.Background())
	c := NewClient(srv.URL, "test-key", testUA)

	errCh := make(chan error, 1)
	go func() {
		_, err := c.Download(ctx, 123)
		errCh <- err
	}()

	time.Sleep(50 * time.Millisecond) // let the CDN request actually start
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Download did not return after context cancellation — the CDN hop isn't using ctx")
	}
}
