package tracker

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/StrangeNoob/shoal/bencode"
	"github.com/StrangeNoob/shoal/metainfo"
)

func bencodeBody(t *testing.T, v any) []byte {
	t.Helper()
	b, err := bencode.Encode(v)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return b
}

// serve returns an httptest server that always replies with body.
func serve(t *testing.T, body []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestPeerString(t *testing.T) {
	p := Peer{IP: net.IPv4(1, 2, 3, 4), Port: 6881}
	if p.String() != "1.2.3.4:6881" {
		t.Errorf("String = %q, want 1.2.3.4:6881", p.String())
	}
}

func TestAnnounceCompactPeers(t *testing.T) {
	var gotCompact string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCompact = r.URL.Query().Get("compact")
		w.Write(bencodeBody(t, map[string]any{
			"interval": int64(900),
			"peers":    string([]byte{1, 2, 3, 4, 0x1A, 0xE1}),
		}))
	}))
	t.Cleanup(srv.Close)

	m := &metainfo.MetaInfo{Announce: srv.URL}
	m.Info.Length = 1000
	var pid [20]byte
	resp, err := Announce(m, pid, 6881)
	if err != nil {
		t.Fatalf("Announce: %v", err)
	}
	if gotCompact != "1" {
		t.Errorf("compact query = %q, want 1", gotCompact)
	}
	if resp.Interval != 900 {
		t.Errorf("Interval = %d, want 900", resp.Interval)
	}
	if len(resp.Peers) != 1 || !resp.Peers[0].IP.Equal(net.IPv4(1, 2, 3, 4)) || resp.Peers[0].Port != 6881 {
		t.Fatalf("Peers = %+v, want one 1.2.3.4:6881", resp.Peers)
	}
}

func TestAnnounceDictPeers(t *testing.T) {
	srv := serve(t, bencodeBody(t, map[string]any{
		"peers": []any{map[string]any{"ip": "5.6.7.8", "port": int64(9000)}},
	}))
	m := &metainfo.MetaInfo{Announce: srv.URL}
	var pid [20]byte
	resp, err := Announce(m, pid, 6881)
	if err != nil {
		t.Fatalf("Announce: %v", err)
	}
	if len(resp.Peers) != 1 || !resp.Peers[0].IP.Equal(net.ParseIP("5.6.7.8")) || resp.Peers[0].Port != 9000 {
		t.Fatalf("Peers = %+v, want one 5.6.7.8:9000", resp.Peers)
	}
}

func TestAnnounceFailureReason(t *testing.T) {
	srv := serve(t, bencodeBody(t, map[string]any{"failure reason": "banned"}))
	m := &metainfo.MetaInfo{Announce: srv.URL}
	var pid [20]byte
	_, err := Announce(m, pid, 6881)
	if err == nil || !strings.Contains(err.Error(), "banned") {
		t.Fatalf("Announce err = %v, want one mentioning 'banned'", err)
	}
}

func TestAnnounceFallsBackToNextTracker(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(bad.Close)
	good := serve(t, bencodeBody(t, map[string]any{"peers": string([]byte{9, 9, 9, 9, 0x1A, 0xE1})}))

	m := &metainfo.MetaInfo{AnnounceList: [][]string{{bad.URL, good.URL}}}
	var pid [20]byte
	resp, err := Announce(m, pid, 6881)
	if err != nil {
		t.Fatalf("Announce: %v", err)
	}
	if len(resp.Peers) != 1 || !resp.Peers[0].IP.Equal(net.IPv4(9, 9, 9, 9)) {
		t.Fatalf("Peers = %+v, want one 9.9.9.9", resp.Peers)
	}
}

func TestAnnounceNoHTTPTracker(t *testing.T) {
	m := &metainfo.MetaInfo{Announce: "udp://tracker:80", AnnounceList: [][]string{{"udp://x:90"}}}
	var pid [20]byte
	if _, err := Announce(m, pid, 6881); err == nil {
		t.Fatal("Announce expected error when no http(s) tracker present")
	}
}

func TestAnnounceBadResponse(t *testing.T) {
	srv := serve(t, []byte("not bencode"))
	m := &metainfo.MetaInfo{Announce: srv.URL}
	var pid [20]byte
	if _, err := Announce(m, pid, 6881); err == nil {
		t.Fatal("Announce expected decode error")
	}
}

func TestAnnounceResponseNotDict(t *testing.T) {
	srv := serve(t, []byte("i1e"))
	m := &metainfo.MetaInfo{Announce: srv.URL}
	var pid [20]byte
	if _, err := Announce(m, pid, 6881); err == nil {
		t.Fatal("Announce expected 'not a dictionary' error")
	}
}

func TestHTTPTrackersDedupAndScheme(t *testing.T) {
	m := &metainfo.MetaInfo{
		Announce: "http://primary/a",
		AnnounceList: [][]string{
			{"udp://x/a", "http://one/a"},
			{"http://one/a", "https://two/a"},
		},
	}
	got := httpTrackers(m)
	want := []string{"http://one/a", "https://two/a", "http://primary/a"}
	if len(got) != len(want) {
		t.Fatalf("httpTrackers = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("httpTrackers[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
