package app

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

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
			if mode != ListingModeTitle {
				a.applyListingMode(mode)
			}
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
