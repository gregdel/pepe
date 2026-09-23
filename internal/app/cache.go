package app

import (
	"container/list"
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
// Keys are caller-defined strings (e.g. "thumb:tt0133093", "fanart:tt0133093").
// A zero-value T stored in the cache is a permanent-failure sentinel — no retry.
type cacheWeightEntry struct {
	key    string
	weight int64
}

type asyncCache[T any] struct {
	cache       map[string]T
	loading     map[string]uint64
	pending     []cacheRequest[T]
	active      int
	maxActive   int
	maxPending  int
	generation  uint64
	done        chan cacheResult[T]
	onDone      func()
	weight      func(T) int64
	maxWeight   int64
	cacheWeight int64
	lru         *list.List
	lruEntries  map[string]*list.Element
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
		loading:    make(map[string]uint64),
		maxActive:  maxActive,
		maxPending: maxPending,
		done:       make(chan cacheResult[T], maxActive),
	}
}

func newWeightedAsyncCache[T any](maxActive, maxPending int, maxWeight int64, weight func(T) int64) *asyncCache[T] {
	cache := newAsyncCache[T](maxActive, maxPending)
	cache.weight = weight
	cache.maxWeight = maxWeight
	cache.lru = list.New()
	cache.lruEntries = make(map[string]*list.Element)
	return cache
}

// reset discards cached and queued entries. In-flight requests are allowed to
// finish, but their results are ignored by the new generation.
func (c *asyncCache[T]) reset() {
	c.generation++
	c.cache = make(map[string]T)
	c.loading = make(map[string]uint64)
	c.pending = nil
	c.cacheWeight = 0
	if c.lru != nil {
		c.lru.Init()
		c.lruEntries = make(map[string]*list.Element)
	}
}

// get returns (val, true) if key has been resolved.
// val may be zero/nil if the fetch failed permanently.
func (c *asyncCache[T]) get(key string) (T, bool) {
	val, ok := c.cache[key]
	if ok && c.lru != nil {
		if entry := c.lruEntries[key]; entry != nil {
			c.lru.MoveToFront(entry)
		}
	}
	return val, ok
}

// fetch triggers an async load for key if it is not already cached or queued.
// Fetches run in goroutines and are limited by maxActive. Recent requests are
// preferred when the pending queue is full.
func (c *asyncCache[T]) fetch(key string, fetchFn func() (T, error)) {
	if _, ok := c.cache[key]; ok {
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

func (c *asyncCache[T]) store(key string, value T) {
	c.cache[key] = value
	if c.weight == nil || c.maxWeight <= 0 {
		return
	}
	weight := c.weight(value)
	if weight <= 0 {
		return
	}
	if weight > c.maxWeight {
		delete(c.cache, key)
		return
	}
	if old := c.lruEntries[key]; old != nil {
		entry := old.Value.(cacheWeightEntry)
		c.cacheWeight -= entry.weight
		c.lru.Remove(old)
	}
	entry := c.lru.PushFront(cacheWeightEntry{key: key, weight: weight})
	c.lruEntries[key] = entry
	c.cacheWeight += weight
	for c.cacheWeight > c.maxWeight {
		oldest := c.lru.Back()
		if oldest == nil {
			break
		}
		old := oldest.Value.(cacheWeightEntry)
		delete(c.cache, old.key)
		delete(c.lruEntries, old.key)
		c.cacheWeight -= old.weight
		c.lru.Remove(oldest)
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
				c.store(res.key, res.val)
			}
			c.startPending()
		default:
			return
		}
	}
}
