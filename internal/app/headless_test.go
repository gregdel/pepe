package app

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"log/slog"
	"strings"
	"testing"
	"time"

	"gioui.org/op/paint"
	"github.com/gregdel/pepe/internal/driver"
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
		movies:     map[string]*papi.Movie{oldMovie.media.ID: oldMovie.movie, newMovie.media.ID: newMovie.movie},
		shows:      map[string]*papi.Show{show.media.ID: show.show},
		movieItems: movies,
		showItems:  shows,
		snapshot:   snapshot,
		log:        slog.Default(),
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

func headlessButtonID(nodes []driver.Node, label string) (uint64, bool) {
	for _, node := range nodes {
		if node.Class == "button" && node.Label == label {
			return node.ID, true
		}
		if id, ok := headlessButtonID(node.Children, label); ok {
			return id, true
		}
	}
	return 0, false
}

func headlessHasClass(nodes []driver.Node, class string) bool {
	for _, node := range nodes {
		if node.Class == class || headlessHasClass(node.Children, class) {
			return true
		}
	}
	return false
}

func TestHeadlessEmptyCatalogNavigationAndRefresh(t *testing.T) {
	a := New(NewEmptyAPIClient(nil), nil, nil, nil, nil)
	d, err := driver.New(driver.Config{Width: 800, Height: 600, PxPerDp: 1, PxPerSp: 1}, a.Layout)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	if a.sectionRoot(SectionMovies) == nil || a.sectionRoot(SectionShows) == nil {
		t.Fatal("empty catalog did not initialize both radix roots")
	}
	if err := d.Key("Enter", nil); err != nil {
		t.Fatal(err)
	}
	if !a.browse.nav.isAtSectionRoot() || a.browse.nav.section != SectionMovies {
		t.Fatalf("empty Movies navigation = %#v", a.browse.nav)
	}
	if err := d.Key("LeftArrow", nil); err != nil {
		t.Fatal(err)
	}
	if err := d.Key("DownArrow", nil); err != nil {
		t.Fatal(err)
	}
	if err := d.Key("Enter", nil); err != nil {
		t.Fatal(err)
	}
	if !a.browse.nav.isAtSectionRoot() || a.browse.nav.section != SectionShows {
		t.Fatalf("empty Shows navigation = %#v", a.browse.nav)
	}
	if err := d.Key("LeftArrow", nil); err != nil {
		t.Fatal(err)
	}
	if err := d.Key("LeftArrow", nil); err != nil {
		t.Fatal(err)
	}
	if err := d.Key("DownArrow", nil); err != nil {
		t.Fatal(err)
	}
	if a.browse.library.mode != ListingModeDateAdded {
		t.Fatalf("listing mode = %v, want DateAdded", a.browse.library.mode)
	}
	if err := d.Key("RightArrow", nil); err != nil {
		t.Fatal(err)
	}
	a.startRefresh()
	if a.refreshing || a.refreshDone != nil || a.refreshError != nil {
		t.Fatal("offline Refresh attempted a network operation or changed refresh state")
	}
	if err := d.Frame(); err != nil {
		t.Fatal(err)
	}
	a.Close()
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
	if headlessHasClass(tree, "editor") {
		t.Error("semantic tree unexpectedly contains a text editor")
	}
	if f.app.browse.focus != browseFocusContent || !f.app.browse.nav.isAtRoot() || f.app.browse.nav.cursor != int(SectionMovies) {
		t.Fatalf("initial browse state = (focus=%v, root=%t, cursor=%d)", f.app.browse.focus, f.app.browse.nav.isAtRoot(), f.app.browse.nav.cursor)
	}
}

func TestHeadlessPlayingBarAndSwitchInputs(t *testing.T) {
	f := newHeadlessFixture(t)
	f.app.playerStatus = PlayerStatusRunning
	f.app.playerTitle = "Old Movie"
	f.app.playerMode = PlayerModeKMS
	stop := make(chan struct{})
	f.app.playerStop = stop
	completion := make(chan playerResult, 1)
	f.app.playerDone = completion
	if err := f.driver.Frame(); err != nil {
		t.Fatal(err)
	}
	if !headlessHasLabel(f.driver.Tree(), "Now playing") || !headlessHasLabel(f.driver.Tree(), "Old Movie") {
		t.Fatal("playing indicator missing from browse")
	}
	headlessKey(t, f, "Enter")
	if f.app.browse.nav.isAtRoot() || !headlessHasLabel(f.driver.Tree(), "Now playing") {
		t.Fatal("browsing while playing lost the indicator")
	}
	f.app.pushMovieDetail(&MediaItem{ID: "movie-new", Title: "New Movie", Type: mediaKindMovie})
	if err := f.driver.Frame(); err != nil {
		t.Fatal(err)
	}
	if !headlessHasLabel(f.driver.Tree(), "Now playing") {
		t.Fatal("playing indicator missing from detail")
	}
	headlessKey(t, f, "Enter")
	if err := f.driver.Click(driver.ClickTarget{Label: "Play movie", Occurrence: 1}); err != nil {
		t.Fatal(err)
	}
	if f.app.playerStatus != PlayerStatusRunning || f.app.playerDone != completion || f.app.pendingPlayback == nil || f.app.pendingPlayback.title != "New Movie (2026)" {
		t.Fatal("keyboard or pointer Play did not queue replacement")
	}
	select {
	case <-stop:
		// The stop request was sent.
	default:
		t.Fatal("current player was not asked to stop")
	}
	if err := f.driver.Frame(); err != nil {
		t.Fatal(err)
	}
	if !headlessHasLabel(f.driver.Tree(), "Stopping playback") || !headlessHasLabel(f.driver.Tree(), "Old Movie") {
		t.Fatal("stopping indicator missing or changed the currently playing title")
	}
}

func TestHeadlessLastAddedPreviews(t *testing.T) {
	f := newHeadlessFixture(t)
	headlessKey(t, f, "LeftArrow")
	headlessKey(t, f, "DownArrow")
	headlessKey(t, f, "RightArrow")
	headlessKey(t, f, "RightArrow")
	for _, label := range []string{"New Movie", "2026  ·  2h 5m  ·  Drama, Sci-Fi"} {
		if !headlessHasLabel(f.driver.Tree(), label) {
			t.Errorf("movie preview semantic tree does not contain %q", label)
		}
	}

	headlessKey(t, f, "LeftArrow")
	headlessKey(t, f, "DownArrow")
	headlessKey(t, f, "RightArrow")
	for _, label := range []string{"Fixture Show", "2024  ·  8.4  ·  2 seasons · 3 episodes"} {
		if !headlessHasLabel(f.driver.Tree(), label) {
			t.Errorf("show preview semantic tree does not contain %q", label)
		}
	}
}

func TestHeadlessLastAddedMovieDetailAndBack(t *testing.T) {
	f := newHeadlessFixture(t)

	headlessKey(t, f, "LeftArrow")
	headlessKey(t, f, "DownArrow")
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

func TestHeadlessMovieDetailScrollBoundaries(t *testing.T) {
	for _, viewport := range []image.Point{image.Pt(1920, 1080), image.Pt(1024, 768)} {
		for _, bright := range []bool{false, true} {
			t.Run(fmt.Sprintf("%dx%d/bright=%t", viewport.X, viewport.Y, bright), func(t *testing.T) {
				f := newHeadlessFixture(t)
				movie := f.app.client.movies["movie-new"]
				movie.Plot = strings.Repeat("A long synopsis exercises backward scrolling across separate detail sections. ", 80)
				shade := uint8(16)
				if bright {
					shade = 240
				}
				art := image.NewUniform(color.NRGBA{R: shade, G: shade, B: shade, A: 255})
				// Use finite offline artwork; no network or player is needed.
				img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
				draw.Draw(img, img.Bounds(), art, image.Point{}, draw.Src)
				f.app.images.store("fanart:movie-new", paint.NewImageOp(img))
				f.app.images.store("thumb:movie-new", paint.NewImageOp(img))
				f.app.pushMovieDetail(&MediaItem{ID: "movie-new", Title: "New Movie", Type: mediaKindMovie})
				f.app.detail.enteredAt = time.Now().Add(-detailFadeDuration)
				if err := f.driver.Resize(viewport.X, viewport.Y); err != nil {
					t.Fatal(err)
				}
				assertTop := func() {
					t.Helper()
					pos := f.app.detailList.Position
					if pos.First != 0 || pos.Offset != 0 {
						t.Fatalf("detail position = %+v, want top", pos)
					}
					for _, label := range []string{"New Movie", "Play movie"} {
						if !headlessLabelInside(f.driver.Tree(), label, viewport) {
							t.Fatalf("%q is not visible inside viewport", label)
						}
					}
				}
				assertTop()
				// Reach the bottom, including repeated Down presses at the boundary.
				for range 80 {
					headlessKey(t, f, "DownArrow")
				}
				bottom := f.app.detailList.Position
				if bottom.First == 0 {
					t.Fatal("fixture did not scroll past the first section")
				}
				headlessKey(t, f, "DownArrow")
				if got := f.app.detailList.Position; got.First != bottom.First || got.Offset != bottom.Offset {
					t.Fatalf("Down at bottom moved position: %+v -> %+v", bottom, got)
				}
				for range 80 {
					headlessKey(t, f, "UpArrow")
				}
				assertTop()
				for range 5 {
					headlessKey(t, f, "UpArrow")
					assertTop()
				}
			})
		}
	}
}

func headlessLabelInside(nodes []driver.Node, label string, viewport image.Point) bool {
	for _, node := range nodes {
		b := node.Bounds
		// Text semantics carry layout constraints, not glyph bounds. Check the
		// origin for text and the full bounds for the Play button.
		if node.Label == label && b.MinX >= 0 && b.MinY >= 0 &&
			b.MinX < viewport.X && b.MinY < viewport.Y && b.MaxX > b.MinX && b.MaxY > b.MinY {
			if node.Class != "button" || (b.MaxX <= viewport.X && b.MaxY <= viewport.Y) {
				return true
			}
		}
		if headlessLabelInside(node.Children, label, viewport) {
			return true
		}
	}
	return false
}

func TestHeadlessMovieDetailShortContentScroll(t *testing.T) {
	f := newHeadlessFixture(t)
	f.app.pushMovieDetail(&MediaItem{ID: "movie-old", Title: "Old Movie", Type: mediaKindMovie})
	f.app.detail.enteredAt = time.Now().Add(-detailFadeDuration)
	if err := f.driver.Frame(); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"UpArrow", "DownArrow", "UpArrow"} {
		for range 5 {
			headlessKey(t, f, key)
			if pos := f.app.detailList.Position; pos.First != 0 || pos.Offset != 0 {
				t.Fatalf("short detail after %s = %+v, want top", key, pos)
			}
		}
	}
}

func TestHeadlessShowSeasonAndEpisodeNavigation(t *testing.T) {
	f := newHeadlessFixture(t)

	headlessKey(t, f, "LeftArrow")
	headlessKey(t, f, "DownArrow")
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
		"Fixture Show",
		"2024  ·  8.4  ·  2 seasons · 3 episodes",
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

func TestHeadlessPrefixRowsHaveIndependentPointerTargets(t *testing.T) {
	f := newHeadlessFixture(t)
	movies := []*listingItem{
		newMovieListingItem(testMovie("a1", "Amber", 2001)),
		newMovieListingItem(testMovie("a2", "Azure", 2002)),
		newMovieListingItem(testMovie("b1", "Bore", 2003)),
		newMovieListingItem(testMovie("b2", "Brie", 2004)),
	}
	items := make(map[string]*papi.Movie, len(movies))
	for _, movie := range movies {
		items[movie.media.ID] = movie.movie
	}
	snapshot := buildLibrarySnapshot(radixListingBuilder{}, movies, nil)
	f.app.client.publishLibrary(items, nil, movies, nil, snapshot)
	f.app.applyLibrarySnapshot(snapshot)
	headlessKey(t, f, "RightArrow")
	children := f.app.browse.nav.getCurrentNode().Children
	if len(children) != 2 || children[0].FullString != "" || children[1].FullString != "" {
		t.Fatalf("expected two prefix groups, got %#v", children)
	}
	first := f.app.clickables[fmt.Sprintf("nav:%p", children[0])]
	second := f.app.clickables[fmt.Sprintf("nav:%p", children[1])]
	if first == nil || second == nil || first == second {
		t.Fatal("prefix rows did not get distinct clickables")
	}

	for _, label := range []string{"A", "B"} {
		id, ok := headlessButtonID(f.driver.Tree(), label)
		if !ok {
			t.Fatalf("no button for prefix %q", label)
		}
		if err := f.driver.Click(driver.ClickTarget{ID: &id}); err != nil {
			t.Fatal(err)
		}
		if node := f.app.browse.nav.getCurrentNode(); node.Label != label {
			t.Fatalf("clicking %q entered %q", label, node.Label)
		}
		headlessKey(t, f, "LeftArrow")
	}
}

func TestHeadlessDuplicateTitlesHaveDistinctPointerTargets(t *testing.T) {
	f := newHeadlessFixture(t)
	first := newMovieListingItem(testMovie("twin-first", "Twin", 2020))
	second := newMovieListingItem(testMovie("twin-second", "Twin", 2020))
	//nolint:staticcheck // Keep the fixture path aligned with the upstream API graph.
	first.movie.Movie.BaseVideo.VideoMetadata.DateAdded = time.Date(2026, time.January, 2, 0, 0, 0, 0, time.UTC)
	//nolint:staticcheck // Keep the fixture path aligned with the upstream API graph.
	second.movie.Movie.BaseVideo.VideoMetadata.DateAdded = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	items := []*listingItem{first, second}
	snapshot := buildLibrarySnapshot(radixListingBuilder{}, items, nil)
	f.app.client.publishLibrary(map[string]*papi.Movie{first.media.ID: first.movie, second.media.ID: second.movie}, nil, items, nil, snapshot)
	f.app.applyLibrarySnapshot(snapshot)
	f.app.applyListingMode(ListingModeDateAdded)
	headlessKey(t, f, "RightArrow")

	matches := func() []driver.Node {
		t.Helper()
		var found []driver.Node
		var visit func([]driver.Node)
		visit = func(nodes []driver.Node) {
			for _, node := range nodes {
				if node.Label == "Twin (2020)" {
					found = append(found, node)
				}
				visit(node.Children)
			}
		}
		visit(f.driver.Tree())
		if len(found) != 4 || found[0].Class != "button" || found[1].ParentID != found[0].ID || found[2].Class != "button" || found[3].ParentID != found[2].ID || found[0].ID == found[2].ID {
			t.Fatalf("duplicate-title semantics = %+v, want two buttons and their nested labels", found)
		}
		return found
	}
	matches()
	if err := f.driver.Click(driver.ClickTarget{Label: "Twin (2020)"}); err == nil || !strings.Contains(err.Error(), "matched 4 nodes") {
		t.Fatalf("duplicate-title click error = %v, want four matches", err)
	}
	for _, tc := range []struct {
		id         string
		occurrence int
		byID       bool
	}{{"twin-first", 1, true}, {"twin-second", 3, true}, {"twin-first", 1, false}, {"twin-second", 3, false}} {
		found := matches()
		target := driver.ClickTarget{Label: "Twin (2020)", Occurrence: tc.occurrence}
		if tc.byID {
			id := found[tc.occurrence-1].ID
			target = driver.ClickTarget{ID: &id}
		}
		if err := f.driver.Click(target); err != nil {
			t.Fatal(err)
		}
		if detail := f.app.detail; detail == nil || detail.kind != detailMovie || detail.movieID != tc.id {
			t.Fatalf("click occurrence %d (by ID %t) opened %+v, want %s", tc.occurrence, tc.byID, detail, tc.id)
		}
		headlessKey(t, f, "LeftArrow")
	}
}

func TestHeadlessBrowseDoesNotExposeTextEditor(t *testing.T) {
	f := newHeadlessFixture(t)
	if headlessHasClass(f.driver.Tree(), "editor") {
		t.Fatal("browse UI unexpectedly contains a text editor")
	}
}
