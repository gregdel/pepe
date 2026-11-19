package app

import (
	"errors"
	"image"
	"sync/atomic"
	"testing"
	"time"

	"gioui.org/io/input"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/paint"
	"gioui.org/unit"
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
				}, posterMaxSize.X, posterMaxSize.Y)
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
		if !ok || img.Size() != posterMaxSize {
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
		{"portrait", image.Pt(1600, 2400), image.Pt(800, 1200), posterMaxSize.X, posterMaxSize.Y},
		{"landscape", image.Pt(3840, 2160), image.Pt(2560, 1440), fanartMaxWidth, fanartMaxHeight},
		{"already small", image.Pt(200, 300), image.Pt(200, 300), posterMaxSize.X, posterMaxSize.Y},
		{"nav row", image.Pt(1000, 1500), image.Pt(96, 144), rowPosterMaxSize.X, rowPosterMaxSize.Y},
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

// A large reduction must average the source: white every third column, shrunk
// 10×, is a uniform third grey (85). 4-tap sampling ranges from 0 to 127.
func TestArtworkLargeReductionDoesNotAlias(t *testing.T) {
	src := image.NewGray(image.Rect(0, 0, 960, 1440))
	for y := range 1440 {
		for x := 0; x < 960; x += 3 {
			src.Pix[y*src.Stride+x] = 0xff
		}
	}
	got, ok := resizeArtwork(src, rowPosterMaxSize.X, rowPosterMaxSize.Y).(*image.RGBA)
	if !ok || got.Bounds().Size() != rowPosterMaxSize {
		t.Fatalf("resized = %T %v, want RGBA %v", got, got.Bounds(), rowPosterMaxSize)
	}
	for i := 0; i < len(got.Pix); i += 4 {
		if v := got.Pix[i]; v < 85-16 || v > 85+16 {
			t.Fatalf("pixel %d = %d, want about 85", i/4, v)
		}
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

func TestAsyncCacheRetriesTransientFailuresWithBackoff(t *testing.T) {
	c := newAsyncCache[int](2, 0)
	transient := errors.New("timed out")
	c.retryable = func(err error) bool { return errors.Is(err, transient) }
	now := time.Unix(1000, 0)
	c.now = func() time.Time { return now }
	var calls atomic.Int32
	var fail atomic.Bool
	fail.Store(true)
	fetch := func() (int, error) {
		calls.Add(1)
		if fail.Load() {
			return 0, transient
		}
		return 5, nil
	}
	resolve := func() {
		t.Helper()
		waitForCache(t, func() bool {
			c.drain()
			return !c.busy()
		})
	}

	c.fetch("a", fetch)
	c.fetch("permanent", func() (int, error) { return 0, errors.New("image file not set") })
	resolve()
	if value, ok := c.get("a"); !ok || value != 0 {
		t.Fatalf("transient failure = (%d, %v), want a cached placeholder", value, ok)
	}
	if at, ok := c.retryAt("a"); !ok || !at.Equal(now.Add(cacheRetryBase)) {
		t.Fatalf("first retryAt = %v, %v, want %v", at, ok, now.Add(cacheRetryBase))
	}
	if _, ok := c.retryAt("permanent"); ok {
		t.Fatal("permanent failure has a retry time")
	}

	now = now.Add(cacheRetryBase)
	if _, ok := c.get("a"); ok {
		t.Fatal("transient failure still cached after its backoff")
	}
	c.fetch("a", fetch)
	resolve()
	if at, _ := c.retryAt("a"); !at.Equal(now.Add(2 * cacheRetryBase)) {
		t.Fatalf("second retryAt = %v, want the backoff doubled to %v", at, now.Add(2*cacheRetryBase))
	}

	now = now.Add(time.Hour)
	if _, ok := c.get("permanent"); !ok {
		t.Fatal("permanent failure was dropped")
	}
	fail.Store(false)
	if _, ok := c.get("a"); ok {
		t.Fatal("transient failure still cached after its second backoff")
	}
	c.fetch("a", fetch)
	resolve()
	if value, ok := c.get("a"); !ok || value != 5 {
		t.Fatalf("retried fetch = (%d, %v), want (5, true)", value, ok)
	}
	if _, ok := c.retryAt("a"); ok || len(c.retries) != 0 {
		t.Fatal("success kept retry state")
	}
	if calls.Load() != 3 {
		t.Fatalf("fetch calls = %d, want 3", calls.Load())
	}
}

func TestAsyncCacheRetryBackoffIsCapped(t *testing.T) {
	c := newAsyncCache[int](1, 0)
	now := time.Unix(1000, 0)
	c.now = func() time.Time { return now }
	for range 40 {
		c.recordRetry("a", true)
	}
	if got := c.retries["a"].at.Sub(now); got != cacheRetryMax {
		t.Fatalf("backoff after many failures = %v, want %v", got, cacheRetryMax)
	}
	c.reset()
	if len(c.retries) != 0 {
		t.Fatal("reset kept retry state")
	}
}

func TestArtworkSchedulesRedrawForTransientRetry(t *testing.T) {
	a := &App{images: newWeightedAsyncCache[paint.ImageOp](2, 0, imageCacheBudget, imageOpWeight)}
	a.images.retryable = isTransientArtworkError
	failure := func() (image.Image, error) {
		return nil, transientError{&artworkError{"artwork request timed out", errors.New("timeout")}}
	}
	frame := func(widget func(C)) (time.Time, bool) {
		var router input.Router
		var ops op.Ops
		widget(C{Ops: &ops, Source: router.Source(), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Constraints: layout.Exact(image.Pt(64, 64))})
		router.Frame(&ops)
		return router.WakeupTime()
	}
	resolve := func() {
		waitForCache(t, func() bool {
			a.images.drain()
			return !a.images.busy()
		})
	}

	frame(func(gtx C) { a.fanart(gtx, "fanart:x", failure) })
	frame(func(gtx C) { a.posterImage(gtx, "thumb:x", failure, 10, 0, posterMaxSize) })
	resolve()
	for _, draw := range []func(C){
		func(gtx C) { a.fanart(gtx, "fanart:x", failure) },
		func(gtx C) { a.posterImage(gtx, "thumb:x", failure, 10, 0, posterMaxSize) },
	} {
		if at, ok := frame(draw); !ok || at.IsZero() {
			t.Fatalf("transient artwork failure scheduled no redraw (%v, %v)", at, ok)
		}
	}
}
