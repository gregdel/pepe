package app

import (
	"encoding/json"
	"image/jpeg"
	"log/slog"
	"net/http"
	"testing"
	"time"
)

func TestWebCatalogListsMoviesThenShows(t *testing.T) {
	f := newHeadlessFixture(t)
	web := &webUI{commands: make(chan webCommand, webQueueSize), client: f.app.client}
	response := webRequest(web.handler(), "GET", "/api/catalog", "")
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	var catalog struct {
		Version uint64     `json:"version"`
		Titles  []webTitle `json:"titles"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &catalog); err != nil {
		t.Fatal(err)
	}
	want := []webTitle{
		{Kind: mediaKindMovie, ID: "movie-old", Title: "Old Movie", Year: 2000, Added: time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC).Unix()},
		{Kind: mediaKindMovie, ID: "movie-new", Title: "New Movie", Year: 2026, Added: time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC).Unix()},
		{Kind: mediaKindShow, ID: "show-one", Title: "Fixture Show", Year: 2024},
	}
	if len(catalog.Titles) != len(want) {
		t.Fatalf("titles = %+v", catalog.Titles)
	}
	for i := range want {
		if catalog.Titles[i] != want[i] {
			t.Errorf("title %d = %+v, want %+v", i, catalog.Titles[i], want[i])
		}
	}

	// A real client's version never restarts at a small number, so a
	// restarted pépé cannot reuse artwork URLs the phone cached as immutable,
	// and stays below 2^53, where JavaScript numbers lose consecutive values.
	if version, _ := NewEmptyAPIClient(nil).webCatalog(); version < 1<<32 || version >= 1<<53 {
		t.Fatalf("new client catalog version = %d, want a clock-seeded value below 2^53", version)
	}
	// Each published library is a new version, which the status reports.
	client := f.app.client
	client.publishLibrary(client.movies, client.shows, client.movieItems, client.showItems, client.snapshot)
	var status webStatus
	if err := json.Unmarshal(webRequest(web.handler(), "GET", "/api/status", "").Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.CatalogVersion != catalog.Version+1 {
		t.Fatalf("status catalog version = %d, want %d", status.CatalogVersion, catalog.Version+1)
	}

	empty := webRequest((&webUI{commands: make(chan webCommand, 1)}).handler(), "GET", "/api/catalog", "")
	if body := empty.Body.String(); body != `{"version":0,"titles":[]}`+"\n" {
		t.Fatalf("catalog without client = %s", body)
	}
}

func TestWebArtworkServesCacheablePosters(t *testing.T) {
	client, err := NewHeadlessPlaybackClient(slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	handler := (&webUI{commands: make(chan webCommand, 1), client: client}).handler()
	for _, path := range []string{"/api/artwork/movie/demo-one?v=1", "/api/artwork/show/mock-neon?v=1"} {
		response := webRequest(handler, "GET", path, "")
		if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "image/jpeg" {
			t.Fatalf("%s = %d %s", path, response.Code, response.Header().Get("Content-Type"))
		}
		if cache := response.Header().Get("Cache-Control"); cache != "private, max-age=31536000, immutable" {
			t.Fatalf("%s Cache-Control = %q", path, cache)
		}
		img, err := jpeg.Decode(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if size := img.Bounds().Size(); size.X > webPosterWidth || size.Y > webPosterHeight || size.X == 0 {
			t.Fatalf("%s poster size = %v", path, size)
		}
	}
	for _, tc := range []struct {
		path   string
		status int
	}{
		{"/api/artwork/movie/unknown", http.StatusNotFound},
		{"/api/artwork/show/demo-one", http.StatusNotFound}, // a movie ID is not a show
		{"/api/artwork/episode/demo-one", http.StatusNotFound},
	} {
		if response := webRequest(handler, "GET", tc.path, ""); response.Code != tc.status || response.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s = %d %q, want %d no-store", tc.path, response.Code, response.Header().Get("Cache-Control"), tc.status)
		}
	}
}

func TestHeadlessWebOpenTitle(t *testing.T) {
	f := newHeadlessFixture(t)
	if err := f.app.startWebUI("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.app.Close)
	handler := f.app.webUI.handler()
	open := func(body string, status int) {
		t.Helper()
		if response := webRequest(handler, "POST", "/api/open", body); response.Code != status {
			t.Fatalf("open %s = %d %s, want %d", body, response.Code, response.Body.String(), status)
		}
		if err := f.driver.Frame(); err != nil {
			t.Fatal(err)
		}
	}

	f.app.subPopup = &SubtitlePopup{}
	f.app.openLogs()
	open(`{"kind":"movie","id":"movie-new"}`, http.StatusAccepted)
	if d := f.app.detail; d == nil || d.kind != detailMovie || d.movieID != "movie-new" {
		t.Fatalf("detail = %+v, want movie-new", f.app.detail)
	}
	if f.app.subPopup != nil || f.app.browse.onPage() || f.app.browse.focus != browseFocusContent {
		t.Fatal("opening a title left the popup, a rail page or the rail focused")
	}
	if media := f.app.selectedBrowseMedia(); media == nil || media.ID != "movie-new" || f.app.browse.nav.section != SectionMovies {
		t.Fatalf("browse selection = %+v, want movie-new", media)
	}
	f.app.popDetail()

	open(`{"kind":"show","id":"show-one"}`, http.StatusAccepted)
	if d := f.app.detail; d == nil || d.show == nil || d.show.showID != "show-one" || d.show.focus != focusTabs {
		t.Fatalf("detail = %+v, want show-one on its season tabs", f.app.detail)
	}
	if media := f.app.selectedBrowseMedia(); media == nil || media.ID != "show-one" || f.app.browse.nav.section != SectionShows {
		t.Fatalf("browse selection = %+v, want show-one", media)
	}

	// A running player defers the launch, as every catalog launch does.
	f.app.playerStatus = PlayerStatusRunning
	open(`{"kind":"movie","id":"movie-old","play":true}`, http.StatusAccepted)
	if p := f.app.pendingPlayback; p == nil || p.mediaKey != movieMediaKey("movie-old") {
		t.Fatalf("pending playback = %+v, want movie-old", p)
	}
	f.app.pendingPlayback, f.app.playerStatus = nil, PlayerStatusIdle

	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{"kind":"movie","id":"unknown"}`, http.StatusNotFound},
		{`{"kind":"show","id":"movie-new"}`, http.StatusNotFound},
		{`{"kind":"episode","id":"show-one"}`, http.StatusBadRequest},
		{`{"kind":"show","id":"show-one","play":true}`, http.StatusBadRequest},
		{`{"kind":"movie","id":"movie-new","extra":1}`, http.StatusBadRequest},
	} {
		open(tc.body, tc.status)
	}

	// The title can leave the library between the request and the drain.
	if !f.app.webUI.enqueue(webCommand{media: &MediaItem{ID: "gone", Type: mediaKindMovie}}) {
		t.Fatal("command not queued")
	}
	if err := f.driver.Frame(); err != nil {
		t.Fatal(err)
	}
	if f.app.webCommandError != "This title is no longer in the library." {
		t.Fatalf("command error = %q", f.app.webCommandError)
	}

	// The setup screen has input priority; drain without laying it out.
	f.app.setup = &setupScreen{}
	detail := f.app.detail
	if response := webRequest(handler, "POST", "/api/open", `{"kind":"movie","id":"movie-new"}`); response.Code != http.StatusAccepted {
		t.Fatal(response.Body.String())
	}
	f.app.drainWebCommands()
	if f.app.webCommandError != "Finish setup on the TV first." || f.app.detail != detail {
		t.Fatalf("setup screen: command error = %q, detail changed = %v", f.app.webCommandError, f.app.detail != detail)
	}
}
