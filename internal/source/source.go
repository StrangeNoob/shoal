// Package source defines where shoal finds torrents. Each provider implements
// the Source interface; the UI talks only to this interface, so adding a new
// site later (or swapping the engine) never touches the TUI.
package source

import "context"

// Result is one searchable torrent, normalized across providers.
type Result struct {
	Title      string
	Source     string // human label, e.g. "Internet Archive"
	SizeBytes  int64
	Popularity int64 // a "health" proxy: downloads, seeders, etc.
	Seeders    int64 // 0 when the source doesn't report it
	Leechers   int64 // 0 when the source doesn't report it
	// SeedersKnown is true only for sources that actually report a seeder count,
	// so consumers can tell a real 0 (dead torrent) from "not reported" (e.g. the
	// Internet Archive). The hide-0-seed filter uses this to avoid dropping
	// results that simply lack swarm data.
	SeedersKnown bool
	Files        int   // 0 when unknown
	Added        int64 // unix seconds, 0 when unknown
	// Category is the media type used by the UI's filter chips. For the Internet
	// Archive this is the item's mediatype ("movies", "audio", "texts",
	// "software", "image", …). Empty when the provider doesn't classify items;
	// the "All" filter ignores it.
	Category   string
	TorrentURL string // URL to a .torrent file (preferred)
	Magnet     string // optional magnet alternative
}

// Source is a searchable torrent provider.
type Source interface {
	Name() string
	Search(ctx context.Context, query string) ([]Result, error)
}
