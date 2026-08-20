package app

// refreshResult crosses from the library-refresh goroutine to the Gio event
// loop. A snapshot is only present after a successful fetch.
type refreshResult struct {
	snapshot LibrarySnapshot
	err      error
}

func (a *App) startRefresh() {
	if a.refreshing {
		return
	}

	a.refreshing = true
	a.refreshError = nil
	a.refreshDone = make(chan refreshResult, 1)

	go func() {
		result := refreshResult{err: a.client.FetchLibrary()}
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
			return
		}

		a.refreshError = nil
		a.applyRefreshResult(result.snapshot)
	default:
	}
}

func (a *App) applyRefreshResult(snapshot LibrarySnapshot) {
	sel := a.captureSelection()
	if a.browse.library.mode == ListingModeDateAdded {
		snapshot = a.snapshotForMode(a.browse.library.mode)
	}
	a.applyLibrarySnapshot(snapshot)
	a.browse.nav = newNavigationState()
	a.images.reset()
	a.nfos.reset()
	a.detail = nil
	a.restoreSelection(sel)
}
