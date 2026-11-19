package app

import (
	"image"
	"strings"
	"testing"

	"gioui.org/layout"
	"github.com/gregdel/pepe/internal/driver"
)

// The D-pad focus is exposed as the only selected semantic node, so headless
// tools can follow it without screenshots.
func TestHeadlessSelectionFollowsFocus(t *testing.T) {
	f := newHeadlessPrefixFixture(t, mediaKindShow)
	selection := func() driver.Selection {
		t.Helper()
		if err := f.driver.Refresh(); err != nil {
			t.Fatal(err)
		}
		selected := driver.Selected(f.driver.Tree(), image.Rectangle{Max: f.driver.Size()})
		if len(selected) != 1 {
			t.Fatalf("selected nodes = %+v, want exactly one", selected)
		}
		return selected[0]
	}
	steps := []struct {
		key  string
		want string
	}{
		{"", "Movies"},
		{"DownArrow", "Shows"},
		{"LeftArrow", "Smart list"},
		{"RightArrow", "Shows"},
		{"RightArrow", "Up"},
		{"RightArrow", "Up"},
		{"DownArrow", "Up in the Air (2009)"},
	}
	for _, step := range steps {
		if step.key != "" {
			headlessKey(t, f, step.key)
		}
		if got := selection(); got.Label != step.want {
			t.Fatalf("after %q selection = %+v, want label %q", step.key, got, step.want)
		}
	}

	headlessKey(t, f, "RightArrow")
	if f.app.detail == nil {
		t.Fatal("Right on a show terminal did not open its detail")
	}
	tab := selection()
	if tab.Label != "Season 1" {
		t.Fatalf("show detail selection = %+v, want the season tab", tab)
	}
	headlessKey(t, f, "DownArrow")
	if episode := selection(); !strings.Contains(episode.Label, "Pilot") {
		t.Fatalf("episode selection = %+v, want the Pilot row", episode)
	}

	// The popup owns the focus; the episode row drawn under it is not selected.
	f.app.subPopup = &SubtitlePopup{
		video: testMovie("movie-new", "New Movie", 2026),
		state: SubtitlePopupReady, entries: subtitleEntries(3),
		list: layout.List{Axis: layout.Vertical},
	}
	if subtitle := selection(); !strings.Contains(subtitle.Label, "Release 00") {
		t.Fatalf("subtitle popup selection = %+v, want its first row", subtitle)
	}
}
