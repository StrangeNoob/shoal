package source

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type PirateBay struct {
	Client    *http.Client
	NameText  string
	Label     string
	Category  string
	Cats      map[int]bool
	BrowseURL string
}

func NewPirateBayMovies() *PirateBay {
	return &PirateBay{
		Client: &http.Client{Timeout: 20 * time.Second}, NameText: "TPB Movies", Label: "TPB",
		Category: "movies", BrowseURL: "https://apibay.org/precompiled/data_top100_207.json",
		Cats: map[int]bool{201: true, 202: true, 207: true, 209: true},
	}
}

func NewPirateBayTV() *PirateBay {
	return &PirateBay{
		Client: &http.Client{Timeout: 20 * time.Second}, NameText: "TPB TV", Label: "TPB",
		Category: "tv", BrowseURL: "https://apibay.org/precompiled/data_top100_208.json",
		Cats: map[int]bool{205: true, 208: true},
	}
}

func (p *PirateBay) setHTTPClient(c *http.Client) { p.Client = c }
func (p *PirateBay) Name() string                 { return p.NameText }

func (p *PirateBay) Search(ctx context.Context, query string) ([]Result, error) {
	q := strings.TrimSpace(query)
	endpoint := p.BrowseURL
	if q != "" {
		endpoint = "https://apibay.org/q.php?q=" + url.QueryEscape(q)
	}
	var items []struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		InfoHash string `json:"info_hash"`
		Seeders  string `json:"seeders"`
		Leechers string `json:"leechers"`
		NumFiles string `json:"num_files"`
		Size     string `json:"size"`
		Added    string `json:"added"`
		Category string `json:"category"`
	}
	if err := fetchJSON(ctx, p.Client, endpoint, &items); err != nil {
		return nil, err
	}
	var out []Result
	for _, item := range items {
		cat, _ := strconv.Atoi(item.Category)
		if q != "" && !p.Cats[cat] {
			continue
		}
		infoHash := strings.ToLower(item.InfoHash)
		if infoHash == "" || infoHash == strings.Repeat("0", 40) || item.ID == "0" {
			continue
		}
		name := item.Name
		if name == "" {
			name = "Unknown"
		}
		seeders, _ := strconv.ParseInt(item.Seeders, 10, 64)
		size, _ := strconv.ParseInt(item.Size, 10, 64)
		leechers, _ := strconv.ParseInt(item.Leechers, 10, 64)
		files, _ := strconv.Atoi(item.NumFiles)
		added, _ := strconv.ParseInt(item.Added, 10, 64)
		out = append(out, Result{
			Title:        name,
			Source:       p.Label,
			SizeBytes:    size,
			Popularity:   seeders,
			Seeders:      seeders,
			SeedersKnown: true,
			Leechers:     leechers,
			Files:        files,
			Added:        added,
			Category:     p.Category,
			Magnet:       buildMagnet(infoHash, name),
		})
	}
	return out, nil
}
