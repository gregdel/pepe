package app

import (
	"errors"
	"image"
	"sync/atomic"
	"testing"
	"time"

	"gioui.org/op/paint"
)

func newAsyncCache[T any](maxActive, maxPending int) *asyncCache[T] {
	return newWeightedAsyncCache(maxActive, maxPending, 1, func(T) int64 { return 0 })
}

func waitForCache(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for cache state")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestAsyncCacheLimitsActiveFetchesAndStartsQueuedWork(t *testing.T) {
	c := newAsyncCache[int](2, 4)
	activeRelease := make(chan struct{})
	queuedRelease := make(chan struct{})
	var startCount atomic.Int32
	var running atomic.Int32
	var maxRunning atomic.Int32

	fetch := func() (int, error) {
		runningNow := running.Add(1)
		for {
			max := maxRunning.Load()
			if runningNow <= max || maxRunning.CompareAndSwap(max, runningNow) {
				break
			}
		}
		startNumber := startCount.Add(1)
		if startNumber <= 2 {
			<-activeRelease
		} else {
			<-queuedRelease
		}
		running.Add(-1)
		return 1, nil
	}

	for i := 0; i < 4; i++ {
		c.fetch(string(rune('a'+i)), fetch)
	}
	waitForCache(t, func() bool { return startCount.Load() == 2 })
	if got := startCount.Load(); got != 2 {
		t.Fatalf("started %d fetches before a slot was available, want 2", got)
	}
	if got := maxRunning.Load(); got > 2 {
		t.Fatalf("maximum concurrent fetches = %d, want at most 2", got)
	}

	close(activeRelease)
	waitForCache(t, func() bool { return len(c.done) == 2 })
	c.drain()
	waitForCache(t, func() bool { return startCount.Load() == 4 })
	if got := len(c.done); got != 0 {
		t.Fatalf("queued fetches completed before release: %d", got)
	}
	close(queuedRelease)
	waitForCache(t, func() bool { return len(c.done) == 2 })
	c.drain()
}

func TestAsyncCacheDropsOldestPendingRequest(t *testing.T) {
	c := newAsyncCache[int](1, 2)
	firstStarted := make(chan struct{})
	firstRelease := make(chan struct{})
	newRelease := make(chan struct{})
	middleRelease := make(chan struct{})
	started := make(chan string, 2)

	c.fetch("first", func() (int, error) {
		close(firstStarted)
		<-firstRelease
		return 1, nil
	})
	<-firstStarted
	c.fetch("old", func() (int, error) {
		started <- "old"
		return 2, nil
	})
	c.fetch("middle", func() (int, error) {
		started <- "middle"
		<-middleRelease
		return 3, nil
	})
	c.fetch("new", func() (int, error) {
		started <- "new"
		<-newRelease
		return 4, nil
	})

	close(firstRelease)
	waitForCache(t, func() bool { return len(c.done) == 1 })
	c.drain()
	waitForCache(t, func() bool { return len(started) == 1 })
	if first := <-started; first != "new" {
		t.Fatalf("first queued request = %q, want new", first)
	}
	close(newRelease)
	waitForCache(t, func() bool { return len(c.done) == 1 })
	c.drain()
	waitForCache(t, func() bool { return len(started) == 1 })
	if second := <-started; second != "middle" {
		t.Fatalf("second queued request = %q, want middle", second)
	}
	close(middleRelease)
	waitForCache(t, func() bool { return len(c.done) == 1 })
	c.drain()

	select {
	case extra := <-started:
		t.Fatalf("unexpected queued request started: %q", extra)
	default:
	}
	if _, ok := c.get("old"); ok {
		t.Fatal("dropped request was resolved")
	}
}

func TestAsyncCacheDeduplicatesRequests(t *testing.T) {
	c := newAsyncCache[int](1, 1)
	var calls atomic.Int32

	fetch := func() (int, error) {
		calls.Add(1)
		return 7, nil
	}
	c.fetch("key", fetch)
	c.fetch("key", fetch)

	waitForCache(t, func() bool { return len(c.done) == 1 })
	c.drain()
	if got := calls.Load(); got != 1 {
		t.Fatalf("fetch called %d times, want 1", got)
	}
	if value, ok := c.get("key"); !ok || value != 7 {
		t.Fatalf("cached value = (%d, %v), want (7, true)", value, ok)
	}
}

func TestAsyncCacheResetIgnoresStaleResults(t *testing.T) {
	c := newAsyncCache[int](1, 1)
	oldStarted := make(chan struct{})
	oldRelease := make(chan struct{})
	newStarted := make(chan struct{})
	newRelease := make(chan struct{})

	c.fetch("key", func() (int, error) {
		close(oldStarted)
		<-oldRelease
		return 1, nil
	})
	<-oldStarted

	c.reset()
	c.fetch("key", func() (int, error) {
		close(newStarted)
		<-newRelease
		return 2, nil
	})
	close(oldRelease)
	waitForCache(t, func() bool { return len(c.done) == 1 })
	c.drain()
	if _, ok := c.get("key"); ok {
		t.Fatal("stale result was inserted after reset")
	}
	<-newStarted
	close(newRelease)
	waitForCache(t, func() bool { return len(c.done) == 1 })
	c.drain()
	if value, ok := c.get("key"); !ok || value != 2 {
		t.Fatalf("cached value = (%d, %v), want (2, true)", value, ok)
	}
}

func TestWeightedCacheEvictsLeastRecentlyUsedAndRefetches(t *testing.T) {
	c := newWeightedAsyncCache[int](1, 0, 5, func(value int) int64 { return int64(value) })
	fetchValue := func(key string, value int) {
		c.fetch(key, func() (int, error) { return value, nil })
		waitForCache(t, func() bool { return len(c.done) == 1 })
		c.drain()
	}

	fetchValue("a", 3)
	if _, ok := c.get("a"); !ok {
		t.Fatal("first successful value was not cached")
	}
	fetchValue("b", 3)
	if _, ok := c.get("a"); ok {
		t.Fatal("least-recently-used value was not evicted")
	}
	if value, ok := c.get("b"); !ok || value != 3 {
		t.Fatalf("remaining value = (%d, %v), want (3, true)", value, ok)
	}
	if c.cacheWeight != 3 {
		t.Fatalf("cache weight = %d, want 3", c.cacheWeight)
	}

	fetchValue("a", 4)
	if _, ok := c.get("b"); ok {
		t.Fatal("re-fetched value did not evict the remaining least-recently-used entry")
	}
	if value, ok := c.get("a"); !ok || value != 4 {
		t.Fatalf("re-fetched value = (%d, %v), want (4, true)", value, ok)
	}
}

func TestWeightedCacheRecencyOversizedAndFailureSentinel(t *testing.T) {
	c := newWeightedAsyncCache[int](1, 0, 5, func(value int) int64 { return int64(value) })
	c.store("a", 2)
	c.store("b", 2)
	if _, ok := c.get("a"); !ok {
		t.Fatal("could not touch the first cache entry")
	}
	c.store("c", 2)
	if _, ok := c.get("b"); ok {
		t.Fatal("cache did not evict the least recently used entry")
	}
	c.fetch("failed", func() (int, error) { return 7, errors.New("failed") })
	waitForCache(t, func() bool { return len(c.done) == 1 })
	c.drain()
	c.store("oversized", 6)
	if _, ok := c.get("oversized"); ok {
		t.Fatal("oversized value was cached")
	}
	if value, ok := c.get("failed"); !ok || value != 0 {
		t.Fatalf("failure sentinel = (%d, %v), want (0, true)", value, ok)
	}
	if c.cacheWeight != 4 {
		t.Fatalf("cache weight = %d, want 4", c.cacheWeight)
	}
	for _, key := range []string{"a", "c"} {
		if _, ok := c.get(key); !ok {
			t.Fatalf("oversized/failure result evicted %q", key)
		}
	}
	c.reset()
	if c.cacheWeight != 0 || c.lru.Len() != 0 || len(c.cache) != 0 || len(c.lruEntries) != 0 {
		t.Fatal("reset retained weighted cache state")
	}
}

func TestPosterImagesStayCachedAcrossListFrames(t *testing.T) {
	// Three decoded posters exceed this budget; three display-sized posters fit.
	c := newWeightedAsyncCache[paint.ImageOp](1, 0, 16<<20, imageOpWeight)
	var fetches atomic.Int32
	for frame := range 2 {
		for _, key := range []string{"first", "second", "third"} {
			if _, ok := c.get(key); ok {
				continue
			}
			c.fetch(key, func() (paint.ImageOp, error) {
				fetches.Add(1)
				return fetchImageOp(func() (image.Image, error) {
					return image.NewRGBA(image.Rect(0, 0, 1600, 2400)), nil
				}, posterMaxWidth, posterMaxHeight)
			})
			waitForCache(t, func() bool { return len(c.done) == 1 })
			c.drain()
		}
		if fetches.Load() != 3 {
			t.Fatalf("frame %d: fetched %d posters, want 3", frame, fetches.Load())
		}
	}
	for _, key := range []string{"first", "second", "third"} {
		img, ok := c.get(key)
		if !ok || img.Size() != image.Pt(posterMaxWidth, posterMaxHeight) {
			t.Errorf("%s: cached image size = %v, cached = %v", key, img.Size(), ok)
		}
	}
}

func TestArtworkSizingPreservesAspectRatio(t *testing.T) {
	for _, tc := range []struct {
		name       string
		src, want  image.Point
		maxW, maxH int
	}{
		{"portrait", image.Pt(1600, 2400), image.Pt(800, 1200), posterMaxWidth, posterMaxHeight},
		{"landscape", image.Pt(3840, 2160), image.Pt(2560, 1440), fanartMaxWidth, fanartMaxHeight},
		{"already small", image.Pt(200, 300), image.Pt(200, 300), posterMaxWidth, posterMaxHeight},
	} {
		t.Run(tc.name, func(t *testing.T) {
			img, err := fetchImageOp(func() (image.Image, error) {
				return image.NewRGBA(image.Rectangle{Max: tc.src}), nil
			}, tc.maxW, tc.maxH)
			if err != nil || img.Size() != tc.want {
				t.Fatalf("image size = %v, err = %v; want %v", img.Size(), err, tc.want)
			}
		})
	}
}

func TestWeightedCacheResetIgnoresStaleInFlightResult(t *testing.T) {
	c := newWeightedAsyncCache[int](1, 1, 10, func(value int) int64 { return int64(value) })
	started := make(chan struct{})
	release := make(chan struct{})
	c.fetch("poster", func() (int, error) {
		close(started)
		<-release
		return 5, nil
	})
	<-started
	c.reset()
	close(release)
	waitForCache(t, func() bool { return len(c.done) == 1 })
	c.drain()
	if _, ok := c.get("poster"); ok || c.cacheWeight != 0 {
		t.Fatal("stale in-flight image survived cache reset")
	}
}

func TestAsyncCacheNotifiesOnSuccessAndFailure(t *testing.T) {
	c := newAsyncCache[int](3, 0)
	completed := make(chan struct{}, 3)
	c.onDone = func() { completed <- struct{}{} }
	var calls atomic.Int32
	partialFailure := func() (int, error) {
		calls.Add(1)
		return 7, errors.New("failed after partial result")
	}

	c.fetch("success", func() (int, error) { return 3, nil })
	c.fetch("failure", func() (int, error) { return 0, errors.New("failed") })
	c.fetch("partial", partialFailure)

	waitForCache(t, func() bool { return len(completed) == 3 })
	c.drain()
	if value, ok := c.get("success"); !ok || value != 3 {
		t.Fatalf("successful request = (%d, %v), want (3, true)", value, ok)
	}
	for _, key := range []string{"failure", "partial"} {
		if value, ok := c.get(key); !ok || value != 0 {
			t.Fatalf("failed request %q = (%d, %v), want (0, true)", key, value, ok)
		}
	}
	c.fetch("partial", partialFailure)
	if calls.Load() != 1 || c.active != 0 {
		t.Fatal("failure sentinel did not prevent a retry")
	}
}

func TestAsyncCacheBusyUntilResultsDrained(t *testing.T) {
	c := newAsyncCache[int](1, 1)
	if c.busy() {
		t.Fatal("new cache is busy")
	}
	release := make(chan struct{})
	fetch := func() (int, error) { <-release; return 1, nil }
	c.fetch("a", fetch)
	c.fetch("b", fetch)
	if !c.busy() {
		t.Fatal("cache with active and queued fetches is not busy")
	}
	close(release)
	waitForCache(t, func() bool {
		c.drain()
		return !c.busy()
	})
	if _, ok := c.get("b"); !ok {
		t.Fatal("queued fetch was not resolved before the cache went idle")
	}
}
