package app

import (
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

const (
	progressVersion = 1
	// watchedFraction of the duration counts as watched: credits rarely reach EOF.
	watchedFraction = 0.9
	// progressSaveDelay coalesces bursts of marks into one write.
	progressSaveDelay = 3 * time.Second
	// progressFlushTimeout bounds how long shutdown waits for a slow disk.
	progressFlushTimeout = 5 * time.Second
)

// progressEntry is one title's watch state, keyed by its media key.
type progressEntry struct {
	Pos     float64 `json:"pos,omitempty"` // resume position in seconds
	Watched bool    `json:"watched,omitempty"`
	At      int64   `json:"at"` // last change, Unix seconds
}

type progressFile struct {
	Version int                      `json:"v"`
	Items   map[string]progressEntry `json:"items"`
}

// progressStore holds watch progress for the event loop. With a file, changes
// are written by one goroutine after progressSaveDelay, newest state only.
type progressStore struct {
	entries map[string]progressEntry // event loop only
	now     func() time.Time

	path    string // empty keeps progress in memory
	mu      sync.Mutex
	pending map[string]progressEntry // newest unsaved state, nil when none
	signal  chan struct{}
	quit    chan struct{}
	done    chan struct{}
	delay   time.Duration
	logger  func() *slog.Logger
}

func newProgressStore() *progressStore {
	return &progressStore{entries: make(map[string]progressEntry), now: time.Now}
}

// DefaultProgressPath follows the XDG base directory spec for state on Linux
// and other Unix systems, and keeps it beside the config elsewhere.
func DefaultProgressPath() (string, error) {
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(dir, "pepe", "progress.json"), nil
	}
	if dir := os.Getenv("XDG_STATE_HOME"); filepath.IsAbs(dir) {
		return filepath.Join(dir, "pepe", "progress.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "pepe", "progress.json"), nil
}

// open loads path and starts the writer. A missing file starts empty. History
// is never overwritten unseen: an unreadable file or one from a newer version
// keeps progress in memory, and a corrupt one is moved aside first.
func (p *progressStore) open(path string, logger func() *slog.Logger) {
	p.logger = logger
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		logger().Warn("cannot read watch progress; not saving", "path", path, "error", err)
		return
	default:
		var file progressFile
		if err := json.Unmarshal(data, &file); err == nil && file.Version > progressVersion {
			logger().Warn("unsupported watch progress version; not saving", "path", path, "version", file.Version)
			return
		} else if err == nil && file.Version == progressVersion {
			if file.Items != nil {
				p.entries = file.Items
			}
			break
		}
		aside := path + ".corrupt"
		if err := os.Rename(path, aside); err != nil {
			logger().Warn("corrupt watch progress cannot be moved aside; not saving", "path", path, "error", err)
			return
		}
		logger().Warn("corrupt watch progress moved aside; starting empty", "path", aside)
	}
	p.path = path
	if p.delay == 0 {
		p.delay = progressSaveDelay
	}
	p.signal = make(chan struct{}, 1)
	p.quit = make(chan struct{})
	p.done = make(chan struct{})
	go p.writer()
}

// close writes any pending change and stops the writer, waiting at most
// progressFlushTimeout.
func (p *progressStore) close() {
	if p == nil || p.done == nil {
		return
	}
	select {
	case <-p.quit:
	default:
		close(p.quit)
	}
	select {
	case <-p.done:
	case <-time.After(progressFlushTimeout):
		p.logger().Warn("watch progress save did not finish before exit", "path", p.path)
	}
}

func (p *progressStore) writer() {
	defer close(p.done)
	var due <-chan time.Time
	for {
		select {
		case <-p.signal:
			if due == nil {
				due = time.After(p.delay)
			}
		case <-due:
			due = nil
			p.flush()
		case <-p.quit:
			p.flush()
			return
		}
	}
}

func (p *progressStore) flush() {
	p.mu.Lock()
	entries := p.pending
	p.pending = nil
	p.mu.Unlock()
	if entries == nil {
		return
	}
	data, err := json.Marshal(progressFile{Version: progressVersion, Items: entries})
	if err == nil {
		err = writeFileAtomic(p.path, data)
	}
	if err != nil {
		p.logger().Error("cannot save watch progress", "path", p.path, "error", err)
	}
}

// changed queues the current state for the writer.
func (p *progressStore) changed() {
	if p.path == "" {
		return
	}
	snapshot := maps.Clone(p.entries)
	p.mu.Lock()
	p.pending = snapshot
	p.mu.Unlock()
	select {
	case p.signal <- struct{}{}:
	default:
	}
}

// position returns the resume position, or 0 when none is worth resuming.
func (p *progressStore) position(key string) float64 {
	if pos := p.entries[key].Pos; pos > minimumResumePosition {
		return pos
	}
	return 0
}

func (p *progressStore) watched(key string) bool {
	return p.entries[key].Watched
}

// recordPlayback stores where playback stopped. Reaching EOF or watchedFraction
// of a known duration marks the title watched and clears its resume point.
func (p *progressStore) recordPlayback(key string, pos, duration float64, eof bool) {
	if key == "" {
		return
	}
	entry := p.entries[key]
	switch {
	case eof || duration > 0 && pos >= watchedFraction*duration:
		entry = progressEntry{Watched: true}
	case pos > minimumResumePosition:
		entry.Pos = pos
	default:
		return
	}
	entry.At = p.now().Unix()
	p.entries[key] = entry
	p.changed()
}

// toggleWatched flips the watched mark. Marking clears the resume point;
// unmarking keeps one left by a partial rewatch.
func (p *progressStore) toggleWatched(key string) {
	if key == "" {
		return
	}
	if entry := p.entries[key]; entry.Watched && entry.Pos > 0 {
		p.entries[key] = progressEntry{Pos: entry.Pos, At: p.now().Unix()}
	} else if entry.Watched {
		delete(p.entries, key)
	} else {
		p.entries[key] = progressEntry{Watched: true, At: p.now().Unix()}
	}
	p.changed()
}

// openProgress keeps progress on disk unless the config turns it off.
func (a *App) openProgress(config *Config) {
	if !config.saveProgress() {
		return
	}
	path, err := DefaultProgressPath()
	if err != nil {
		a.logger().Warn("no state directory; watch progress is not saved", "error", err)
		return
	}
	a.progress.open(path, a.logger)
}

// currentWatchKey is the media key that w marks: the movie on its detail page
// or selected in browse, or the focused episode in a show's episode list.
func (a *App) currentWatchKey() string {
	if d := a.detail; d != nil {
		if d.kind == detailMovie {
			if movie, ok := a.client.getMovie(d.movieID); ok {
				return movieMediaKey(movie.ImdbID)
			}
			return ""
		}
		if d.show.focus != focusEpisodes {
			return ""
		}
		if node := d.show.currentEpisode(); node != nil && node.Episode != nil {
			return episodeMediaKey(node.ShowID, node.Episode.Season, node.Episode.Episode)
		}
		return ""
	}
	if a.browse.onPage() || a.browse.focus == browseFocusModeRail {
		return ""
	}
	if media := a.selectedBrowseMedia(); media != nil && media.Type == mediaKindMovie {
		if movie, ok := a.client.getMovie(media.ID); ok {
			return movieMediaKey(movie.ImdbID)
		}
	}
	return ""
}

func (a *App) toggleWatched() {
	a.progress.toggleWatched(a.currentWatchKey())
}

// watchedHint advertises w where it applies, naming what it will do.
func (a *App) watchedHint() []hint {
	key := a.currentWatchKey()
	if key == "" {
		return nil
	}
	if a.progress.watched(key) {
		return []hint{{key: "w", label: "unwatched"}}
	}
	return []hint{{key: "w", label: "watched"}}
}
