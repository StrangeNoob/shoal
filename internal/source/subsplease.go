package source

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type SubsPlease struct {
	Client *http.Client
	Base   string
}

type subsPleaseDownload struct {
	Res    string `json:"res"`
	Magnet string `json:"magnet"`
}

func NewSubsPlease() *SubsPlease {
	return &SubsPlease{Client: &http.Client{Timeout: 20 * time.Second}, Base: "https://subsplease.org/api/"}
}

func (s *SubsPlease) setHTTPClient(c *http.Client) { s.Client = c }
func (s *SubsPlease) Name() string                 { return "SubsPlease" }

func (s *SubsPlease) Search(ctx context.Context, query string) ([]Result, error) {
	params := url.Values{"tz": []string{"UTC"}}
	if q := strings.TrimSpace(query); q != "" {
		params.Set("f", "search")
		params.Set("s", q)
	} else {
		params.Set("f", "latest")
	}
	var payload map[string]struct {
		Show        string               `json:"show"`
		Episode     string               `json:"episode"`
		ReleaseDate string               `json:"release_date"`
		Downloads   []subsPleaseDownload `json:"downloads"`
	}
	if err := fetchJSON(ctx, s.Client, s.Base+"?"+params.Encode(), &payload); err != nil {
		return nil, err
	}
	var out []Result
	for _, entry := range payload {
		dl := pickSubsPleaseDownload(entry.Downloads)
		if dl.Magnet == "" {
			continue
		}
		parsed := parseMagnet(dl.Magnet)
		if parsed == nil {
			continue
		}
		show := entry.Show
		if show == "" {
			show = "Unknown"
		}
		name := show
		if entry.Episode != "" {
			name += " - " + entry.Episode
		}
		if dl.Res != "" {
			name += " [" + dl.Res + "p]"
		}
		out = append(out, Result{
			Title:     name,
			Source:    "SubsPlease",
			SizeBytes: magnetXL(parsed.Magnet),
			Added:     parseTimeUnix(entry.ReleaseDate),
			Category:  "anime",
			Magnet:    parsed.Magnet,
		})
	}
	return out, nil
}

func pickSubsPleaseDownload(downloads []subsPleaseDownload) subsPleaseDownload {
	for _, want := range []string{"1080", "720", "480"} {
		for _, dl := range downloads {
			if dl.Res == want && dl.Magnet != "" {
				return dl
			}
		}
	}
	for _, dl := range downloads {
		if dl.Magnet != "" {
			return dl
		}
	}
	return subsPleaseDownload{}
}

func magnetXL(magnet string) int64 {
	u, err := url.Parse(magnet)
	if err != nil {
		return 0
	}
	n, _ := strconv.ParseInt(u.Query().Get("xl"), 10, 64)
	return n
}
