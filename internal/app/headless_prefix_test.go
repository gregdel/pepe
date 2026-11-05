package app

import (
	"fmt"
	"image"
	"testing"

	"github.com/gregdel/pepe/internal/driver"
	"github.com/odwrtw/polochon/lib/papi"
)

func newHeadlessPrefixFixture(t *testing.T, kind MediaKind) *headlessFixture {
	t.Helper()
	f := newHeadlessFixture(t)
	var movies, shows []*listingItem
	movieMap := make(map[string]*papi.Movie)
	showMap := make(map[string]*papi.Show)
	for _, entry := range []struct {
		id, title string
		year      int
	}{{"prefix", "Up", 0}, {"descendant", "Up in the Air", 2009}} {
		if kind == mediaKindMovie {
			movie := testMovie(entry.id, entry.title, entry.year)
			movieMap[entry.id] = movie
			movies = append(movies, newMovieListingItem(movie))
		} else {
			show := testShow(entry.id, entry.title, entry.year)
			show.Seasons = map[int]*papi.Season{1: {Episodes: map[int]*papi.Episode{1: headlessEpisode(1, 1, "Pilot")}}}
			showMap[entry.id] = show
			shows = append(shows, newShowListingItem(show))
		}
	}
	snapshot := buildLibrarySnapshot(radixListingBuilder{}, movies, shows)
	f.app.client.publishLibrary(movieMap, showMap, movies, shows, snapshot)
	f.app.applyLibrarySnapshot(snapshot)
	return f
}

func TestHeadlessPrefixTitlesReachable(t *testing.T) {
	for _, kind := range []MediaKind{mediaKindMovie, mediaKindShow} {
		for _, viewport := range []image.Point{image.Pt(1920, 1080), image.Pt(1024, 768)} {
			t.Run(fmt.Sprintf("%s/%dx%d", kind, viewport.X, viewport.Y), func(t *testing.T) {
				f := newHeadlessPrefixFixture(t, kind)
				if err := f.driver.Resize(viewport.X, viewport.Y); err != nil {
					t.Fatal(err)
				}
				if kind == mediaKindShow {
					headlessKey(t, f, "DownArrow")
				}
				headlessKey(t, f, "RightArrow")
				group := f.app.browse.nav.getSelectedChild()
				if group == nil || group.IsTerminal || group.Label != "Up" || group.SubtreeCount != 2 {
					t.Fatalf("prefix group = %+v", group)
				}
				headlessKey(t, f, "RightArrow")
				if f.app.detail != nil || f.app.browse.nav.getCurrentNode() != group {
					t.Fatal("Right opened details instead of entering the prefix group")
				}
				firstID, firstOK := headlessButtonID(f.driver.Tree(), "Up")
				secondID, secondOK := headlessButtonID(f.driver.Tree(), "Up in the Air (2009)")
				if !firstOK || !secondOK || firstID == secondID {
					t.Fatal("prefix terminals do not have distinct labeled pointer targets")
				}
				assertDetail := func(id string) {
					t.Helper()
					d := f.app.detail
					if d == nil {
						t.Fatal("terminal did not open detail")
					}
					if kind == mediaKindMovie {
						if d.kind != detailMovie || d.movieID != id {
							t.Fatalf("movie detail = %+v, want %q", d, id)
						}
					} else if d.kind != detailShow || d.show == nil || d.show.showID != id || d.show.focus != focusTabs {
						t.Fatalf("show detail = %+v, want %q with tabs focused", d, id)
					}
				}
				for i, id := range []string{"prefix", "descendant"} {
					if i > 0 {
						headlessKey(t, f, "DownArrow")
					}
					node := f.app.browse.nav.getSelectedChild()
					if node == nil || node.Media == nil || node.Media.ID != id {
						t.Fatalf("selected terminal = %+v, want %q", node, id)
					}
					if !headlessLabelInside(f.driver.Tree(), node.FullString, viewport) {
						t.Fatalf("terminal %q is not visible", node.FullString)
					}
					if !headlessHasLabel(f.driver.Tree(), node.Media.Title) || !headlessHasLabel(f.driver.Tree(), "details") {
						t.Fatalf("terminal %q has no media preview", node.FullString)
					}
					headlessKey(t, f, "Enter")
					if f.app.detail != nil || f.app.browse.nav.getSelectedChild() != node {
						t.Fatal("Enter changed terminal navigation")
					}
					// A successful refresh must restore the terminal's identity and path.
					snapshot := buildLibrarySnapshot(radixListingBuilder{}, f.app.client.movieItems, f.app.client.showItems)
					f.app.applyRefreshResult(snapshot)
					if selected := f.app.selectedBrowseMedia(); selected == nil || selected.ID != id || f.app.browse.nav.getCurrentPath() != "Up" {
						t.Fatalf("refresh restored %+v, want %q in Up group", selected, id)
					}
					headlessKey(t, f, "RightArrow")
					assertDetail(id)
					headlessKey(t, f, "LeftArrow")
					if f.app.detail != nil || f.app.browse.nav.getCurrentPath() != "Up" {
						t.Fatal("Left from detail lost the prefix group")
					}
				}
				headlessKey(t, f, "UpArrow")
				if selected := f.app.selectedBrowseMedia(); selected == nil || selected.ID != "prefix" {
					t.Fatal("Up did not return to the prefix terminal")
				}
				for _, entry := range []struct{ label, id string }{{"Up", "prefix"}, {"Up in the Air (2009)", "descendant"}} {
					buttonID, ok := headlessButtonID(f.driver.Tree(), entry.label)
					if !ok {
						t.Fatalf("missing pointer target %q", entry.label)
					}
					if err := f.driver.Click(driver.ClickTarget{ID: &buttonID}); err != nil {
						t.Fatal(err)
					}
					assertDetail(entry.id)
					headlessKey(t, f, "LeftArrow")
				}
				headlessKey(t, f, "LeftArrow")
				if !f.app.browse.nav.isAtSectionRoot() {
					t.Fatal("Left from group did not return to section root")
				}
			})
		}
	}
}
