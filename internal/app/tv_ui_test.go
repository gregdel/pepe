package app

import (
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"gioui.org/layout"
	"github.com/gregdel/pepe/internal/driver"
	"github.com/odwrtw/polochon/lib/papi"
)

func tvCapture(t *testing.T, f *headlessFixture, name string) {
	t.Helper()
	if dir := os.Getenv("PEPE_UI_SCREENSHOT_DIR"); dir != "" {
		if err := f.driver.Screenshot(filepath.Join(dir, name+".png")); err != nil {
			t.Fatal(err)
		}
	}
}

func tvAssertSafeButton(t *testing.T, f *headlessFixture, label string) {
	t.Helper()
	size := f.driver.Size()
	// Layout uses 24dp edge spacing at the 1920dp reference width.
	margin := size.X * 24 / 1920
	safe := image.Rect(margin, margin, size.X-margin, size.Y-margin)
	var find func([]driver.Node) bool
	find = func(nodes []driver.Node) bool {
		for _, node := range nodes {
			if node.Class == "button" && node.Label == label {
				b := node.Bounds
				if !image.Rect(b.MinX, b.MinY, b.MaxX, b.MaxY).In(safe) {
					t.Fatalf("%q bounds %+v outside content area %v", label, b, safe)
				}
				return true
			}
			if find(node.Children) {
				return true
			}
		}
		return false
	}
	if !find(f.driver.Tree()) {
		t.Fatalf("missing button %q", label)
	}
}

func tvAssertPopupHintsAtBottom(t *testing.T, f *headlessFixture) {
	t.Helper()
	var check func([]driver.Node)
	check = func(nodes []driver.Node) {
		for _, node := range nodes {
			if node.Label == "navigate" && node.Bounds.MinY < f.driver.Size().Y/2 {
				t.Fatalf("popup help rendered at top: %+v", node.Bounds)
			}
			check(node.Children)
		}
	}
	check(f.driver.Tree())
}

func TestTVMockFullscreenPlaybackReturnsToBrowse(t *testing.T) {
	f := newHeadlessFixture(t)
	client, err := NewHeadlessPlaybackClient(nil)
	if err != nil {
		t.Fatal(err)
	}
	f.app.client.client = client.client
	dir := t.TempDir()
	argsPath := filepath.Join(dir, "args")
	playerPath := filepath.Join(dir, "mock-mpv")
	t.Setenv("PEPE_TV_MPV_ARGS", argsPath)
	if err := os.WriteFile(playerPath, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$PEPE_TV_MPV_ARGS\"\nsleep 0.15\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	f.app.mpvPath = playerPath
	f.app.playerFullscreen = true
	f.app.applyListingMode(ListingModeDateAdded)
	headlessKey(t, f, "RightArrow")
	headlessKey(t, f, "RightArrow")
	headlessKey(t, f, "Enter")
	if f.app.playerStatus != PlayerStatusRunning {
		t.Fatalf("mock launch failed: %s", f.app.playerError)
	}
	done := f.app.playerDone
	t.Cleanup(func() {
		if f.app.playerStop != nil {
			close(f.app.playerStop)
		}
	})
	waitForCache(t, func() bool { return len(done) == 1 })
	if err := f.driver.Frame(); err != nil {
		t.Fatal(err)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "\n--fs\n") || !strings.Contains(string(args), "\n--no-border\n") {
		t.Fatalf("fullscreen flags missing: %s", args)
	}
	if f.app.playerStatus != PlayerStatusIdle {
		t.Fatalf("mock playback did not complete: %s", f.app.playerError)
	}
	headlessKey(t, f, "LeftArrow")
	media := f.app.selectedBrowseMedia()
	if f.app.detail != nil || f.app.browse.focus != browseFocusContent || media == nil || media.ID != "movie-new" {
		t.Fatal("return from playback lost browse focus/selection")
	}
}

func TestTVBrowseLayout(t *testing.T) {
	for _, size := range []image.Point{image.Pt(1920, 1080), image.Pt(1280, 720)} {
		t.Run(fmt.Sprintf("%dx%d", size.X, size.Y), func(t *testing.T) {
			f := newHeadlessFixture(t)
			movies := make([]*listingItem, 26)
			movieMap := make(map[string]*papi.Movie)
			for i := range movies {
				id := fmt.Sprintf("tv-%02d", i)
				movie := testMovie(id, fmt.Sprintf("%c %s", 'A'+i, strings.Repeat("Long title ", 8)), 2026)
				movies[i] = newMovieListingItem(movie)
				movieMap[id] = movie
			}
			snapshot := buildLibrarySnapshot(radixListingBuilder{}, movies, nil)
			f.app.client.publishLibrary(movieMap, nil, movies, nil, snapshot)
			f.app.applyLibrarySnapshot(snapshot)
			if err := f.driver.Resize(size.X, size.Y); err != nil {
				t.Fatal(err)
			}
			tvAssertSafeButton(t, f, "Movies")
			tvAssertSafeButton(t, f, "Smart list")
			tvCapture(t, f, fmt.Sprintf("home-%d", size.X))
			headlessKey(t, f, "LeftArrow")
			// Labels are visible in addition to the button's accessibility label.
			if !headlessHasLabel(f.driver.Tree(), "Last added") {
				t.Fatal("expanded mode rail has no labels")
			}
			tvAssertSafeButton(t, f, "Last added")
			tvCapture(t, f, fmt.Sprintf("rail-%d", size.X))
			headlessKey(t, f, "RightArrow")
			headlessKey(t, f, "RightArrow")
			if pos := f.app.leftList.Position; pos.Count < 12 || pos.Count > 15 {
				t.Fatalf("visible rows = %d, expected TV-sized density", pos.Count)
			}
			for range 25 {
				node := f.app.browse.nav.getSelectedChild()
				tvAssertSafeButton(t, f, node.FullString)
				headlessKey(t, f, "DownArrow")
			}
			tvCapture(t, f, fmt.Sprintf("terminal-missing-art-%d", size.X))
			f.app.browse.nav.returnToHome()
			f.app.applyLibrarySnapshot(buildLibrarySnapshot(radixListingBuilder{}, []*listingItem{
				newMovieListingItem(testMovie("a1", "Alpha", 2026)),
				newMovieListingItem(testMovie("a2", "Alpine", 2026)),
			}, nil))
			headlessKey(t, f, "RightArrow")
			for _, label := range []string{"Title group", "Inside this group · largest groups first", "narrow this group"} {
				if headlessHasLabel(f.driver.Tree(), label) {
					t.Fatalf("redundant group explanation %q", label)
				}
			}
			for _, label := range []string{"Alp", "Alpha (2026)", "Alpine (2026)"} {
				if !headlessHasLabel(f.driver.Tree(), label) {
					t.Fatalf("missing group content %q", label)
				}
			}
			tvCapture(t, f, fmt.Sprintf("radix-group-%d", size.X))
			headlessKey(t, f, "RightArrow")
			if !headlessHasLabel(f.driver.Tree(), "Movies / Alp") {
				t.Fatal("accumulated prefix absent from header")
			}
		})
	}
}

func TestTVMovieContentBalancesAndScrolls(t *testing.T) {
	for _, size := range []image.Point{image.Pt(1920, 1080), image.Pt(1280, 720)} {
		t.Run(fmt.Sprintf("%dx%d", size.X, size.Y), func(t *testing.T) {
			f := newHeadlessFixture(t)
			f.app.pushMovieDetail(&MediaItem{ID: "movie-new", Title: "New Movie", Type: mediaKindMovie})
			if err := f.driver.Resize(size.X, size.Y); err != nil {
				t.Fatal(err)
			}
			// The movie content list is the only scroll region while idle.
			listBounds := func() image.Rectangle {
				var bounds image.Rectangle
				var walk func([]driver.Node)
				walk = func(nodes []driver.Node) {
					for _, node := range nodes {
						if slices.Contains(node.Gestures, "scroll") {
							b := node.Bounds
							bounds = bounds.Union(image.Rect(b.MinX, b.MinY, b.MaxX, b.MaxY))
						}
						walk(node.Children)
					}
				}
				walk(f.driver.Tree())
				return bounds
			}
			short := listBounds()
			centerY := (short.Min.Y + short.Max.Y) / 2
			if short.Empty() || centerY < size.Y*2/5 || centerY > size.Y*3/5 {
				t.Fatalf("short movie content is not vertically balanced: %v", short)
			}
			f.app.client.movies["movie-new"].Plot = strings.Repeat("A lengthy synopsis with enough words to require scrolling. ", 80)
			if err := f.driver.Frame(); err != nil {
				t.Fatal(err)
			}
			long := listBounds()
			if long.Dy() <= short.Dy() || long.Max.Y >= size.Y {
				t.Fatalf("long movie panel is not viewport-bounded: short %v, long %v", short, long)
			}
			for range 8 {
				headlessKey(t, f, "DownArrow")
			}
			if pos := f.app.detailList.Position; pos.First == 0 && pos.Offset == 0 {
				t.Fatal("long movie content cannot be scrolled with directional input")
			}
		})
	}
}

func TestTVDelayedArtworkAndMissingSynopsis(t *testing.T) {
	f := newHeadlessFixture(t)
	release := make(chan struct{})
	art := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	// The catalog backdrop already tried the fixture's offline fanart.
	f.app.backdrops.reset()
	f.app.backdrops.fetch("fanart:movie-new", func() (backdrop, error) {
		<-release
		return newBackdrop(art), nil
	})
	f.app.pushMovieDetail(&MediaItem{ID: "movie-new", Title: "New Movie", Type: mediaKindMovie})
	if err := f.driver.Resize(1280, 720); err != nil {
		t.Fatal(err)
	}
	tvCapture(t, f, "movie-loading-art-1280")
	if !headlessHasLabel(f.driver.Tree(), "Play movie") {
		t.Fatal("loading artwork hid the action")
	}
	close(release)
	waitForCache(t, func() bool {
		if err := f.driver.Refresh(); err != nil {
			t.Fatal(err)
		}
		bd, ok := f.app.backdrops.get("fanart:movie-new")
		return ok && bd.sharp.Size().X == 64
	})
	tvCapture(t, f, "movie-loaded-art-1280")
	f.app.client.shows["show-one"].Seasons[1].Episodes[1].Plot = ""
	f.app.pushShowDetail(&MediaItem{ID: "show-one", Title: "Fixture Show", Type: mediaKindShow})
	if err := f.driver.Refresh(); err != nil {
		t.Fatal(err)
	}
	if !headlessHasLabel(f.driver.Tree(), "Pilot") || !headlessHasLabel(f.driver.Tree(), "Play S01E01 Pilot") {
		t.Fatal("missing synopsis hid episode information")
	}
	tvCapture(t, f, "show-missing-synopsis-1280")
}

func TestTVDetailsAndOverlays(t *testing.T) {
	for _, size := range []image.Point{image.Pt(1920, 1080), image.Pt(1280, 720)} {
		for _, shade := range []uint8{16, 255} {
			t.Run(fmt.Sprintf("%dx%d/art-%d", size.X, size.Y, shade), func(t *testing.T) {
				f := newHeadlessFixture(t)
				art := image.NewNRGBA(image.Rect(0, 0, 64, 64))
				for y := range 64 {
					for x := range 64 {
						art.SetNRGBA(x, y, color.NRGBA{R: shade, G: shade, B: shade, A: 255})
					}
				}
				for _, key := range []string{"fanart:movie-new", "thumb:movie-new", "fanart:show-one", "poster:show-one"} {
					storeArtwork(f.app, key, art)
				}
				f.app.resumePos[movieMediaKey("movie-new")] = 300
				f.app.pushMovieDetail(&MediaItem{ID: "movie-new", Title: "A longer movie title that must remain readable from the sofa", Type: mediaKindMovie})
				if err := f.driver.Resize(size.X, size.Y); err != nil {
					t.Fatal(err)
				}
				// Refresh alone must complete the transition, without input or resize.
				if err := f.driver.Refresh(); err != nil {
					t.Fatal(err)
				}
				if time.Since(f.app.detail.enteredAt) < detailFadeDuration {
					t.Fatal("driver left detail transition unfinished")
				}
				tvAssertSafeButton(t, f, "Play movie")
				if !headlessHasLabel(f.driver.Tree(), "Resume 5:00") {
					t.Fatal("resume action missing")
				}
				if headlessHasLabel(f.driver.Tree(), "Status: Idle") || headlessHasLabel(f.driver.Tree(), "Added: ") {
					t.Fatal("administrative detail remains in main flow")
				}
				tvCapture(t, f, fmt.Sprintf("movie-%d-art-%d", size.X, shade))
				f.app.playerStatus = PlayerStatusError
				f.app.playerError = "Fixture playback error"
				if err := f.driver.Frame(); err != nil {
					t.Fatal(err)
				}
				if !headlessHasLabel(f.driver.Tree(), "Fixture playback error") {
					t.Fatal("playback failure is hidden")
				}
				f.app.playerStatus = PlayerStatusIdle
				f.app.popDetail()
				show := f.app.client.shows["show-one"]
				show.Plot = strings.Repeat("Long show synopsis should not overwhelm the selected episode. ", 20)
				show.Seasons = make(map[int]*papi.Season)
				for s := 1; s <= 18; s++ {
					episodes := make(map[int]*papi.Episode)
					for ep := 1; ep <= 24; ep++ {
						episodes[ep] = headlessEpisode(s, ep, fmt.Sprintf("Episode %d with a longer descriptive title", ep))
					}
					show.Seasons[s] = &papi.Season{Episodes: episodes}
				}
				f.app.pushShowDetail(&MediaItem{ID: "show-one", Title: "Fixture Show", Type: mediaKindShow})
				if err := f.driver.Refresh(); err != nil {
					t.Fatal(err)
				}
				if headlessHasLabel(f.driver.Tree(), show.Plot) {
					t.Fatal("show summary competes with episode content")
				}
				tvAssertSafeButton(t, f, "Season 1")
				// The poster has its own column; it must not consume list height.
				var checkTitle func([]driver.Node)
				checkTitle = func(nodes []driver.Node) {
					for _, node := range nodes {
						if node.Label == "Fixture Show" && node.Bounds.MinX < size.X*280/1920 {
							t.Fatal("show title does not leave a substantial poster column")
						}
						checkTitle(node.Children)
					}
				}
				checkTitle(f.driver.Tree())
				if pos := f.app.detail.show.epList.Position; pos.Count < 10 {
					t.Fatalf("compact show header leaves only %d visible episode rows", pos.Count)
				}
				tvCapture(t, f, fmt.Sprintf("show-tabs-%d-art-%d", size.X, shade))
				for range 17 {
					headlessKey(t, f, "RightArrow")
				}
				s := f.app.detail.show
				if s.seasonCursor != 17 {
					t.Fatal("season tabs did not scroll to final season")
				}
				tabPos := s.tabsList.Position
				if s.seasonCursor < tabPos.First || s.seasonCursor > tabPos.First+tabPos.Count-1 || (s.seasonCursor == tabPos.First+tabPos.Count-1 && tabPos.OffsetLast < 0) {
					t.Fatalf("selected season not fully visible: %+v", tabPos)
				}
				headlessKey(t, f, "DownArrow")
				for range 23 {
					headlessKey(t, f, "DownArrow")
				}
				pos := s.epList.Position
				if s.epCursor != 23 || s.epCursor < pos.First || s.epCursor > pos.First+pos.Count-1 || (s.epCursor == pos.First+pos.Count-1 && pos.OffsetLast < 0) {
					t.Fatalf("selected episode not fully visible: cursor %d, %+v", s.epCursor, pos)
				}
				code, title := episodeDisplayParts(s.currentEpisode())
				tvAssertSafeButton(t, f, "Play "+code+" "+title)
				tvCapture(t, f, fmt.Sprintf("show-episodes-%d-art-%d", size.X, shade))
				f.app.subPopup = &SubtitlePopup{video: f.app.client.movies["movie-new"], state: SubtitlePopupReady, entries: subtitleEntries(60), list: layout.List{Axis: layout.Vertical}}
				if err := f.driver.Frame(); err != nil {
					t.Fatal(err)
				}
				for range 30 {
					headlessKey(t, f, "DownArrow")
				}
				assertSubtitleVisible(t, f)
				tvAssertPopupHintsAtBottom(t, f)
				tvCapture(t, f, fmt.Sprintf("subtitles-%d-art-%d", size.X, shade))
			})
		}
	}
}
