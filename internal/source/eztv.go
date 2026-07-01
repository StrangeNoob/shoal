package source

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

type EZTV struct {
	Client *http.Client
	Base   string
}

func NewEZTV() *EZTV {
	return &EZTV{Client: &http.Client{Timeout: 20 * time.Second}, Base: "https://eztvx.to/api/get-torrents"}
}

func (e *EZTV) setHTTPClient(c *http.Client) { e.Client = c }
func (e *EZTV) Name() string                 { return "EZTV" }

func (e *EZTV) Search(ctx context.Context, query string) ([]Result, error) {
	if strings.TrimSpace(query) != "" {
		return nil, nil
	}
	var payload struct {
		Torrents []struct {
			Title      string      `json:"title"`
			Filename   string      `json:"filename"`
			Hash       string      `json:"hash"`
			MagnetURL  string      `json:"magnet_url"`
			Seeds      int64       `json:"seeds"`
			Peers      int64       `json:"peers"`
			SizeBytes  json.Number `json:"size_bytes"`
			ReleasedAt int64       `json:"date_released_unix"`
		} `json:"torrents"`
	}
	if err := fetchJSON(ctx, e.Client, e.Base+"?limit=100&page=1", &payload); err != nil {
		return nil, err
	}
	var out []Result
	for _, tor := range payload.Torrents {
		hash := strings.ToLower(tor.Hash)
		if hash == "" {
			continue
		}
		name := tor.Title
		if name == "" {
			name = tor.Filename
		}
		if name == "" {
			name = hash
		}
		magnet := tor.MagnetURL
		if magnet == "" {
			magnet = buildMagnet(hash, name)
		}
		size, _ := tor.SizeBytes.Int64()
		out = append(out, Result{
			Title:      name,
			Source:     "EZTV",
			SizeBytes:  size,
			Popularity: tor.Seeds,
			Seeders:    tor.Seeds,
			Leechers:   tor.Peers,
			Added:      tor.ReleasedAt,
			Category:   "tv",
			Magnet:     magnet,
		})
	}
	return out, nil
}
