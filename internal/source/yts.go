package source

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type YTS struct {
	Client *http.Client
	Hosts  []string
}

func NewYTS() *YTS {
	return &YTS{Client: &http.Client{Timeout: 20 * time.Second}, Hosts: []string{"yts.mx", "yts.am", "yts.rs"}}
}

func (y *YTS) setHTTPClient(c *http.Client) { y.Client = c }
func (y *YTS) Name() string                 { return "YTS" }

func (y *YTS) Search(ctx context.Context, query string) ([]Result, error) {
	params := url.Values{"limit": []string{"50"}}
	if q := strings.TrimSpace(query); q != "" {
		params.Set("query_term", q)
	} else {
		params.Set("sort_by", "date_added")
	}

	var payload struct {
		Data struct {
			Movies []struct {
				TitleLong string `json:"title_long"`
				Title     string `json:"title"`
				Uploaded  int64  `json:"date_uploaded_unix"`
				Torrents  []struct {
					Hash      string `json:"hash"`
					Quality   string `json:"quality"`
					Type      string `json:"type"`
					SizeBytes int64  `json:"size_bytes"`
					Seeds     int64  `json:"seeds"`
					Peers     int64  `json:"peers"`
				} `json:"torrents"`
			} `json:"movies"`
		} `json:"data"`
	}
	if err := fetchFirstJSON(ctx, y.Client, y.hosts(), "/api/v2/list_movies.json?"+params.Encode(), &payload); err != nil {
		return nil, err
	}

	var out []Result
	for _, movie := range payload.Data.Movies {
		base := movie.TitleLong
		if base == "" {
			base = movie.Title
		}
		if base == "" {
			base = "Unknown"
		}
		for _, tor := range movie.Torrents {
			if tor.Hash == "" {
				continue
			}
			infoHash := strings.ToLower(tor.Hash)
			tag := strings.TrimSpace(strings.Join([]string{tor.Quality, tor.Type}, " "))
			name := base
			if tag != "" {
				name += " [" + tag + "]"
			}
			out = append(out, Result{
				Title:      name,
				Source:     "YTS",
				SizeBytes:  tor.SizeBytes,
				Popularity: tor.Seeds,
				Seeders:    tor.Seeds,
				Leechers:   tor.Peers,
				Added:      movie.Uploaded,
				Category:   "movies",
				Magnet:     buildMagnet(infoHash, name),
			})
		}
	}
	return out, nil
}

func (y *YTS) hosts() []string {
	if len(y.Hosts) > 0 {
		return y.Hosts
	}
	return []string{"yts.mx"}
}

func fetchFirstJSON(ctx context.Context, client *http.Client, hosts []string, path string, out any) error {
	var last error
	for _, host := range hosts {
		err := fetchJSON(ctx, client, "https://"+host+path, out)
		if err == nil {
			return nil
		}
		last = err
	}
	if last != nil {
		return last
	}
	return fmt.Errorf("no hosts configured")
}
