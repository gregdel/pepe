package app

import (
	"bytes"
	"errors"
	"image"
	"image/jpeg"
	"net/http"
	"strconv"

	"github.com/odwrtw/polochon/lib/papi"
)

// Web search posters: about twice the phone's CSS size, as a small JPEG.
const (
	webPosterWidth    = 240
	webPosterHeight   = 360
	webPosterQuality  = 80
	webArtworkFetches = 3 // concurrent Polochon fetches for lazily loaded rows
)

// webTitle is one search entry; the phone matches titles itself.
type webTitle struct {
	Kind  MediaKind `json:"kind"`
	ID    string    `json:"id"`
	Title string    `json:"title"`
	Year  int       `json:"year,omitempty"`
	Added int64     `json:"added,omitempty"` // unix seconds
}

// webCatalog lists movies then shows with the generation that identifies the
// published library, which versions artwork URLs.
func (c *APIClient) webCatalog() (uint64, []webTitle) {
	titles := []webTitle{}
	if c == nil {
		return 0, titles
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, items := range [][]*listingItem{c.movieItems, c.showItems} {
		for _, item := range items {
			title := webTitle{Kind: item.media.Type, ID: item.media.ID, Title: item.media.Title, Year: item.media.Year}
			if added := item.dateAdded(); !added.IsZero() {
				title.Added = added.Unix()
			}
			titles = append(titles, title)
		}
	}
	return c.generation, titles
}

func (c *APIClient) catalogGeneration() uint64 {
	if c == nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.generation
}

// mediaItem resolves a stable identity against the current library.
func (c *APIClient) mediaItem(kind MediaKind, id string) (*MediaItem, bool) {
	if c == nil {
		return nil, false
	}
	switch kind {
	case mediaKindMovie:
		if movie, ok := c.getMovie(id); ok {
			return newMovieListingItem(movie).media, true
		}
	case mediaKindShow:
		if show, ok := c.getShow(id); ok {
			return newShowListingItem(show).media, true
		}
	}
	return nil, false
}

// catalogRoutes serves search data. Handlers read only the concurrency-safe
// APIClient; opening a title is queued for the event loop like any command.
func (web *webUI) catalogRoutes(mux *http.ServeMux) {
	fetches := make(chan struct{}, webArtworkFetches)
	mux.HandleFunc("GET /api/catalog", func(w http.ResponseWriter, r *http.Request) {
		version, titles := web.client.webCatalog()
		writeWebJSON(w, http.StatusOK, struct {
			Version uint64     `json:"version"`
			Titles  []webTitle `json:"titles"`
		}{version, titles})
	})
	mux.HandleFunc("GET /api/artwork/{kind}/{id}", func(w http.ResponseWriter, r *http.Request) {
		kind, id := MediaKind(r.PathValue("kind")), r.PathValue("id")
		if _, ok := web.client.mediaItem(kind, id); !ok {
			writeWebError(w, http.StatusNotFound, "title not found")
			return
		}
		select {
		case fetches <- struct{}{}:
			defer func() { <-fetches }()
		case <-r.Context().Done():
			return
		}
		var img image.Image
		var err error
		if kind == mediaKindShow {
			img, err = web.client.fetchShowImage(id, func(s *papi.Show) *papi.File { return s.PosterFile })
		} else {
			img, err = web.client.fetchMovieImage(id, func(m *papi.Movie) *papi.File { return m.ThumbFile })
		}
		if err != nil {
			web.client.log.Debug("web artwork unavailable", "kind", kind, "id", id, "reason", artworkLogReason(err))
			status := http.StatusBadGateway
			var failure *artworkError
			if errors.As(err, &failure) && !isTransientArtworkError(err) {
				status = http.StatusNotFound
			}
			writeWebError(w, status, "artwork unavailable")
			return
		}
		var body bytes.Buffer
		if err := jpeg.Encode(&body, resizeArtwork(img, webPosterWidth, webPosterHeight), &jpeg.Options{Quality: webPosterQuality}); err != nil {
			writeWebError(w, http.StatusInternalServerError, "artwork unavailable")
			return
		}
		// The page adds the catalog generation (?v=) to every artwork URL,
		// so a library refresh changes the URL instead of serving stale art.
		w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Content-Length", strconv.Itoa(body.Len()))
		_, _ = w.Write(body.Bytes())
	})
	mux.HandleFunc("POST /api/open", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Kind MediaKind `json:"kind"`
			ID   string    `json:"id"`
			Play bool      `json:"play"`
		}
		if !decodeWebJSON(w, r, &request) {
			return
		}
		if request.Kind != mediaKindMovie && request.Kind != mediaKindShow {
			writeWebError(w, http.StatusBadRequest, "unknown title type")
			return
		}
		if request.Play && request.Kind != mediaKindMovie {
			writeWebError(w, http.StatusBadRequest, "only movies play directly; open the show instead")
			return
		}
		if _, ok := web.client.mediaItem(request.Kind, request.ID); !ok {
			writeWebError(w, http.StatusNotFound, "This title is no longer in the library.")
			return
		}
		web.accept(w, webCommand{media: &MediaItem{ID: request.ID, Type: request.Kind}, play: request.Play})
	})
}

// openFromWeb shows a searched title on the TV: its detail page, with the
// browse list selecting it so Back lands on it, or a movie's playback.
func (a *App) openFromWeb(kind MediaKind, id string, play bool) {
	if a.setup != nil {
		a.webCommandError = "Finish setup on the TV first."
		return
	}
	media, ok := a.client.mediaItem(kind, id)
	if !ok {
		a.webCommandError = "This title is no longer in the library."
		return
	}
	if play {
		a.launchMovie(media)
		return
	}
	a.closeSubtitlePopup()
	a.browse.closePage()
	a.browse.focus = browseFocusContent
	section := SectionMovies
	if kind == mediaKindShow {
		section = SectionShows
	}
	if a.browse.nav.navigateToMedia(a.sectionRoot(section), kind, id) {
		a.browse.nav.section = section
	}
	a.detail = nil
	if kind == mediaKindShow {
		a.pushShowDetail(media)
		if a.detail == nil {
			a.webCommandError = "This show has no episodes yet."
		}
		return
	}
	a.pushMovieDetail(media)
}
