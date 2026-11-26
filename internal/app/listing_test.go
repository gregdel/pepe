package app

import (
	"fmt"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	polochon "github.com/odwrtw/polochon/lib"
	"github.com/odwrtw/polochon/lib/papi"
)

func testMovie(id, title string, year int) *papi.Movie {
	return &papi.Movie{Movie: &polochon.Movie{ImdbID: id, Title: title, Year: year}}
}

func testShow(id, title string, year int) *papi.Show {
	return &papi.Show{Show: &polochon.Show{ImdbID: id, Title: title, Year: year}}
}

func testEpisode(dateAdded time.Time) *papi.Episode {
	return &papi.Episode{ShowEpisode: &polochon.ShowEpisode{
		BaseVideo: polochon.BaseVideo{VideoMetadata: polochon.VideoMetadata{DateAdded: dateAdded}},
	}}
}

func TestRadixListingBuilderPreservesCurrentBehavior(t *testing.T) {
	items := []*listingItem{
		newMovieListingItem(testMovie("tt001", "The Matrix", 1999)),
		newMovieListingItem(testMovie("tt002", "The Matrix Reloaded", 2003)),
		newMovieListingItem(testMovie("tt003", "Avatar", 0)),
	}

	listing := (radixListingBuilder{}).build(items)
	expectedStrings := []string{
		"Avatar",
		"Matrix (1999)",
		"Matrix Reloaded (2003)",
		"The Matrix (1999)",
		"The Matrix Reloaded (2003)",
	}
	strings := listing.tree.AllStrings()
	sort.Strings(strings)
	if !reflect.DeepEqual(strings, expectedStrings) {
		t.Fatalf("AllStrings() = %q, want %q", strings, expectedStrings)
	}

	if got := listing.tree.Root().SubtreeCount; got != len(expectedStrings) {
		t.Fatalf("root subtree count = %d, want %d", got, len(expectedStrings))
	}
	for _, title := range expectedStrings {
		node := listing.tree.Lookup(title)
		if node == nil || !node.IsTerminal || node.FullString != title {
			t.Errorf("Lookup(%q) = %#v, want terminal with matching full string", title, node)
		}
	}

	root := listing.tree.Root()
	if len(root.Children) != 3 {
		t.Fatalf("root children = %d, want 3", len(root.Children))
	}
	for i, want := range []struct {
		label string
		count int
	}{{"Matrix ", 2}, {"The Matrix ", 2}, {"Avatar", 1}} {
		if child := root.Children[i]; child.Label != want.label || child.SubtreeCount != want.count {
			t.Errorf("root child %d = (%q, %d), want (%q, %d)", i, child.Label, child.SubtreeCount, want.label, want.count)
		}
	}
}

func TestRadixListingBuilderAttachesMedia(t *testing.T) {
	item := newMovieListingItem(testMovie("tt0133093", "The Matrix", 1999))
	listing := (radixListingBuilder{}).build([]*listingItem{item})

	for _, title := range []string{"The Matrix (1999)", "Matrix (1999)"} {
		node := listing.tree.Lookup(title)
		if node == nil || node.Media == nil {
			t.Fatalf("Lookup(%q) did not attach media", title)
		}
		if node.Media.Type != "movie" || node.Media.ID != "tt0133093" {
			t.Errorf("Lookup(%q) media = (%q, %q), want (movie, tt0133093)", title, node.Media.Type, node.Media.ID)
		}
		if node.Media != item.media {
			t.Errorf("Lookup(%q) media pointer does not match listing item", title)
		}
	}

	if node := NewRadixTree([]string{"standalone"}).Lookup("standalone"); node.Media != nil {
		t.Errorf("string-only radix terminal media = %#v, want nil", node.Media)
	}
}

func testOriginalMovie(id, title, original string, year int) *papi.Movie {
	movie := testMovie(id, title, year)
	movie.OriginalTitle = original
	return movie
}

func TestMovieTitlesPreferOriginal(t *testing.T) {
	for _, tc := range []struct{ title, original, wantTitle, wantAlt string }{
		{"Le Voyage de Chihiro", "Sen to Chihiro", "Sen to Chihiro", "Le Voyage de Chihiro"},
		{"Fixture", "", "Fixture", ""},
		{"Fixture", "  ", "Fixture", ""},
		{"The Matrix", "the matrix", "the matrix", ""},
		{"", "Original", "Original", ""},
		// Roboto cannot draw these: the TV shows the localized title alone.
		{"Spirited Away", "千と千尋の神隠し", "Spirited Away", ""},
		{"", "기생충", "기생충", ""},
		{"پیکه", "PK", "PK", ""},
	} {
		title, alt := movieTitles(testOriginalMovie("tt1", tc.title, tc.original, 0))
		if title != tc.wantTitle || alt != tc.wantAlt {
			t.Errorf("movieTitles(%q, %q) = (%q, %q), want (%q, %q)", tc.title, tc.original, title, alt, tc.wantTitle, tc.wantAlt)
		}
	}
}

func TestListingsShowOriginalTitleAndAliasLocalized(t *testing.T) {
	spirited := newMovieListingItem(testOriginalMovie("tt001", "The Spirited Away", "Sen to Chihiro", 2001))
	plain := newMovieListingItem(testMovie("tt002", "Avatar", 2009))
	// Its localized title is another movie's shown title, which keeps it.
	clash := newMovieListingItem(testOriginalMovie("tt003", "Avatar", "Abatâ", 2009))
	items := []*listingItem{spirited, plain, clash}

	radix := (radixListingBuilder{}).build(items)
	want := []string{"Abatâ (2009)", "Avatar (2009)", "Sen to Chihiro (2001)", "Spirited Away (2001)", "The Spirited Away (2001)"}
	got := radix.tree.AllStrings()
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("radix strings = %q, want %q", got, want)
	}
	for title, media := range map[string]*MediaItem{
		"Sen to Chihiro (2001)":    spirited.media,
		"The Spirited Away (2001)": spirited.media,
		"Spirited Away (2001)":     spirited.media,
		"Avatar (2009)":            plain.media,
		"Abatâ (2009)":             clash.media,
	} {
		if node := radix.tree.Lookup(title); node == nil || node.Media != media || radix.titleToMedia[title] != media {
			t.Errorf("%q does not resolve to %q", title, media.ID)
		}
	}

	dated := (dateAddedListingBuilder{}).build(items)
	var rows []string
	for _, child := range dated.tree.Root().Children {
		rows = append(rows, child.FullString)
	}
	sort.Strings(rows)
	if want := []string{"Abatâ (2009)", "Avatar (2009)", "Sen to Chihiro (2001)"}; !reflect.DeepEqual(rows, want) {
		t.Fatalf("last added rows = %q, want %q", rows, want)
	}
	if spirited.movie.Title != "The Spirited Away" || spirited.media.AltTitle != "The Spirited Away" {
		t.Errorf("listing item = %+v, movie title %q", spirited.media, spirited.movie.Title)
	}
}

func TestBuildLibrarySnapshotPreservesLegacyCollisions(t *testing.T) {
	movieFirst := newMovieListingItem(testMovie("tt001", "Duplicate", 2000))
	movieLast := newMovieListingItem(testMovie("tt002", "Duplicate", 2000))
	show := newShowListingItem(testShow("tt003", "Duplicate", 2000))

	snapshot := buildLibrarySnapshot(radixListingBuilder{}, []*listingItem{movieFirst, movieLast}, []*listingItem{show})
	const title = "Duplicate (2000)"

	if media := snapshot.titleToMedia[title]; media.Type != "show" || media.ID != "tt003" {
		t.Errorf("merged media = (%q, %q), want (show, tt003)", media.Type, media.ID)
	}
	if node := snapshot.movieRadix.Lookup(title); node.Media.Type != "movie" || node.Media.ID != "tt002" {
		t.Errorf("movie terminal media = (%q, %q), want (movie, tt002)", node.Media.Type, node.Media.ID)
	}
	if node := snapshot.showRadix.Lookup(title); node.Media.Type != "show" || node.Media.ID != "tt003" {
		t.Errorf("show terminal media = (%q, %q), want (show, tt003)", node.Media.Type, node.Media.ID)
	}
	if snapshot.movieCount != 2 || snapshot.showCount != 1 {
		t.Errorf("counts = (%d, %d), want (2, 1)", snapshot.movieCount, snapshot.showCount)
	}
}

type recordingListingBuilder struct {
	calls   [][]*listingItem
	onBuild func()
}

func (b *recordingListingBuilder) build(items []*listingItem) mediaListing {
	b.calls = append(b.calls, append([]*listingItem(nil), items...))
	if b.onBuild != nil {
		b.onBuild()
	}

	title := "empty"
	media := &MediaItem{}
	if len(items) > 0 {
		title = fmt.Sprintf("%s-%s", items[0].media.Type, items[0].media.ID)
		media = items[0].media
	}
	return mediaListing{
		tree:         NewRadixTree([]string{title}),
		titleToMedia: map[string]*MediaItem{title: media},
	}
}

func TestSnapshotForUsesCachedCatalog(t *testing.T) {
	movieItems := []*listingItem{
		newMovieListingItem(testMovie("tt001", "First", 2001)),
		newMovieListingItem(testMovie("tt002", "Second", 2002)),
	}
	showItems := []*listingItem{
		newShowListingItem(testShow("tt003", "Third", 2003)),
	}
	client := &APIClient{
		movieItems: movieItems,
		showItems:  showItems,
		snapshot:   LibrarySnapshot{movieCount: len(movieItems), showCount: len(showItems)},
	}
	builderRanOutsideLock := true
	builder := &recordingListingBuilder{onBuild: func() {
		if !client.mu.TryLock() {
			builderRanOutsideLock = false
			return
		}
		client.mu.Unlock()
	}}

	snapshot := client.snapshotFor(builder)

	if len(builder.calls) != 2 {
		t.Fatalf("builder calls = %d, want 2", len(builder.calls))
	}
	if !builderRanOutsideLock {
		t.Error("builder ran while APIClient.mu was held")
	}
	if !reflect.DeepEqual(builder.calls[0], movieItems) {
		t.Errorf("movie builder input = %#v, want %#v", builder.calls[0], movieItems)
	}
	if !reflect.DeepEqual(builder.calls[1], showItems) {
		t.Errorf("show builder input = %#v, want %#v", builder.calls[1], showItems)
	}
	if snapshot.movieRadix.Lookup("movie-tt001") == nil || snapshot.showRadix.Lookup("show-tt003") == nil {
		t.Error("snapshot did not retain separately built section trees")
	}
	if snapshot.movieCount != 2 || snapshot.showCount != 1 {
		t.Errorf("counts = (%d, %d), want (2, 1)", snapshot.movieCount, snapshot.showCount)
	}
}

func TestGetSnapshotDoesNotShareTitleMap(t *testing.T) {
	media := &MediaItem{ID: "original", Type: mediaKindMovie}
	client := &APIClient{snapshot: LibrarySnapshot{
		titleToMedia: map[string]*MediaItem{"title": media},
	}}

	snapshot := client.getSnapshot()
	snapshot.titleToMedia["title"] = &MediaItem{ID: "changed"}
	if got := client.getSnapshot().titleToMedia["title"]; got != media {
		t.Errorf("default snapshot media = %p, want %p", got, media)
	}
}

func TestSnapshotForDoesNotReplaceDefaultSnapshot(t *testing.T) {
	defaultMedia := &MediaItem{ID: "default", Type: mediaKindMovie}
	defaultMovies := NewRadixTree([]string{"default-movie"})
	defaultShows := NewRadixTree([]string{"default-show"})
	client := &APIClient{
		movieItems: []*listingItem{newMovieListingItem(testMovie("tt001", "Movie", 2001))},
		showItems:  []*listingItem{newShowListingItem(testShow("tt002", "Show", 2002))},
		snapshot: LibrarySnapshot{
			movieRadix: defaultMovies, showRadix: defaultShows,
			titleToMedia: map[string]*MediaItem{"default": defaultMedia},
			movieCount:   1, showCount: 1,
		},
	}
	builder := &recordingListingBuilder{}

	custom := client.snapshotFor(builder)
	custom.titleToMedia["custom"] = &MediaItem{ID: "custom"}
	defaultSnapshot := client.getSnapshot()

	if defaultSnapshot.movieRadix != defaultMovies || defaultSnapshot.showRadix != defaultShows {
		t.Error("snapshotFor replaced the cached default trees")
	}
	if defaultSnapshot.titleToMedia["default"] != defaultMedia {
		t.Error("snapshotFor replaced the cached default media map")
	}
	if _, ok := defaultSnapshot.titleToMedia["custom"]; ok {
		t.Error("mutating custom snapshot map changed the cached default map")
	}
	if custom.movieRadix == defaultMovies || custom.showRadix == defaultShows {
		t.Error("custom snapshot reused a cached default tree")
	}
}

func TestFetchPublicationIncludesListingCatalog(t *testing.T) {
	oldMovie := newMovieListingItem(testMovie("tt-old", "Old Movie", 2000))
	oldShow := newShowListingItem(testShow("tt-old-show", "Old Show", 2000))
	client := &APIClient{
		movies:     map[string]*papi.Movie{oldMovie.media.ID: oldMovie.movie},
		shows:      map[string]*papi.Show{oldShow.media.ID: oldShow.show},
		movieItems: []*listingItem{oldMovie},
		showItems:  []*listingItem{oldShow},
		snapshot: LibrarySnapshot{
			movieRadix:   NewRadixTree([]string{"old-movie"}),
			showRadix:    NewRadixTree([]string{"old-show"}),
			titleToMedia: map[string]*MediaItem{"old": oldMovie.media},
			movieCount:   1, showCount: 1,
		},
	}
	newMovie := newMovieListingItem(testMovie("tt-new", "New Movie", 2001))
	newShow := newShowListingItem(testShow("tt-new-show", "New Show", 2001))
	movieItems := []*listingItem{newMovie}
	showItems := []*listingItem{newShow}
	snapshot := buildLibrarySnapshot(radixListingBuilder{}, movieItems, showItems)
	movies := map[string]*papi.Movie{newMovie.media.ID: newMovie.movie}
	shows := map[string]*papi.Show{newShow.media.ID: newShow.show}

	client.publishLibrary(movies, shows, movieItems, showItems, snapshot)

	client.mu.RLock()
	defer client.mu.RUnlock()
	if !reflect.DeepEqual(client.movies, movies) || !reflect.DeepEqual(client.shows, shows) {
		t.Error("publication did not replace raw maps")
	}
	if !reflect.DeepEqual(client.movieItems, movieItems) || !reflect.DeepEqual(client.showItems, showItems) {
		t.Error("publication did not replace listing catalogs")
	}
	if !reflect.DeepEqual(client.snapshot, snapshot) {
		t.Error("publication did not replace the default snapshot")
	}
}

func TestListingItemDateAddedMovie(t *testing.T) {
	want := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.FixedZone("UTC+1", 3600))
	movie := testMovie("tt001", "Movie", 2001)
	//nolint:staticcheck // Keep the fixture path aligned with the upstream API graph.
	movie.VideoMetadata.DateAdded = want
	if got := newMovieListingItem(movie).dateAdded(); got != want {
		t.Errorf("dateAdded() = %v, want %v", got, want)
	}

	if got := newMovieListingItem(testMovie("tt002", "Zero", 2002)).dateAdded(); !got.IsZero() {
		t.Errorf("zero movie date = %v, want zero", got)
	}
}

func TestListingItemDateAddedShowUsesNewestEpisode(t *testing.T) {
	oldest := time.Date(2022, time.January, 1, 0, 0, 0, 0, time.UTC)
	middle := time.Date(2024, time.June, 1, 0, 0, 0, 0, time.UTC)
	want := time.Date(2025, time.December, 31, 0, 0, 0, 0, time.UTC)
	show := testShow("tt001", "Show", 2001)
	show.Seasons = map[int]*papi.Season{
		3: {Episodes: map[int]*papi.Episode{9: testEpisode(middle), 1: testEpisode(time.Time{})}},
		1: {Episodes: map[int]*papi.Episode{7: testEpisode(want), 2: testEpisode(oldest)}},
	}

	if got := newShowListingItem(show).dateAdded(); got != want {
		t.Errorf("dateAdded() = %v, want %v", got, want)
	}
}

func TestListingItemDateAddedMissing(t *testing.T) {
	zero := time.Time{}
	showWithoutSeasons := testShow("tt001", "Empty", 2001)
	showWithNilSeason := testShow("tt002", "Nil season", 2002)
	showWithNilSeason.Seasons = map[int]*papi.Season{1: nil}
	showWithNilEpisode := testShow("tt003", "Nil episode", 2003)
	showWithNilEpisode.Seasons = map[int]*papi.Season{1: {Episodes: map[int]*papi.Episode{1: nil}}}
	showWithZeroEpisode := testShow("tt004", "Zero episode", 2004)
	showWithZeroEpisode.Seasons = map[int]*papi.Season{1: {Episodes: map[int]*papi.Episode{1: testEpisode(zero)}}}

	for name, item := range map[string]*listingItem{
		"nil item":           nil,
		"empty wrapper":      {},
		"both raw sources":   {movie: testMovie("tt005", "Both", 2005), show: testShow("tt005", "Both", 2005)},
		"nil movie metadata": {movie: &papi.Movie{}},
		"no seasons":         newShowListingItem(showWithoutSeasons),
		"nil season":         newShowListingItem(showWithNilSeason),
		"nil episode":        newShowListingItem(showWithNilEpisode),
		"zero episode":       newShowListingItem(showWithZeroEpisode),
	} {
		t.Run(name, func(t *testing.T) {
			if got := item.dateAdded(); !got.IsZero() {
				t.Errorf("dateAdded() = %v, want zero", got)
			}
		})
	}
}

func TestDateAddedListingBuilderOrdersCanonicalItemsWithoutMutation(t *testing.T) {
	newest := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	tied := time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC)
	oldest := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)

	zero := newMovieListingItem(testMovie("tt005", "Zero", 2005))
	older := newMovieListingItem(testMovie("tt004", "Older", 2004))
	//nolint:staticcheck // Keep the fixture path aligned with the upstream API graph.
	older.movie.VideoMetadata.DateAdded = oldest
	latest := newMovieListingItem(testMovie("tt003", "The Latest", 2003))
	//nolint:staticcheck // Keep the fixture path aligned with the upstream API graph.
	latest.movie.VideoMetadata.DateAdded = newest
	alphaLast := newMovieListingItem(testMovie("tt002", "Alpha", 2002))
	//nolint:staticcheck // Keep the fixture path aligned with the upstream API graph.
	alphaLast.movie.VideoMetadata.DateAdded = tied
	alphaFirst := newMovieListingItem(testMovie("tt001", "Alpha", 2002))
	//nolint:staticcheck // Keep the fixture path aligned with the upstream API graph.
	alphaFirst.movie.VideoMetadata.DateAdded = tied
	items := []*listingItem{zero, older, latest, alphaLast, alphaFirst}
	original := append([]*listingItem(nil), items...)

	listing := (dateAddedListingBuilder{}).build(items)

	if !reflect.DeepEqual(items, original) {
		t.Fatal("date builder mutated its input slice")
	}
	root := listing.tree.Root()
	if root.SubtreeCount != len(items) || len(root.Children) != len(items) {
		t.Fatalf("flat root = (%d count, %d children), want (%d, %d)", root.SubtreeCount, len(root.Children), len(items), len(items))
	}
	wantIDs := []string{"tt003", "tt001", "tt002", "tt004", "tt005"}
	for i, wantID := range wantIDs {
		node := root.Children[i]
		if !node.IsTerminal || node.SubtreeCount != 1 || node.Media == nil || node.Media.ID != wantID {
			t.Errorf("child %d = %#v, want terminal media %s", i, node, wantID)
		}
	}
	if got := root.Children[0].FullString; got != "The Latest (2003)" {
		t.Errorf("newest title = %q, want canonical title", got)
	}
	if strings := listing.tree.AllStrings(); len(strings) != len(items) {
		t.Errorf("AllStrings() returned %d rows, want %d", len(strings), len(items))
	}
	for _, title := range listing.tree.AllStrings() {
		if title == "Latest (2003)" {
			t.Error("date builder added a stripped 'The' alias")
		}
	}
}

func TestDateAddedListingBuilderKeepsDuplicateRowsAndCatalogCollisionWinner(t *testing.T) {
	newerDate := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	olderDate := time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC)
	newer := newMovieListingItem(testMovie("tt001", "Duplicate", 2000))
	//nolint:staticcheck // Keep the fixture path aligned with the upstream API graph.
	newer.movie.VideoMetadata.DateAdded = newerDate
	older := newMovieListingItem(testMovie("tt002", "Duplicate", 2000))
	//nolint:staticcheck // Keep the fixture path aligned with the upstream API graph.
	older.movie.VideoMetadata.DateAdded = olderDate

	listing := (dateAddedListingBuilder{}).build([]*listingItem{newer, older})
	root := listing.tree.Root()

	if len(root.Children) != 2 {
		t.Fatalf("duplicate rows = %d, want 2", len(root.Children))
	}
	if root.Children[0].Media != newer.media || root.Children[1].Media != older.media {
		t.Errorf("duplicate row identities = (%p, %p), want (%p, %p)", root.Children[0].Media, root.Children[1].Media, newer.media, older.media)
	}
	if got := listing.titleToMedia["Duplicate (2000)"]; got != older.media {
		t.Errorf("title map winner = %p, want last catalog item %p", got, older.media)
	}
}

// TestSnapshotForConcurrentAccess tests concurrent access to snapshotFor
func TestSnapshotForConcurrentAccess(t *testing.T) {
	movieItems := []*listingItem{
		newMovieListingItem(testMovie("tt001", "First", 2001)),
		newMovieListingItem(testMovie("tt002", "Second", 2002)),
	}
	showItems := []*listingItem{
		newShowListingItem(testShow("tt003", "Third", 2003)),
	}
	client := &APIClient{
		movieItems: movieItems,
		showItems:  showItems,
		snapshot:   LibrarySnapshot{movieCount: len(movieItems), showCount: len(showItems)},
	}

	// Create multiple goroutines that call snapshotFor concurrently
	var wg sync.WaitGroup
	numGoroutines := 10

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			// Each goroutine calls snapshotFor with a different builder
			builder := radixListingBuilder{}
			snapshot := client.snapshotFor(builder)

			// Verify the snapshot is valid
			if snapshot.movieRadix == nil || snapshot.showRadix == nil {
				t.Errorf("snapshotFor returned invalid snapshot in goroutine %d", goroutineID)
			}
		}(i)
	}

	wg.Wait()
}

type pausedListingBuilder struct {
	started chan struct{}
	resume  <-chan struct{}
	once    sync.Once
}

func (b *pausedListingBuilder) build(items []*listingItem) mediaListing {
	b.once.Do(func() {
		close(b.started)
		<-b.resume
	})
	return (radixListingBuilder{}).build(items)
}

func TestSnapshotForConcurrentPublication(t *testing.T) {
	type version struct {
		movies     map[string]*papi.Movie
		shows      map[string]*papi.Show
		movieItems []*listingItem
		showItems  []*listingItem
		snapshot   LibrarySnapshot
	}
	makeVersion := func(prefix string) version {
		v := version{movies: make(map[string]*papi.Movie), shows: make(map[string]*papi.Show)}
		for _, suffix := range []string{"First", "Second"} {
			movie := newMovieListingItem(testMovie(prefix+"-movie-"+suffix, prefix+" Movie "+suffix, 2001))
			show := newShowListingItem(testShow(prefix+"-show-"+suffix, prefix+" Show "+suffix, 2002))
			v.movies[movie.media.ID], v.shows[show.media.ID] = movie.movie, show.show
			v.movieItems = append(v.movieItems, movie)
			v.showItems = append(v.showItems, show)
		}
		v.snapshot = buildLibrarySnapshot(radixListingBuilder{}, v.movieItems, v.showItems)
		return v
	}
	versions := []version{makeVersion("Alpha"), makeVersion("Beta")}
	client := NewEmptyAPIClient(nil)
	publish := func(v version) {
		client.publishLibrary(v.movies, v.shows, v.movieItems, v.showItems, v.snapshot)
	}
	check := func(got LibrarySnapshot, want version) error {
		if got.movieRadix == nil || got.showRadix == nil || got.movieRadix.Root() == nil || got.showRadix.Root() == nil ||
			got.movieCount != len(want.movieItems) || got.showCount != len(want.showItems) ||
			got.movieRadix.Root().SubtreeCount != len(want.movieItems) || got.showRadix.Root().SubtreeCount != len(want.showItems) ||
			len(got.titleToMedia) != len(want.movieItems)+len(want.showItems) {
			return fmt.Errorf("invalid roots, counts or title map: %+v", got)
		}
		for _, section := range []struct {
			tree  *RadixTree
			items []*listingItem
		}{{got.movieRadix, want.movieItems}, {got.showRadix, want.showItems}} {
			for _, item := range section.items {
				title := formatTitle(item.media.Title, item.media.Year)
				node := section.tree.Lookup(title)
				if node == nil || !node.IsTerminal || node.FullString != title || node.Media != item.media || got.titleToMedia[title] != item.media {
					return fmt.Errorf("inconsistent identity for %q: node=%+v map=%+v", title, node, got.titleToMedia[title])
				}
			}
		}
		return nil
	}
	publish(versions[0])

	// Stop a custom build after it copied both sections. Publishing the next
	// catalog must finish before that build resumes, without mixing generations.
	resume := make(chan struct{})
	release := sync.OnceFunc(func() { close(resume) })
	defer release()
	builder := &pausedListingBuilder{started: make(chan struct{}), resume: resume}
	oldSnapshot := make(chan LibrarySnapshot, 1)
	go func() { oldSnapshot <- client.snapshotFor(builder) }()
	select {
	case <-builder.started:
	case <-time.After(5 * time.Second):
		t.Fatal("snapshot builder did not start")
	}
	published := make(chan struct{})
	go func() {
		publish(versions[1])
		close(published)
	}()
	select {
	case <-published:
	case <-time.After(5 * time.Second):
		t.Fatal("publication blocked while a snapshot was being built")
	}
	if err := check(client.getSnapshot(), versions[1]); err != nil {
		t.Fatalf("published snapshot: %v", err)
	}
	release()
	select {
	case snapshot := <-oldSnapshot:
		if err := check(snapshot, versions[0]); err != nil {
			t.Fatalf("in-flight snapshot: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("snapshot builder did not finish")
	}
	if err := check(client.getSnapshot(), versions[1]); err != nil {
		t.Fatalf("in-flight custom build changed the published snapshot: %v", err)
	}

	// Race readers against repeated publication; each independent snapshot must
	// describe one complete catalog, even if consecutive reads see different ones.
	stop, writing, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; ; i++ {
			publish(versions[i%len(versions)])
			if i == 0 {
				close(writing)
			}
			select {
			case <-stop:
				return
			default:
			}
		}
	}()
	<-writing
	var readers sync.WaitGroup
	for range 4 {
		readers.Go(func() {
			for range 100 {
				for _, snapshot := range []LibrarySnapshot{client.getSnapshot(), client.snapshotFor(radixListingBuilder{})} {
					if errA, errB := check(snapshot, versions[0]), check(snapshot, versions[1]); errA != nil && errB != nil {
						t.Errorf("snapshot matches neither published catalog: %v; %v", errA, errB)
						return
					}
				}
			}
		})
	}
	readers.Wait()
	close(stop)
	<-done
}
