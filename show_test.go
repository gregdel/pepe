package main

import (
	"testing"
	"time"

	"github.com/odwrtw/polochon/lib/papi"
)

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
