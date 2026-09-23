package app

import (
	"log/slog"

	polochon "github.com/odwrtw/polochon/lib"
	"github.com/odwrtw/polochon/lib/papi"
)

// NewHeadlessPlaybackClient supplies an offline catalog for the opt-in Gio
// playback demo. Its download URLs are only passed to the mock player.
func NewHeadlessPlaybackClient(log *slog.Logger) (*APIClient, error) {
	client, err := NewAPIClient("http://127.0.0.1", "headless-fixture", log)
	if err != nil {
		return nil, err
	}
	movies := []*papi.Movie{
		{Movie: &polochon.Movie{ImdbID: "demo-one", Title: "First Movie", Year: 2025}},
		{Movie: &polochon.Movie{ImdbID: "demo-two", Title: "Second Movie", Year: 2026}},
	}
	movieMap := make(map[string]*papi.Movie, len(movies))
	items := make([]*listingItem, 0, len(movies))
	for _, movie := range movies {
		movieMap[movie.ImdbID] = movie
		items = append(items, newMovieListingItem(movie))
	}
	client.publishLibrary(movieMap, nil, items, nil, buildLibrarySnapshot(radixListingBuilder{}, items, nil))
	return client, nil
}
