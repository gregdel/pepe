package main

import (
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
			Type:  "movie",
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
			Type:  "show",
		},
		show: show,
	}
}

// A future date builder must sort newest first, put zero dates last, then
// resolve equal dates by display title, media type, and IMDb ID.
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
