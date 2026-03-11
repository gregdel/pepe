package main

import (
	"context"
	"fmt"
	"image"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"

	polochon "github.com/odwrtw/polochon/lib"
	"github.com/odwrtw/polochon/lib/nfo"
	"github.com/odwrtw/polochon/lib/papi"
)

// MediaItem represents a unified movie or show for search
type MediaItem struct {
	ID    string
	Title string
	Year  int
	Type  string // "movie" or "show"
}

// APIClient wraps the papi client with caching
type APIClient struct {
	client   *papi.Client
	token    string
	endpoint string

	mu           sync.RWMutex
	movies       map[string]*papi.Movie
	shows        map[string]*papi.Show
	movieRadix   *RadixTree
	showRadix    *RadixTree
	titleToMedia map[string]*MediaItem
	movieCount   int
	showCount    int

	log *slog.Logger
}

type LibrarySnapshot struct {
	movieRadix   *RadixTree
	showRadix    *RadixTree
	titleToMedia map[string]*MediaItem
	movieCount   int
	showCount    int
}

func newAPIClient(endpoint, token string, log *slog.Logger) (*APIClient, error) {
	papiClient, err := papi.New(endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to create papi client: %w", err)
	}

	papiClient.SetToken(token)

	c := &APIClient{
		client:       papiClient,
		token:        token,
		endpoint:     endpoint,
		movies:       make(map[string]*papi.Movie),
		shows:        make(map[string]*papi.Show),
		titleToMedia: make(map[string]*MediaItem),
		log:          log,
	}

	return c, nil
}

func formatTitle(title string, year int) string {
	if year > 0 {
		return fmt.Sprintf("%s (%d)", title, year)
	}
	return title
}

func stripThePrefix(title string) (string, bool) {
	if len(title) > 4 && strings.HasPrefix(strings.ToLower(title), "the ") {
		return title[4:], true
	}
	return title, false
}

func (c *APIClient) fetchLibrary() error {
	c.log.Info("fetching library from API")

	movieCollection, err := c.client.GetMovies()
	if err != nil {
		return fmt.Errorf("failed to fetch movies: %w", err)
	}

	showCollection, err := c.client.GetShows()
	if err != nil {
		return fmt.Errorf("failed to fetch shows: %w", err)
	}

	movieList := movieCollection.List()
	showList := showCollection.List()

	movies := make(map[string]*papi.Movie)
	shows := make(map[string]*papi.Show)
	titleToMedia := make(map[string]*MediaItem)

	var movieTitles []string
	var showTitles []string

	for _, movie := range movieList {
		movies[movie.ImdbID] = movie

		// Format title with year
		displayTitle := formatTitle(movie.Title, movie.Year)

		item := MediaItem{
			ID:    movie.ImdbID,
			Title: movie.Title,
			Year:  movie.Year,
			Type:  "movie",
		}

		// Add main title
		movieTitles = append(movieTitles, displayTitle)
		titleToMedia[displayTitle] = &item

		// Also index without "The " prefix if present
		if strippedTitle, hasThe := stripThePrefix(displayTitle); hasThe {
			movieTitles = append(movieTitles, strippedTitle)
			titleToMedia[strippedTitle] = &item
		}
	}

	for _, show := range showList {
		shows[show.ImdbID] = show

		// Format title with year
		displayTitle := formatTitle(show.Title, show.Year)

		item := MediaItem{
			ID:    show.ImdbID,
			Title: show.Title,
			Year:  show.Year,
			Type:  "show",
		}

		// Add main title
		showTitles = append(showTitles, displayTitle)
		titleToMedia[displayTitle] = &item

		// Also index without "The " prefix if present
		if strippedTitle, hasThe := stripThePrefix(displayTitle); hasThe {
			showTitles = append(showTitles, strippedTitle)
			titleToMedia[strippedTitle] = &item
		}
	}

	// Build radix trees
	movieRadix := NewRadixTree(movieTitles)
	showRadix := NewRadixTree(showTitles)

	c.mu.Lock()
	c.movies = movies
	c.shows = shows
	c.movieRadix = movieRadix
	c.showRadix = showRadix
	c.titleToMedia = titleToMedia
	c.movieCount = len(movies)
	c.showCount = len(shows)
	c.mu.Unlock()

	c.log.Info("library fetched", "movies", len(movies), "shows", len(shows))
	return nil
}

func (c *APIClient) getSnapshot() LibrarySnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()

	titleToMedia := make(map[string]*MediaItem, len(c.titleToMedia))
	for key, value := range c.titleToMedia {
		titleToMedia[key] = value
	}

	return LibrarySnapshot{
		movieRadix:   c.movieRadix,
		showRadix:    c.showRadix,
		titleToMedia: titleToMedia,
		movieCount:   c.movieCount,
		showCount:    c.showCount,
	}
}

func (c *APIClient) getDownloadURL(target papi.Downloadable) (string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.client.DownloadURL(target)
}

func (c *APIClient) getToken() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.token
}

func (c *APIClient) getMovie(id string) (*papi.Movie, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	movie, ok := c.movies[id]
	return movie, ok
}

func (c *APIClient) getShow(id string) (*papi.Show, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	show, ok := c.shows[id]
	return show, ok
}

// fetchMovieImage fetches a movie image file selected by pick.
func (c *APIClient) fetchMovieImage(id string, pick func(*papi.Movie) *papi.File) (image.Image, error) {
	movie, ok := c.getMovie(id)
	if !ok {
		return nil, fmt.Errorf("movie %s not found", id)
	}
	f := pick(movie)
	if f == nil {
		return nil, fmt.Errorf("movie %s: image file not set", id)
	}
	return c.fetchImageFile(f)
}

// fetchShowImage fetches a show image file selected by pick.
func (c *APIClient) fetchShowImage(id string, pick func(*papi.Show) *papi.File) (image.Image, error) {
	show, ok := c.getShow(id)
	if !ok {
		return nil, fmt.Errorf("show %s not found", id)
	}
	f := pick(show)
	if f == nil {
		return nil, fmt.Errorf("show %s: image file not set", id)
	}
	return c.fetchImageFile(f)
}

// fetchImageFile is the shared HTTP GET + decode path for any papi image file.
func (c *APIClient) fetchImageFile(f papi.Downloadable) (image.Image, error) {
	rawURL, err := c.getDownloadURL(f)
	if err != nil {
		return nil, fmt.Errorf("image URL: %w", err)
	}
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Auth-Token", c.getToken())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("image fetch returned %s", resp.Status)
	}
	img, _, err := image.Decode(resp.Body)
	return img, err
}

// fetchMovieNFO downloads and parses the NFO file for the given movie ID.
// It returns the polochon.Movie with fields populated from the NFO XML.
func (c *APIClient) fetchMovieNFO(id string) (*polochon.Movie, error) {
	movie, ok := c.getMovie(id)
	if !ok {
		return nil, fmt.Errorf("movie %s not found", id)
	}
	if movie.NFO == nil {
		return nil, fmt.Errorf("movie %s: no NFO file", id)
	}

	rawURL, err := c.getDownloadURL(movie.NFO)
	if err != nil {
		return nil, fmt.Errorf("movie %s: NFO URL: %w", id, err)
	}

	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Auth-Token", c.getToken())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("movie %s: NFO fetch returned %s", id, resp.Status)
	}

	m := &polochon.Movie{}
	if err := nfo.Read(resp.Body, m); err != nil {
		return nil, fmt.Errorf("movie %s: NFO parse: %w", id, err)
	}

	return m, nil
}

// Events returns a channel that emits a signal whenever the server reports a
// library change via SSE.  The channel is closed when ctx is cancelled.
// Internally the papi client reconnects with exponential backoff.
func (c *APIClient) Events(ctx context.Context) <-chan struct{} {
	return c.client.Events(ctx)
}

// Config holds the application configuration
type Config struct {
	PolochonURL string
	Token       string
}

func loadConfig() (*Config, error) {
	url := os.Getenv("POLOCHON_URL")
	if url == "" {
		return nil, fmt.Errorf("POLOCHON_URL environment variable is required")
	}

	token := os.Getenv("POLOCHON_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("POLOCHON_TOKEN environment variable is required")
	}

	return &Config{
		PolochonURL: url,
		Token:       token,
	}, nil
}
