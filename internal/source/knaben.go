package source

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// Knaben is a meta-index: one query reaches ~30 upstream sites (The Pirate Bay,
// RuTracker, Nyaa, Tokyo Toshokan, …) through a single API, so it fills the gaps
// left by the per-site providers when one of them is blocked or down.
type Knaben struct {
	Client *http.Client
	Base   string
}

func NewKnaben() *Knaben {
	// 30s, not the usual 20s: the API is slow by measurement — ~8s for 50 rows,
	// ~22s for 100 — so a shorter timeout throws away a working response.
	return &Knaben{Client: &http.Client{Timeout: 30 * time.Second}, Base: "https://api.knaben.org/v1"}
}

func (k *Knaben) setHTTPClient(c *http.Client) { k.Client = c }
func (k *Knaben) Name() string                 { return "Knaben" }

func (k *Knaben) Search(ctx context.Context, query string) ([]Result, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, nil // ponytail: search-only — the API rejects an empty query
	}
	req := map[string]any{
		// "100%" requires every query word in the title; "score" ignores the
		// query almost entirely and returns whatever is most seeded.
		"search_type":     "100%",
		"search_field":    "title",
		"query":           q,
		"order_by":        "seeders",
		"order_direction": "desc",
		"size":            50, // response time scales with size; 100 rows takes ~22s
		"hide_unsafe":     true,
		"hide_xxx":        true,
	}
	var payload struct {
		Hits []struct {
			Title     string `json:"title"`
			Hash      string `json:"hash"`
			MagnetURL string `json:"magnetUrl"`
			Bytes     int64  `json:"bytes"`
			Seeders   int64  `json:"seeders"`
			Peers     int64  `json:"peers"`
			Category  string `json:"category"`
			Date      string `json:"date"`
		} `json:"hits"`
	}
	if err := postJSON(ctx, k.Client, k.Base, req, &payload); err != nil {
		return nil, err
	}
	out := make([]Result, 0, len(payload.Hits))
	for _, hit := range payload.Hits {
		magnet := hit.MagnetURL
		if parseMagnet(magnet) == nil {
			// Some rows carry a hash but no magnet; build one from the hash.
			infoHash := normalizeInfoHash(hit.Hash)
			if len(infoHash) != 40 {
				continue
			}
			magnet = buildMagnet(infoHash, hit.Title)
		}
		name := hit.Title
		if name == "" {
			name = "Unknown"
		}
		out = append(out, Result{
			Title:        name,
			Source:       "Knaben",
			SizeBytes:    hit.Bytes,
			Popularity:   hit.Seeders,
			Seeders:      hit.Seeders,
			SeedersKnown: true,
			Leechers:     hit.Peers,
			Added:        parseTimeUnix(hit.Date),
			Category:     knabenCategory(hit.Category),
			Magnet:       magnet,
		})
	}
	return out, nil
}

// knabenCategory folds Knaben's free-form category ("Anime / Subbed", "PC /
// Games") onto the UI's filter chips. Unknown values stay empty, which only
// means the result shows under "All".
func knabenCategory(raw string) string {
	s := strings.ToLower(raw)
	switch {
	case strings.Contains(s, "anime"):
		return "anime"
	case strings.Contains(s, "game"):
		return "games"
	case strings.Contains(s, "movie"):
		return "movies"
	case strings.Contains(s, "tv"):
		return "tv"
	case strings.Contains(s, "audio"), strings.Contains(s, "music"):
		return "audio"
	case strings.Contains(s, "book"), strings.Contains(s, "comic"):
		return "texts"
	case strings.Contains(s, "pc"), strings.Contains(s, "mac"), strings.Contains(s, "app"),
		strings.Contains(s, "software"), strings.Contains(s, "android"):
		return "software"
	}
	return ""
}
