package app

import (
	"bytes"
	"errors"
	"image"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gioui.org/op/paint"
	"gioui.org/widget"
	"github.com/gregdel/pepe/internal/driver"
	"github.com/odwrtw/polochon/lib/papi"
)

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for condition")
		}
		time.Sleep(time.Millisecond)
	}
}

func refreshTestClient(items []*listingItem) *APIClient {
	movies := make(map[string]*papi.Movie, len(items))
	for _, item := range items {
		movies[item.media.ID] = item.movie
	}
	return &APIClient{
		movies:     movies,
		shows:      make(map[string]*papi.Show),
		movieItems: items,
		snapshot:   buildLibrarySnapshot(radixListingBuilder{}, items, nil),
	}
}

func TestSSEWakesIdleEventLoopAndRefreshes(t *testing.T) {
	events := make(chan struct{}, 1)
	invalidated := make(chan struct{}, 2)
	a := New(NewEmptyAPIClient(nil), events, nil, func() { invalidated <- struct{}{} }, nil)
	defer a.Close()

	started := make(chan struct{}, 1)
	a.refreshLibraryFn = func() error {
		started <- struct{}{}
		return nil
	}
	events <- struct{}{}
	select {
	case <-invalidated:
	case <-time.After(time.Second):
		t.Fatal("SSE did not invalidate the idle UI")
	}
	select {
	case <-started:
		t.Fatal("SSE forwarder mutated app state outside the event loop")
	default:
	}

	a.drainChannels()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("SSE notification did not start a refresh")
	}
	waitFor(t, func() bool { return len(a.refreshDone) == 1 })
	a.drainRefresh()
	if a.refreshing {
		t.Fatal("refresh remained active after completion")
	}
}

func TestSSEDuringRefreshCoalescesAndFollowsUpAfterFailure(t *testing.T) {
	for _, mode := range []ListingMode{ListingModeTitle, ListingModeDateAdded} {
		t.Run(listingModeLabel(mode), func(t *testing.T) {
			item := newMovieListingItem(testMovie("tt100", "Selected", 2001))
			client := refreshTestClient([]*listingItem{item})
			events := make(chan struct{}, 8)
			invalidated := make(chan struct{}, 8)
			a := New(client, events, nil, func() { invalidated <- struct{}{} }, nil)
			defer a.Close()
			a.applyListingMode(mode)
			a.browse.nav.resetToSection(SectionMovies, a.sectionRoot(SectionMovies))
			a.detail = &detailScreen{kind: detailMovie, movieID: "tt100"}

			firstStarted := make(chan struct{}, 1)
			firstRelease := make(chan struct{})
			secondStarted := make(chan struct{}, 1)
			secondRelease := make(chan struct{})
			var calls atomic.Int32
			a.refreshLibraryFn = func() error {
				switch calls.Add(1) {
				case 1:
					firstStarted <- struct{}{}
					<-firstRelease
					return errors.New("temporary failure")
				case 2:
					secondStarted <- struct{}{}
					<-secondRelease
					refreshed := newMovieListingItem(testMovie("tt100", "Selected Updated", 2001))
					client.publishLibrary(map[string]*papi.Movie{"tt100": refreshed.movie}, nil, []*listingItem{refreshed}, nil, buildLibrarySnapshot(radixListingBuilder{}, []*listingItem{refreshed}, nil))
					return nil
				default:
					return errors.New("unexpected extra refresh")
				}
			}

			a.startRefresh()
			<-firstStarted
			for range 8 {
				events <- struct{}{}
			}
			waitFor(t, func() bool { return len(invalidated) == 8 })
			// Drain the bounded signal more than once to cover a concurrently
			// delivered burst; the pending state remains a single follow-up.
			a.drainChannels()
			a.drainChannels()
			if !a.refreshPending {
				t.Fatal("in-flight SSE event was not marked pending")
			}
			if got := calls.Load(); got != 1 {
				t.Fatalf("concurrent refresh count = %d, want 1", got)
			}

			close(firstRelease)
			waitFor(t, func() bool { return len(a.refreshDone) == 1 })
			a.drainRefresh()
			select {
			case <-secondStarted:
			case <-time.After(time.Second):
				t.Fatal("pending SSE change did not start a follow-up refresh")
			}
			if a.detail == nil || a.detail.movieID != "tt100" || a.browse.nav.getSelectedChild().Media.ID != "tt100" {
				t.Fatal("failed refresh changed detail or selection before the follow-up")
			}
			if got := calls.Load(); got != 2 {
				t.Fatalf("refresh count after burst = %d, want exactly 2", got)
			}

			close(secondRelease)
			waitFor(t, func() bool { return len(a.refreshDone) == 1 })
			a.drainRefresh()
			if a.detail != nil {
				t.Fatal("successful refresh should retain existing detail-dismissal behavior")
			}
			selected := a.browse.nav.getSelectedChild()
			if selected == nil || selected.Media == nil || selected.Media.ID != "tt100" {
				t.Fatalf("selection after refresh = %#v, want tt100", selected)
			}
			if got := a.browse.library.mode; got != mode {
				t.Fatalf("listing mode after refresh = %v, want %v", got, mode)
			}
			if got := calls.Load(); got != 2 {
				t.Fatalf("refresh count after completion = %d, want exactly 2", got)
			}
		})
	}
}

func TestHTTPRefreshFailurePreservesCatalogAndUI(t *testing.T) {
	const initialMovies = `{"tt001":{"title":"Up","date_added":"2024-01-01T00:00:00Z"},"tt002":{"title":"Up in the Air","year":2009,"date_added":"2025-01-01T00:00:00Z"}}`
	const changedMovies = `{"tt001":{"title":"Revised","date_added":"2026-01-01T00:00:00Z"},"tt999":{"title":"New Movie"}}`
	const initialShows = `{"tt101":{"title":"Fixture Show","year":2022,"seasons":{"1":{"1":{"title":"Pilot","date_added":"2025-01-01T00:00:00Z"}}}}}`
	const changedShows = `{"tt201":{"title":"New Show","seasons":{"1":{"1":{"title":"New Pilot"}}}}}`

	for _, mode := range []ListingMode{ListingModeTitle, ListingModeDateAdded} {
		t.Run(listingModeLabel(mode), func(t *testing.T) {
			requests := make(chan string, 6)
			var movies, shows atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				select {
				case requests <- r.URL.Path:
				default:
					t.Error("too many catalog requests")
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				if r.Header.Get("X-Auth-Token") != "fixture-token" {
					t.Error("catalog request did not receive the token")
				}
				switch r.URL.Path {
				case "/movies":
					if movies.Add(1) == 1 {
						_, _ = w.Write([]byte(initialMovies))
					} else {
						_, _ = w.Write([]byte(changedMovies))
					}
				case "/shows":
					switch shows.Add(1) {
					case 1:
						_, _ = w.Write([]byte(initialShows))
					case 2:
						http.Error(w, `{"error":"fixture unavailable"}`, http.StatusServiceUnavailable)
					default:
						_, _ = w.Write([]byte(changedShows))
					}
				default:
					t.Errorf("unexpected catalog request: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()

			var logs bytes.Buffer
			client, err := NewAPIClient(server.URL, "fixture-token", slog.New(slog.NewTextHandler(&logs, nil)))
			if err != nil {
				t.Fatal(err)
			}
			if err := client.FetchLibrary(); err != nil {
				t.Fatal(err)
			}
			oldMovie, movieOK := client.getMovie("tt001")
			oldShow, showOK := client.getShow("tt101")
			if !movieOK || !showOK || oldMovie.Title != "Up" || oldShow.Title != "Fixture Show" || len(oldShow.Seasons) != 1 {
				t.Fatal("initial HTTP catalog was not populated")
			}
			if newMovieListingItem(oldMovie).dateAdded().IsZero() || newShowListingItem(oldShow).dateAdded().IsZero() {
				t.Fatal("initial HTTP catalog did not decode movie and episode dates")
			}
			originalSnapshot := client.getSnapshot()
			client.mu.RLock()
			originalMovies := append([]*listingItem(nil), client.movieItems...)
			originalShows := append([]*listingItem(nil), client.showItems...)
			client.mu.RUnlock()
			invalidated := make(chan struct{}, 2)
			a := New(client, nil, nil, func() { invalidated <- struct{}{} }, nil)
			defer a.Close()
			// Populate the alternate snapshot even when Smart list is active.
			a.applyListingMode(ListingModeDateAdded)
			if mode == ListingModeTitle {
				a.applyListingMode(mode)
			}
			if !a.browse.nav.navigateToMedia(a.sectionRoot(SectionMovies), mediaKindMovie, "tt001") {
				t.Fatal("could not select the original movie")
			}
			nav := a.browse.nav
			navigation := *nav
			navigation.path = append([]*RadixNode(nil), nav.path...)
			view, selected, dateSnapshot := a.browse.library, a.selectedBrowseMedia(), a.dateAddedSnapshot
			if selected == nil || dateSnapshot == nil {
				t.Fatal("fixture did not retain the selected movie and date-added cache")
			}
			detail := &detailScreen{kind: detailMovie, movieID: "tt001"}
			a.detail = detail
			a.images.store("fixture:image", paint.NewImageOp(image.NewNRGBA(image.Rect(0, 0, 2, 3))))
			a.images.store("fixture:failed", paint.ImageOp{})
			imageGeneration, imageWeight := a.images.generation, a.images.cacheWeight
			clickable := new(widget.Clickable)
			a.clickables["fixture"] = clickable
			logs.Reset()

			waitResult := func() refreshResult {
				t.Helper()
				select {
				case result := <-a.refreshDone:
					select {
					case <-invalidated:
					case <-time.After(5 * time.Second):
						t.Fatal("refresh did not invalidate the UI")
					}
					return result
				case <-time.After(5 * time.Second):
					t.Fatal("refresh did not finish")
					return refreshResult{}
				}
			}
			checkRequests := func() {
				t.Helper()
				for _, want := range []string{"/movies", "/shows"} {
					select {
					case got := <-requests:
						if got != want {
							t.Fatalf("request = %q, want %q", got, want)
						}
					case <-time.After(5 * time.Second):
						t.Fatalf("missing request %q", want)
					}
				}
			}
			checkRequests() // Initial nonempty catalog.
			a.startRefresh()
			result := waitResult()
			checkRequests() // Changed movies succeeded; the following shows request failed.
			if result.err == nil || !strings.Contains(result.err.Error(), "failed to fetch shows") || !strings.Contains(result.err.Error(), "503") {
				t.Fatalf("refresh error = %v, want shows HTTP 503", result.err)
			}
			if result.snapshot.movieRadix != nil || result.snapshot.showRadix != nil || result.snapshot.titleToMedia != nil || result.snapshot.movieCount != 0 || result.snapshot.showCount != 0 {
				t.Fatal("failed refresh published a snapshot")
			}
			a.refreshDone <- result
			a.drainRefresh()
			if a.refreshing || a.refreshDone != nil || a.refreshError == nil || !strings.Contains(logs.String(), "failed to refresh library") {
				t.Fatal("failed refresh did not retain and log its error")
			}
			if a.browse.nav != nav || !reflect.DeepEqual(*a.browse.nav, navigation) || a.selectedBrowseMedia() != selected || a.detail != detail {
				t.Fatal("failed refresh changed navigation, selection or detail")
			}
			if !reflect.DeepEqual(a.browse.library, view) || a.dateAddedSnapshot != dateSnapshot || a.clickables["fixture"] != clickable {
				t.Fatal("failed refresh changed the displayed catalog or its UI caches")
			}
			if cached, ok := a.images.get("fixture:image"); !ok || cached.Size() != image.Pt(2, 3) {
				t.Fatal("failed refresh discarded the cached image")
			}
			if cached, ok := a.images.get("fixture:failed"); !ok || cached.Size() != (image.Point{}) || len(a.images.cache) != 2 || a.images.generation != imageGeneration || a.images.cacheWeight != imageWeight {
				t.Fatal("failed refresh changed the image failure sentinel or cache state")
			}
			if movie, ok := client.getMovie("tt001"); !ok || movie != oldMovie || movie.Title != "Up" {
				t.Fatal("failed refresh replaced the original movie")
			}
			if show, ok := client.getShow("tt101"); !ok || show != oldShow || show.Title != "Fixture Show" {
				t.Fatal("failed refresh replaced the original show")
			}
			if _, ok := client.getMovie("tt999"); ok {
				t.Fatal("failed refresh published the partially fetched movies")
			}
			client.mu.RLock()
			itemsUnchanged := reflect.DeepEqual(client.movieItems, originalMovies) && reflect.DeepEqual(client.showItems, originalShows)
			client.mu.RUnlock()
			if snapshot := client.getSnapshot(); !itemsUnchanged || snapshot.movieRadix != originalSnapshot.movieRadix || snapshot.showRadix != originalSnapshot.showRadix || !reflect.DeepEqual(snapshot, originalSnapshot) {
				t.Fatal("failed refresh changed the client catalog or default snapshot")
			}

			d, err := driver.New(driver.Config{Width: 800, Height: 80, PxPerDp: 1, PxPerSp: 1}, a.layoutHeaderStatus)
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			a.drainRefresh()
			if err := d.Frame(); err != nil {
				t.Fatal(err)
			}
			if !headlessHasLabel(d.Tree(), "Refresh failed") || !headlessHasLabel(d.Tree(), "2 movies · 1 shows") {
				t.Fatal("failure status did not persist alongside the original counts")
			}

			a.startRefresh()
			if !a.refreshing || a.refreshError != nil {
				t.Fatal("retry did not clear the failure state")
			}
			if err := d.Frame(); err != nil {
				t.Fatal(err)
			}
			if !headlessHasLabel(d.Tree(), "Refreshing…") || headlessHasLabel(d.Tree(), "Refresh failed") {
				t.Fatal("retry did not replace the failure status")
			}
			result = waitResult()
			checkRequests()
			select {
			case path := <-requests:
				t.Fatalf("unexpected extra request: %s", path)
			default:
			}
			if result.err != nil {
				t.Fatalf("retry failed: %v", result.err)
			}
			a.refreshDone <- result
			a.drainRefresh()
			if a.refreshing || a.refreshError != nil || a.detail != nil || a.selectedBrowseMedia() == nil || a.selectedBrowseMedia().Title != "Revised" {
				t.Fatal("successful retry did not apply the new catalog and restore selection")
			}
			if show, ok := client.getShow("tt201"); !ok || show.Title != "New Show" {
				t.Fatal("successful retry did not publish the new shows")
			}
			if _, ok := a.images.get("fixture:image"); ok {
				t.Fatal("successful retry did not reset stale artwork")
			}
			if err := d.Frame(); err != nil {
				t.Fatal(err)
			}
			if headlessHasLabel(d.Tree(), "Refresh failed") || headlessHasLabel(d.Tree(), "Refreshing…") {
				t.Fatal("successful retry left a refresh status in the header")
			}
		})
	}
}

func TestSSEClosedChannelStopsForwarder(t *testing.T) {
	events := make(chan struct{})
	var invalidations atomic.Int32
	a := New(NewEmptyAPIClient(nil), events, nil, func() { invalidations.Add(1) }, nil)
	close(events)
	select {
	case <-a.sseDone:
	case <-time.After(time.Second):
		t.Fatal("forwarder did not exit after SSE channel closed")
	}
	a.Close()
	a.drainChannels()
	if got := invalidations.Load(); got != 0 {
		t.Fatalf("invalidation count after channel close = %d, want 0", got)
	}
	if a.refreshing {
		t.Fatal("closed SSE channel started a refresh")
	}
}

func TestInitialLoadFailureRetriesUntilSuccess(t *testing.T) {
	invalidated := make(chan struct{}, 8)
	a := New(NewEmptyAPIClient(nil), nil, nil, func() { invalidated <- struct{}{} }, nil)
	defer a.Close()
	var calls atomic.Int32
	a.refreshLibraryFn = func() error {
		if calls.Add(1) < 3 {
			return errors.New("server down")
		}
		return nil
	}
	a.loadRetryDelay = time.Millisecond
	a.startWithLoadError(errors.New("initial failure"))
	if a.refreshError == nil || !a.loadPending || a.loadRetryDue == nil {
		t.Fatalf("initial state = (%v, %t, retry scheduled %t)", a.refreshError, a.loadPending, a.loadRetryDue != nil)
	}
	waitFor(t, func() bool {
		a.drainChannels()
		return !a.loadPending
	})
	if got := calls.Load(); got != 3 {
		t.Fatalf("fetch attempts = %d, want 3", got)
	}
	if a.refreshError != nil || a.loadRetryDue != nil || a.loadRetryTimer != nil || a.refreshing {
		t.Fatalf("after success = (%v, retry %t, refreshing %t)", a.refreshError, a.loadRetryDue != nil, a.refreshing)
	}
}

func TestInitialLoadRetryBackoffIsCapped(t *testing.T) {
	a := New(NewEmptyAPIClient(nil), nil, nil, nil, nil)
	defer a.Close()
	// loadRetryDelay holds the next delay; the first retry waits initialLoadRetryMin.
	var next []time.Duration
	for range 6 {
		a.scheduleLoadRetry()
		a.stopLoadRetry()
		next = append(next, a.loadRetryDelay)
	}
	want := []time.Duration{10 * time.Second, 20 * time.Second, 40 * time.Second, time.Minute, time.Minute, time.Minute}
	if !reflect.DeepEqual(next, want) {
		t.Fatalf("next retry delays = %v, want %v", next, want)
	}
}

func TestManualRefreshSuccessCancelsLoadRetry(t *testing.T) {
	a := New(NewEmptyAPIClient(nil), nil, nil, nil, nil)
	defer a.Close()
	a.refreshLibraryFn = func() error { return nil }
	a.startWithLoadError(errors.New("initial failure")) // first retry in 5s
	a.startRefresh()
	waitFor(t, func() bool {
		a.drainChannels()
		return !a.refreshing
	})
	if a.loadPending || a.loadRetryDue != nil || a.loadRetryTimer != nil || a.refreshError != nil {
		t.Fatalf("manual success left retry state = (%t, %t, %v)", a.loadPending, a.loadRetryDue != nil, a.refreshError)
	}
}

func TestDisplayServerHidesCredentials(t *testing.T) {
	for in, want := range map[string]string{
		"https://polochon.example":                                  "https://polochon.example",
		"http://user:secret@polochon.example:8080/api?token=x#frag": "http://polochon.example:8080/api",
		"not a url": "",
	} {
		if got := displayServer(in); got != want {
			t.Errorf("displayServer(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHeadlessInitialLoadFailureExplainsServer(t *testing.T) {
	client := NewEmptyAPIClient(nil)
	client.server = displayServer("https://user:secret@polochon.example")
	a := New(client, nil, &Config{MPVPath: "mpv"}, nil, nil)
	defer a.Close()
	a.refreshLibraryFn = func() error { return nil }
	a.startWithLoadError(errors.New("dial tcp: connection refused"))
	d, err := driver.New(driver.Config{Width: 1024, Height: 768, PxPerDp: 1, PxPerSp: 1}, a.Layout)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	tree := d.Tree()
	for _, label := range []string{"Server unreachable · retrying", "Can't reach the polochon server", "https://polochon.example", "dial tcp: connection refused", "Retrying in 5s"} {
		if !headlessHasLabel(tree, label) {
			t.Fatalf("load failure screen missing %q", label)
		}
	}
	if headlessHasLabel(tree, "0 items in the library") || headlessHasLabel(tree, "Refresh failed") {
		t.Fatal("load failure still looks like an empty library")
	}
	a.loadRetryAt = time.Now().Add(1500 * time.Millisecond)
	if err := d.Frame(); err != nil {
		t.Fatal(err)
	}
	if !headlessHasLabel(d.Tree(), "Retrying in 2s") {
		t.Fatal("countdown does not round up to whole seconds")
	}

	a.startRefresh()
	waitFor(t, func() bool {
		a.drainChannels()
		return !a.loadPending
	})
	if err := d.Frame(); err != nil {
		t.Fatal(err)
	}
	if tree := d.Tree(); headlessHasLabel(tree, "Can't reach the polochon server") || !headlessHasLabel(tree, "0 items in the library") {
		t.Fatal("successful load did not restore the normal section hero")
	}
}
