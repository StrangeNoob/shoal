package source

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// TorrentsCSV searches a large open dataset of infohashes. It reports no
// category, so its rows only show under the "All" filter.
type TorrentsCSV struct {
	Client *http.Client
	Base   string
}

func NewTorrentsCSV() *TorrentsCSV {
	return &TorrentsCSV{
		Client: &http.Client{Timeout: 20 * time.Second},
		Base:   "https://torrents-csv.com/service/search",
	}
}

func (t *TorrentsCSV) setHTTPClient(c *http.Client) { t.Client = c }
func (t *TorrentsCSV) Name() string                 { return "Torrents-CSV" }

func (t *TorrentsCSV) Search(ctx context.Context, query string) ([]Result, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, nil // ponytail: search-only — the API answers 400 without a query
	}
	// The API caps a page at 25 rows and returns a `next` cursor; one page is
	// enough beside the other providers. Page through `next` if that changes.
	endpoint := t.Base + "?size=25&q=" + url.QueryEscape(q)
	var payload struct {
		Torrents []struct {
			InfoHash  string `json:"infohash"`
			Name      string `json:"name"`
			SizeBytes int64  `json:"size_bytes"`
			Seeders   int64  `json:"seeders"`
			Leechers  int64  `json:"leechers"`
			Created   int64  `json:"created_unix"`
		} `json:"torrents"`
	}
	if err := fetchJSON(ctx, t.Client, endpoint, &payload); err != nil {
		return nil, err
	}
	out := make([]Result, 0, len(payload.Torrents))
	for _, tor := range payload.Torrents {
		infoHash := normalizeInfoHash(tor.InfoHash)
		if len(infoHash) != 40 {
			continue
		}
		name := tor.Name
		if name == "" {
			name = infoHash
		}
		out = append(out, Result{
			Title:        name,
			Source:       "CSV",
			SizeBytes:    tor.SizeBytes,
			Popularity:   tor.Seeders,
			Seeders:      tor.Seeders,
			SeedersKnown: true,
			Leechers:     tor.Leechers,
			Added:        tor.Created,
			Magnet:       buildMagnet(infoHash, name),
		})
	}
	return out, nil
}
