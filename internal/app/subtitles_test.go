package app

import (
	"encoding/json"
	"fmt"
	"image"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gioui.org/layout"
	polochon "github.com/odwrtw/polochon/lib"
)

func subtitleEntries(count int) []*polochon.SubtitleEntry {
	entries := make([]*polochon.SubtitleEntry, count)
	for i := range entries {
		entries[i] = &polochon.SubtitleEntry{
			ID: fmt.Sprintf("subtitle-%02d", i), Language: polochon.EN,
			Source: "Fixture", Description: fmt.Sprintf("Release %02d", i),
		}
	}
	return entries
}

func assertSubtitleVisible(t *testing.T, f *headlessFixture) {
	t.Helper()
	p := f.app.subPopup
	pos := p.list.Position
	last := pos.First + pos.Count - 1
	if p.cursor < pos.First || p.cursor > last || pos.Count == 0 ||
		(p.cursor == pos.First && pos.Offset > 0) ||
		(p.cursor == last && pos.OffsetLast < 0) {
		t.Fatalf("subtitle cursor %d not fully visible: %+v", p.cursor, pos)
	}
	if !headlessHasLabel(f.driver.Tree(), p.entries[p.cursor].Description) {
		t.Fatalf("selected subtitle %d missing from rendered tree", p.cursor)
	}
}

func TestHeadlessSubtitleCursorVisibility(t *testing.T) {
	for _, viewport := range []image.Point{image.Pt(1920, 1080), image.Pt(1024, 768)} {
		t.Run(fmt.Sprintf("%dx%d", viewport.X, viewport.Y), func(t *testing.T) {
			f := newHeadlessFixture(t)
			p := &SubtitlePopup{
				video: testMovie("movie-new", "New Movie", 2026),
				state: SubtitlePopupReady, entries: subtitleEntries(60),
				list: layout.List{Axis: layout.Vertical},
			}
			f.app.subPopup = p
			if err := f.driver.Resize(viewport.X, viewport.Y); err != nil {
				t.Fatal(err)
			}
			if p.list.Position.Count >= len(p.entries) {
				t.Fatal("fixture must overflow the subtitle list")
			}
			assertSubtitleVisible(t, f)
			for i := 1; i < len(p.entries); i++ {
				headlessKey(t, f, "DownArrow")
				if p.cursor != i {
					t.Fatalf("cursor = %d, want %d", p.cursor, i)
				}
				assertSubtitleVisible(t, f)
			}
			for range 3 {
				headlessKey(t, f, "DownArrow")
				if p.cursor != len(p.entries)-1 {
					t.Fatal("Down moved beyond the final subtitle")
				}
				assertSubtitleVisible(t, f)
			}
			for i := len(p.entries) - 2; i >= 0; i-- {
				headlessKey(t, f, "UpArrow")
				if p.cursor != i {
					t.Fatalf("cursor = %d, want %d", p.cursor, i)
				}
				assertSubtitleVisible(t, f)
			}
			for range 3 {
				headlessKey(t, f, "UpArrow")
				if p.cursor != 0 {
					t.Fatal("Up moved before the first subtitle")
				}
				assertSubtitleVisible(t, f)
			}
			for range 30 {
				headlessKey(t, f, "DownArrow")
			}
			// Shrink the result viewport without moving the cursor.
			if err := f.driver.Resize(800, 400); err != nil {
				t.Fatal(err)
			}
			assertSubtitleVisible(t, f)
		})
	}
}

func TestHeadlessSubtitleEmptyAndShortLists(t *testing.T) {
	for _, count := range []int{0, 1, 3} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			f := newHeadlessFixture(t)
			p := &SubtitlePopup{
				video: testMovie("movie-new", "New Movie", 2026),
				state: SubtitlePopupReady, entries: subtitleEntries(count),
				list: layout.List{Axis: layout.Vertical},
			}
			f.app.subPopup = p
			for range 5 {
				headlessKey(t, f, "DownArrow")
				if count > 0 {
					assertSubtitleVisible(t, f)
				}
			}
			for range 5 {
				headlessKey(t, f, "UpArrow")
			}
			if p.cursor != 0 || p.list.Position.First != 0 || p.list.Position.Offset != 0 {
				t.Fatalf("short list did not remain at top: cursor=%d position=%+v", p.cursor, p.list.Position)
			}
			if count == 0 {
				headlessKey(t, f, "Enter")
				if p.state != SubtitlePopupReady || p.downloadDone != nil {
					t.Fatal("empty list started a download")
				}
			}
		})
	}
}

func TestHeadlessSubtitleHTTPFailures(t *testing.T) {
	for _, operation := range []string{"search", "download"} {
		t.Run(operation, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/movies/movie-new/subtitles/"+string(polochon.EN)+"/available" {
					t.Errorf("unexpected subtitle path: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				if operation == "download" && r.Method == http.MethodGet {
					_ = json.NewEncoder(w).Encode(subtitleEntries(1))
					return
				}
				w.WriteHeader(http.StatusBadGateway)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": operation + " failed"})
			}))
			defer server.Close()
			client, err := NewAPIClient(server.URL, "fixture-token", nil)
			if err != nil {
				t.Fatal(err)
			}
			f := newHeadlessFixture(t)
			f.app.client.client = client.client
			f.app.pushMovieDetail(&MediaItem{ID: "movie-new", Type: mediaKindMovie})
			detail := f.app.detail
			headlessKey(t, f, "S")
			p := f.app.subPopup
			waitForCache(t, func() bool { return len(p.resultCh) == 1 })
			if err := f.driver.Frame(); err != nil {
				t.Fatal(err)
			}
			if operation == "download" {
				if p.state != SubtitlePopupReady || len(p.entries) != 1 {
					t.Fatalf("search state = %v, entries = %v", p.state, p.entries)
				}
				headlessKey(t, f, "Enter")
				waitForCache(t, func() bool { return len(p.downloadDone) == 1 })
			}
			// Drain the failure, then verify that another frame retains it.
			for range 2 {
				if err := f.driver.Frame(); err != nil {
					t.Fatal(err)
				}
				if f.app.subPopup != p || p.state != SubtitlePopupError ||
					!strings.Contains(p.errMsg, operation+" failed") || p.downloadDone != nil ||
					!headlessHasLabel(f.driver.Tree(), "Error: "+p.errMsg) {
					t.Fatalf("subtitle failure state = %+v", p)
				}
			}
			dismiss := "Enter"
			if operation == "download" {
				dismiss = "Escape"
			}
			headlessKey(t, f, dismiss)
			if f.app.subPopup != nil || f.app.detail != detail || f.app.playerStatus != PlayerStatusIdle {
				t.Fatal("dismissing subtitle failure changed detail or started playback")
			}
		})
	}
}

func TestHeadlessSubtitleCloseReopenIgnoresStaleResults(t *testing.T) {
	for _, operation := range []string{"search", "download"} {
		for _, fail := range []bool{false, true} {
			for _, oldFirst := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/fail=%t/old-first=%t", operation, fail, oldFirst), func(t *testing.T) {
					oldRelease, newRelease := make(chan struct{}, 1), make(chan struct{}, 1)
					oldStarted, newStarted := make(chan struct{}, 1), make(chan struct{}, 1)
					oldEntries, newEntries := subtitleEntries(1), subtitleEntries(2)
					oldEntries[0].Description = "Stale search result"
					oldPath := "/movies/movie-old/subtitles/" + string(polochon.EN) + "/available"
					newPath := "/movies/movie-new/subtitles/" + string(polochon.EN) + "/available"
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						switch {
						case r.URL.Path == oldPath:
							if operation == "search" || r.Method == http.MethodPost {
								oldStarted <- struct{}{}
								<-oldRelease
								if fail {
									w.WriteHeader(http.StatusBadGateway)
									_, _ = fmt.Fprint(w, `{"error":"stale request failed"}`)
									return
								}
							}
							if r.Method == http.MethodGet {
								_ = json.NewEncoder(w).Encode(oldEntries)
							} else {
								w.WriteHeader(http.StatusNoContent)
							}
						case r.URL.Path == newPath && r.Method == http.MethodGet:
							newStarted <- struct{}{}
							<-newRelease
							_ = json.NewEncoder(w).Encode(newEntries)
						default:
							t.Errorf("unexpected subtitle request: %s %s", r.Method, r.URL.Path)
							w.WriteHeader(http.StatusNotFound)
						}
					}))
					t.Cleanup(server.Close)
					// Unblock handlers even when an assertion fails before release.
					t.Cleanup(func() { close(oldRelease); close(newRelease) })
					client, err := NewAPIClient(server.URL, "fixture-token", nil)
					if err != nil {
						t.Fatal(err)
					}
					f := newHeadlessFixture(t)
					f.app.client.client = client.client
					f.app.pushMovieDetail(&MediaItem{ID: "movie-old", Type: mediaKindMovie})
					headlessKey(t, f, "S")
					old := f.app.subPopup
					if operation == "download" {
						waitForCache(t, func() bool { return len(old.resultCh) == 1 })
						if err := f.driver.Frame(); err != nil {
							t.Fatal(err)
						}
						headlessKey(t, f, "Enter")
					}
					waitForCache(t, func() bool { return len(oldStarted) == 1 })
					headlessKey(t, f, "Escape")
					if f.app.subPopup != nil {
						t.Fatal("Escape did not close in-flight popup")
					}
					f.app.pushMovieDetail(&MediaItem{ID: "movie-new", Type: mediaKindMovie})
					headlessKey(t, f, "S")
					current := f.app.subPopup
					if current == nil || current == old {
						t.Fatal("reopen did not create a new popup")
					}
					waitForCache(t, func() bool { return len(newStarted) == 1 })
					finishOld := func() {
						t.Helper()
						oldRelease <- struct{}{}
						waitForCache(t, func() bool {
							if operation == "download" {
								return len(old.downloadDone) == 1
							}
							return len(old.resultCh) == 1
						})
						if err := f.driver.Frame(); err != nil {
							t.Fatal(err)
						}
					}
					if oldFirst {
						finishOld()
						if f.app.subPopup != current || current.state != SubtitlePopupLoading ||
							len(current.entries) != 0 || current.errMsg != "" {
							t.Fatal("old result changed the loading replacement popup")
						}
					}
					newRelease <- struct{}{}
					waitForCache(t, func() bool { return len(current.resultCh) == 1 })
					if err := f.driver.Frame(); err != nil {
						t.Fatal(err)
					}
					if !oldFirst {
						finishOld()
					}
					if f.app.subPopup != current || current.state != SubtitlePopupReady ||
						current.errMsg != "" || current.downloadDone != nil || !current.doneAt.IsZero() ||
						len(current.entries) != 2 || *current.entries[0] != *newEntries[0] ||
						!headlessHasLabel(f.driver.Tree(), newEntries[1].Description) ||
						headlessHasLabel(f.driver.Tree(), oldEntries[0].Description) {
						t.Fatalf("replacement popup was changed by old %s result: %+v", operation, current)
					}
				})
			}
		}
	}
}

func TestHeadlessSubtitleDownloadsVisibleSelection(t *testing.T) {
	entries := subtitleEntries(60)
	type downloadRequest struct {
		path  string
		entry polochon.SubtitleEntry
		err   error
	}
	requests := make(chan downloadRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if err := json.NewEncoder(w).Encode(entries); err != nil {
				t.Error(err)
			}
		case http.MethodPost:
			request := downloadRequest{path: r.URL.Path}
			request.err = json.NewDecoder(r.Body).Decode(&request.entry)
			requests <- request
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()
	client, err := NewAPIClient(server.URL, "fixture-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	f := newHeadlessFixture(t)
	f.app.client.client = client.client
	f.app.pushMovieDetail(&MediaItem{ID: "movie-new", Type: mediaKindMovie})
	headlessKey(t, f, "S")
	p := f.app.subPopup
	if p == nil {
		t.Fatal("subtitle action did not open popup")
	}
	// Wait on the result channel, then let the UI thread drain it normally.
	select {
	case result := <-p.resultCh:
		p.resultCh <- result
	case <-time.After(5 * time.Second):
		t.Fatal("subtitle search did not complete")
	}
	if err := f.driver.Frame(); err != nil {
		t.Fatal(err)
	}
	if p.state != SubtitlePopupReady {
		t.Fatalf("subtitle search state = %v: %s", p.state, p.errMsg)
	}
	for range 30 {
		headlessKey(t, f, "DownArrow")
	}
	assertSubtitleVisible(t, f)
	want := *p.entries[p.cursor]
	headlessKey(t, f, "Enter")
	select {
	case request := <-requests:
		if request.err != nil || request.path != "/movies/movie-new/subtitles/"+string(polochon.EN)+"/available" || request.entry != want {
			t.Fatalf("download = %+v, want entry %+v on movie English endpoint", request, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("subtitle download was not requested")
	}
	select {
	case result := <-p.downloadDone:
		p.downloadDone <- result
	case <-time.After(5 * time.Second):
		t.Fatal("subtitle download did not complete")
	}
	if err := f.driver.Frame(); err != nil {
		t.Fatal(err)
	}
	if p.state != SubtitlePopupDone {
		t.Fatalf("subtitle completion state = %v: %s", p.state, p.errMsg)
	}
}
