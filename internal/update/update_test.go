package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIsNewer(t *testing.T) {
	cases := []struct {
		cur, latest string
		want        bool
	}{
		{"0.2.0", "0.3.0", true},
		{"0.2.0", "0.2.1", true},
		{"1.0.0", "0.9.9", false},
		{"0.2.0", "0.2.0", false},
		{"v0.2.0", "0.3.0", true},
		{"dev", "0.1.0", true},
		{"", "0.1.0", true},
	}
	for _, c := range cases {
		if got := isNewer(c.cur, c.latest); got != c.want {
			t.Errorf("isNewer(%q,%q)=%v want %v", c.cur, c.latest, got, c.want)
		}
	}
}

func TestMatchAsset(t *testing.T) {
	names := []string{"checksums.txt", "shoal_0.3.0_linux_amd64.tar.gz", "shoal_0.3.0_darwin_arm64.tar.gz", "shoal_0.3.0_windows_amd64.zip"}
	if got, ok := matchAsset(names, "darwin", "arm64"); !ok || got != "shoal_0.3.0_darwin_arm64.tar.gz" {
		t.Fatalf("darwin/arm64 = %q,%v", got, ok)
	}
	if got, ok := matchAsset(names, "windows", "amd64"); !ok || got != "shoal_0.3.0_windows_amd64.zip" {
		t.Fatalf("windows/amd64 = %q,%v", got, ok)
	}
	if _, ok := matchAsset(names, "plan9", "mips"); ok {
		t.Fatal("plan9/mips should not match")
	}
}

func TestChecksumFor(t *testing.T) {
	body := "aaa  shoal_0.3.0_linux_amd64.tar.gz\nbbb  shoal_0.3.0_darwin_arm64.tar.gz\n"
	if got, err := checksumFor(body, "shoal_0.3.0_darwin_arm64.tar.gz"); err != nil || got != "bbb" {
		t.Fatalf("checksumFor = %q,%v", got, err)
	}
	if _, err := checksumFor(body, "nope"); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func tarGz(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(data))}); err != nil {
		t.Fatal(err)
	}
	tw.Write(data)
	tw.Close()
	gw.Close()
	return buf.Bytes()
}

func zipArc(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create(name)
	w.Write(data)
	zw.Close()
	return buf.Bytes()
}

func TestExtractBinary(t *testing.T) {
	want := []byte("BINARY-BYTES")
	got, err := extractBinary(bytes.NewReader(tarGz(t, "shoal", want)), false, "shoal")
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("tar.gz extract = %q,%v", got, err)
	}
	got, err = extractBinary(bytes.NewReader(zipArc(t, "shoal.exe", want)), true, "shoal")
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("zip extract = %q,%v", got, err)
	}
}

func TestApply(t *testing.T) {
	bin := []byte("NEW-SHOAL-BINARY")
	archive := tarGz(t, "shoal", bin)
	sum := sha256.Sum256(archive)
	assetName := "shoal_0.3.0_" + goosArch() + ".tar.gz"

	mux := http.NewServeMux()
	var base string
	mux.HandleFunc("/repos/StrangeNoob/shoal/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"tag_name":"v0.3.0","assets":[
			{"name":%q,"browser_download_url":%q},
			{"name":"checksums.txt","browser_download_url":%q}]}`,
			assetName, base+"/dl/"+assetName, base+"/dl/checksums.txt")
	})
	mux.HandleFunc("/dl/"+assetName, func(w http.ResponseWriter, r *http.Request) { w.Write(archive) })
	mux.HandleFunc("/dl/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), assetName)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	base = srv.URL

	oldBase := apiBase
	apiBase = srv.URL
	t.Cleanup(func() { apiBase = oldBase })

	var applied []byte
	to, up, err := Apply(context.Background(), "0.2.0", func(r io.Reader) error {
		applied, _ = io.ReadAll(r)
		return nil
	})
	if err != nil || up || to != "0.3.0" {
		t.Fatalf("Apply = %q,%v,%v", to, up, err)
	}
	if !bytes.Equal(applied, bin) {
		t.Fatalf("applied bytes = %q, want the extracted binary", applied)
	}

	// already up to date
	_, up, err = Apply(context.Background(), "9.9.9", func(io.Reader) error { return nil })
	if err != nil || !up {
		t.Fatalf("up-to-date Apply = up:%v err:%v", up, err)
	}
}

// goosArch mirrors the runtime target so the fake asset name matches matchAsset.
func goosArch() string { return runtimeGOOS + "_" + runtimeGOARCH }

func TestApplyChecksumMismatch(t *testing.T) {
	bin := []byte("NEW-SHOAL-BINARY")
	archive := tarGz(t, "shoal", bin)
	assetName := "shoal_0.3.0_" + goosArch() + ".tar.gz"

	mux := http.NewServeMux()
	var base string
	mux.HandleFunc("/repos/StrangeNoob/shoal/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"tag_name":"v0.3.0","assets":[
			{"name":%q,"browser_download_url":%q},
			{"name":"checksums.txt","browser_download_url":%q}]}`,
			assetName, base+"/dl/"+assetName, base+"/dl/checksums.txt")
	})
	mux.HandleFunc("/dl/"+assetName, func(w http.ResponseWriter, r *http.Request) { w.Write(archive) })
	mux.HandleFunc("/dl/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		// Deliberately wrong checksum (sha256 of the empty string) so it never matches the archive.
		fmt.Fprintf(w, "%s  %s\n", strings.Repeat("0", 64), assetName)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	base = srv.URL

	oldBase := apiBase
	apiBase = srv.URL
	t.Cleanup(func() { apiBase = oldBase })

	var applyCalled bool
	_, _, err := Apply(context.Background(), "0.2.0", func(io.Reader) error {
		applyCalled = true
		return nil
	})
	if err == nil {
		t.Fatal("expected checksum mismatch error, got nil")
	}
	if applyCalled {
		t.Fatal("applyFn must not be called on checksum mismatch")
	}
}
