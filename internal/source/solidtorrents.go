package source

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type SolidTorrents struct {
	Client *http.Client
	Base   string
}

func NewSolidTorrents() *SolidTorrents {
	return &SolidTorrents{Client: &http.Client{Timeout: 20 * time.Second}, Base: "https://solidtorrents.net/api/v1/search"}
}

func (s *SolidTorrents) setHTTPClient(c *http.Client) { s.Client = c }
func (s *SolidTorrents) Name() string                 { return "SolidTorrents" }

func (s *SolidTorrents) Search(ctx context.Context, query string) ([]Result, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		q = "tv show"
	}
	endpoint := s.Base + "?q=" + url.QueryEscape(q)
	var payload struct {
		Success bool `json:"success"`
		Results []struct {
			InfoHash  string `json:"infohash"`
			Title     string `json:"title"`
			Size      int64  `json:"size"`
			Seeders   int64  `json:"seeders"`
			Leechers  int64  `json:"leechers"`
			UpdatedAt string `json:"updatedAt"`
		} `json:"results"`
	}
	if err := fetchJSON(ctx, s.Client, endpoint, &payload); err != nil {
		return nil, err
	}
	var out []Result
	for _, item := range payload.Results {
		if item.InfoHash == "" {
			continue
		}
		name := item.Title
		if name == "" {
			name = "Unknown"
		}
		infoHash := strings.ToLower(item.InfoHash)
		out = append(out, Result{
			Title:      name,
			Source:     "Solid",
			SizeBytes:  item.Size,
			Popularity: item.Seeders,
			Seeders:    item.Seeders,
			Leechers:   item.Leechers,
			Added:      parseTimeUnix(item.UpdatedAt),
			Category:   "tv",
			Magnet:     buildMagnet(infoHash, name),
		})
	}
	return out, nil
}
