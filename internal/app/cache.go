package app

import (
	"container/list"
	_ "image/jpeg"
	_ "image/png"
	"time"
)

// Transient failures are retried after a backoff that doubles from
// cacheRetryBase up to cacheRetryMax.
const (
	cacheRetryBase = 2 * time.Second
	cacheRetryMax  = time.Minute
)

// cacheResult carries the result of an async fetch.
type cacheResult[T any] struct {
	key        string
	val        T
	generation uint64
	retry      bool
}

type cacheRetry struct {
	at       time.Time
	attempts int
}

type cacheRequest[T any] struct {
	key        string
	fetchFn    func() (T, error)
	generation uint64
}

type cacheWeightEntry struct {
	key    string
	weight int64
}

// asyncCache is a generic on-demand cache with async fetching.
// Keys are caller-defined strings (e.g. "thumb:tt0133093", "fanart:tt0133093").
// A zero-value T stored in the cache is a failure sentinel. It is permanent
// unless retryable reports the error as transient: then get treats the key as
// unresolved again once its backoff has elapsed, so the caller fetches it anew.
//
// Only what is on screen waits for a fetch slot: sweep, called once per
// frame, drops queued requests for keys the previous frame did not ask for
// (get or fetch). Fetches already running finish, since on a high-latency
// link cancelling them wastes the round trip already paid.
type asyncCache[T any] struct {
	cache       map[string]T
	loading     map[string]uint64
	wanted      map[string]bool // keys asked for since the last sweep
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
	retryable   func(error) bool
	retries     map[string]cacheRetry
	now         func() time.Time
}

// The budget must be positive; weight must be non-nil and zero for failures.
func newWeightedAsyncCache[T any](maxActive, maxPending int, maxWeight int64, weight func(T) int64) *asyncCache[T] {
	if maxActive < 1 {
		maxActive = 1
	}
	if maxPending < 0 {
		maxPending = 0
	}
	return &asyncCache[T]{
		cache:      make(map[string]T),
		loading:    make(map[string]uint64),
		wanted:     make(map[string]bool),
		maxActive:  maxActive,
		maxPending: maxPending,
		done:       make(chan cacheResult[T], maxActive),
		weight:     weight,
		maxWeight:  maxWeight,
		lru:        list.New(),
		lruEntries: make(map[string]*list.Element),
		retries:    make(map[string]cacheRetry),
		now:        time.Now,
	}
}

// reset discards cached and queued entries. In-flight requests are allowed to
// finish, but their results are ignored by the new generation.
func (c *asyncCache[T]) reset() {
	c.generation++
	c.cache = make(map[string]T)
	c.loading = make(map[string]uint64)
	c.pending = nil
	c.cacheWeight = 0
	c.lru.Init()
	c.lruEntries = make(map[string]*list.Element)
	c.retries = make(map[string]cacheRetry)
}

// busy reports whether fetches are in flight, queued, or awaiting drain.
func (c *asyncCache[T]) busy() bool {
	return c.active > 0 || len(c.pending) > 0
}

// get returns (val, true) if key has been resolved.
// val may be zero/nil if the fetch failed. A transient failure whose backoff
// has elapsed is dropped and reported as unresolved, so the caller refetches.
func (c *asyncCache[T]) get(key string) (T, bool) {
	c.wanted[key] = true
	val, ok := c.cache[key]
	if retry, failed := c.retries[key]; ok && failed && !c.now().Before(retry.at) {
		delete(c.cache, key)
		var zero T
		return zero, false
	}
	if entry := c.lruEntries[key]; ok && entry != nil {
		c.lru.MoveToFront(entry)
	}
	return val, ok
}

// retryAt reports when a transient failure cached for key may be retried.
func (c *asyncCache[T]) retryAt(key string) (time.Time, bool) {
	if _, ok := c.cache[key]; !ok {
		return time.Time{}, false
	}
	retry, ok := c.retries[key]
	return retry.at, ok
}

// fetch triggers an async load for key if it is not already cached or queued.
// Fetches run in goroutines and are limited by maxActive. Recent requests are
// preferred when the pending queue is full.
func (c *asyncCache[T]) fetch(key string, fetchFn func() (T, error)) {
	c.wanted[key] = true
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
		val, err := request.fetchFn()
		retry := false
		if err != nil {
			var zero T
			val = zero
			retry = c.retryable != nil && c.retryable(err)
		}
		c.done <- cacheResult[T]{
			key:        request.key,
			val:        val,
			generation: request.generation,
			retry:      retry,
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
				c.recordRetry(res.key, res.retry)
				c.store(res.key, res.val)
			}
			c.startPending()
		default:
			return
		}
	}
}

// sweep drops queued requests for keys not asked for since the last sweep,
// then starts tracking the next frame.
func (c *asyncCache[T]) sweep() {
	kept := c.pending[:0]
	for _, request := range c.pending {
		if c.wanted[request.key] {
			kept = append(kept, request)
		} else if c.loading[request.key] == request.generation {
			delete(c.loading, request.key)
		}
	}
	clear(c.pending[len(kept):])
	c.pending = kept
	clear(c.wanted)
}

// recordRetry schedules the next attempt after a transient failure, doubling
// the delay each time, and forgets the attempts after any other result.
func (c *asyncCache[T]) recordRetry(key string, retry bool) {
	if !retry {
		delete(c.retries, key)
		return
	}
	next := c.retries[key]
	delay := cacheRetryMax
	if next.attempts < 16 {
		delay = min(cacheRetryBase<<next.attempts, cacheRetryMax)
	}
	next.attempts++
	next.at = c.now().Add(delay)
	c.retries[key] = next
}
