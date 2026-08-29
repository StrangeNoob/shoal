package subtitles

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// ErrRateLimited is returned when the OpenSubtitles API responds 429.
var ErrRateLimited = errors.New("subtitles: rate limited (429)")

// ErrBadKey is returned when the OpenSubtitles API rejects the configured
// API key (401/403).
var ErrBadKey = errors.New("subtitles: API key rejected (401/403)")

// Result is one subtitle file returned by Search, taken from the first entry
// of a search hit's attributes.files.
type Result struct {
	FileID    int64
	FileName  string
	Language  string
	HashMatch bool
}

// Client talks to the OpenSubtitles REST API (https://api.opensubtitles.com).
type Client struct {
	baseURL   string
	apiKey    string
	userAgent string

	// HTTP overrides the client used for requests; nil uses http.DefaultClient.
	HTTP *http.Client
}

// NewClient builds a Client for baseURL, authenticating with apiKey and
// identifying itself as userAgent on every request.
func NewClient(baseURL, apiKey, userAgent string) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, userAgent: userAgent}
}

// AppVersion is the shoal version reported in the User-Agent. Set once at
// startup (cmd/shoal) from the resolved build version; "dev" otherwise.
var AppVersion = "dev"

// NewDefaultClient builds the Client every production caller uses: the public
// OpenSubtitles API, a request timeout (never hang a fetch forever), and
// shoal's versioned User-Agent — which OpenSubtitles asks API consumers to send.
func NewDefaultClient(apiKey string) *Client {
	c := NewClient("https://api.opensubtitles.com/api/v1", apiKey, "shoal "+versionedUA(AppVersion))
	c.HTTP = &http.Client{Timeout: 30 * time.Second}
	return c
}

// versionedUA normalizes AppVersion into the "vX.Y.Z" shape OpenSubtitles
// expects in a User-Agent, tolerating callers that already included the "v"
// prefix. AppVersion is "dev" (its zero value) before a real build version is
// resolved, or possibly empty in tests — both map to a fixed placeholder
// rather than sending "shoal v" or "shoal vdev".
func versionedUA(version string) string {
	if version == "" || version == "dev" {
		return "v0.0.0-dev"
	}
	if strings.HasPrefix(version, "v") {
		return version
	}
	return "v" + version
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *Client) do(method, endpoint string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, endpoint, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Api-Key", c.apiKey)
	req.Header.Set("User-Agent", c.userAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer func() { _ = resp.Body.Close() }()
		switch resp.StatusCode {
		case http.StatusTooManyRequests:
			return nil, ErrRateLimited
		case http.StatusUnauthorized, http.StatusForbidden:
			return nil, ErrBadKey
		default:
			// Bounded: the body is normally a short JSON error, but it's
			// still an unauthenticated-status response, so never buffer it
			// unbounded.
			const maxErrBody = 1 << 10
			b, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrBody))
			return nil, fmt.Errorf("subtitles: unexpected status %s: %s", resp.Status, strings.TrimSpace(string(b)))
		}
	}
	return resp, nil
}

// Search looks up subtitles by moviehash, free-text query, or both, filtered
// to lang. Empty parameters are omitted from the request. Entries with no
// files are skipped.
func (c *Client) Search(hash, query, lang string) ([]Result, error) {
	v := url.Values{}
	if hash != "" {
		v.Set("moviehash", hash)
	}
	if lang != "" {
		v.Set("languages", lang)
	}
	if query != "" {
		v.Set("query", query)
	}
	resp, err := c.do(http.MethodGet, c.baseURL+"/subtitles?"+v.Encode(), nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var parsed struct {
		Data []struct {
			Attributes struct {
				Language       string `json:"language"`
				MoviehashMatch bool   `json:"moviehash_match"`
				Files          []struct {
					FileID   int64  `json:"file_id"`
					FileName string `json:"file_name"`
				} `json:"files"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("subtitles: decode search response: %w", err)
	}

	results := make([]Result, 0, len(parsed.Data))
	for _, d := range parsed.Data {
		if len(d.Attributes.Files) == 0 {
			continue
		}
		f := d.Attributes.Files[0]
		results = append(results, Result{
			FileID:    f.FileID,
			FileName:  f.FileName,
			Language:  d.Attributes.Language,
			HashMatch: d.Attributes.MoviehashMatch,
		})
	}
	return results, nil
}

// Download fetches the subtitle file identified by fileID: it posts the
// file_id, then follows the returned link (an unauthenticated download URL)
// and returns the file's raw bytes.
func (c *Client) Download(fileID int64) ([]byte, error) {
	body, err := json.Marshal(struct {
		FileID int64 `json:"file_id"`
	}{FileID: fileID})
	if err != nil {
		return nil, err
	}
	resp, err := c.do(http.MethodPost, c.baseURL+"/download", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var parsed struct {
		Link     string `json:"link"`
		FileName string `json:"file_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("subtitles: decode download response: %w", err)
	}

	// The link is unauthenticated and comes straight from the API response,
	// so validate it before following: require an absolute https URL. The
	// one exception is a loopback host over http, which only exists so
	// httptest-backed tests (that serve both the API and the "CDN" from the
	// same local server) keep working — a real download link must be https.
	link, err := url.Parse(parsed.Link)
	if err != nil || validateLinkURL(link) != nil {
		return nil, fmt.Errorf("subtitles: download link is not an absolute https URL: %q", parsed.Link)
	}

	// The same policy must hold for every redirect target — http.Client
	// follows redirects automatically, and a validated link could otherwise
	// bounce to an http or internal address. The client copy keeps the
	// caller's Timeout while adding the redirect check.
	lc := *c.httpClient()
	if link.Scheme == "https" {
		// Real-world mode: redirects must stay https, and every ACTUAL dial
		// target (checked post-DNS, so a rebinding hostname can't dodge it)
		// must be a public address — an API-controlled link must not reach
		// loopback, LAN, or link-local services from the daemon's position.
		lc.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			if req.URL.Scheme != "https" || req.URL.Host == "" {
				return fmt.Errorf("subtitles: redirect target is not an absolute https URL: %q", req.URL)
			}
			return nil
		}
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.DialContext = (&net.Dialer{Timeout: 30 * time.Second, Control: rejectNonPublicAddr}).DialContext
		lc.Transport = tr
	} else {
		// Loopback-http mode exists only for httptest-backed tests; redirects
		// may only go to other loopback/https targets.
		lc.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			return validateLinkURL(req.URL)
		}
	}
	fileResp, err := lc.Get(link.String())
	if err != nil {
		return nil, err
	}
	defer func() { _ = fileResp.Body.Close() }()
	if fileResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("subtitles: unexpected status %s fetching subtitle file", fileResp.Status)
	}
	// Cap the CDN read — and treat hitting the cap as an error rather than
	// silently truncating: a partial body written as a "valid" .srt would be
	// trusted by the auto-fetch existence guard and never retried.
	data, err := io.ReadAll(io.LimitReader(fileResp.Body, maxDownloadBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxDownloadBytes {
		return nil, fmt.Errorf("subtitles: subtitle file exceeds %d MiB limit", maxDownloadBytes>>20)
	}
	return data, nil
}

// maxDownloadBytes caps a subtitle download; a real .srt is kilobytes.
const maxDownloadBytes = 10 << 20

// validateLinkURL enforces the download-link policy (absolute https, or
// loopback http for tests) on the initial link and on every redirect target.
func validateLinkURL(u *url.URL) error {
	switch {
	case u == nil || u.Host == "":
	case u.Scheme == "https":
		return nil
	case u.Scheme == "http" && isLoopbackHost(u.Hostname()):
		return nil
	}
	return fmt.Errorf("subtitles: download link target is not an absolute https URL: %q", u)
}

// rejectNonPublicAddr is a net.Dialer Control hook that refuses connections to
// any non-public address (loopback, RFC-1918 private, link-local, unspecified,
// multicast). It sees the literal address being dialed — after DNS resolution —
// so it holds for redirects and DNS-rebinding tricks alike.
func rejectNonPublicAddr(network, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("subtitles: bad dial address %q: %w", address, err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
		return fmt.Errorf("subtitles: download link resolves to a non-public address %q", host)
	}
	return nil
}

// isLoopbackHost reports whether host (already stripped of a port by
// url.URL.Hostname) names the local machine — the only case a plain http
// download link is allowed, so tests can serve their fake CDN over
// httptest's http:// loopback server without weakening the check for real
// OpenSubtitles links, which must be https.
func isLoopbackHost(host string) bool {
	return host == "127.0.0.1" || host == "::1" || host == "localhost"
}
