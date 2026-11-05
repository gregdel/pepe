package app

import (
	"fmt"
	"image"
	"testing"
	"time"

	"gioui.org/layout"
	polochon "github.com/odwrtw/polochon/lib"
	"github.com/odwrtw/polochon/lib/papi"
)

func TestHeadlessShowListVisibility(t *testing.T) {
	f := newHeadlessFixture(t)
	show := f.app.client.shows["show-one"]
	show.Seasons = make(map[int]*papi.Season)
	for season := 1; season <= 20; season++ {
		episodes := make(map[int]*papi.Episode)
		if season < 20 {
			for ep := 1; ep <= 30; ep++ {
				episodes[ep] = headlessEpisode(season, ep, fmt.Sprintf("Episode %02d", ep))
			}
		}
		show.Seasons[season] = &papi.Season{Episodes: episodes}
	}
	f.app.pushShowDetail(&MediaItem{ID: "show-one", Title: "Fixture Show", Type: mediaKindShow})
	f.app.detail.enteredAt = time.Now().Add(-detailFadeDuration)
	if err := f.driver.Resize(1280, 720); err != nil {
		t.Fatal(err)
	}
	s := f.app.detail.show
	frame := func(t *testing.T) {
		t.Helper()
		if err := f.driver.Frame(); err != nil {
			t.Fatal(err)
		}
	}
	visible := func(t *testing.T, name string, list *layout.List, cursor int) {
		t.Helper()
		pos := list.Position
		last := pos.First + pos.Count - 1
		if pos.Count == 0 || cursor < pos.First || cursor > last ||
			(cursor == pos.First && pos.Offset > 0) || (cursor == last && pos.OffsetLast < 0) {
			t.Fatalf("%s cursor %d not fully visible: %+v", name, cursor, pos)
		}
	}
	for range 12 {
		headlessKey(t, f, "RightArrow")
	}
	headlessKey(t, f, "DownArrow")
	for range 18 {
		headlessKey(t, f, "DownArrow")
	}
	for _, size := range []image.Point{image.Pt(900, 600), image.Pt(1280, 720)} {
		if err := f.driver.Resize(size.X, size.Y); err != nil {
			t.Fatal(err)
		}
		visible(t, "tabs after resize", &s.tabsList, s.seasonCursor)
		visible(t, "episodes after resize", &s.epList, s.epCursor)
	}

	for _, tc := range []struct {
		name   string
		list   *layout.List
		cursor *int
	}{
		{"tabs", &s.tabsList, &s.seasonCursor},
		{"episodes", &s.epList, &s.epCursor},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Put a non-selected row partly beyond the leading edge, then select it.
			tc.list.Position.First, tc.list.Position.Offset = 4, 5
			*tc.cursor = 5
			frame(t)
			pos := tc.list.Position
			if pos.First != 4 || pos.Offset <= 0 || pos.OffsetLast >= 0 || pos.Count < 3 {
				t.Fatalf("fixture needs clipped first and last rows: %+v", pos)
			}
			*tc.cursor = pos.First
			frame(t)
			visible(t, tc.name+" first", tc.list, *tc.cursor)

			// Recreate a trailing partial row and select it without resizing.
			tc.list.Position.First, tc.list.Position.Offset = 4, 5
			*tc.cursor = 5
			frame(t)
			pos = tc.list.Position
			if pos.OffsetLast >= 0 {
				t.Fatalf("fixture needs a clipped last row: %+v", pos)
			}
			*tc.cursor = pos.First + pos.Count - 1
			frame(t)
			visible(t, tc.name+" last", tc.list, *tc.cursor)
		})
	}

	// Switching from a scrolled episode list to an empty season and back must
	// restore a valid first-episode selection without scrolling the empty list.
	s.focus = focusTabs
	s.seasonCursor = 18
	frame(t)
	headlessKey(t, f, "RightArrow")
	if s.seasonCursor != 19 || s.epCursor != 0 || !headlessHasLabel(f.driver.Tree(), "No episodes") {
		t.Fatal("empty season did not render with the episode cursor reset")
	}
	headlessKey(t, f, "DownArrow")
	headlessKey(t, f, "DownArrow")
	if s.epCursor != 0 || s.currentEpisode() != nil {
		t.Fatal("navigation moved the cursor in an empty season")
	}
	s.focus = focusTabs
	headlessKey(t, f, "LeftArrow")
	visible(t, "episodes after empty season", &s.epList, s.epCursor)
}

func TestHeadlessShowWithoutSeasons(t *testing.T) {
	f := newHeadlessFixture(t)
	f.app.client.shows["show-one"].Seasons = nil
	f.app.pushShowDetail(&MediaItem{ID: "show-one", Type: mediaKindShow})
	if f.app.detail != nil {
		t.Fatal("opened a show with no seasons")
	}
}

func TestEpisodeDisplayParts(t *testing.T) {
	node := &DynamicNode{
		Label: "S01E03 - Signal Bloom",
		Episode: &papi.Episode{ShowEpisode: &polochon.ShowEpisode{
			Season:  1,
			Episode: 3,
			Title:   "Signal Bloom",
		}},
	}
	code, title := episodeDisplayParts(node)
	if code != "S01E03" || title != "Signal Bloom" {
		t.Fatalf("episodeDisplayParts() = (%q, %q), want (S01E03, Signal Bloom)", code, title)
	}
}

func TestBuildShowSummary(t *testing.T) {
	show := testShow("tt001", "Show", 2001)
	show.Rating = 8.3
	show.Seasons = map[int]*papi.Season{1: {Episodes: map[int]*papi.Episode{1: testEpisode(time.Time{})}}}
	if got, want := buildShowSummary(show), "2001  ·  8.3  ·  1 seasons · 1 episodes"; got != want {
		t.Fatalf("buildShowSummary() = %q, want %q", got, want)
	}

	show.Year, show.Rating, show.Seasons = 0, 0, nil
	if got, want := buildShowSummary(show), "0 seasons · 0 episodes"; got != want {
		t.Fatalf("buildShowSummary() without metadata = %q, want %q", got, want)
	}
}

func TestBuildEpisodeMetaLine(t *testing.T) {
	ep := &papi.Episode{ShowEpisode: &polochon.ShowEpisode{
		Aired:     "2018-02-26",
		Runtime:   45,
		Rating:    8.3,
		BaseVideo: polochon.BaseVideo{VideoMetadata: polochon.VideoMetadata{ReleaseGroup: "TECHNICAL"}},
	}}
	if got, want := buildEpisodeMetaLine(ep), "Aired Feb 26, 2018  ·  45 min  ·  8.3"; got != want {
		t.Fatalf("buildEpisodeMetaLine() = %q, want %q", got, want)
	}
}

func TestPushShowDetailStartsOnSeasonTabs(t *testing.T) {
	show := testShow("tt001", "Show", 2001)
	show.Seasons = map[int]*papi.Season{1: {Episodes: map[int]*papi.Episode{1: testEpisode(time.Time{})}}}
	a := &App{client: &APIClient{shows: map[string]*papi.Show{show.ImdbID: show}}}

	a.pushShowDetail(&MediaItem{ID: show.ImdbID, Title: show.Title, Type: mediaKindShow})

	if a.detail == nil || a.detail.show == nil || a.detail.show.focus != focusTabs {
		t.Fatalf("show detail focus = %#v, want season tabs", a.detail)
	}
	if a.detail.show.seasonCursor != 0 || a.detail.show.epCursor != 0 {
		t.Fatalf("initial cursors = season %d, episode %d, want 0, 0", a.detail.show.seasonCursor, a.detail.show.epCursor)
	}
}

func TestHandleShowKeysLeftExitsFromEpisodeFocus(t *testing.T) {
	for _, action := range []inputAction{actionLeft} {
		t.Run("left", func(t *testing.T) {
			a := &App{detail: &detailScreen{}}
			s := &showPage{focus: focusEpisodes}

			a.handleShowAction(action, s)

			if a.detail != nil {
				t.Fatal("left did not exit show details")
			}
		})
	}
}

func TestHandleShowKeysLeftSelectsPreviousSeasonFromTabs(t *testing.T) {
	for _, action := range []inputAction{actionLeft} {
		t.Run("left", func(t *testing.T) {
			a := &App{detail: &detailScreen{}}
			s := &showPage{
				focus:        focusTabs,
				seasonCursor: 1,
				seasons:      []*DynamicNode{{}, {}},
			}

			a.handleShowAction(action, s)

			if a.detail == nil {
				t.Fatal("left exited while season tabs had focus")
			}
			if s.seasonCursor != 0 {
				t.Fatalf("season cursor = %d, want 0", s.seasonCursor)
			}
		})
	}
}

func TestHandleShowKeysLeftExitsFromFirstSeasonTab(t *testing.T) {
	a := &App{detail: &detailScreen{}}
	s := &showPage{focus: focusTabs, seasons: []*DynamicNode{{}}}

	a.handleShowAction(actionLeft, s)

	if a.detail != nil {
		t.Fatal("left from the first season tab did not exit show details")
	}
}
