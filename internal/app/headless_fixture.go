package app

import (
	"fmt"
	"image"
	"image/color"
	"log/slog"
	"net/http"
	"time"

	polochon "github.com/odwrtw/polochon/lib"
	index "github.com/odwrtw/polochon/lib/media_index"
	"github.com/odwrtw/polochon/lib/papi"
)

// mockTitle is one title of the offline catalog. A nil artwork has no file.
type mockTitle struct {
	id, title, plot string
	original        string // movies only; empty when it is the title
	year            int
	fanart, poster  *mockArtwork
}

// mockMovies are the offline catalog's movies, newest first. Each one's plot
// says which case its artwork covers; shared title prefixes form groups.
func mockMovies() []mockTitle {
	whiteColor := color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	movie := func(id, title string, year int, plot string, p mockPattern) mockTitle {
		return mockTitle{id: id, title: title, year: year, plot: plot, fanart: mockFanart(p), poster: mockPoster(p)}
	}
	portrait := movie("mock-portrait", "Portrait Fanart", 2016, "Portrait fanart, cropped to cover a landscape box.", sunsetPattern)
	portrait.fanart.size = image.Pt(600, 1200)
	wide := movie("mock-wide", "Ultra Wide Fanart", 2015, "Very wide fanart, cropped to cover the box.", oceanPattern)
	wide.fanart.size = image.Pt(3200, 600)
	tiny := movie("mock-tiny", "Tiny Fanart", 2014, "A one-pixel white fanart, scaled up.", solidPattern(whiteColor))
	tiny.fanart.size = image.Pt(1, 1)
	missing := movie("mock-missing", "Missing Fanart", 2013, "The title has a poster but no fanart file.", sunsetPattern)
	missing.fanart = nil
	slow := movie("mock-slow", "Slow Artwork", 2012, "Artwork takes three seconds: the previous backdrop stays, then cross-fades.", oceanPattern)
	slow.fanart.delay, slow.poster.delay = 3*time.Second, 3*time.Second
	flaky := movie("mock-flaky", "Flaky Server", 2011, "The fanart fails twice with 503, then loads on the second retry.", neonPattern)
	flaky.fanart.transient = 2
	corrupt := movie("mock-corrupt", "Corrupt Artwork", 2010, "The fanart is a truncated PNG: a permanent failure.", sunsetPattern)
	corrupt.fanart.corrupt = true
	white := movie("mock-white", "Bright White", 2024, "Uniform white artwork: the brightest case for text contrast. Its original title differs, so the localized one shows beneath.", solidPattern(whiteColor))
	white.original = "Éclat Blanc"

	return []mockTitle{
		movie("demo-one", "First Movie", 2025, "A warm sunset scene with a bright sun beside the preview text.", sunsetPattern),
		movie("demo-two", "Second Movie", 2026, "A pale sky over deep water with fine wave crests.", oceanPattern),
		white,
		movie("mock-horizon", "Bright Horizon", 2023, "Dark at the top, bright where the preview text sits.", horizonPattern),
		movie("mock-black", "Pitch Black", 2022, "Uniform black artwork.", solidPattern(color.NRGBA{A: 0xff})),
		movie("mock-yellow", "Saturated Yellow", 2021, "Saturated yellow: very bright, highly chromatic artwork.", solidPattern(color.NRGBA{R: 0xff, G: 0xe0, A: 0xff})),
		movie("mock-blue", "Saturated Blue", 2020, "Saturated blue: chromatic but dark artwork.", solidPattern(color.NRGBA{R: 0x10, G: 0x30, B: 0xff, A: 0xff})),
		movie("mock-checker", "Fine Checkerboard", 2019, "Three-pixel black and white cells: aliasing, blur and resizing.", checkerPattern(3)),
		movie("mock-stripes", "Hue Stripes", 2018, "Vertical bars of saturated hues: the ambient copy's edge extension must not streak.", hueStripesPattern(12)),
		movie("mock-gradient", "Smooth Gradient", 2017, "A slow dark diagonal ramp that shows any banding.", gradientPattern),
		portrait, wide, tiny, missing, slow, flaky, corrupt,
	}
}

func mockShows() []mockTitle {
	return []mockTitle{
		{id: "mock-neon", title: "Neon Nights", plot: "Bright magenta, cyan and violet glows on black.", fanart: mockFanart(neonPattern), poster: mockPoster(neonPattern)},
		{id: "mock-night", title: "Night Shift", plot: "A very dark sky with sparse stars.", fanart: mockFanart(nightPattern), poster: mockPoster(nightPattern)},
	}
}

// mockDateAdded is i days before the catalog's newest date. Movies use their
// index and episodes their show and season, spreading them over Last added.
func mockDateAdded(i int) time.Time {
	return time.Date(2026, time.November, 1, 20, 0, 0, 0, time.UTC).AddDate(0, 0, -i)
}

func mockVideo(size int64, quality string, added time.Time) polochon.BaseVideo {
	return polochon.BaseVideo{
		File: polochon.File{Size: size},
		VideoMetadata: polochon.VideoMetadata{
			DateAdded:    added,
			Quality:      polochon.Quality(quality),
			VideoCodec:   "H.264",
			AudioCodec:   "AAC",
			Container:    "mkv",
			ReleaseGroup: "MOCK",
		},
	}
}

// mockFile registers art under a name unique to the title and returns the
// papi file that downloads it, or nil without artwork.
func mockFile(arts map[string]*mockArtwork, id, kind string, art *mockArtwork, owner papi.Resource) *papi.File {
	if art == nil {
		return nil
	}
	name := id + "-" + kind + ".png"
	arts[name] = art
	return papi.NewFile(&index.File{Name: name}, owner)
}

// NewHeadlessPlaybackClient supplies an offline catalog for the opt-in Gio
// playback demo, with generated artwork (headless_artwork.go). Its video
// download URLs are only passed to the mock player.
func NewHeadlessPlaybackClient(log *slog.Logger) (*APIClient, error) {
	client, err := NewAPIClient("http://127.0.0.1", "headless-fixture", log)
	if err != nil {
		return nil, err
	}
	arts := make(map[string]*mockArtwork)

	movieMap := make(map[string]*papi.Movie)
	var movieItems []*listingItem
	for i, m := range mockMovies() {
		movie := &papi.Movie{Movie: &polochon.Movie{
			BaseVideo:     mockVideo(2_400_000_000, "1080p", mockDateAdded(i)),
			ImdbID:        m.id,
			Title:         m.title,
			OriginalTitle: m.original,
			Year:          m.year,
			Plot:          m.plot,
			Tagline:       "Generated artwork for offline testing",
			Rating:        7.5,
			Runtime:       95 + 5*i,
			Genres:        []string{"Test", "Artwork"},
		}}
		movie.FanartFile = mockFile(arts, m.id, "fanart", m.fanart, movie)
		movie.ThumbFile = mockFile(arts, m.id, "poster", m.poster, movie)
		movieMap[m.id] = movie
		movieItems = append(movieItems, newMovieListingItem(movie))
	}

	showMap := make(map[string]*papi.Show)
	var showItems []*listingItem
	for i, s := range mockShows() {
		show := &papi.Show{Show: &polochon.Show{ImdbID: s.id, Title: s.title, Year: 2024 - i, Plot: s.plot, Rating: 8.2}}
		show.FanartFile = mockFile(arts, s.id, "fanart", s.fanart, show)
		show.PosterFile = mockFile(arts, s.id, "poster", s.poster, show)
		show.Seasons = make(map[int]*papi.Season)
		for season := 1; season <= 2-i; season++ {
			episodes := make(map[int]*papi.Episode)
			for episode := 1; episode <= 3; episode++ {
				episodes[episode] = &papi.Episode{ShowEpisode: &polochon.ShowEpisode{
					BaseVideo:  mockVideo(1_100_000_000, "720p", mockDateAdded(2*i+2-season)),
					Title:      fmt.Sprintf("Chapter %d", episode),
					ShowTitle:  s.title,
					Season:     season,
					Episode:    episode,
					Aired:      "2026-01-02",
					Plot:       "Episode synopsis for " + s.title + ".",
					Runtime:    45,
					Rating:     8,
					ShowImdbID: s.id,
				}}
			}
			show.Seasons[season] = &papi.Season{ShowImdbID: s.id, Season: season, Episodes: episodes}
		}
		showMap[s.id] = show
		showItems = append(showItems, newShowListingItem(show))
	}

	client.artworkClient = &http.Client{Timeout: artworkRequestTimeout, Transport: newMockArtworkTransport(arts)}
	client.publishLibrary(movieMap, showMap, movieItems, showItems, buildLibrarySnapshot(radixListingBuilder{}, movieItems, showItems))
	return client, nil
}
