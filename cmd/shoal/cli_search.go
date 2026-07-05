package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/StrangeNoob/shoal/internal/config"
	"github.com/StrangeNoob/shoal/internal/source"
)

type searchRow struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	SizeBytes  int64  `json:"size_bytes"`
	Seeders    int64  `json:"seeders"`
	Leechers   int64  `json:"leechers"`
	Source     string `json:"source"`
	Category   string `json:"category"`
	Magnet     string `json:"magnet"`
	TorrentURL string `json:"torrent_url"`
}

// toRows sorts best-first (seeders, then popularity), caps at limit, and derives
// each row's short id from its magnet infohash ("—" when there is none). Rows
// with no derivable id rank last regardless of seeders — `download` can't act
// on them, so they're not "best" no matter how popular.
func toRows(results []source.Result, limit int) []searchRow {
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Seeders != results[j].Seeders {
			return results[i].Seeders > results[j].Seeders
		}
		return results[i].Popularity > results[j].Popularity
	})
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	rows := make([]searchRow, 0, len(results))
	for _, r := range results {
		id := "—"
		if ih := source.ParseMagnetInfoHash(r.Magnet); ih != "" {
			id = ih[:8]
		}
		rows = append(rows, searchRow{
			ID: id, Title: r.Title, SizeBytes: r.SizeBytes, Seeders: r.Seeders,
			Leechers: r.Leechers, Source: r.Source, Category: r.Category,
			Magnet: r.Magnet, TorrentURL: r.TorrentURL,
		})
	}
	return rows
}

// rowsToCache builds the short-id cache (skips magnet-less "—" rows).
func rowsToCache(rows []searchRow) map[string]cacheEntry {
	m := map[string]cacheEntry{}
	for _, r := range rows {
		if r.ID == "—" {
			continue
		}
		m[r.ID] = cacheEntry{Magnet: r.Magnet, TorrentURL: r.TorrentURL, Title: r.Title}
	}
	return m
}

// searchCore runs a search, shapes rows, and writes the short-id cache.
func searchCore(ctx context.Context, src source.Source, query string, limit int, base string) ([]searchRow, error) {
	results, err := src.Search(ctx, query)
	if err != nil {
		return nil, err
	}
	rows := toRows(results, limit)
	_ = writeCache(base, rowsToCache(rows))
	return rows, nil
}

func filterSources(srcs []source.Source, name string) []source.Source {
	var out []source.Source
	for _, s := range srcs {
		if strings.Contains(strings.ToLower(s.Name()), strings.ToLower(name)) {
			out = append(out, s)
		}
	}
	return out
}

func sourceNames(srcs []source.Source) []string {
	names := make([]string, len(srcs))
	for i, s := range srcs {
		names[i] = s.Name()
	}
	return names
}

// selectSearchSources decides which providers a search hits. An explicit --source
// filters the full set (overriding any disabled state); otherwise the config's
// enabled set is used.
func selectSearchSources(all []source.Source, srcName string, disabled []string) (srcs []source.Source, unknownSource, allDisabled bool) {
	if srcName != "" {
		srcs = filterSources(all, srcName)
		if len(srcs) == 0 {
			return nil, true, false
		}
		return srcs, false, false
	}
	srcs = source.EnabledSources(disabled)
	if len(srcs) == 0 {
		return nil, false, true
	}
	return srcs, false, false
}

func runSearch(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit JSON")
	srcName := fs.String("source", "", "limit to sources matching name")
	limit := fs.Int("limit", 30, "max results (0 = no limit)")
	positionals, err := parseArgs(fs, args)
	if err != nil {
		return 2
	}
	query := strings.Join(positionals, " ")
	if query == "" {
		fmt.Fprintln(os.Stderr, `usage: shoal search "<query>" [--json] [--source name] [--limit N]`)
		return 2
	}

	cfg := config.Load()
	all := source.DefaultSources()
	srcs, unknownSource, allDisabled := selectSearchSources(all, *srcName, cfg.DisabledSources)
	if unknownSource {
		fmt.Fprintf(os.Stderr, "no source matches %q; available: %s\n",
			*srcName, strings.Join(sourceNames(all), ", "))
		return 1
	}
	if allDisabled {
		fmt.Fprintln(os.Stderr, "all sources are disabled — enable one with 'shoal sources enable <name>'")
		printSearch(out, []searchRow{}, *jsonOut) // empty result set ("[]" for --json)
		return 0
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rows, err := searchCore(ctx, source.NewMulti(srcs...), query, *limit, configDir())
	if err != nil {
		fmt.Fprintln(os.Stderr, "search failed:", err)
		return 1
	}
	printSearch(out, rows, *jsonOut)
	return 0
}

func printSearch(out io.Writer, rows []searchRow, asJSON bool) {
	if asJSON {
		b, _ := json.MarshalIndent(rows, "", "  ")
		fmt.Fprintln(out, string(b))
		return
	}
	if len(rows) == 0 {
		fmt.Fprintln(out, "no results")
		return
	}
	table := make([][]string, 0, len(rows))
	for _, r := range rows {
		table = append(table, []string{
			r.ID,
			humanBytes(r.SizeBytes),
			fmt.Sprintf("%d", r.Seeders),
			fmt.Sprintf("%d", r.Leechers),
			r.Source,
			truncate(r.Title, 60),
		})
	}
	printTable(out, []string{"ID", "SIZE", "SEED", "LEECH", "SOURCE", "TITLE"}, table)
}
