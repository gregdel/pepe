package app

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
		} else {
			a.refreshError = nil
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
	a.detail = nil
	a.restoreSelection(sel)
}
