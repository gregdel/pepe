package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

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
	client        *papi.Client
	token         string
	server        string // polochon_url without credentials or query, for display
	artworkClient *http.Client

	mu         sync.RWMutex
	movies     map[string]*papi.Movie
	shows      map[string]*papi.Show
	movieItems []*listingItem
	showItems  []*listingItem
	snapshot   LibrarySnapshot

	log *slog.Logger
}

const (
	apiRequestTimeout     = 15 * time.Second
	artworkRequestTimeout = 15 * time.Second
	artworkPayloadLimit   = 16 << 20
	artworkMaxDimension   = 8192
	artworkMaxPixelCount  = 16_777_216
)

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
	papiClient.SetTimeout(apiRequestTimeout)

	c := NewEmptyAPIClient(log)
	c.client, c.token = papiClient, token
	c.server = displayServer(endpoint)
	if endpointURL, err := url.Parse(endpoint); err == nil && strings.EqualFold(endpointURL.Scheme, "http") {
		c.log.Warn("polochon_url uses unencrypted HTTP; use the canonical HTTPS URL if available: HTTP-to-HTTPS artwork redirects are blocked")
	}
	return c, nil
}

func emptyLibrarySnapshot() LibrarySnapshot {
	return LibrarySnapshot{
		movieRadix:   NewRadixTree(nil),
		showRadix:    NewRadixTree(nil),
		titleToMedia: make(map[string]*MediaItem),
	}
}

// NewEmptyAPIClient creates an empty, non-networked client for headless UI
// development and tests. A configured client can be supplied when catalog data
// is needed.
func NewEmptyAPIClient(log *slog.Logger) *APIClient {
	if log == nil {
		log = slog.Default()
	}
	return &APIClient{
		artworkClient: &http.Client{Timeout: artworkRequestTimeout},
		movies:        make(map[string]*papi.Movie),
		shows:         make(map[string]*papi.Show),
		snapshot:      emptyLibrarySnapshot(),
		log:           log,
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
	if c.client == nil {
		return fmt.Errorf("library API client is not configured")
	}
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
	c.snapshot = snapshot
}

func (c *APIClient) getSnapshot() LibrarySnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()

	snapshot := c.snapshot
	snapshot.titleToMedia = make(map[string]*MediaItem, len(c.snapshot.titleToMedia))
	for key, value := range c.snapshot.titleToMedia {
		snapshot.titleToMedia[key] = value
	}
	return snapshot
}

func (c *APIClient) snapshotFor(builder listingBuilder) LibrarySnapshot {
	// Copy the immutable catalog slices under lock, then build outside the lock.
	c.mu.RLock()
	movieItems := append([]*listingItem(nil), c.movieItems...)
	showItems := append([]*listingItem(nil), c.showItems...)
	c.mu.RUnlock()

	return buildLibrarySnapshot(builder, movieItems, showItems)
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

// artworkError carries an app-controlled diagnostic for logging. The original
// error remains available to callers, but its text may contain credentials or
// server-controlled content and must not be logged by the artwork UI.
type artworkError struct {
	reason string
	err    error
}

func (e *artworkError) Error() string { return e.err.Error() }
func (e *artworkError) Unwrap() error { return e.err }

// transientError marks an artwork failure that may succeed later: a network
// error or timeout, a 5xx/408/429 response, or an interrupted download.
type transientError struct{ error }

func (e transientError) Unwrap() error { return e.error }

func isTransientArtworkError(err error) bool {
	var transient transientError
	return errors.As(err, &transient)
}

func artworkLogReason(err error) string {
	var failure *artworkError
	if errors.As(err, &failure) {
		return failure.reason
	}
	return "artwork load failed"
}

func fetchMediaImage[T any](c *APIClient, id string, lookup func(string) (T, bool), pick func(T) *papi.File, kind string) (image.Image, error) {
	entity, ok := lookup(id)
	if !ok {
		return nil, &artworkError{"media not found", fmt.Errorf("%s %s not found", kind, id)}
	}
	f := pick(entity)
	if f == nil {
		return nil, &artworkError{"image file not set", fmt.Errorf("%s %s: image file not set", kind, id)}
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
	img, err := decodeArtwork(body)
	if isTransientArtworkError(err) {
		return nil, err
	}
	if err != nil {
		reason := "artwork decode failed (corrupt image or size limit exceeded)"
		if errors.Is(err, image.ErrFormat) {
			reason = "unsupported image format"
		}
		return nil, &artworkError{reason, err}
	}
	return img, nil
}

func decodeArtwork(body io.Reader) (image.Image, error) {
	payload, err := io.ReadAll(io.LimitReader(body, artworkPayloadLimit+1))
	if err != nil {
		reason := "artwork download interrupted"
		if errors.Is(err, context.DeadlineExceeded) {
			reason = "artwork request timed out"
		}
		return nil, transientError{&artworkError{reason, err}}
	}
	if len(payload) > artworkPayloadLimit {
		return nil, fmt.Errorf("artwork payload exceeds %d bytes", artworkPayloadLimit)
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	if config.Width < 1 || config.Height < 1 || config.Width > artworkMaxDimension || config.Height > artworkMaxDimension || int64(config.Width)*int64(config.Height) > artworkMaxPixelCount {
		return nil, fmt.Errorf("artwork dimensions %dx%d exceed the limit", config.Width, config.Height)
	}
	img, _, err := image.Decode(bytes.NewReader(payload))
	return img, err
}

// downloadFile fetches a Polochon download URL with the configured token.
// Callers own and must close the returned body.
func (c *APIClient) downloadFile(file papi.Downloadable) (io.ReadCloser, error) {
	rawURL, err := c.getDownloadURL(file)
	if err != nil {
		return nil, &artworkError{"invalid artwork download URL", fmt.Errorf("download URL: %w", err)}
	}
	return c.fetchAuthenticatedURL(rawURL)
}

func (c *APIClient) fetchAuthenticatedURL(rawURL string) (io.ReadCloser, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, &artworkError{"invalid artwork request URL", err}
	}
	req.Header.Set("X-Auth-Token", c.getToken())

	// Copy configuration without mutating the client shared by artwork workers.
	// The transport and its connection pool remain shared.
	client := http.Client{Timeout: artworkRequestTimeout}
	if c.artworkClient != nil {
		client = *c.artworkClient
	}
	checkRedirect := client.CheckRedirect
	origin := *req.URL
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return &artworkError{"stopped after 10 redirects", fmt.Errorf("stopped after 10 redirects")}
		}
		if checkRedirect != nil {
			if err := checkRedirect(next, via); err != nil {
				return err
			}
		}
		// Custom headers are copied on every hop, including cross-host hops.
		// Check after any existing callback so it cannot relax this boundary.
		if !sameArtworkOrigin(&origin, next.URL) {
			return &artworkError{"artwork redirect changes origin", fmt.Errorf("artwork redirect changes origin")}
		}
		return nil
	}
	resp, err := client.Do(req)
	if err != nil {
		var failure *artworkError
		if errors.As(err, &failure) {
			return nil, err
		}
		reason := "artwork request failed (check connection, TLS and redirects)"
		if errors.Is(err, context.DeadlineExceeded) {
			reason = "artwork request timed out"
		}
		return nil, transientError{&artworkError{reason, err}}
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		var failure error = &artworkError{
			fmt.Sprintf("download returned %d %s", resp.StatusCode, http.StatusText(resp.StatusCode)),
			fmt.Errorf("download returned %s", resp.Status),
		}
		switch {
		case resp.StatusCode >= 500, resp.StatusCode == http.StatusRequestTimeout, resp.StatusCode == http.StatusTooManyRequests:
			failure = transientError{failure}
		}
		return nil, failure
	}
	return resp.Body, nil
}

func sameArtworkOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) &&
		strings.EqualFold(a.Hostname(), b.Hostname()) &&
		artworkOriginPort(a) == artworkOriginPort(b)
}

func artworkOriginPort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	switch strings.ToLower(u.Scheme) {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}

// Events returns a channel that emits a signal whenever the server reports a
// library change via SSE.  The channel is closed when ctx is cancelled.
// Internally the papi client reconnects with exponential backoff.
func (c *APIClient) Events(ctx context.Context) <-chan struct{} {
	return c.client.Events(ctx)
}

// displayServer strips credentials, query and fragment from polochon_url so it
// can be shown on screen.
func displayServer(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return ""
	}
	u.User, u.RawQuery, u.Fragment = nil, "", ""
	return u.String()
}
