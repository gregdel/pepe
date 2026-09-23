package app

import (
	"errors"
	"testing"
	"time"

	"gioui.org/op/paint"
	"github.com/odwrtw/polochon/lib/papi"
)

func newModeTestApp(movieItems, showItems []*listingItem) *App {
	snapshot := buildLibrarySnapshot(radixListingBuilder{}, movieItems, showItems)
	client := &APIClient{
		movieItems: movieItems,
		showItems:  showItems,
		snapshot:   snapshot,
	}
	return &App{
		client: client,
		browse: browseState{
			library: libraryView{LibrarySnapshot: snapshot, mode: ListingModeTitle},
			nav:     newNavigationState(),
			focus:   browseFocusContent,
		},
	}
}

func TestModeRailAppliesImmediately(t *testing.T) {
	a := newModeTestApp(nil, nil)

	a.navigateLeft()
	if a.browse.focus != browseFocusModeRail {
		t.Fatal("Left from Home did not focus the mode rail")
	}
	a.navigateDown()
	if a.browse.library.mode != ListingModeDateAdded || a.browse.focus != browseFocusModeRail {
		t.Errorf("Down = (mode %v, focus %v), want Date added with rail focus", a.browse.library.mode, a.browse.focus)
	}
	a.activateBrowseSelection()
	if a.browse.focus != browseFocusContent || !a.browse.nav.isAtRoot() {
		t.Error("Enter from the mode rail did not return to Home")
	}
}

func TestRemoteFlowAppliesDateModeAndReentersSection(t *testing.T) {
	oldDate := time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC)
	newDate := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	oldMovie := newMovieListingItem(testMovie("movie-old", "Old Movie", 2000))
	//nolint:staticcheck // Keep the fixture path aligned with the upstream API graph.
	oldMovie.movie.VideoMetadata.DateAdded = oldDate
	newMovie := newMovieListingItem(testMovie("movie-new", "New Movie", 2001))
	//nolint:staticcheck // Keep the fixture path aligned with the upstream API graph.
	newMovie.movie.VideoMetadata.DateAdded = newDate
	oldShow := newShowListingItem(testShow("show-old", "Old Show", 2000))
	oldShow.show.Seasons = map[int]*papi.Season{1: {Episodes: map[int]*papi.Episode{1: testEpisode(oldDate)}}}
	newShow := newShowListingItem(testShow("show-new", "New Show", 2001))
	newShow.show.Seasons = map[int]*papi.Season{1: {Episodes: map[int]*papi.Episode{1: testEpisode(newDate)}}}
	a := newModeTestApp([]*listingItem{oldMovie, newMovie}, []*listingItem{oldShow, newShow})
	a.browse.nav.resetToSection(SectionShows, a.sectionRoot(SectionShows))
	a.navigateLeft()
	if !a.browse.nav.isAtRoot() || a.browse.nav.cursor != int(SectionShows) || a.browse.focus != browseFocusContent {
		t.Fatal("Left from Shows did not return to Home with Shows selected")
	}
	a.navigateLeft()
	if a.browse.focus != browseFocusModeRail {
		t.Fatal("second Left from Home did not focus the mode rail")
	}
	a.navigateDown()

	if a.browse.library.mode != ListingModeDateAdded || a.browse.focus != browseFocusModeRail {
		t.Errorf("mode after Down = %v with focus %v, want Date added with rail focus", a.browse.library.mode, a.browse.focus)
	}
	if !a.browse.nav.isAtRoot() || a.browse.nav.cursor != int(SectionShows) {
		t.Error("changing mode did not preserve Home with Shows selected")
	}
	a.navigateRight()
	a.navigateRight()
	if !a.browse.nav.isAtSectionRoot() || a.browse.nav.section != SectionShows {
		t.Fatal("Right from the rail and Home did not reenter Shows")
	}
	if selected := a.browse.nav.getSelectedChild(); selected == nil || selected.Media == nil || selected.Media.ID != "show-new" {
		t.Errorf("selected show = %#v, want the first newly sorted show", selected)
	}
}

func TestChangingModeFromHomePreservesSelectedSection(t *testing.T) {
	a := newModeTestApp(nil, nil)
	a.browse.nav.cursor = int(SectionShows)
	a.navigateLeft()
	a.navigateDown()

	a.navigateRight()

	if !a.browse.nav.isAtRoot() || a.browse.nav.cursor != int(SectionShows) {
		t.Errorf("Home navigation = (at root %t, cursor %d), want Shows selected at Home", a.browse.nav.isAtRoot(), a.browse.nav.cursor)
	}
	if a.browse.focus != browseFocusContent {
		t.Error("Right from the mode rail did not return focus to Home")
	}
}

func TestModeRailExitKeepsActiveListing(t *testing.T) {
	a := newModeTestApp(nil, nil)
	a.navigateLeft()
	a.navigateDown()
	a.navigateRight()

	if a.browse.library.mode != ListingModeDateAdded {
		t.Fatalf("mode after leaving rail = %v, want Date added", a.browse.library.mode)
	}
	if a.browse.focus != browseFocusContent || !a.browse.nav.isAtRoot() {
		t.Fatal("Right from the mode rail did not return to Home")
	}
}

func TestLeftMovesFromSectionToHomeToModeRail(t *testing.T) {
	a := newModeTestApp(nil, nil)
	a.browse.nav.resetToSection(SectionMovies, a.sectionRoot(SectionMovies))

	a.navigateLeft()
	if !a.browse.nav.isAtRoot() || a.browse.nav.cursor != int(SectionMovies) || a.browse.focus != browseFocusContent {
		t.Error("Left from a section root did not return to Home")
	}
	a.navigateLeft()
	if a.browse.focus != browseFocusModeRail || !a.browse.nav.isAtRoot() {
		t.Error("Left from Home did not focus the mode rail")
	}
	a.navigateRight()
	if a.browse.focus != browseFocusContent || !a.browse.nav.isAtRoot() {
		t.Error("Right from the mode rail did not return to Home")
	}
}

func TestRightOpensMovieDetailWhileEnterDoesNothing(t *testing.T) {
	movie := newMovieListingItem(testMovie("tt001", "Movie", 2001))
	a := newModeTestApp([]*listingItem{movie}, nil)
	a.browse.nav.resetToSection(SectionMovies, a.sectionRoot(SectionMovies))

	a.activateBrowseSelection()
	if a.detail != nil {
		t.Fatal("Enter opened movie details")
	}

	a.navigateRight()
	if a.detail == nil || a.detail.kind != detailMovie || a.detail.movieID != "tt001" {
		t.Fatalf("Right detail = %#v, want movie tt001", a.detail)
	}
}

func TestMediaForNodePrefersNodeIdentity(t *testing.T) {
	correct := &MediaItem{ID: "correct", Type: mediaKindMovie}
	wrong := &MediaItem{ID: "wrong", Type: mediaKindMovie}
	a := &App{browse: browseState{library: libraryView{LibrarySnapshot: LibrarySnapshot{titleToMedia: map[string]*MediaItem{"Duplicate": wrong}}}}}
	node := &RadixNode{FullString: "Duplicate", Media: correct}

	if got := a.mediaForNode(node); got != correct {
		t.Errorf("mediaForNode() = %p, want node identity %p", got, correct)
	}
	if got := a.mediaForNode(&RadixNode{FullString: "Duplicate"}); got != wrong {
		t.Errorf("string-only fallback = %p, want title map identity %p", got, wrong)
	}
}

func TestApplyLibrarySnapshotReplacesActiveViewWithoutChangingMode(t *testing.T) {
	a := newModeTestApp(nil, nil)
	a.browse.library.mode = ListingModeDateAdded

	movie := newMovieListingItem(testMovie("tt001", "Replacement", 2001))
	snapshot := buildLibrarySnapshot(radixListingBuilder{}, []*listingItem{movie}, nil)
	a.applyLibrarySnapshot(snapshot)

	if a.browse.library.mode != ListingModeDateAdded {
		t.Fatalf("listing mode = %v, want Date added", a.browse.library.mode)
	}
	if a.browse.library.movieRadix != snapshot.movieRadix || a.browse.library.showRadix != snapshot.showRadix {
		t.Fatal("active radix trees were not replaced from the snapshot")
	}
	if a.browse.library.titleToMedia["Replacement"] != snapshot.titleToMedia["Replacement"] || a.browse.library.movieCount != 1 || a.browse.library.showCount != 0 {
		t.Fatal("active library metadata was not replaced from the snapshot")
	}
}

func TestRefreshCompletionRebuildsActiveModeAndRestoresMedia(t *testing.T) {
	oldDate := time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC)
	newDate := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	first := newMovieListingItem(testMovie("tt001", "First", 2001))
	//nolint:staticcheck // Keep the fixture path aligned with the upstream API graph.
	first.movie.VideoMetadata.DateAdded = newDate
	second := newMovieListingItem(testMovie("tt002", "Second", 2002))
	//nolint:staticcheck // Keep the fixture path aligned with the upstream API graph.
	second.movie.VideoMetadata.DateAdded = oldDate
	a := newModeTestApp([]*listingItem{first, second}, nil)
	a.applyListingMode(ListingModeDateAdded)
	a.browse.nav.resetToSection(SectionMovies, a.sectionRoot(SectionMovies))
	a.browse.nav.cursor = 1

	refreshedFirst := newMovieListingItem(testMovie("tt001", "First", 2001))
	//nolint:staticcheck // Keep the fixture path aligned with the upstream API graph.
	refreshedFirst.movie.VideoMetadata.DateAdded = oldDate
	refreshedSecond := newMovieListingItem(testMovie("tt002", "Second", 2002))
	//nolint:staticcheck // Keep the fixture path aligned with the upstream API graph.
	refreshedSecond.movie.VideoMetadata.DateAdded = newDate
	refreshedItems := []*listingItem{refreshedFirst, refreshedSecond}
	defaultSnapshot := buildLibrarySnapshot(radixListingBuilder{}, refreshedItems, nil)
	a.client.publishLibrary(nil, nil, refreshedItems, nil, defaultSnapshot)
	a.refreshDone = make(chan refreshResult, 1)
	a.refreshDone <- refreshResult{snapshot: defaultSnapshot}
	a.refreshing = true
	a.images = newAsyncCache[paint.ImageOp](1, 0)

	a.drainChannels()

	if a.browse.library.mode != ListingModeDateAdded {
		t.Fatalf("active mode = %v, want Date added", a.browse.library.mode)
	}
	if got := a.browse.library.movieRadix.Root().Children[0].Media.ID; got != "tt002" {
		t.Errorf("first refreshed item = %q, want tt002", got)
	}
	selected := a.browse.nav.getSelectedChild()
	if selected == nil || selected.Media == nil || selected.Media.ID != "tt002" || a.browse.nav.cursor != 0 {
		t.Errorf("restored selection = %#v at cursor %d, want tt002 at cursor 0", selected, a.browse.nav.cursor)
	}
}

func TestDateAddedSnapshotIsReusedUntilSuccessfulRefresh(t *testing.T) {
	old := newMovieListingItem(testMovie("old", "Old", 2001))
	a := newModeTestApp([]*listingItem{old}, nil)
	a.images = newAsyncCache[paint.ImageOp](1, 0)
	a.applyListingMode(ListingModeDateAdded)
	first := a.browse.library.movieRadix

	a.applyListingMode(ListingModeTitle)
	a.applyListingMode(ListingModeDateAdded)
	if a.browse.library.movieRadix != first {
		t.Fatal("switching back rebuilt the unchanged date listing")
	}

	newItem := newMovieListingItem(testMovie("new", "New", 2002))
	items := []*listingItem{newItem}
	updated := buildLibrarySnapshot(radixListingBuilder{}, items, nil)
	a.client.publishLibrary(nil, nil, items, nil, updated)
	a.applyRefreshResult(updated)
	if a.browse.library.movieRadix == first || a.browse.library.movieRadix.Root().Children[0].Media.ID != "new" {
		t.Fatal("successful refresh did not rebuild the cached date listing")
	}
}

func TestRefreshFailurePreservesCurrentUIState(t *testing.T) {
	a := newModeTestApp(nil, nil)
	detail := &detailScreen{kind: detailMovie}
	refreshErr := errors.New("server unavailable")
	a.detail = detail
	a.refreshing = true
	a.refreshDone = make(chan refreshResult, 1)
	a.refreshDone <- refreshResult{err: refreshErr}

	a.drainRefresh()

	if a.refreshing || a.refreshDone != nil {
		t.Fatal("failed refresh did not finish cleanly")
	}
	if !errors.Is(a.refreshError, refreshErr) {
		t.Fatalf("refresh error = %v, want %v", a.refreshError, refreshErr)
	}
	if a.detail != detail {
		t.Fatal("failed refresh unexpectedly dismissed the detail screen")
	}
	if a.browse.nav.isAtRoot() == false {
		t.Fatal("failed refresh unexpectedly changed navigation")
	}
}
