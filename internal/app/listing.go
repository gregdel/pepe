package app

import (
	"sort"
	"time"

	"github.com/odwrtw/polochon/lib/papi"
)

// listingItem pairs immutable app-owned identity with exactly one raw API source.
type listingItem struct {
	media *MediaItem
	movie *papi.Movie
	show  *papi.Show
}

func newMovieListingItem(movie *papi.Movie) *listingItem {
	return &listingItem{
		media: &MediaItem{
			ID:    movie.ImdbID,
			Title: movie.Title,
			Year:  movie.Year,
			Type:  mediaKindMovie,
		},
		movie: movie,
	}
}

func newShowListingItem(show *papi.Show) *listingItem {
	return &listingItem{
		media: &MediaItem{
			ID:    show.ImdbID,
			Title: show.Title,
			Year:  show.Year,
			Type:  mediaKindShow,
		},
		show: show,
	}
}

func (item *listingItem) dateAdded() time.Time {
	if item == nil || (item.movie == nil && item.show == nil) || (item.movie != nil && item.show != nil) {
		return time.Time{}
	}

	if item.movie != nil {
		if item.movie.Movie == nil {
			return time.Time{}
		}
		return item.movie.DateAdded
	}
	if item.show.Show == nil {
		return time.Time{}
	}

	var newest time.Time
	for _, season := range item.show.Seasons {
		if season == nil {
			continue
		}
		for _, episode := range season.Episodes {
			if episode == nil || episode.ShowEpisode == nil || episode.DateAdded.IsZero() {
				continue
			}
			if newest.IsZero() || episode.DateAdded.After(newest) {
				newest = episode.DateAdded
			}
		}
	}

	return newest
}

type mediaListing struct {
	tree         *RadixTree
	titleToMedia map[string]*MediaItem
}

type listingBuilder interface {
	build([]*listingItem) mediaListing
}

type radixListingBuilder struct{}

func (radixListingBuilder) build(items []*listingItem) mediaListing {
	titles := make([]string, 0, len(items)*2)
	titleToMedia := make(map[string]*MediaItem, len(items)*2)

	for _, item := range items {
		displayTitle := formatTitle(item.media.Title, item.media.Year)
		titles = append(titles, displayTitle)
		titleToMedia[displayTitle] = item.media

		if strippedTitle, hasThe := stripThePrefix(displayTitle); hasThe {
			titles = append(titles, strippedTitle)
			titleToMedia[strippedTitle] = item.media
		}
	}

	tree := NewRadixTree(titles)
	attachListingMedia(tree.Root(), titleToMedia)

	return mediaListing{tree: tree, titleToMedia: titleToMedia}
}

type dateAddedListingBuilder struct{}

func (dateAddedListingBuilder) build(items []*listingItem) mediaListing {
	type datedItem struct {
		item  *listingItem
		title string
		date  time.Time
	}
	sortedItems := make([]datedItem, 0, len(items))
	for _, item := range items {
		sortedItems = append(sortedItems, datedItem{
			item: item, title: formatTitle(item.media.Title, item.media.Year), date: item.dateAdded(),
		})
	}
	sort.SliceStable(sortedItems, func(i, j int) bool {
		left, right := sortedItems[i], sortedItems[j]
		if left.date.IsZero() != right.date.IsZero() {
			return !left.date.IsZero()
		}
		if !left.date.Equal(right.date) {
			return left.date.After(right.date)
		}
		if left.title != right.title {
			return left.title < right.title
		}
		if left.item.media.Type != right.item.media.Type {
			return left.item.media.Type < right.item.media.Type
		}
		return left.item.media.ID < right.item.media.ID
	})

	root := &RadixNode{
		Children:     make([]*RadixNode, 0, len(sortedItems)),
		SubtreeCount: len(sortedItems),
	}
	for _, entry := range sortedItems {
		root.Children = append(root.Children, &RadixNode{
			Label:        entry.title,
			IsTerminal:   true,
			FullString:   entry.title,
			Media:        entry.item.media,
			SubtreeCount: 1,
		})
	}

	// Preserve catalog-order collision winners independently of display sorting.
	titleToMedia := make(map[string]*MediaItem, len(items))
	for _, item := range items {
		titleToMedia[formatTitle(item.media.Title, item.media.Year)] = item.media
	}

	return mediaListing{
		tree:         &RadixTree{root: root},
		titleToMedia: titleToMedia,
	}
}

func attachListingMedia(node *RadixNode, titleToMedia map[string]*MediaItem) {
	if node == nil {
		return
	}

	if node.IsTerminal {
		node.Media = titleToMedia[node.FullString]
	}
	for _, child := range node.Children {
		attachListingMedia(child, titleToMedia)
	}
}

func buildLibrarySnapshot(builder listingBuilder, movieItems, showItems []*listingItem) LibrarySnapshot {
	movies := builder.build(movieItems)
	shows := builder.build(showItems)
	titleToMedia := make(map[string]*MediaItem, len(movies.titleToMedia)+len(shows.titleToMedia))

	for title, media := range movies.titleToMedia {
		titleToMedia[title] = media
	}
	for title, media := range shows.titleToMedia {
		titleToMedia[title] = media
	}

	return LibrarySnapshot{
		movieRadix:   movies.tree,
		showRadix:    shows.tree,
		titleToMedia: titleToMedia,
		movieCount:   len(movieItems),
		showCount:    len(showItems),
	}
}
