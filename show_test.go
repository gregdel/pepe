package main

import (
	"testing"

	"gioui.org/io/key"
)

func TestHandleShowKeysLeftExitsFromEpisodeFocus(t *testing.T) {
	for _, name := range []key.Name{key.NameLeftArrow, key.Name("H")} {
		t.Run(string(name), func(t *testing.T) {
			a := &App{detail: &detailScreen{}}
			s := &showPage{focus: focusEpisodes}

			a.handleShowKeys(key.Event{Name: name}, s)

			if a.detail != nil {
				t.Fatal("left did not exit show details")
			}
		})
	}
}

func TestHandleShowKeysLeftSelectsPreviousSeasonFromTabs(t *testing.T) {
	for _, name := range []key.Name{key.NameLeftArrow, key.Name("H")} {
		t.Run(string(name), func(t *testing.T) {
			a := &App{detail: &detailScreen{}}
			s := &showPage{
				focus:        focusTabs,
				seasonCursor: 1,
				seasons:      []*DynamicNode{{}, {}},
			}

			a.handleShowKeys(key.Event{Name: name}, s)

			if a.detail == nil {
				t.Fatal("left exited while season tabs had focus")
			}
			if s.seasonCursor != 0 {
				t.Fatalf("season cursor = %d, want 0", s.seasonCursor)
			}
		})
	}
}
