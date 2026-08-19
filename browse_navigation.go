package main

// Browse navigation keeps directional traversal and mode selection separate
// from Gio event delivery.
func (a *App) navigateUp() {
	if a.browse.focus == browseFocusModeRail {
		if a.browse.library.mode == ListingModeTitle {
			a.applyListingMode(listingModeCount - 1)
		} else {
			a.applyListingMode(a.browse.library.mode - 1)
		}
		a.browse.focus = browseFocusModeRail
		return
	}
	if a.browse.nav.isAtRoot() {
		a.browse.nav.cursor = 1 - a.browse.nav.cursor
		return
	}
	a.browse.nav.moveCursorUp(a.browse.nav.getChildrenCount())
}

func (a *App) navigateDown() {
	if a.browse.focus == browseFocusModeRail {
		a.applyListingMode((a.browse.library.mode + 1) % listingModeCount)
		a.browse.focus = browseFocusModeRail
		return
	}
	if a.browse.nav.isAtRoot() {
		a.browse.nav.cursor = 1 - a.browse.nav.cursor
		return
	}
	a.browse.nav.moveCursorDown(a.browse.nav.getChildrenCount())
}

func (a *App) navigateLeft() {
	if a.browse.focus == browseFocusModeRail {
		return
	}
	if a.browse.nav.isAtRoot() {
		a.browse.focus = browseFocusModeRail
		return
	}
	if a.browse.nav.isAtSectionRoot() {
		a.browse.nav.returnToHome()
		return
	}
	a.browse.nav.goBack()
}

func (a *App) navigateEscape() {
	if a.browse.focus == browseFocusModeRail {
		a.browse.focus = browseFocusContent
		return
	}
	if a.browse.nav.isAtSectionRoot() {
		a.browse.nav.returnToHome()
		return
	}
	a.browse.nav.goBack()
}

func (a *App) navigateRight() {
	if a.browse.focus == browseFocusModeRail {
		a.browse.focus = browseFocusContent
		return
	}
	a.navigateForward(true)
}

func (a *App) activateBrowseSelection() {
	if a.browse.focus == browseFocusModeRail {
		a.browse.focus = browseFocusContent
		return
	}
	a.navigateForward(false)
}

func (a *App) navigateForward(openTerminal bool) {
	if a.browse.nav.isAtRoot() {
		section := SectionMovies
		if a.browse.nav.cursor == int(SectionShows) {
			section = SectionShows
		}
		a.browse.nav.resetToSection(section, a.sectionRoot(section))
		return
	}

	child := a.browse.nav.getSelectedChild()
	if child == nil {
		return
	}
	if !child.IsTerminal {
		a.browse.nav.goForward(child)
		return
	}
	if !openTerminal {
		return
	}

	media := a.mediaForNode(child)
	if media == nil {
		return
	}
	if media.Type == mediaKindShow {
		a.pushShowDetail(media)
		return
	}
	a.pushMovieDetail(media)
}

func (a *App) toggleSection() {
	a.browse.focus = browseFocusContent
	section := SectionMovies
	if a.browse.nav.section == SectionMovies {
		section = SectionShows
	}
	a.browse.nav.resetToSection(section, a.sectionRoot(section))
}
