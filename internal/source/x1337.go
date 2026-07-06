package source

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type X1337 struct {
	Client   *http.Client
	NameText string
	Label    string
	Category string
	Hosts    []string
	Kind     string
}

func New1337xMovies() *X1337 {
	return &X1337{
		Client: &http.Client{Timeout: 20 * time.Second}, NameText: "1337x Movies", Label: "1337x",
		Category: "movies", Kind: "Movies", Hosts: []string{"1337x.to", "1337x.st", "x1337x.ws", "1337xx.to"},
	}
}

func New1337xTV() *X1337 {
	return &X1337{
		Client: &http.Client{Timeout: 20 * time.Second}, NameText: "1337x TV", Label: "1337x",
		Category: "tv", Kind: "TV", Hosts: []string{"1337x.to", "1337x.st", "x1337x.ws", "1337xx.to"},
	}
}

func (x *X1337) setHTTPClient(c *http.Client) { x.Client = c }
func (x *X1337) Name() string                 { return x.NameText }

type x1337Row struct {
	Name      string
	Path      string
	Seeders   int64
	Leechers  int64
	SizeBytes int64
}

var (
	x1337RowRE     = regexp.MustCompile(`(?is)<tr[\s>](.*?)</tr>`)
	x1337LinkRE    = regexp.MustCompile(`(?is)href=["'](/torrent/[^"']+)["'][^>]*>([^<]+)</a>`)
	x1337SeedsRE   = regexp.MustCompile(`(?is)class=["'][^"']*coll-2 seeds[^"']*["'][^>]*>\s*(\d+)`)
	x1337LeechesRE = regexp.MustCompile(`(?is)class=["'][^"']*coll-3 leeches[^"']*["'][^>]*>\s*(\d+)`)
	x1337SizeRE    = regexp.MustCompile(`(?is)class=["'][^"']*coll-4 size[^"']*["'][^>]*>\s*([\d.]+\s*[KMGT]i?B)`)
	x1337MagnetRE  = regexp.MustCompile(`(?is)magnet:\?xt=urn:btih:[^"'<>\s]+`)
)

func (x *X1337) Search(ctx context.Context, query string) ([]Result, error) {
	q := strings.TrimSpace(query)
	path := "/popular-movies"
	if x.Kind == "TV" {
		path = "/popular-tv"
	}
	if q != "" {
		path = "/category-search/" + strings.ReplaceAll(url.QueryEscape(q), "+", "%20") + "/" + x.Kind + "/1/"
		path = strings.ReplaceAll(path, "%20", "+")
	}

	base, html, err := x.fetchFirstText(ctx, path)
	if err != nil {
		return nil, err
	}
	rows := parse1337Rows(html)
	if q != "" {
		rows = filter1337Rows(rows, q)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Seeders > rows[j].Seeders })
	if len(rows) > 8 {
		rows = rows[:8]
	}

	out := make([]Result, 0, len(rows))
	for _, row := range rows {
		magnet := x.detailMagnet(ctx, base, row.Path)
		parsed := parseMagnet(magnet)
		if parsed == nil {
			continue
		}
		out = append(out, Result{
			Title:        row.Name,
			Source:       x.Label,
			SizeBytes:    row.SizeBytes,
			Popularity:   row.Seeders,
			Seeders:      row.Seeders,
			SeedersKnown: true,
			Leechers:     row.Leechers,
			Category:     x.Category,
			Magnet:       parsed.Magnet,
		})
	}
	return out, nil
}

func (x *X1337) fetchFirstText(ctx context.Context, path string) (base, body string, err error) {
	var last error
	for _, host := range x.hosts() {
		candidate := "https://" + host
		b, e := fetchBytes(ctx, x.Client, candidate+path)
		if e == nil {
			return candidate, string(b), nil
		}
		last = e
	}
	if last == nil {
		last = fmt.Errorf("1337x has no hosts")
	}
	return "", "", last
}

func (x *X1337) detailMagnet(ctx context.Context, base, path string) string {
	b, err := fetchBytes(ctx, x.Client, base+path)
	if err != nil {
		return ""
	}
	return unescapeEntities(x1337MagnetRE.FindString(string(b)))
}

func (x *X1337) hosts() []string {
	if len(x.Hosts) > 0 {
		return x.Hosts
	}
	return []string{"1337x.to"}
}

func parse1337Rows(htmlText string) []x1337Row {
	start := strings.Index(htmlText, "table-list")
	if start < 0 {
		return nil
	}
	matches := x1337RowRE.FindAllStringSubmatch(htmlText[start:], -1)
	out := make([]x1337Row, 0, len(matches))
	for _, m := range matches {
		row := m[1]
		link := x1337LinkRE.FindStringSubmatch(row)
		if len(link) != 3 {
			continue
		}
		seeders, _ := strconv.ParseInt(firstSubmatch(x1337SeedsRE, row), 10, 64)
		leechers, _ := strconv.ParseInt(firstSubmatch(x1337LeechesRE, row), 10, 64)
		out = append(out, x1337Row{
			Name:      unescapeEntities(link[2]),
			Path:      link[1],
			Seeders:   seeders,
			Leechers:  leechers,
			SizeBytes: parseSize(firstSubmatch(x1337SizeRE, row)),
		})
	}
	return out
}

func filter1337Rows(rows []x1337Row, query string) []x1337Row {
	stop := map[string]bool{"the": true, "a": true, "an": true, "of": true, "and": true, "or": true, "to": true}
	var tokens []string
	for _, t := range strings.Fields(strings.ToLower(query)) {
		if !stop[t] {
			tokens = append(tokens, t)
		}
	}
	if len(tokens) == 0 {
		tokens = strings.Fields(strings.ToLower(query))
	}
	if len(tokens) == 0 {
		return rows
	}
	out := rows[:0]
	for _, row := range rows {
		name := strings.ToLower(row.Name)
		ok := true
		for _, t := range tokens {
			if !strings.Contains(name, t) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, row)
		}
	}
	return out
}
