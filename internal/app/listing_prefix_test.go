package app

import (
	"fmt"
	"reflect"
	"sort"
	"testing"
)

func TestRadixListingPrefixTerminals(t *testing.T) {
	for _, titles := range [][]string{
		{"Up", "Up in the Air (2009)"},
		{"Up", "Up in", "Up in the Air", "Up on the Roof"},
		{"Été", "Été indien", "Été indien encore"},
		{"The Up", "The Up in the Air"},
	} {
		t.Run(titles[0], func(t *testing.T) {
			items := make([]*listingItem, 0, len(titles)+1)
			for _, title := range titles {
				items = append(items, newMovieListingItem(testMovie(title, title, 0)))
			}
			// Catalog-order collision winners must survive the transformation.
			winner := newMovieListingItem(testMovie("winner", titles[0], 0))
			items = append(items, winner)
			listing := (radixListingBuilder{}).build(items)

			want := make([]string, 0, len(listing.titleToMedia))
			for title, media := range listing.titleToMedia {
				want = append(want, title)
				node := listing.tree.Lookup(title)
				if node == nil || !node.IsTerminal || len(node.Children) != 0 || node.FullString != title || node.Media != media {
					t.Fatalf("Lookup(%q) = %+v, want terminal leaf with media %p", title, node, media)
				}
			}
			got := listing.tree.AllStrings()
			sort.Strings(got)
			sort.Strings(want)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("AllStrings = %q, want %q", got, want)
			}
			if listing.tree.Lookup(titles[0]).Media != winner.media {
				t.Fatal("prefix title lost the catalog-order collision winner")
			}
			for _, missing := range []string{"", titles[0] + " missing", titles[0][:1]} {
				if node := listing.tree.Lookup(missing); node != nil {
					t.Fatalf("Lookup(%q) = %+v, want nil", missing, node)
				}
			}

			var check func(*RadixNode) int
			check = func(node *RadixNode) int {
				count := 0
				if node.IsTerminal {
					count = 1
				} else if node.FullString != "" || node.Media != nil {
					t.Fatalf("group retained terminal identity: %+v", node)
				}
				for i, child := range node.Children {
					count += check(child)
					if i > 0 {
						prev := node.Children[i-1]
						if prev.SubtreeCount < child.SubtreeCount || (prev.SubtreeCount == child.SubtreeCount && prev.Label > child.Label) {
							t.Fatal("children lost deterministic count/label ordering")
						}
					}
				}
				if count != node.SubtreeCount {
					t.Fatalf("node %q count = %d, want %d", node.Label, node.SubtreeCount, count)
				}
				return count
			}
			if count := check(listing.tree.Root()); count != len(want) {
				t.Fatalf("root count = %d, want %d", count, len(want))
			}
			if winner.media.Title != titles[0] || winner.movie.Title != titles[0] {
				t.Fatal("builder mutated the source item")
			}
			// Normalization is safe to repeat and does not duplicate terminals.
			normalizeListingTree(listing.tree.Root(), listing.titleToMedia)
			if count := check(listing.tree.Root()); count != len(want) {
				t.Fatal("normalizing twice changed the tree")
			}
		})
	}
}

func TestRadixListingAliasCollisions(t *testing.T) {
	for _, kind := range []MediaKind{mediaKindMovie, mediaKindShow} {
		for _, aliasLast := range []bool{false, true} {
			t.Run(string(kind)+"/aliasLast="+fmt.Sprint(aliasLast), func(t *testing.T) {
				makeItem := func(id, title string) *listingItem {
					if kind == mediaKindMovie {
						return newMovieListingItem(testMovie(id, title, 0))
					}
					return newShowListingItem(testShow(id, title, 0))
				}
				canonical, alias := makeItem("canonical", "Up"), makeItem("alias", "The Up")
				items := []*listingItem{alias, canonical, makeItem("longer", "The Up in the Air")}
				winner := canonical
				if aliasLast {
					items[0], items[1] = items[1], items[0]
					winner = alias
				}
				listing := (radixListingBuilder{}).build(items)
				for range 2 {
					for title, want := range map[string]*MediaItem{
						"Up": winner.media, "The Up": alias.media,
						"Up in the Air": items[2].media, "The Up in the Air": items[2].media,
					} {
						node := listing.tree.Lookup(title)
						if node == nil || !node.IsTerminal || len(node.Children) != 0 || node.Media != want || listing.titleToMedia[title] != want {
							t.Fatalf("Lookup(%q) = %+v, map = %p, want leaf with media %p", title, node, listing.titleToMedia[title], want)
						}
					}
					if got := listing.tree.Root().SubtreeCount; got != 4 {
						t.Fatalf("root count = %d, want 4", got)
					}
					normalizeListingTree(listing.tree.Root(), listing.titleToMedia)
				}
			})
		}
	}
}

func TestPrefixSelectionRestoration(t *testing.T) {
	for _, id := range []string{"prefix", "descendant"} {
		t.Run(id, func(t *testing.T) {
			items := []*listingItem{
				newMovieListingItem(testMovie("prefix", "Up", 0)),
				newMovieListingItem(testMovie("descendant", "Up in the Air", 2009)),
			}
			a := newModeTestApp(items, nil)
			root := a.sectionRoot(SectionMovies)
			if !a.browse.nav.navigateToMedia(root, mediaKindMovie, id) || a.browse.nav.getCurrentPath() != "Up" {
				t.Fatal("could not reconstruct the prefix group's navigation path")
			}
			sel := a.captureSelection()
			if sel == nil || sel.mediaID != id {
				t.Fatalf("captureSelection = %+v, want %q", sel, id)
			}
			// Rebuild the tree so restoration cannot rely on node pointers.
			a.applyLibrarySnapshot(buildLibrarySnapshot(radixListingBuilder{}, items, nil))
			a.browse.nav = newNavigationState()
			a.restoreSelection(sel)
			if selected := a.selectedBrowseMedia(); selected == nil || selected.ID != id || a.browse.nav.getCurrentPath() != "Up" {
				t.Fatalf("restored media = %+v, want %q in Up group", selected, id)
			}
			// Mode changes intentionally reset selection to the section root.
			for _, mode := range []ListingMode{ListingModeDateAdded, ListingModeTitle} {
				a.applyListingMode(mode)
				if !a.browse.nav.isAtSectionRoot() || a.browse.nav.section != SectionMovies {
					t.Fatal("mode change lost the browse section")
				}
				if !a.browse.nav.navigateToMedia(a.sectionRoot(SectionMovies), mediaKindMovie, id) {
					t.Fatalf("media %q unreachable in mode %v", id, mode)
				}
			}
		})
	}
}
