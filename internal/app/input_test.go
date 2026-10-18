package app

import (
	"testing"

	"gioui.org/io/key"
)

func TestActionForKeyAliases(t *testing.T) {
	tests := []struct {
		name string
		key  key.Name
		want inputAction
	}{
		{"up arrow", key.NameUpArrow, actionUp},
		{"vim up", "K", actionUp},
		{"down arrow", key.NameDownArrow, actionDown},
		{"vim down", "J", actionDown},
		{"left arrow", key.NameLeftArrow, actionLeft},
		{"vim left", "H", actionLeft},
		{"right arrow", key.NameRightArrow, actionRight},
		{"vim right", "L", actionRight},
		{"enter", key.NameEnter, actionActivate},
		{"return", key.NameReturn, actionActivate},
		{"escape", key.NameEscape, actionBack},
		{"remote home", key.NameHome, actionHome},
		{"remote blue shares S", "S", actionSubtitles},
		{"remote red shares Home", key.NameHome, actionHome},
		{"remote green shares R", "R", actionRefresh},
		{"remote yellow shares Tab", key.NameTab, actionToggleSection},
		{"old remote F1 is unbound", key.NameF1, actionUnknown},
		{"tab", key.NameTab, actionToggleSection},
		{"refresh", "R", actionRefresh},
		{"subtitles", "S", actionSubtitles},
		{"quit", "Q", actionQuit},
		{"unknown", "X", actionUnknown},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := actionForKey(test.key); got != test.want {
				t.Errorf("actionForKey(%q) = %v, want %v", test.key, got, test.want)
			}
		})
	}
}

func TestKeyFiltersCoverEveryBinding(t *testing.T) {
	if len(keyFilters) != len(keyBindings) {
		t.Fatalf("key filter count = %d, want %d", len(keyFilters), len(keyBindings))
	}
}

func TestListingModeLabel(t *testing.T) {
	if got := listingModeLabel(ListingModeTitle); got != "Smart list" {
		t.Fatalf("title mode label = %q", got)
	}
	if got := listingModeLabel(ListingModeDateAdded); got != "Last added" {
		t.Fatalf("date-added mode label = %q", got)
	}
}
