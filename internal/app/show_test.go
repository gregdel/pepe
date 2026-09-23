package app

import (
	"testing"
	"time"

	polochon "github.com/odwrtw/polochon/lib"
	"github.com/odwrtw/polochon/lib/papi"
)

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
