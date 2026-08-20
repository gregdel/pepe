package app

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

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

func TestAsyncCacheNotifiesOnSuccessAndFailure(t *testing.T) {
	c := newAsyncCache[int](2, 0)
	completed := make(chan struct{}, 2)
	c.onDone = func() { completed <- struct{}{} }

	c.fetch("success", func() (int, error) { return 3, nil })
	c.fetch("failure", func() (int, error) { return 0, errors.New("failed") })

	waitForCache(t, func() bool { return len(completed) == 2 })
	c.drain()
	if _, ok := c.get("success"); !ok {
		t.Fatal("successful request was not resolved")
	}
	if value, ok := c.get("failure"); !ok || value != 0 {
		t.Fatalf("failed request = (%d, %v), want (0, true)", value, ok)
	}
}
