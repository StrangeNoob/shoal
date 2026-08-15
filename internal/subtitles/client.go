package subtitles

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
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
		defer resp.Body.Close()
		switch resp.StatusCode {
		case http.StatusTooManyRequests:
			return nil, ErrRateLimited
		case http.StatusUnauthorized, http.StatusForbidden:
			return nil, ErrBadKey
		default:
			return nil, fmt.Errorf("subtitles: unexpected status %s", resp.Status)
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
	defer resp.Body.Close()

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
	defer resp.Body.Close()

	var parsed struct {
		Link     string `json:"link"`
		FileName string `json:"file_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("subtitles: decode download response: %w", err)
	}

	fileResp, err := c.httpClient().Get(parsed.Link)
	if err != nil {
		return nil, err
	}
	defer fileResp.Body.Close()
	if fileResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("subtitles: unexpected status %s fetching subtitle file", fileResp.Status)
	}
	return io.ReadAll(fileResp.Body)
}
