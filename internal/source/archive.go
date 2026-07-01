package source

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// Archive searches the Internet Archive (archive.org), a large library of
// legal, freely distributable media. Every item is offered as a .torrent, so
// it's an ideal first source: real content, real swarms (with web seeds), and
// nothing that raises the legal questions a piracy index would.
type Archive struct {
	Client *http.Client
}

// NewArchive returns an Internet Archive source with a sensible HTTP client.
func NewArchive() *Archive {
	return &Archive{Client: &http.Client{Timeout: 20 * time.Second}}
}

func (a *Archive) Name() string { return "Internet Archive" }

// Search queries the archive.org advanced-search API and maps each hit to a
// Result whose TorrentURL points at that item's auto-generated .torrent.
//
// We now also request the item's mediatype and stash it on Result.Category so
// the UI can offer media-type filters (All / Movies / Audio / Software / …)
// without a second request. Filtering is done client-side in the UI: the
// Source interface stays a plain Search(query).
func (a *Archive) Search(ctx context.Context, query string) ([]Result, error) {
	q := url.Values{}
	q.Set("q", query)
	q.Add("fl[]", "identifier")
	q.Add("fl[]", "title")
	q.Add("fl[]", "item_size")
	q.Add("fl[]", "downloads")
	q.Add("fl[]", "mediatype")
	q.Set("rows", "40")
	q.Set("output", "json")
	endpoint := "https://archive.org/advancedsearch.php?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "github.com/StrangeNoob/shoal/0.2 (terminal torrent client)")

	resp, err := a.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("archive.org returned status %d", resp.StatusCode)
	}

	var payload struct {
		Response struct {
			Docs []struct {
				Identifier string      `json:"identifier"`
				Title      flexString  `json:"title"`
				ItemSize   json.Number `json:"item_size"`
				Downloads  json.Number `json:"downloads"`
				Mediatype  flexString  `json:"mediatype"`
			} `json:"docs"`
		} `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode archive.org response: %w", err)
	}

	results := make([]Result, 0, len(payload.Response.Docs))
	for _, d := range payload.Response.Docs {
		if d.Identifier == "" {
			continue
		}
		size, _ := d.ItemSize.Int64()
		pop, _ := d.Downloads.Int64()
		title := string(d.Title)
		if title == "" {
			title = d.Identifier
		}
		results = append(results, Result{
			Title:      title,
			Source:     "Internet Archive",
			SizeBytes:  size,
			Popularity: pop,
			Category:   string(d.Mediatype),
			// Every IA item exposes a generated .torrent at this stable path.
			TorrentURL: fmt.Sprintf("https://archive.org/download/%s/%s_archive.torrent", d.Identifier, d.Identifier),
		})
	}
	return results, nil
}

// flexString tolerates an archive.org field that is sometimes a JSON string and
// sometimes an array of strings (the API does both for "title" and "mediatype").
type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	if len(b) == 0 {
		return nil
	}
	switch b[0] {
	case '"':
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*f = flexString(s)
	case '[':
		var arr []string
		if err := json.Unmarshal(b, &arr); err != nil {
			return err
		}
		if len(arr) > 0 {
			*f = flexString(arr[0])
		}
	default:
		*f = flexString(string(b))
	}
	return nil
}
