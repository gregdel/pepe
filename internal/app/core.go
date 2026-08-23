package app

import (
	"context"
	"fmt"
	"image"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	polochon "github.com/odwrtw/polochon/lib"
	"github.com/odwrtw/polochon/lib/papi"
)

// MediaItem represents a unified movie or show for search
type MediaItem struct {
	ID    string
	Title string
	Year  int
	Type  MediaKind
}

// MediaKind identifies the catalog collection that owns a media item.
type MediaKind string

const (
	mediaKindMovie MediaKind = "movie"
	mediaKindShow  MediaKind = "show"
)

// APIClient wraps the papi client with caching
type APIClient struct {
	client *papi.Client
	token  string

	mu           sync.RWMutex
	movies       map[string]*papi.Movie
	shows        map[string]*papi.Show
	movieItems   []*listingItem
	showItems    []*listingItem
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

func NewAPIClient(endpoint, token string, log *slog.Logger) (*APIClient, error) {
	papiClient, err := papi.New(endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to create papi client: %w", err)
	}

	papiClient.SetToken(token)

	if log == nil {
		log = slog.Default()
	}
	c := &APIClient{
		client:       papiClient,
		token:        token,
		movies:       make(map[string]*papi.Movie),
		shows:        make(map[string]*papi.Show),
		titleToMedia: make(map[string]*MediaItem),
		log:          log,
	}

	return c, nil
}

// NewEmptyAPIClient creates an empty, non-networked client for headless UI
// development and tests. A configured client can be supplied when catalog data
// is needed.
func NewEmptyAPIClient(log *slog.Logger) *APIClient {
	if log == nil {
		log = slog.Default()
	}
	return &APIClient{
		movies:       make(map[string]*papi.Movie),
		shows:        make(map[string]*papi.Show),
		titleToMedia: make(map[string]*MediaItem),
		log:          log,
	}
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

func (c *APIClient) FetchLibrary() error {
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
	movieItems := make([]*listingItem, 0, len(movieList))
	showItems := make([]*listingItem, 0, len(showList))

	for _, movie := range movieList {
		movies[movie.ImdbID] = movie
		movieItems = append(movieItems, newMovieListingItem(movie))
	}

	for _, show := range showList {
		shows[show.ImdbID] = show
		showItems = append(showItems, newShowListingItem(show))
	}

	snapshot := buildLibrarySnapshot(radixListingBuilder{}, movieItems, showItems)
	c.publishLibrary(movies, shows, movieItems, showItems, snapshot)

	c.log.Info("library fetched", "movies", len(movies), "shows", len(shows))
	return nil
}

func (c *APIClient) publishLibrary(movies map[string]*papi.Movie, shows map[string]*papi.Show, movieItems, showItems []*listingItem, snapshot LibrarySnapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.movies = movies
	c.shows = shows
	c.movieItems = movieItems
	c.showItems = showItems
	c.movieRadix = snapshot.movieRadix
	c.showRadix = snapshot.showRadix
	c.titleToMedia = snapshot.titleToMedia
	c.movieCount = snapshot.movieCount
	c.showCount = snapshot.showCount
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

func (c *APIClient) snapshotFor(builder listingBuilder) LibrarySnapshot {
	// Copy the immutable catalog slices under lock, then build outside the lock.
	c.mu.RLock()
	movieItems := append([]*listingItem(nil), c.movieItems...)
	showItems := append([]*listingItem(nil), c.showItems...)
	movieCount := c.movieCount
	showCount := c.showCount
	c.mu.RUnlock()

	snapshot := buildLibrarySnapshot(builder, movieItems, showItems)
	snapshot.movieCount = movieCount
	snapshot.showCount = showCount
	return snapshot
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

func (c *APIClient) listAvailableSubtitles(video polochon.Video, lang polochon.Language) ([]*polochon.SubtitleEntry, error) {
	return c.client.ListAvailableSubtitles(video, lang)
}

func (c *APIClient) downloadSubtitle(video polochon.Video, lang polochon.Language, entry *polochon.SubtitleEntry) error {
	return c.client.DownloadSubtitle(video, lang, entry)
}

func fetchMediaImage[T any](c *APIClient, id string, lookup func(string) (T, bool), pick func(T) *papi.File, kind string) (image.Image, error) {
	entity, ok := lookup(id)
	if !ok {
		return nil, fmt.Errorf("%s %s not found", kind, id)
	}
	f := pick(entity)
	if f == nil {
		return nil, fmt.Errorf("%s %s: image file not set", kind, id)
	}
	return c.fetchImageFile(f)
}

func (c *APIClient) fetchMovieImage(id string, pick func(*papi.Movie) *papi.File) (image.Image, error) {
	return fetchMediaImage(c, id, c.getMovie, pick, "movie")
}

func (c *APIClient) fetchShowImage(id string, pick func(*papi.Show) *papi.File) (image.Image, error) {
	return fetchMediaImage(c, id, c.getShow, pick, "show")
}

// fetchImageFile is the shared HTTP GET + decode path for any papi image file.
func (c *APIClient) fetchImageFile(f papi.Downloadable) (image.Image, error) {
	body, err := c.downloadFile(f)
	if err != nil {
		return nil, fmt.Errorf("image fetch: %w", err)
	}
	defer func() { _ = body.Close() }()
	img, _, err := image.Decode(body)
	return img, err
}

// downloadFile fetches a Polochon download URL with the configured token.
// Callers own and must close the returned body.
func (c *APIClient) downloadFile(file papi.Downloadable) (io.ReadCloser, error) {
	rawURL, err := c.getDownloadURL(file)
	if err != nil {
		return nil, fmt.Errorf("download URL: %w", err)
	}
	return c.fetchAuthenticatedURL(rawURL)
}

func (c *APIClient) fetchAuthenticatedURL(rawURL string) (io.ReadCloser, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Auth-Token", c.getToken())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("download returned %s", resp.Status)
	}
	return resp.Body, nil
}

// Events returns a channel that emits a signal whenever the server reports a
// library change via SSE.  The channel is closed when ctx is cancelled.
// Internally the papi client reconnects with exponential backoff.
func (c *APIClient) Events(ctx context.Context) <-chan struct{} {
	return c.client.Events(ctx)
}
