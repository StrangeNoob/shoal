package source

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Nyaa struct {
	Client *http.Client
	Base   string
}

func NewNyaa() *Nyaa {
	return &Nyaa{Client: &http.Client{Timeout: 20 * time.Second}, Base: "https://nyaa.si/"}
}

func (n *Nyaa) setHTTPClient(c *http.Client) { n.Client = c }
func (n *Nyaa) Name() string                 { return "Nyaa" }

func (n *Nyaa) Search(ctx context.Context, query string) ([]Result, error) {
	params := url.Values{}
	params.Set("page", "rss")
	params.Set("q", strings.TrimSpace(query))
	params.Set("c", "0_0")
	params.Set("f", "0")
	body, err := fetchBytes(ctx, n.Client, strings.TrimRight(n.Base, "/")+"/?"+params.Encode())
	if err != nil {
		return nil, err
	}
	return parseNyaaRSS(string(body)), nil
}

func parseNyaaRSS(xmlText string) []Result {
	items := strings.Split(xmlText, "<item>")
	out := make([]Result, 0, len(items))
	for _, item := range items[1:] {
		infoHash := strings.ToLower(tag(item, "nyaa:infoHash"))
		name := tag(item, "title")
		if infoHash == "" || name == "" {
			continue
		}
		seeders, _ := strconv.ParseInt(tag(item, "nyaa:seeders"), 10, 64)
		leechers, _ := strconv.ParseInt(tag(item, "nyaa:leechers"), 10, 64)
		out = append(out, Result{
			Title:      name,
			Source:     "Nyaa",
			SizeBytes:  parseSize(tag(item, "nyaa:size")),
			Popularity: seeders,
			Seeders:    seeders,
			Leechers:   leechers,
			Added:      parseTimeUnix(tag(item, "pubDate")),
			Category:   "anime",
			Magnet:     buildMagnet(infoHash, name),
		})
	}
	return out
}

func tag(xmlText, name string) string {
	re := regexp.MustCompile(`(?is)<` + regexp.QuoteMeta(name) + `>(?:<!\[CDATA\[)?(.*?)(?:\]\]>)?</` + regexp.QuoteMeta(name) + `>`)
	return firstSubmatch(re, xmlText)
}
