package source

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func torlinkPointedAt[T interface{ setHTTPClient(*http.Client) }](srv *httptest.Server, s T) T {
	u, _ := url.Parse(srv.URL)
	s.setHTTPClient(&http.Client{Transport: redirectTransport{u.Scheme, u.Host}})
	return s
}

func TestBuildMagnetAndParseMagnet(t *testing.T) {
	hash := "abcdef0123456789abcdef0123456789abcdef01"
	magnet := buildMagnet(hash, "My Movie 2024")
	if !strings.Contains(magnet, "xt=urn%3Abtih%3A"+hash) && !strings.Contains(magnet, "xt=urn:btih:"+hash) {
		t.Fatalf("magnet missing hash: %q", magnet)
	}
	if !strings.Contains(magnet, "dn=My+Movie+2024") && !strings.Contains(magnet, "dn=My%20Movie%202024") {
		t.Fatalf("magnet missing encoded display name: %q", magnet)
	}
	if !strings.Contains(magnet, "tr=") {
		t.Fatalf("magnet missing trackers: %q", magnet)
	}

	parsed := parseMagnet("magnet:?xt=urn:btih:MFRGGZDFMZTWQ2LKNNWG23TPOBYXE43U&dn=Example")
	if parsed == nil {
		t.Fatal("parseMagnet returned nil for a base32 infohash")
	}
	if len(parsed.InfoHash) != 40 || parsed.Name != "Example" {
		t.Fatalf("parsed magnet = %+v, want 40-char hash and name Example", parsed)
	}
}

func TestFetchWordpressRSSMapsMagnetItems(t *testing.T) {
	const hash = "abcdef0123456789abcdef0123456789abcdef01"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, "feed=rss2") {
			t.Errorf("query = %q, want rss feed query", r.URL.RawQuery)
		}
		w.Write([]byte(`<rss><channel><item>
			<title>FitGirl &amp; Friends</title>
			<pubDate>Mon, 01 Jul 2024 12:00:00 GMT</pubDate>
			<a href="magnet:?xt=urn:btih:` + hash + `&amp;dn=FitGirl">download</a>
		</item></channel></rss>`))
	}))
	t.Cleanup(srv.Close)

	got, err := fetchWordpressRSS(context.Background(), srv.URL, "FitGirl", "games", "bunny", nil)
	if err != nil {
		t.Fatalf("fetchWordpressRSS: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("results = %d, want 1", len(got))
	}
	if got[0].Title != "FitGirl & Friends" || got[0].Category != "games" || got[0].Source != "FitGirl" {
		t.Fatalf("result = %+v", got[0])
	}
	if got[0].Added == 0 {
		t.Fatalf("wordpress Added = 0, want the pubDate parsed")
	}
}

func TestYTSSearchMapsMovieTorrents(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/list_movies.json" {
			t.Fatalf("path = %q, want YTS list endpoint", r.URL.Path)
		}
		w.Write([]byte(`{"data":{"movies":[{"title_long":"Movie 2024","date_uploaded_unix":1710000000,"torrents":[{"hash":"ABCDEF0123456789ABCDEF0123456789ABCDEF01","quality":"1080p","type":"BluRay","size_bytes":1234,"seeds":9,"peers":2}]}]}}`))
	}))
	t.Cleanup(srv.Close)

	src := torlinkPointedAt(srv, NewYTS())
	got, err := src.Search(context.Background(), "movie")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("results = %d, want 1", len(got))
	}
	if got[0].Title != "Movie 2024 [1080p BluRay]" || got[0].Popularity != 9 || got[0].Category != "movies" {
		t.Fatalf("result = %+v", got[0])
	}
	if got[0].Seeders != 9 || got[0].Leechers != 2 || got[0].Added != 1710000000 {
		t.Fatalf("yts fields = %+v, want seeders 9 leechers 2 added 1710000000", got[0])
	}
}

func TestPirateBaySearchFiltersCategory(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[
			{"id":"1","name":"Movie","info_hash":"abcdef0123456789abcdef0123456789abcdef01","seeders":"10","leechers":"1","num_files":"2","size":"1000","added":"1710000000","category":"207"},
			{"id":"2","name":"TV","info_hash":"bbbbbb0123456789abcdef0123456789abcdef01","seeders":"20","leechers":"2","size":"2000","category":"208"}
		]`))
	}))
	t.Cleanup(srv.Close)

	src := torlinkPointedAt(srv, NewPirateBayMovies())
	got, err := src.Search(context.Background(), "movie")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	g := got[0]
	if len(got) != 1 || g.Title != "Movie" || g.Category != "movies" || g.Popularity != 10 {
		t.Fatalf("results = %+v, want only Movie", got)
	}
	if g.Seeders != 10 || g.Leechers != 1 || g.Files != 2 || g.Added != 1710000000 {
		t.Fatalf("piratebay fields = %+v, want seeders 10 leechers 1 files 2 added 1710000000", g)
	}
}

func TestEZTVSearchOnlyBrowsesLatest(t *testing.T) {
	src := NewEZTV()
	got, err := src.Search(context.Background(), "specific show")
	if err != nil {
		t.Fatalf("Search non-empty: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("non-empty query results = %d, want 0", len(got))
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"torrents":[{"title":"Show S01E01","hash":"abcdef0123456789abcdef0123456789abcdef01","seeds":7,"peers":3,"size_bytes":"4096","date_released_unix":1710000000}]}`))
	}))
	t.Cleanup(srv.Close)

	src = torlinkPointedAt(srv, src)
	got, err = src.Search(context.Background(), "")
	if err != nil {
		t.Fatalf("Search empty: %v", err)
	}
	if len(got) != 1 || got[0].Title != "Show S01E01" || got[0].Category != "tv" {
		t.Fatalf("results = %+v", got)
	}
	if got[0].Seeders != 7 || got[0].Leechers != 3 || got[0].Added != 1710000000 {
		t.Fatalf("eztv fields = %+v, want seeders 7 leechers 3 added 1710000000", got[0])
	}
}

func TestSolidTorrentsSearchMapsResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"success":true,"results":[{"infohash":"abcdef0123456789abcdef0123456789abcdef01","title":"Solid TV","size":2048,"seeders":5,"leechers":1,"updatedAt":"2024-07-01T12:00:00Z"}]}`))
	}))
	t.Cleanup(srv.Close)

	src := torlinkPointedAt(srv, NewSolidTorrents())
	got, err := src.Search(context.Background(), "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 1 || got[0].Title != "Solid TV" || got[0].Category != "tv" || got[0].Popularity != 5 {
		t.Fatalf("results = %+v", got)
	}
	if got[0].Seeders != 5 || got[0].Leechers != 1 || got[0].Added == 0 {
		t.Fatalf("solidtorrents fields = %+v, want seeders 5 leechers 1 added>0", got[0])
	}
}

func TestNyaaSearchParsesRSS(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<rss><channel><item>
			<title><![CDATA[Anime Episode]]></title>
			<nyaa:infoHash>ABCDEF0123456789ABCDEF0123456789ABCDEF01</nyaa:infoHash>
			<nyaa:seeders>11</nyaa:seeders>
			<nyaa:leechers>4</nyaa:leechers>
			<nyaa:size>1.5 GiB</nyaa:size>
			<pubDate>Mon, 01 Jul 2024 12:00:00 GMT</pubDate>
		</item></channel></rss>`))
	}))
	t.Cleanup(srv.Close)

	src := torlinkPointedAt(srv, NewNyaa())
	got, err := src.Search(context.Background(), "anime")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 1 || got[0].Title != "Anime Episode" || got[0].Category != "anime" || got[0].Popularity != 11 {
		t.Fatalf("results = %+v", got)
	}
	if got[0].Seeders != 11 || got[0].Leechers != 4 || got[0].Added == 0 {
		t.Fatalf("nyaa fields = %+v, want seeders 11 leechers 4 added>0", got[0])
	}
}

func TestSubsPleasePicksBestResolution(t *testing.T) {
	const hash = "abcdef0123456789abcdef0123456789abcdef01"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"one":{"show":"Show","episode":"03","release_date":"2024-07-01T12:00:00Z","downloads":[
			{"res":"480","magnet":"magnet:?xt=urn:btih:bbbbbb0123456789abcdef0123456789abcdef01&dn=Low"},
			{"res":"1080","magnet":"magnet:?xt=urn:btih:` + hash + `&dn=High&xl=999"}
		]}}`))
	}))
	t.Cleanup(srv.Close)

	src := torlinkPointedAt(srv, NewSubsPlease())
	got, err := src.Search(context.Background(), "show")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 1 || got[0].Title != "Show - 03 [1080p]" || got[0].SizeBytes != 999 || got[0].Category != "anime" {
		t.Fatalf("results = %+v", got)
	}
	if got[0].Added == 0 {
		t.Fatalf("subsplease Added = 0, want the release_date parsed")
	}
}

func Test1337xSearchFetchesDetailMagnets(t *testing.T) {
	const hash = "abcdef0123456789abcdef0123456789abcdef01"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/torrent/") {
			w.Write([]byte(`detail <a href="magnet:?xt=urn:btih:` + hash + `&dn=Movie">magnet</a>`))
			return
		}
		w.Write([]byte(`<html><body><table class="table-list"><tr>
			<td><a href="/torrent/1/movie/">Movie Result</a></td>
			<td class="coll-2 seeds">8</td>
			<td class="coll-3 leeches">2</td>
			<td class="coll-4 size">700 MiB</td>
		</tr></table></body></html>`))
	}))
	t.Cleanup(srv.Close)

	src := torlinkPointedAt(srv, New1337xMovies())
	got, err := src.Search(context.Background(), "movie")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 1 || got[0].Title != "Movie Result" || got[0].Category != "movies" || got[0].Popularity != 8 {
		t.Fatalf("results = %+v", got)
	}
}

func TestTorlinkSourcesRegistered(t *testing.T) {
	got := NewTorlinkSources()
	want := map[string]bool{
		"FitGirl": true, "YTS": true, "TPB Movies": true, "1337x Movies": true,
		"EZTV": true, "SolidTorrents": true, "TPB TV": true, "1337x TV": true,
		"Nyaa": true, "SubsPlease": true,
	}
	if len(got) != len(want) {
		t.Fatalf("registered sources = %d, want %d", len(got), len(want))
	}
	for _, s := range got {
		if !want[s.Name()] {
			t.Fatalf("unexpected source %q", s.Name())
		}
		delete(want, s.Name())
	}
	if len(want) != 0 {
		t.Fatalf("missing sources: %v", want)
	}
}
