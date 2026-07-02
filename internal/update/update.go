// Package update self-updates the shoal binary from GitHub Releases.
package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/minio/selfupdate"
)

const (
	repoOwner = "StrangeNoob"
	repoName  = "shoal"
	binName   = "shoal"
)

// apiBase is the GitHub API root; overridable in tests.
var apiBase = "https://api.github.com"

// runtimeGOOS/GOARCH are indirections so tests can match the fake asset name.
var (
	runtimeGOOS   = runtime.GOOS
	runtimeGOARCH = runtime.GOARCH
)

type Asset struct{ Name, URL string }

type Release struct {
	Version string // tag without a leading "v", e.g. "0.3.0"
	Assets  []Asset
}

func httpClient() *http.Client { return &http.Client{Timeout: 30 * time.Second} }

func authHeader(req *http.Request) {
	tok := os.Getenv("GITHUB_TOKEN")
	if tok == "" {
		tok = os.Getenv("GH_TOKEN")
	}
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
}

// DisplayVersion formats a build version for humans: "dev" or "v1.2.3".
func DisplayVersion(v string) string {
	if v == "" || v == "dev" {
		return "dev"
	}
	return "v" + strings.TrimPrefix(v, "v")
}

// CheckLatest fetches the latest published release.
func CheckLatest(ctx context.Context) (Release, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases/latest", apiBase, repoOwner, repoName)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	authHeader(req)
	resp, err := httpClient().Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusNotFound) &&
			os.Getenv("GITHUB_TOKEN") == "" && os.Getenv("GH_TOKEN") == "" {
			return Release{}, fmt.Errorf("github releases API: %s (set GITHUB_TOKEN if the repo is private)", resp.Status)
		}
		return Release{}, fmt.Errorf("github releases API: %s", resp.Status)
	}
	var p struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		return Release{}, err
	}
	rel := Release{Version: strings.TrimPrefix(p.TagName, "v")}
	for _, a := range p.Assets {
		rel.Assets = append(rel.Assets, Asset{Name: a.Name, URL: a.URL})
	}
	return rel, nil
}

func parseVer(v string) []int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if v == "" || v == "dev" {
		return nil
	}
	v = strings.SplitN(v, "-", 2)[0] // drop any prerelease suffix
	fields := strings.Split(v, ".")
	out := make([]int, 3)
	for i := 0; i < 3; i++ {
		if i < len(fields) {
			n, err := strconv.Atoi(fields[i])
			if err != nil {
				return nil
			}
			out[i] = n
		}
	}
	return out
}

func isNewer(current, latest string) bool {
	c, l := parseVer(current), parseVer(latest)
	if c == nil {
		return true
	}
	if l == nil {
		return false
	}
	for i := 0; i < 3; i++ {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

// Newer reports whether latest is a newer version than current (exported for
// the UI's launch check).
func Newer(current, latest string) bool { return isNewer(current, latest) }

func matchAsset(names []string, goos, goarch string) (string, bool) {
	suffix := "_" + goos + "_" + goarch + "."
	for _, n := range names {
		if strings.Contains(n, suffix) && (strings.HasSuffix(n, ".tar.gz") || strings.HasSuffix(n, ".zip")) {
			return n, true
		}
	}
	return "", false
}

func checksumFor(checksums, filename string) (string, error) {
	for _, line := range strings.Split(checksums, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[1] == filename {
			return f[0], nil
		}
	}
	return "", fmt.Errorf("no checksum for %q", filename)
}

func extractBinary(archive io.Reader, zipped bool, name string) ([]byte, error) {
	want := map[string]bool{name: true, name + ".exe": true}
	if zipped {
		data, err := io.ReadAll(archive)
		if err != nil {
			return nil, err
		}
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if want[f.Name] {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(rc)
			}
		}
		return nil, fmt.Errorf("%s not found in zip", name)
	}
	gz, err := gzip.NewReader(archive)
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("%s not found in archive", name)
		}
		if err != nil {
			return nil, err
		}
		if want[h.Name] {
			return io.ReadAll(tr)
		}
	}
}

func download(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	authHeader(req)
	resp, err := httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// Apply updates the running binary to the latest release. applyFn performs the
// swap (defaults to selfupdate.Apply; tests inject a stub). Returns upToDate
// when already current.
func Apply(ctx context.Context, current string, applyFn func(io.Reader) error) (updatedTo string, upToDate bool, err error) {
	if applyFn == nil {
		applyFn = func(r io.Reader) error { return selfupdate.Apply(r, selfupdate.Options{}) }
	}
	rel, err := CheckLatest(ctx)
	if err != nil {
		return "", false, err
	}
	if !isNewer(current, rel.Version) {
		return rel.Version, true, nil
	}
	names := make([]string, len(rel.Assets))
	byName := make(map[string]string, len(rel.Assets))
	for i, a := range rel.Assets {
		names[i] = a.Name
		byName[a.Name] = a.URL
	}
	assetName, ok := matchAsset(names, runtimeGOOS, runtimeGOARCH)
	if !ok {
		return "", false, fmt.Errorf("no release asset for %s/%s", runtimeGOOS, runtimeGOARCH)
	}
	sumsURL, ok := byName["checksums.txt"]
	if !ok {
		return "", false, fmt.Errorf("release has no checksums.txt")
	}
	archive, err := download(ctx, byName[assetName])
	if err != nil {
		return "", false, err
	}
	sums, err := download(ctx, sumsURL)
	if err != nil {
		return "", false, err
	}
	want, err := checksumFor(string(sums), assetName)
	if err != nil {
		return "", false, err
	}
	if got := sha256.Sum256(archive); hex.EncodeToString(got[:]) != want {
		return "", false, fmt.Errorf("checksum mismatch for %s", assetName)
	}
	bin, err := extractBinary(bytes.NewReader(archive), strings.HasSuffix(assetName, ".zip"), binName)
	if err != nil {
		return "", false, err
	}
	if err := applyFn(bytes.NewReader(bin)); err != nil {
		return "", false, err
	}
	return rel.Version, false, nil
}
