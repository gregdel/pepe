package main

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
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
	MPVPath     string

	Fullscreen       bool
	PlayerFullscreen bool
}

type fileConfig struct {
	PolochonURL      string `json:"polochon_url"`
	Token            string `json:"polochon_token"`
	MPVPath          string `json:"mpv_path"`
	Fullscreen       bool   `json:"fullscreen"`
	PlayerFullscreen bool   `json:"player_fullscreen"`
}

func defaultConfigPath() string {
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "polochon-gio", "config.json")
	}
	return filepath.Join(".", "polochon-gio.json")
}

func loadConfig(path string) (*Config, error) {
	cfg := &Config{
		MPVPath: "mpv",
	}

	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			if !os.IsNotExist(err) {
				return nil, fmt.Errorf("read config file %s: %w", path, err)
			}
		} else {
			var fc fileConfig
			if err := json.Unmarshal(b, &fc); err != nil {
				return nil, fmt.Errorf("parse config file %s: %w", path, err)
			}
			cfg.PolochonURL = strings.TrimSpace(fc.PolochonURL)
			cfg.Token = strings.TrimSpace(fc.Token)
			if strings.TrimSpace(fc.MPVPath) != "" {
				cfg.MPVPath = strings.TrimSpace(fc.MPVPath)
			}
			cfg.Fullscreen = fc.Fullscreen
			cfg.PlayerFullscreen = fc.PlayerFullscreen
		}
	}

	url := os.Getenv("POLOCHON_URL")
	if url != "" {
		cfg.PolochonURL = strings.TrimSpace(url)
	}
	if cfg.PolochonURL == "" {
		return nil, fmt.Errorf("POLOCHON_URL environment variable is required")
	}

	token := os.Getenv("POLOCHON_TOKEN")
	if token != "" {
		cfg.Token = strings.TrimSpace(token)
	}
	if cfg.Token == "" {
		return nil, fmt.Errorf("POLOCHON_TOKEN environment variable is required")
	}

	if mpvPath := strings.TrimSpace(os.Getenv("POLOCHON_MPV")); mpvPath != "" {
		cfg.MPVPath = mpvPath
	}

	return cfg, nil
}
