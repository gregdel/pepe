package main

import (
	_ "image/jpeg"
	_ "image/png"
)

// cacheResult carries the result of an async fetch.
type cacheResult[T any] struct {
	key        string
	val        T
	generation uint64
}

type cacheRequest[T any] struct {
	key        string
	fetchFn    func() (T, error)
	generation uint64
}

// asyncCache is a generic on-demand cache with async fetching.
// Keys are caller-defined strings (e.g. "thumb:tt0133093", "nfo:tt0133093").
// A zero-value T in the cache with resolved[key]==true is a permanent-failure
// sentinel — no retry.
type asyncCache[T any] struct {
	cache      map[string]T
	resolved   map[string]bool
	loading    map[string]uint64
	pending    []cacheRequest[T]
	active     int
	maxActive  int
	maxPending int
	generation uint64
	done       chan cacheResult[T]
	onDone     func()
}

func newAsyncCache[T any](maxActive, maxPending int) *asyncCache[T] {
	if maxActive < 1 {
		maxActive = 1
	}
	if maxPending < 0 {
		maxPending = 0
	}
	return &asyncCache[T]{
		cache:      make(map[string]T),
		resolved:   make(map[string]bool),
		loading:    make(map[string]uint64),
		maxActive:  maxActive,
		maxPending: maxPending,
		done:       make(chan cacheResult[T], maxActive),
	}
}

// reset discards cached and queued entries. In-flight requests are allowed to
// finish, but their results are ignored by the new generation.
func (c *asyncCache[T]) reset() {
	c.generation++
	c.cache = make(map[string]T)
	c.resolved = make(map[string]bool)
	c.loading = make(map[string]uint64)
	c.pending = nil
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

// fetch triggers an async load for key if it is not already cached or queued.
// Fetches run in goroutines and are limited by maxActive. Recent requests are
// preferred when the pending queue is full.
func (c *asyncCache[T]) fetch(key string, fetchFn func() (T, error)) {
	if c.resolved[key] {
		return // already resolved (success or permanent failure)
	}
	if _, ok := c.loading[key]; ok {
		return // already in flight
	}

	request := cacheRequest[T]{key: key, fetchFn: fetchFn, generation: c.generation}
	c.loading[key] = c.generation
	if c.active < c.maxActive {
		c.start(request)
		return
	}
	if c.maxPending == 0 {
		delete(c.loading, key)
		return
	}
	if len(c.pending) >= c.maxPending {
		// Prefer recent requests during fast navigation. A dropped request will
		// be retried if it is still visible on a later frame.
		dropped := c.pending[0]
		c.pending = c.pending[1:]
		if c.loading[dropped.key] == dropped.generation {
			delete(c.loading, dropped.key)
		}
	}
	c.pending = append(c.pending, request)
}

func (c *asyncCache[T]) start(request cacheRequest[T]) {
	c.active++
	go func() {
		val, _ := request.fetchFn()
		c.done <- cacheResult[T]{
			key:        request.key,
			val:        val,
			generation: request.generation,
		}
		if c.onDone != nil {
			c.onDone()
		}
	}()
}

func (c *asyncCache[T]) startPending() {
	for c.active < c.maxActive && len(c.pending) > 0 {
		last := len(c.pending) - 1
		request := c.pending[last]
		c.pending = c.pending[:last]
		if c.loading[request.key] != request.generation {
			continue
		}
		c.start(request)
	}
}

// drain reads all pending results from done without blocking. It also starts
// queued work as active slots become available.
func (c *asyncCache[T]) drain() {
	for {
		select {
		case res := <-c.done:
			c.active--
			if res.generation == c.generation && c.loading[res.key] == res.generation {
				delete(c.loading, res.key)
				c.cache[res.key] = res.val
				c.resolved[res.key] = true
			}
			c.startPending()
		default:
			return
		}
	}
}
