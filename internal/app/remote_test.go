package app

import (
	"testing"

	"gioui.org/io/key"
)

func TestRemoteBackFromDetails(t *testing.T) {
	for _, test := range []struct {
		name   string
		detail *detailScreen
	}{
		{"movie", &detailScreen{kind: detailMovie}},
		{"show tabs", &detailScreen{kind: detailShow, show: &showPage{focus: focusTabs, seasonCursor: 2}}},
		{"show episodes", &detailScreen{kind: detailShow, show: &showPage{focus: focusEpisodes}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			a := &App{detail: test.detail}
			a.dispatchAction(actionForKey(key.NameEscape))
			if a.detail != nil {
				t.Fatal("remote Back did not leave details")
			}
		})
	}
}

func TestRemoteBackClosesPopupBeforeDetails(t *testing.T) {
	detail := &detailScreen{kind: detailMovie}
	a := &App{detail: detail, subPopup: &SubtitlePopup{}}
	a.dispatchAction(actionForKey(key.NameEscape))
	if a.subPopup != nil || a.detail != detail {
		t.Fatal("remote Back must close only the subtitle popup first")
	}
}

func TestRemoteHome(t *testing.T) {
	a := &App{
		detail: &detailScreen{kind: detailMovie},
		browse: browseState{
			nav:     newNavigationState(),
			focus:   browseFocusModeRail,
			library: libraryView{mode: ListingModeDateAdded},
		},
	}
	a.browse.nav.resetToSection(SectionShows, &RadixNode{})
	a.browse.nav.goForward(&RadixNode{})
	a.dispatchAction(actionForKey(key.NameHome))
	if a.detail != nil || !a.browse.nav.isAtRoot() || a.browse.focus != browseFocusContent {
		t.Fatal("Home did not restore browse home")
	}
	if a.browse.library.mode != ListingModeDateAdded {
		t.Fatal("Home changed the listing mode")
	}
	if a.browse.nav.cursor != int(SectionShows) {
		t.Fatal("Home lost the selected media section")
	}
}

func TestRemoteHomeClosesPopupFirst(t *testing.T) {
	detail := &detailScreen{kind: detailMovie}
	a := &App{detail: detail, subPopup: &SubtitlePopup{}}
	a.dispatchAction(actionForKey(key.NameHome))
	if a.subPopup != nil || a.detail != detail {
		t.Fatal("Home must respect subtitle popup input priority")
	}
}
