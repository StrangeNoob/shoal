package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/StrangeNoob/shoal/internal/engine"
)

const streamIH = "0123456789abcdef0123456789abcdef01234567"

// readyFile is a selected file that's already fully playable (HeadBytes >=
// Length and TailDone), so tests that only care about target-picking or
// path-joining don't need to exercise the poll loop.
func readyFile(path string, length int64) engine.FileDetail {
	return engine.FileDetail{Path: path, Length: length, HeadBytes: length, TailDone: true, Selected: true}
}

func TestStreamNoArgs(t *testing.T) {
	var buf bytes.Buffer
	if code := runStream(nil, &buf); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}

func TestStreamPicksLargestVideoFileOverBiggerNonVideo(t *testing.T) {
	fake := &fakeEngine{
		statuses: []engine.Status{{InfoHash: streamIH, Path: "/data/Show"}},
		detail: engine.Detail{Files: []engine.FileDetail{
			readyFile("sample.mkv", 50),
			readyFile("movie.mp4", 200),
			readyFile("readme.txt", 1000), // bigger, but not a video extension
		}},
	}
	serveFakeDaemon(t, fake)

	var buf bytes.Buffer
	if code := runStream([]string{streamIH}, &buf); code != 0 {
		t.Fatalf("exit = %d: %s", code, buf.String())
	}
	want := "/data/Show/movie.mp4\n"
	if buf.String() != want {
		t.Fatalf("stdout = %q, want %q", buf.String(), want)
	}
}

func TestStreamNoVideoExtensionPicksLargestFile(t *testing.T) {
	fake := &fakeEngine{
		statuses: []engine.Status{{InfoHash: streamIH, Path: "/data/Archive"}},
		detail: engine.Detail{Files: []engine.FileDetail{
			readyFile("small.bin", 10),
			readyFile("big.bin", 500),
		}},
	}
	serveFakeDaemon(t, fake)

	var buf bytes.Buffer
	if code := runStream([]string{streamIH}, &buf); code != 0 {
		t.Fatalf("exit = %d: %s", code, buf.String())
	}
	want := "/data/Archive/big.bin\n"
	if buf.String() != want {
		t.Fatalf("stdout = %q, want %q", buf.String(), want)
	}
}

func TestStreamFilesGlobOverridesPick(t *testing.T) {
	fake := &fakeEngine{
		statuses: []engine.Status{{InfoHash: streamIH, Path: "/data/Show"}},
		detail: engine.Detail{Files: []engine.FileDetail{
			readyFile("movie.mp4", 200),
			readyFile("extras/behind-the-scenes.mkv", 20),
		}},
	}
	serveFakeDaemon(t, fake)

	var buf bytes.Buffer
	if code := runStream([]string{streamIH, "--files", "*behind*"}, &buf); code != 0 {
		t.Fatalf("exit = %d: %s", code, buf.String())
	}
	want := "/data/Show/extras/behind-the-scenes.mkv\n"
	if buf.String() != want {
		t.Fatalf("stdout = %q, want %q", buf.String(), want)
	}
}

func TestStreamFilesGlobNoMatch(t *testing.T) {
	fake := &fakeEngine{
		statuses: []engine.Status{{InfoHash: streamIH, Path: "/data/Show"}},
		detail: engine.Detail{Files: []engine.FileDetail{
			readyFile("movie.mp4", 200),
		}},
	}
	serveFakeDaemon(t, fake)

	var buf bytes.Buffer
	if code := runStream([]string{streamIH, "--files", "*.nope"}, &buf); code == 0 {
		t.Fatalf("no glob match should exit non-zero, got 0")
	}
	if buf.String() != "" {
		t.Fatalf("stdout must stay empty on error, got %q", buf.String())
	}
}

func TestStreamDeselectedFileErrors(t *testing.T) {
	fake := &fakeEngine{
		statuses: []engine.Status{{InfoHash: streamIH, Path: "/data/Show"}},
		detail: engine.Detail{Files: []engine.FileDetail{
			{Path: "movie.mp4", Length: 200, Selected: false},
		}},
	}
	serveFakeDaemon(t, fake)

	var buf bytes.Buffer
	if code := runStream([]string{streamIH}, &buf); code == 0 {
		t.Fatalf("deselected target should exit non-zero, got 0")
	}
	if buf.String() != "" {
		t.Fatalf("stdout must stay empty on error, got %q", buf.String())
	}
}

func TestStreamWaitsForPlayability(t *testing.T) {
	old := waitPollInterval
	waitPollInterval = time.Millisecond
	t.Cleanup(func() { waitPollInterval = old })

	fake := &fakeEngine{
		statuses: []engine.Status{{InfoHash: streamIH, Path: "/data/Show"}},
		detailSeq: []engine.Detail{
			{Files: []engine.FileDetail{{Path: "movie.mp4", Length: 100, Selected: true, HeadBytes: 40, TailDone: false}}},
			{Files: []engine.FileDetail{{Path: "movie.mp4", Length: 100, Selected: true, HeadBytes: 100, TailDone: false}}},
			{Files: []engine.FileDetail{{Path: "movie.mp4", Length: 100, Selected: true, HeadBytes: 100, TailDone: true}}},
		},
	}
	serveFakeDaemon(t, fake)

	var buf bytes.Buffer
	done := make(chan int, 1)
	go func() { done <- runStream([]string{streamIH}, &buf) }()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit = %d: %s", code, buf.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stream hung instead of completing once playable")
	}
	want := "/data/Show/movie.mp4\n"
	if buf.String() != want {
		t.Fatalf("stdout = %q, want %q", buf.String(), want)
	}
}

func TestStreamMagnetAddsAndEnablesSequential(t *testing.T) {
	fake := &fakeEngine{
		statuses: []engine.Status{{InfoHash: streamIH, Path: "/data/Show"}},
		detail:   engine.Detail{Files: []engine.FileDetail{readyFile("movie.mp4", 10)}},
	}
	serveFakeDaemon(t, fake)

	var buf bytes.Buffer
	magnet := "magnet:?xt=urn:btih:" + streamIH
	if code := runStream([]string{magnet}, &buf); code != 0 {
		t.Fatalf("exit = %d: %s", code, buf.String())
	}
	if m := fake.gotMagnets(); len(m) != 1 || m[0] != magnet {
		t.Fatalf("daemon did not receive the magnet: %v", m)
	}
	seq := fake.gotSequential()
	if len(seq) != 1 || !strings.EqualFold(seq[0].infoHash, streamIH) || !seq[0].on {
		t.Fatalf("sequential not enabled for the added magnet: %+v", seq)
	}
}

func TestStreamByIDPrefix(t *testing.T) {
	fake := &fakeEngine{
		statuses: []engine.Status{{InfoHash: streamIH, Path: "/data/Show"}},
		detail:   engine.Detail{Files: []engine.FileDetail{readyFile("movie.mp4", 10)}},
	}
	serveFakeDaemon(t, fake)

	var buf bytes.Buffer
	if code := runStream([]string{streamIH[:8]}, &buf); code != 0 {
		t.Fatalf("exit = %d: %s", code, buf.String())
	}
	seq := fake.gotSequential()
	if len(seq) != 1 || !strings.EqualFold(seq[0].infoHash, streamIH) || !seq[0].on {
		t.Fatalf("sequential not enabled via id prefix: %+v", seq)
	}
}

func TestStreamAbsolutePathSingleFileTorrent(t *testing.T) {
	// Ground truth (anacrolix/torrent storage/file-client.go): a single-file
	// torrent's on-disk path has no subdirectory, so FileDetail.Path duplicates
	// the trailing component of Status.Path — joining them again must not
	// double it up.
	fake := &fakeEngine{
		statuses: []engine.Status{{InfoHash: streamIH, Path: "/data/Movie.2024.mkv"}},
		detail:   engine.Detail{Files: []engine.FileDetail{readyFile("Movie.2024.mkv", 10)}},
	}
	serveFakeDaemon(t, fake)

	var buf bytes.Buffer
	if code := runStream([]string{streamIH}, &buf); code != 0 {
		t.Fatalf("exit = %d: %s", code, buf.String())
	}
	want := "/data/Movie.2024.mkv\n"
	if buf.String() != want {
		t.Fatalf("stdout = %q, want %q (single-file torrent must not double-join)", buf.String(), want)
	}
}

func TestStreamAbsolutePathMultiFileTorrent(t *testing.T) {
	fake := &fakeEngine{
		statuses: []engine.Status{{InfoHash: streamIH, Path: "/data/MyShow"}},
		detail:   engine.Detail{Files: []engine.FileDetail{readyFile("Season1/ep01.mkv", 10)}},
	}
	serveFakeDaemon(t, fake)

	var buf bytes.Buffer
	if code := runStream([]string{streamIH, "--files", "*ep01*"}, &buf); code != 0 {
		t.Fatalf("exit = %d: %s", code, buf.String())
	}
	want := "/data/MyShow/Season1/ep01.mkv\n"
	if buf.String() != want {
		t.Fatalf("stdout = %q, want %q", buf.String(), want)
	}
}
