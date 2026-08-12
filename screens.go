package main

import (
	"time"

	"gioui.org/layout"
)

// ── Detail screens ────────────────────────────────────────────────────────────
//
// The UI has two levels: the browse screen (radix tree navigation) and an
// optional full-screen detail page pushed on top of it. App.detail == nil means
// the browse screen is active. Detail pages never nest deeper than one level.

type detailKind int

const (
	detailMovie detailKind = iota
	detailShow
)

// detailScreen is a pushed full-screen page.
type detailScreen struct {
	kind      detailKind
	enteredAt time.Time // for the fade-in transition

	// movie page
	movieID    string
	movieTitle string

	// show page
	show *showPage
}

// showFocusZone identifies which part of the show page has focus.
type showFocusZone int

const (
	focusEpisodes showFocusZone = iota
	focusTabs
)

// showPage holds the state of the show detail page: season tabs + episode list.
type showPage struct {
	showID  string
	title   string
	seasons []*DynamicNode

	seasonCursor int
	epCursor     int
	focus        showFocusZone

	// persistent scroll state (must survive between frames)
	tabsList layout.List
	epList   layout.List
	infoList layout.List
}

// currentSeason returns the selected season node, or nil when empty.
func (s *showPage) currentSeason() *DynamicNode {
	if s.seasonCursor < 0 || s.seasonCursor >= len(s.seasons) {
		return nil
	}
	return s.seasons[s.seasonCursor]
}

// currentEpisode returns the focused episode node, or nil when none.
func (s *showPage) currentEpisode() *DynamicNode {
	season := s.currentSeason()
	if season == nil || s.epCursor < 0 || s.epCursor >= len(season.Children) {
		return nil
	}
	return season.Children[s.epCursor]
}

// switchSeason moves the season cursor by delta (clamped) and resets the
// episode cursor to the first episode of the newly selected season.
func (s *showPage) switchSeason(delta int) {
	next := s.seasonCursor + delta
	if next < 0 {
		next = 0
	}
	if next > len(s.seasons)-1 {
		next = len(s.seasons) - 1
	}
	if next != s.seasonCursor {
		s.seasonCursor = next
		s.epCursor = 0
	}
}

// up moves the focus/cursor up: within the episode list, then to the tabs.
func (s *showPage) up() {
	if s.focus == focusTabs {
		return
	}
	if s.epCursor == 0 {
		s.focus = focusTabs
		return
	}
	s.epCursor--
}

// down moves the focus/cursor down: from the tabs into the episode list, then
// within the episode list (clamped at the last episode).
func (s *showPage) down() {
	if s.focus == focusTabs {
		s.focus = focusEpisodes
		return
	}
	season := s.currentSeason()
	if season == nil {
		return
	}
	if s.epCursor < len(season.Children)-1 {
		s.epCursor++
	}
}

// left switches to the previous season (both focus zones).
func (s *showPage) left() { s.switchSeason(-1) }

// right switches to the next season (both focus zones).
func (s *showPage) right() { s.switchSeason(1) }

// ── Transitions on App ────────────────────────────────────────────────────────

// pushMovieDetail opens the full-screen detail page for a movie.
func (a *App) pushMovieDetail(media *MediaItem) {
	a.detail = &detailScreen{
		kind:       detailMovie,
		enteredAt:  time.Now(),
		movieID:    media.ID,
		movieTitle: media.Title,
	}
}

// pushShowDetail opens the full-screen detail page for a show. Does nothing
// when the show has no seasons/episodes.
func (a *App) pushShowDetail(media *MediaItem) {
	show, ok := a.client.getShow(media.ID)
	if !ok {
		return
	}
	seasonNodes := createSeasonNodes(show)
	if len(seasonNodes) == 0 {
		return
	}
	a.detail = &detailScreen{
		kind:      detailShow,
		enteredAt: time.Now(),
		show: &showPage{
			showID:   show.ImdbID,
			title:    media.Title,
			seasons:  seasonNodes,
			tabsList: layout.List{Axis: layout.Horizontal},
			epList:   layout.List{Axis: layout.Vertical},
			infoList: layout.List{Axis: layout.Vertical},
		},
	}
}

// popDetail returns to the browse screen.
func (a *App) popDetail() {
	a.detail = nil
}
