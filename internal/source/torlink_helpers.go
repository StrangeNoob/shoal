package source

import (
	"context"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const torlinkUserAgent = "shoal (+https://github.com/baairon/torlink-style-sources)"

type clientSetter interface {
	setHTTPClient(*http.Client)
}

type parsedMagnet struct {
	InfoHash string
	Name     string
	Magnet   string
}

// The colons in xt=urn:btih: are literal in raw magnets but percent-encoded to
// %3a by url.Values.Encode (which buildMagnet uses), so accept either form.
var magnetHashRE = regexp.MustCompile(`(?i)xt=urn(?::|%3a)btih(?::|%3a)([a-f0-9]{40}|[a-z2-7]{32})`)

// ParseMagnetInfoHash returns the 40-char hex infohash from a magnet URI, or ""
// when the input isn't a magnet the client understands.
func ParseMagnetInfoHash(magnet string) string {
	if pm := parseMagnet(magnet); pm != nil {
		return pm.InfoHash
	}
	return ""
}

func parseMagnet(input string) *parsedMagnet {
	s := strings.TrimSpace(input)
	if !strings.HasPrefix(strings.ToLower(s), "magnet:?") {
		return nil
	}
	m := magnetHashRE.FindStringSubmatch(s)
	if len(m) != 2 {
		return nil
	}
	infoHash := normalizeInfoHash(m[1])
	name := infoHash
	if u, err := url.Parse(s); err == nil {
		if dn := u.Query().Get("dn"); dn != "" {
			name = dn
		}
	}
	return &parsedMagnet{InfoHash: infoHash, Name: name, Magnet: s}
}

func normalizeInfoHash(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) != 32 {
		return strings.ToLower(raw)
	}
	dec, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(raw))
	if err != nil || len(dec) != 20 {
		return strings.ToLower(raw)
	}
	return hex.EncodeToString(dec)
}

func httpClient(c *http.Client) *http.Client {
	if c != nil {
		return c
	}
	return &http.Client{Timeout: 20 * time.Second}
}

func fetchBytes(ctx context.Context, client *http.Client, endpoint string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", torlinkUserAgent)
	resp, err := httpClient(client).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("source returned status %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

func fetchJSON(ctx context.Context, client *http.Client, endpoint string, out any) error {
	b, err := fetchBytes(ctx, client, endpoint)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func unescapeEntities(s string) string {
	return html.UnescapeString(strings.TrimSpace(s))
}

func parseTimeUnix(s string) int64 {
	if strings.TrimSpace(s) == "" {
		return 0
	}
	if n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil {
		return n
	}
	for _, layout := range []string{time.RFC1123Z, time.RFC1123, time.RFC3339, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, strings.TrimSpace(s)); err == nil {
			return t.Unix()
		}
	}
	return 0
}

func firstSubmatch(re *regexp.Regexp, s string) string {
	m := re.FindStringSubmatch(s)
	if len(m) < 2 {
		return ""
	}
	return unescapeEntities(m[1])
}

var sizeRE = regexp.MustCompile(`(?i)([\d.]+)\s*([KMGT]?I?B)`)

func parseSize(s string) int64 {
	m := sizeRE.FindStringSubmatch(s)
	if len(m) != 3 {
		return 0
	}
	n, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0
	}
	unit := strings.ToUpper(m[2])
	mul := float64(1)
	switch unit {
	case "KB":
		mul = 1000
	case "MB":
		mul = 1e6
	case "GB":
		mul = 1e9
	case "TB":
		mul = 1e12
	case "KIB":
		mul = 1024
	case "MIB":
		mul = 1024 * 1024
	case "GIB":
		mul = 1024 * 1024 * 1024
	case "TIB":
		mul = 1024 * 1024 * 1024 * 1024
	}
	return int64(math.Round(n * mul))
}

func fetchWordpressRSS(ctx context.Context, base, sourceLabel, category, query string, client *http.Client) ([]Result, error) {
	q := strings.TrimSpace(query)
	endpoint := strings.TrimRight(base, "/")
	if q == "" {
		endpoint += "/feed/"
	} else {
		v := url.Values{}
		v.Set("s", q)
		v.Set("feed", "rss2")
		endpoint += "/?" + v.Encode()
	}
	body, err := fetchBytes(ctx, client, endpoint)
	if err != nil {
		return nil, err
	}
	return parseWordpressRSS(string(body), sourceLabel, category), nil
}

var (
	rssTitleRE  = regexp.MustCompile(`(?is)<title>(?:<!\[CDATA\[)?(.*?)(?:\]\]>)?</title>`)
	rssMagnetRE = regexp.MustCompile(`(?is)href=["'](magnet:\?xt=urn:btih:[^"']+)["']`)
	rssDateRE   = regexp.MustCompile(`(?is)<pubDate>(.*?)</pubDate>`)
)

func parseWordpressRSS(xml, sourceLabel, category string) []Result {
	items := strings.Split(xml, "<item>")
	out := make([]Result, 0, len(items))
	for _, item := range items[1:] {
		magnet := firstSubmatch(rssMagnetRE, item)
		parsed := parseMagnet(magnet)
		if parsed == nil {
			continue
		}
		title := firstSubmatch(rssTitleRE, item)
		if title == "" {
			title = parsed.Name
		}
		out = append(out, Result{
			Title:    title,
			Source:   sourceLabel,
			Category: category,
			Added:    parseTimeUnix(firstSubmatch(rssDateRE, item)),
			Magnet:   parsed.Magnet,
		})
	}
	return out
}
