package main

import (
	"gioui.org/app"
	polochon "github.com/odwrtw/polochon/lib"
)

// nfoResult carries the result of an async NFO fetch.
type nfoResult struct {
	key string
	m   *polochon.Movie
	err error
}

// nfoCache is an on-demand cache for parsed NFO data with async fetching.
// Keys are caller-defined strings (e.g. "nfo:tt0133093").
// A nil value in the cache map is a permanent-failure sentinel — no retry.
type nfoCache struct {
	cache   map[string]*polochon.Movie
	loading map[string]bool
	done    chan nfoResult
}

func newNFOCache() *nfoCache {
	return &nfoCache{
		cache:   make(map[string]*polochon.Movie),
		loading: make(map[string]bool),
		done:    make(chan nfoResult, 8),
	}
}

// reset discards all cached entries and in-flight state.
func (c *nfoCache) reset() {
	c.cache = make(map[string]*polochon.Movie)
	c.loading = make(map[string]bool)
}

// get returns (movie, true) if key has been resolved.
// movie may be nil if the fetch failed permanently.
func (c *nfoCache) get(key string) (*polochon.Movie, bool) {
	m, ok := c.cache[key]
	return m, ok
}

// fetch triggers an async load for key if not already cached or in-flight.
// fetchFn is called in a goroutine; on completion the result is sent to done
// and w.Invalidate() wakes the event loop.
func (c *nfoCache) fetch(key string, fetchFn func() (*polochon.Movie, error), w *app.Window) {
	if _, ok := c.cache[key]; ok {
		return // already resolved
	}
	if c.loading[key] {
		return // already in flight
	}
	c.loading[key] = true
	go func() {
		m, err := fetchFn()
		c.done <- nfoResult{key: key, m: m, err: err}
		w.Invalidate()
	}()
}

// drain reads all pending results from done without blocking.
// Returns true if at least one NFO arrived successfully.
func (c *nfoCache) drain() bool {
	invalidate := false
	for {
		select {
		case res := <-c.done:
			delete(c.loading, res.key)
			c.cache[res.key] = res.m // nil on error = permanent sentinel
			if res.err == nil && res.m != nil {
				invalidate = true
			}
		default:
			return invalidate
		}
	}
}
