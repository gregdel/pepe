package app

import (
	"image"
	"strings"
	"testing"

	"github.com/odwrtw/polochon/lib/papi"
)

func TestHeadlessPlaybackClient(t *testing.T) {
	client, err := NewHeadlessPlaybackClient(nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := client.getSnapshot()
	if snapshot.movieCount != len(mockMovies()) || snapshot.showCount != len(mockShows()) {
		t.Fatalf("fixture counts = (%d, %d)", snapshot.movieCount, snapshot.showCount)
	}
	for _, item := range []struct{ id, title string }{
		{"demo-one", "First Movie (2025)"},
		{"demo-two", "Second Movie (2026)"},
	} {
		movie, ok := client.getMovie(item.id)
		if !ok {
			t.Fatalf("movie %q missing", item.id)
		}
		if _, err := client.getDownloadURL(movie); err != nil {
			t.Fatalf("movie %q download URL: %v", item.id, err)
		}
		node := snapshot.movieRadix.Lookup(item.title)
		if node == nil || node.Media == nil || node.Media.ID != item.id {
			t.Fatalf("movie %q terminal = %+v", item.id, node)
		}
	}
	show, ok := client.getShow("mock-neon")
	if !ok {
		t.Fatal("show missing")
	}
	if _, err := client.getDownloadURL(show.Seasons[1].Episodes[1]); err != nil {
		t.Fatalf("episode download URL: %v", err)
	}
}

// Generated artwork goes through the real download and decode path, and each
// failure scenario fails the way it says.
func TestHeadlessPlaybackArtwork(t *testing.T) {
	client, err := NewHeadlessPlaybackClient(nil)
	if err != nil {
		t.Fatal(err)
	}
	fanart := func(m *papi.Movie) *papi.File { return m.FanartFile }
	for _, tc := range []struct {
		id   string
		size image.Point
		want string
	}{
		{id: "demo-one", size: mockFanartSize},
		{id: "mock-portrait", size: image.Pt(600, 1200)},
		{id: "mock-tiny", size: image.Pt(1, 1)},
		{id: "mock-missing", want: "image file not set"},
		{id: "mock-corrupt", want: "artwork decode failed"},
		{id: "mock-flaky", want: "503"},
		{id: "mock-flaky", want: "503"},
		{id: "mock-flaky", size: mockFanartSize},
	} {
		img, err := client.fetchMovieImage(tc.id, fanart)
		if tc.want != "" {
			if err == nil || !strings.Contains(artworkLogReason(err), tc.want) {
				t.Fatalf("%s fanart error = %v, want %q", tc.id, err, tc.want)
			}
			continue
		}
		if err != nil || img.Bounds().Size() != tc.size {
			t.Fatalf("%s fanart = %v, %v; want %v", tc.id, img, err, tc.size)
		}
	}
	if img, err := client.fetchShowImage("mock-neon", func(s *papi.Show) *papi.File { return s.PosterFile }); err != nil || img.Bounds().Size() != mockPosterSize {
		t.Fatalf("show poster = %v, %v", img, err)
	}
}
