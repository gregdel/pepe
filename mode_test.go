package main

import (
	"errors"
	"testing"
	"time"

	"gioui.org/op/paint"
	polochon "github.com/odwrtw/polochon/lib"
	"github.com/odwrtw/polochon/lib/papi"
)

func newModeTestApp(movieItems, showItems []*listingItem) *App {
	snapshot := buildLibrarySnapshot(radixListingBuilder{}, movieItems, showItems)
	client := &APIClient{
		movieItems:   movieItems,
		showItems:    showItems,
		movieRadix:   snapshot.movieRadix,
		showRadix:    snapshot.showRadix,
		titleToMedia: snapshot.titleToMedia,
		movieCount:   len(movieItems),
		showCount:    len(showItems),
	}
	return &App{
		client:       client,
		movieRadix:   snapshot.movieRadix,
		showRadix:    snapshot.showRadix,
		titleToMedia: snapshot.titleToMedia,
		movieCount:   snapshot.movieCount,
		showCount:    snapshot.showCount,
		nav:          newNavigationState(),
		listingMode:  ListingModeTitle,
		browseFocus:  browseFocusContent,
	}
}

func TestModeRailAppliesImmediatelyAndStaysFocused(t *testing.T) {
	a := newModeTestApp(nil, nil)

	a.navigateLeft()
	if a.browseFocus != browseFocusModeRail {
		t.Fatal("Left from Home did not focus the mode rail")
	}
	a.navigateDown()
	if a.listingMode != ListingModeDateAdded || a.browseFocus != browseFocusModeRail {
		t.Errorf("Down = (mode %v, focus %v), want Date added with rail focus", a.listingMode, a.browseFocus)
	}
	a.navigateUp()
	if a.listingMode != ListingModeTitle || a.browseFocus != browseFocusModeRail {
		t.Errorf("Up = (mode %v, focus %v), want Title with rail focus", a.listingMode, a.browseFocus)
	}
	a.activateBrowseSelection()
	if a.browseFocus != browseFocusContent || !a.nav.isAtRoot() {
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
	a.nav.resetToSection(SectionShows, a.showRadix.Root())
	a.navigateLeft()
	if !a.nav.isAtRoot() || a.nav.cursor != int(SectionShows) || a.browseFocus != browseFocusContent {
		t.Fatal("Left from Shows did not return to Home with Shows selected")
	}
	a.navigateLeft()
	if a.browseFocus != browseFocusModeRail {
		t.Fatal("second Left from Home did not focus the mode rail")
	}
	a.navigateDown()

	if a.listingMode != ListingModeDateAdded || a.browseFocus != browseFocusModeRail {
		t.Errorf("mode after Down = %v with focus %v, want Date added with rail focus", a.listingMode, a.browseFocus)
	}
	if !a.nav.isAtRoot() || a.nav.cursor != int(SectionShows) {
		t.Error("changing mode did not preserve Home with Shows selected")
	}
	if got := a.movieRadix.Root().Children[0].Media.ID; got != "movie-new" {
		t.Errorf("first movie = %q, want movie-new", got)
	}
	if got := a.showRadix.Root().Children[0].Media.ID; got != "show-new" {
		t.Errorf("first show = %q, want show-new", got)
	}

	a.navigateRight()
	a.navigateRight()
	if !a.nav.isAtSectionRoot() || a.nav.section != SectionShows {
		t.Fatal("Right from the rail and Home did not reenter Shows")
	}
	if selected := a.nav.getSelectedChild(); selected == nil || selected.Media == nil || selected.Media.ID != "show-new" {
		t.Errorf("selected show = %#v, want the first newly sorted show", selected)
	}
}

func TestChangingModeFromHomePreservesSelectedSection(t *testing.T) {
	a := newModeTestApp(nil, nil)
	a.nav.cursor = int(SectionShows)
	a.navigateLeft()
	a.navigateDown()

	a.navigateRight()

	if !a.nav.isAtRoot() || a.nav.cursor != int(SectionShows) {
		t.Errorf("Home navigation = (at root %t, cursor %d), want Shows selected at Home", a.nav.isAtRoot(), a.nav.cursor)
	}
	if a.browseFocus != browseFocusContent {
		t.Error("Right from the mode rail did not return focus to Home")
	}
}

func TestLeftMovesFromSectionToHomeToModeRail(t *testing.T) {
	a := newModeTestApp(nil, nil)
	a.nav.resetToSection(SectionMovies, a.movieRadix.Root())

	a.navigateLeft()
	if !a.nav.isAtRoot() || a.nav.cursor != int(SectionMovies) || a.browseFocus != browseFocusContent {
		t.Error("Left from a section root did not return to Home")
	}
	a.navigateLeft()
	if a.browseFocus != browseFocusModeRail || !a.nav.isAtRoot() {
		t.Error("Left from Home did not focus the mode rail")
	}
	a.navigateRight()
	if a.browseFocus != browseFocusContent || !a.nav.isAtRoot() {
		t.Error("Right from the mode rail did not return to Home")
	}
}

func TestMediaForNodePrefersNodeIdentity(t *testing.T) {
	correct := &MediaItem{ID: "correct", Type: mediaKindMovie}
	wrong := &MediaItem{ID: "wrong", Type: mediaKindMovie}
	a := &App{titleToMedia: map[string]*MediaItem{"Duplicate": wrong}}
	node := &RadixNode{FullString: "Duplicate", Media: correct}

	if got := a.mediaForNode(node); got != correct {
		t.Errorf("mediaForNode() = %p, want node identity %p", got, correct)
	}
	if got := a.mediaForNode(&RadixNode{FullString: "Duplicate"}); got != wrong {
		t.Errorf("string-only fallback = %p, want title map identity %p", got, wrong)
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
	a.nav.resetToSection(SectionMovies, a.movieRadix.Root())
	a.nav.cursor = 1

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
	a.nfos = newAsyncCache[*polochon.Movie](1, 0)

	a.drainChannels()

	if a.listingMode != ListingModeDateAdded {
		t.Fatalf("active mode = %v, want Date added", a.listingMode)
	}
	if got := a.movieRadix.Root().Children[0].Media.ID; got != "tt002" {
		t.Errorf("first refreshed item = %q, want tt002", got)
	}
	selected := a.nav.getSelectedChild()
	if selected == nil || selected.Media == nil || selected.Media.ID != "tt002" || a.nav.cursor != 0 {
		t.Errorf("restored selection = %#v at cursor %d, want tt002 at cursor 0", selected, a.nav.cursor)
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
	if a.nav.isAtRoot() == false {
		t.Fatal("failed refresh unexpectedly changed navigation")
	}
}
