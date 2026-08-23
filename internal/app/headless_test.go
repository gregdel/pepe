package app

import (
	"log/slog"
	"testing"
	"time"

	"github.com/gregdel/polochon-gio/internal/driver"
	polochon "github.com/odwrtw/polochon/lib"
	"github.com/odwrtw/polochon/lib/papi"
)

type headlessFixture struct {
	app    *App
	driver *driver.Driver
}

func newHeadlessFixture(t *testing.T) *headlessFixture {
	t.Helper()

	oldMovie := newMovieListingItem(testMovie("movie-old", "Old Movie", 2000))
	//nolint:staticcheck // Keep the fixture path aligned with the upstream API graph.
	oldMovie.movie.VideoMetadata.DateAdded = time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC)
	newMovie := newMovieListingItem(testMovie("movie-new", "New Movie", 2026))
	newMovie.movie.Plot = "Metadata supplied by the catalog"
	newMovie.movie.Tagline = "No extra request required"
	newMovie.movie.Runtime = 125
	newMovie.movie.Genres = []string{"Drama", "Sci-Fi"}
	newMovie.movie.Size = 2_200_000_000
	newMovie.movie.Quality = polochon.Quality("1080p")
	newMovie.movie.VideoCodec = "H.264"
	newMovie.movie.AudioCodec = "Dolby Digital"
	newMovie.movie.Container = "mkv"
	newMovie.movie.ReleaseGroup = "FIXTURE"
	//nolint:staticcheck // Keep the fixture path aligned with the upstream API graph.
	newMovie.movie.VideoMetadata.DateAdded = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

	show := newShowListingItem(testShow("show-one", "Fixture Show", 2024))
	show.show.Plot = "A catalog show synopsis that remains concise and readable."
	show.show.Rating = 8.4
	show.show.Seasons = map[int]*papi.Season{
		1: {Episodes: map[int]*papi.Episode{
			1: headlessEpisode(1, 1, "Pilot"),
			2: headlessEpisode(1, 2, "Second Episode"),
		}},
		2: {Episodes: map[int]*papi.Episode{
			1: headlessEpisode(2, 1, "New Season"),
		}},
	}

	movies := []*listingItem{oldMovie, newMovie}
	shows := []*listingItem{show}
	snapshot := buildLibrarySnapshot(radixListingBuilder{}, movies, shows)
	client := &APIClient{
		movies:       map[string]*papi.Movie{oldMovie.media.ID: oldMovie.movie, newMovie.media.ID: newMovie.movie},
		shows:        map[string]*papi.Show{show.media.ID: show.show},
		movieItems:   movies,
		showItems:    shows,
		movieRadix:   snapshot.movieRadix,
		showRadix:    snapshot.showRadix,
		titleToMedia: snapshot.titleToMedia,
		movieCount:   snapshot.movieCount,
		showCount:    snapshot.showCount,
		log:          slog.Default(),
	}

	a := New(client, nil, &Config{MPVPath: "mpv"}, nil, nil)
	d, err := driver.New(driver.Config{
		Width: 1024, Height: 768, PxPerDp: 1, PxPerSp: 1,
	}, a.Layout)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)

	return &headlessFixture{app: a, driver: d}
}

func headlessEpisode(season, episode int, title string) *papi.Episode {
	return &papi.Episode{ShowEpisode: &polochon.ShowEpisode{
		BaseVideo: polochon.BaseVideo{
			File: polochon.File{Size: 1_100_000_000},
			VideoMetadata: polochon.VideoMetadata{
				Quality:      polochon.Quality("720p"),
				VideoCodec:   "H.264",
				AudioCodec:   "Dolby Digital",
				Container:    "mkv",
				ReleaseGroup: "FIXTURE",
			},
		},
		Season:  season,
		Episode: episode,
		Title:   title,
		Aired:   "2026-01-02",
		Plot:    "Episode details for " + title,
		Runtime: 45,
		Rating:  8.1,
	}}
}

func headlessKey(t *testing.T, f *headlessFixture, name string) {
	t.Helper()
	if err := f.driver.Key(name, nil); err != nil {
		t.Fatal(err)
	}
}

func headlessHasLabel(nodes []driver.Node, label string) bool {
	for _, node := range nodes {
		if node.Label == label || headlessHasLabel(node.Children, label) {
			return true
		}
	}
	return false
}

func headlessHasClass(nodes []driver.Node, class string) bool {
	for _, node := range nodes {
		if node.Class == class || headlessHasClass(node.Children, class) {
			return true
		}
	}
	return false
}

func TestHeadlessHomeSemanticTree(t *testing.T) {
	f := newHeadlessFixture(t)
	tree := f.driver.Tree()

	for _, label := range []string{
		"Smart list", "Last added", "Home", "Movies", "Shows", "2 movies · 1 shows",
	} {
		if !headlessHasLabel(tree, label) {
			t.Errorf("semantic tree does not contain %q", label)
		}
	}
	if !headlessHasClass(tree, "editor") {
		t.Error("semantic tree does not contain the search editor")
	}
	if f.app.browse.focus != browseFocusContent || !f.app.browse.nav.isAtRoot() || f.app.browse.nav.cursor != int(SectionMovies) {
		t.Fatalf("initial browse state = (focus=%v, root=%t, cursor=%d)", f.app.browse.focus, f.app.browse.nav.isAtRoot(), f.app.browse.nav.cursor)
	}
}

func TestHeadlessLastAddedMovieDetailAndBack(t *testing.T) {
	f := newHeadlessFixture(t)

	headlessKey(t, f, "LeftArrow")
	headlessKey(t, f, "UpArrow")
	if f.app.browse.library.mode != ListingModeDateAdded || f.app.browse.focus != browseFocusModeRail {
		t.Fatalf("mode-rail state = (mode=%v, focus=%v)", f.app.browse.library.mode, f.app.browse.focus)
	}
	headlessKey(t, f, "RightArrow")
	headlessKey(t, f, "RightArrow")
	if !f.app.browse.nav.isAtSectionRoot() || f.app.browse.nav.section != SectionMovies {
		t.Fatalf("movie section state = (root=%t, section=%v)", f.app.browse.nav.isAtSectionRoot(), f.app.browse.nav.section)
	}

	headlessKey(t, f, "RightArrow")
	if f.app.detail == nil || f.app.detail.kind != detailMovie || f.app.detail.movieID != "movie-new" {
		t.Fatalf("movie detail = %#v, want movie-new", f.app.detail)
	}
	for _, label := range []string{
		"Metadata supplied by the catalog",
		"No extra request required",
		"2026  ·  2h 5m  ·  Drama, Sci-Fi",
		"Play movie",
		"1080p  ·  H.264 + Dolby Digital  ·  MKV  ·  2.2 GB  ·  FIXTURE",
	} {
		if !headlessHasLabel(f.driver.Tree(), label) {
			t.Errorf("movie detail semantic tree does not contain %q", label)
		}
	}

	headlessKey(t, f, "LeftArrow")
	if f.app.detail != nil || !f.app.browse.nav.isAtSectionRoot() {
		t.Fatalf("after back = (detail=%v, section-root=%t)", f.app.detail, f.app.browse.nav.isAtSectionRoot())
	}
}

func TestHeadlessShowSeasonAndEpisodeNavigation(t *testing.T) {
	f := newHeadlessFixture(t)

	headlessKey(t, f, "LeftArrow")
	headlessKey(t, f, "UpArrow")
	headlessKey(t, f, "RightArrow")
	headlessKey(t, f, "DownArrow")
	headlessKey(t, f, "RightArrow")
	headlessKey(t, f, "RightArrow")

	if f.app.detail == nil || f.app.detail.kind != detailShow || f.app.detail.show == nil {
		t.Fatalf("show detail = %#v", f.app.detail)
	}
	show := f.app.detail.show
	if show.focus != focusTabs || show.seasonCursor != 0 || show.epCursor != 0 {
		t.Fatalf("initial show focus = (focus=%v, season=%d, episode=%d)", show.focus, show.seasonCursor, show.epCursor)
	}
	for _, label := range []string{
		"A catalog show synopsis that remains concise and readable.",
		"Play episode",
		"Aired Jan 2, 2026  ·  45 min  ·  8.1",
		"Episode details for Pilot",
		"720p  ·  H.264 + Dolby Digital  ·  MKV  ·  1.1 GB  ·  FIXTURE",
	} {
		if !headlessHasLabel(f.driver.Tree(), label) {
			t.Errorf("show detail semantic tree does not contain %q", label)
		}
	}

	headlessKey(t, f, "DownArrow")
	headlessKey(t, f, "DownArrow")
	if show.focus != focusEpisodes || show.epCursor != 1 {
		t.Fatalf("episode focus = (focus=%v, episode=%d)", show.focus, show.epCursor)
	}

	headlessKey(t, f, "UpArrow")
	headlessKey(t, f, "UpArrow")
	headlessKey(t, f, "RightArrow")
	if show.focus != focusTabs || show.seasonCursor != 1 || show.epCursor != 0 {
		t.Fatalf("season navigation = (focus=%v, season=%d, episode=%d)", show.focus, show.seasonCursor, show.epCursor)
	}

	headlessKey(t, f, "DownArrow")
	headlessKey(t, f, "LeftArrow")
	if f.app.detail != nil {
		t.Fatal("left from show episode focus did not close detail")
	}
}

func TestHeadlessEditorInput(t *testing.T) {
	f := newHeadlessFixture(t)
	if err := f.driver.Type("fixture query"); err != nil {
		t.Fatal(err)
	}
	if got := f.app.searchEditor.Text(); got != "fixture query" {
		t.Fatalf("search text = %q, want fixture query", got)
	}
	if !headlessHasClass(f.driver.Tree(), "editor") {
		t.Fatal("editor disappeared after text input")
	}
}
