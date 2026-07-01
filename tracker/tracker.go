// Package tracker talks to a BitTorrent HTTP tracker. A tracker is the
// "phonebook" of a swarm: you send it your infohash and it replies with a list
// of peers (IP:port) currently sharing that torrent.
//
// Only HTTP/HTTPS trackers are implemented here. Many torrents also list UDP
// trackers (udp://...); those use a different binary protocol and are noted as
// a TODO in the README.
package tracker

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/StrangeNoob/shoal/bencode"
	"github.com/StrangeNoob/shoal/metainfo"
)

// Peer is a single address returned by the tracker.
type Peer struct {
	IP   net.IP
	Port uint16
}

func (p Peer) String() string {
	return net.JoinHostPort(p.IP.String(), strconv.Itoa(int(p.Port)))
}

// Response is the useful part of a tracker's reply.
type Response struct {
	Interval int // seconds the tracker asks us to wait between announces
	Peers    []Peer
}

// Announce contacts the torrent's trackers (announce-list first, then the
// single announce URL) and returns the first successful reply.
func Announce(m *metainfo.MetaInfo, peerID [20]byte, port uint16) (*Response, error) {
	urls := httpTrackers(m)
	if len(urls) == 0 {
		return nil, errors.New("tracker: no http(s) tracker found in metainfo")
	}
	var lastErr error
	for _, tr := range urls {
		resp, err := announceOne(tr, m, peerID, port)
		if err == nil {
			return resp, nil
		}
		lastErr = fmt.Errorf("%s: %w", tr, err)
	}
	return nil, lastErr
}

func httpTrackers(m *metainfo.MetaInfo) []string {
	seen := map[string]bool{}
	var out []string
	add := func(u string) {
		if u == "" || seen[u] {
			return
		}
		if pu, err := url.Parse(u); err == nil && (pu.Scheme == "http" || pu.Scheme == "https") {
			seen[u] = true
			out = append(out, u)
		}
	}
	for _, tier := range m.AnnounceList {
		for _, u := range tier {
			add(u)
		}
	}
	add(m.Announce)
	return out
}

func announceOne(tr string, m *metainfo.MetaInfo, peerID [20]byte, port uint16) (*Response, error) {
	base, err := url.Parse(tr)
	if err != nil {
		return nil, err
	}
	// info_hash and peer_id are raw 20-byte values; url.Values.Encode handles
	// the percent-escaping of the non-printable bytes for us.
	q := url.Values{}
	q.Set("info_hash", string(m.InfoHash[:]))
	q.Set("peer_id", string(peerID[:]))
	q.Set("port", strconv.Itoa(int(port)))
	q.Set("uploaded", "0")
	q.Set("downloaded", "0")
	q.Set("left", strconv.FormatInt(m.TotalLength(), 10))
	q.Set("compact", "1")
	base.RawQuery = q.Encode()

	client := &http.Client{Timeout: 15 * time.Second}
	res, err := client.Get(base.String())
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	if err != nil {
		return nil, err
	}

	decoded, err := bencode.Decode(body)
	if err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	d, ok := decoded.(map[string]any)
	if !ok {
		return nil, errors.New("response is not a dictionary")
	}
	if fail, ok := d["failure reason"].(string); ok {
		return nil, fmt.Errorf("tracker error: %s", fail)
	}

	resp := &Response{}
	if iv, ok := d["interval"].(int64); ok {
		resp.Interval = int(iv)
	}

	switch peers := d["peers"].(type) {
	case string:
		// Compact form (BEP 23): 6 bytes per peer, 4 for IPv4 + 2 for port.
		resp.Peers, err = parseCompactPeers([]byte(peers))
		if err != nil {
			return nil, err
		}
	case []any:
		// Dictionary form: a list of {ip, port} dicts.
		for _, pe := range peers {
			pm, ok := pe.(map[string]any)
			if !ok {
				continue
			}
			ipStr, _ := pm["ip"].(string)
			portN, _ := pm["port"].(int64)
			ip := net.ParseIP(ipStr)
			if ip == nil || portN <= 0 {
				continue
			}
			resp.Peers = append(resp.Peers, Peer{IP: ip, Port: uint16(portN)})
		}
	}
	return resp, nil
}

func parseCompactPeers(b []byte) ([]Peer, error) {
	const peerSize = 6
	if len(b)%peerSize != 0 {
		return nil, errors.New("tracker: malformed compact peer list")
	}
	peers := make([]Peer, 0, len(b)/peerSize)
	for i := 0; i < len(b); i += peerSize {
		ip := net.IPv4(b[i], b[i+1], b[i+2], b[i+3])
		port := binary.BigEndian.Uint16(b[i+4 : i+6])
		peers = append(peers, Peer{IP: ip, Port: port})
	}
	return peers, nil
}
