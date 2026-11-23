package app

import "time"

const (
	initialLoadRetryMin = 5 * time.Second
	initialLoadRetryMax = time.Minute
)

// refreshResult crosses from the library-refresh goroutine to the Gio event
// loop. A snapshot is only present after a successful fetch.
type refreshResult struct {
	snapshot LibrarySnapshot
	err      error
}

func (a *App) startRefresh() {
	if a.refreshing || a.client == nil || (a.client.client == nil && a.refreshLibraryFn == nil) {
		return
	}

	a.refreshing = true
	a.refreshError = nil
	a.refreshDone = make(chan refreshResult, 1)

	go func() {
		fetch := a.client.FetchLibrary
		if a.refreshLibraryFn != nil {
			fetch = a.refreshLibraryFn
		}
		result := refreshResult{err: fetch()}
		if result.err == nil {
			result.snapshot = a.client.getSnapshot()
		}
		a.refreshDone <- result
		if a.invalidate != nil {
			a.invalidate()
		}
	}()
}

func (a *App) drainRefresh() {
	if a.refreshDone == nil {
		return
	}

	select {
	case result := <-a.refreshDone:
		a.refreshing = false
		a.refreshDone = nil
		if result.err != nil {
			a.refreshError = result.err
			if a.client != nil && a.client.log != nil {
				a.client.log.Error("failed to refresh library", "error", result.err)
			}
			if a.loadPending {
				a.loadErr = result.err
				a.scheduleLoadRetry()
			}
		} else {
			a.refreshError = nil
			a.loadPending = false
			a.loadErr = nil
			a.stopLoadRetry()
			a.applyRefreshResult(result.snapshot)
		}

		pending := a.refreshPending
		a.refreshPending = false
		if pending {
			a.startRefresh()
		}
	default:
	}
}

// startWithLoadError keeps the window usable when the first library fetch
// fails: the header shows the failure and the load is retried with a capped
// backoff until it succeeds. SSE reconnects send no event, so a server coming
// back would otherwise go unnoticed.
func (a *App) startWithLoadError(err error) {
	a.refreshError = err
	a.loadPending = true
	a.loadErr = err
	a.scheduleLoadRetry()
}

func (a *App) scheduleLoadRetry() {
	if a.loadRetryDue != nil {
		return
	}
	if a.loadRetryDelay == 0 {
		a.loadRetryDelay = initialLoadRetryMin
	}
	delay := a.loadRetryDelay
	a.loadRetryDelay = min(2*delay, initialLoadRetryMax)
	due := make(chan struct{}, 1)
	invalidate := a.invalidate
	a.loadRetryDue = due
	a.loadRetryAt = time.Now().Add(delay)
	a.loadRetryTimer = time.AfterFunc(delay, func() {
		due <- struct{}{}
		if invalidate != nil {
			invalidate()
		}
	})
}

func (a *App) stopLoadRetry() {
	if a.loadRetryTimer != nil {
		a.loadRetryTimer.Stop()
	}
	a.loadRetryTimer = nil
	a.loadRetryDue = nil
	a.loadRetryAt = time.Time{}
}

func (a *App) drainLoadRetry() {
	if a.loadRetryDue == nil {
		return
	}
	select {
	case <-a.loadRetryDue:
		a.loadRetryTimer = nil
		a.loadRetryDue = nil
		a.loadRetryAt = time.Time{}
		a.startRefresh()
	default:
	}
}

func (a *App) libraryChanged() {
	if a.client != nil && a.client.log != nil {
		a.client.log.Info("SSE: library changed, triggering refresh")
	}
	if a.refreshing {
		a.refreshPending = true
		return
	}
	a.startRefresh()
}

func (a *App) applyRefreshResult(snapshot LibrarySnapshot) {
	sel := a.captureSelection()
	a.dateAddedSnapshot = nil
	if a.browse.library.mode == ListingModeDateAdded {
		snapshot = a.snapshotForMode(a.browse.library.mode)
	}
	a.applyLibrarySnapshot(snapshot)
	a.browse.nav = newNavigationState()
	a.images.reset()
	a.thumbs.reset()
	a.backdrops.reset()
	a.detail = nil
	a.restoreSelection(sel)
}
