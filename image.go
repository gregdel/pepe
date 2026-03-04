package main

import (
	"image"
	_ "image/jpeg"
	_ "image/png"

	"gioui.org/app"
)

// imageResult carries the result of an async image fetch.
type imageResult struct {
	key string
	img image.Image
	err error
}

// imageCache is a generic on-demand image cache with async fetching.
// Keys are caller-defined strings (e.g. "thumb:tt0133093", "fanart:tt0133093").
// A nil value in the cache map is a permanent-failure sentinel — no retry.
type imageCache struct {
	cache   map[string]image.Image
	loading map[string]bool
	done    chan imageResult
}

func newImageCache() *imageCache {
	return &imageCache{
		cache:   make(map[string]image.Image),
		loading: make(map[string]bool),
		done:    make(chan imageResult, 8),
	}
}

// reset discards all cached images and in-flight state.
// Call on library refresh so stale entries don't linger.
func (c *imageCache) reset() {
	c.cache = make(map[string]image.Image)
	c.loading = make(map[string]bool)
}

// get returns (img, true) if key has been resolved.
// img may be nil if the fetch failed permanently.
func (c *imageCache) get(key string) (image.Image, bool) {
	img, ok := c.cache[key]
	return img, ok
}

// fetch triggers an async load for key if it is not already cached or in-flight.
// fetchFn is called in a goroutine; on completion the result is sent to done
// and w.Invalidate() is called to wake the event loop.
func (c *imageCache) fetch(key string, fetchFn func() (image.Image, error), w *app.Window) {
	if _, ok := c.cache[key]; ok {
		return // already resolved (success or permanent failure)
	}
	if c.loading[key] {
		return // already in flight
	}
	c.loading[key] = true
	go func() {
		img, err := fetchFn()
		c.done <- imageResult{key: key, img: img, err: err}
		w.Invalidate()
	}()
}

// drain reads all pending results from done without blocking.
// Returns true if at least one image arrived successfully (caller should Invalidate).
func (c *imageCache) drain() bool {
	invalidate := false
	for {
		select {
		case res := <-c.done:
			delete(c.loading, res.key)
			c.cache[res.key] = res.img // nil on error = permanent sentinel
			if res.err == nil && res.img != nil {
				invalidate = true
			}
		default:
			return invalidate
		}
	}
}
