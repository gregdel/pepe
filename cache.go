package main

import (
	_ "image/jpeg"
	_ "image/png"

	"gioui.org/app"
)

// cacheResult carries the result of an async fetch.
type cacheResult[T any] struct {
	key string
	val T
	err error
}

// asyncCache is a generic on-demand cache with async fetching.
// Keys are caller-defined strings (e.g. "thumb:tt0133093", "nfo:tt0133093").
// A zero-value T in the cache with resolved[key]==true is a permanent-failure
// sentinel — no retry.
type asyncCache[T any] struct {
	cache    map[string]T
	resolved map[string]bool
	loading  map[string]bool
	done     chan cacheResult[T]
}

func newAsyncCache[T any]() *asyncCache[T] {
	return &asyncCache[T]{
		cache:    make(map[string]T),
		resolved: make(map[string]bool),
		loading:  make(map[string]bool),
		done:     make(chan cacheResult[T], 8),
	}
}

// reset discards all cached entries and in-flight state.
func (c *asyncCache[T]) reset() {
	c.cache = make(map[string]T)
	c.resolved = make(map[string]bool)
	c.loading = make(map[string]bool)
}

// get returns (val, true) if key has been resolved.
// val may be zero/nil if the fetch failed permanently.
func (c *asyncCache[T]) get(key string) (T, bool) {
	val, ok := c.resolved[key]
	if !ok {
		var zero T
		return zero, false
	}
	_ = val
	return c.cache[key], true
}

// fetch triggers an async load for key if it is not already cached or in-flight.
// fetchFn is called in a goroutine; on completion the result is sent to done
// and w.Invalidate() is called to wake the event loop.
func (c *asyncCache[T]) fetch(key string, fetchFn func() (T, error), w *app.Window) {
	if c.resolved[key] {
		return // already resolved (success or permanent failure)
	}
	if c.loading[key] {
		return // already in flight
	}
	c.loading[key] = true
	go func() {
		val, err := fetchFn()
		c.done <- cacheResult[T]{key: key, val: val, err: err}
		w.Invalidate()
	}()
}

// drain reads all pending results from done without blocking.
// Returns true if at least one result arrived successfully (caller should Invalidate).
func (c *asyncCache[T]) drain() bool {
	invalidate := false
	for {
		select {
		case res := <-c.done:
			delete(c.loading, res.key)
			c.cache[res.key] = res.val
			c.resolved[res.key] = true
			if res.err == nil && any(res.val) != nil {
				invalidate = true
			}
		default:
			return invalidate
		}
	}
}
