package main

import (
	"image"
	"strings"
	"time"

	"gioui.org/app"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/widget/material"
	polochon "github.com/odwrtw/polochon/lib"
)

// ── Player state ─────────────────────────────────────────────────────────────

type ListingMode int

const (
	ListingModeTitle ListingMode = iota
	ListingModeDateAdded
	listingModeCount
)

type browseFocus int

const (
	browseFocusContent browseFocus = iota
	browseFocusModeRail
)

// ── Subtitle popup state ──────────────────────────────────────────────────────

type SubtitlePopupState int

const (
	SubtitlePopupLoading SubtitlePopupState = iota
	SubtitlePopupReady
	SubtitlePopupDownloading
	SubtitlePopupDone
	SubtitlePopupError
)

type subPopupListResult struct {
	entries []*polochon.SubtitleEntry
	err     error
}

type subPopupDownloadResult struct{ err error }

// SubtitlePopup holds all state for the subtitle search overlay.
type SubtitlePopup struct {
	video        polochon.Video
	lang         polochon.Language
	state        SubtitlePopupState
	entries      []*polochon.SubtitleEntry
	cursor       int
	errMsg       string
	doneAt       time.Time
	list         layout.List
	resultCh     chan subPopupListResult
	downloadDone chan subPopupDownloadResult
}

// ── App ──────────────────────────────────────────────────────────────────────

// App holds all application state across frames.
type App struct {
	client *APIClient

	// library snapshot
	movieRadix   *RadixTree
	showRadix    *RadixTree
	titleToMedia map[string]*MediaItem
	movieCount   int
	showCount    int

	nav *NavigationState

	listingMode ListingMode
	browseFocus browseFocus

	// pushed full-screen detail page (nil = browse screen)
	detail *detailScreen

	// player
	playerStatus     PlayerStatus
	playerMode       PlayerMode
	playerError      string
	mpvPath          string
	playerFullscreen bool
	playerDone       chan error         // goroutine → event loop
	resumePos        map[string]float64 // mediaKey -> last position (seconds)
	playbackTracker  *playbackTracker

	// library refresh
	refreshing   bool
	refreshError error
	refreshDone  chan refreshResult // goroutine → event loop
	sseCh        <-chan struct{}    // SSE events from server

	// image cache (thumbs, fanarts, posters — all media types)
	images *asyncCache[paint.ImageOp]

	// nfo cache — parsed NFO data per movie ID
	nfos *asyncCache[*polochon.Movie]

	// subtitle search popup (nil = closed)
	subPopup *SubtitlePopup

	// Gio persistent list state (must survive between frames)
	leftList        layout.List
	rightList       layout.List
	playerLinesList layout.List
	detailList      layout.List

	// key input tag — a stable pointer used as event.Tag
	keyTag *bool

	w  *app.Window
	th *material.Theme
}

// savedSelection captures the identity of the currently selected media item so
// navigation can be restored after a library refresh.
type savedSelection struct {
	section   Section
	mediaType MediaKind
	mediaID   string
}

// captureSelection records what is currently selected, or returns nil if
// nothing meaningful is selected (e.g. browsing prefix nodes or at root).
func (a *App) captureSelection() *savedSelection {
	media := a.selectedBrowseMedia()
	if media == nil {
		return nil
	}
	return &savedSelection{
		section:   a.nav.section,
		mediaType: media.Type,
		mediaID:   media.ID,
	}
}

// restoreSelection navigates to the previously captured selection in the
// freshly applied library snapshot. Falls back to root if the item was removed.
func (a *App) restoreSelection(sel *savedSelection) {
	if sel == nil {
		return
	}

	var sectionRoot *RadixNode
	if sel.section == SectionMovies {
		sectionRoot = a.movieRadix.Root()
	} else {
		sectionRoot = a.showRadix.Root()
	}

	a.nav.section = sel.section
	if !a.nav.navigateToMedia(sectionRoot, sel.mediaType, sel.mediaID) {
		a.nav = newNavigationState()
	}
}

func (a *App) mediaForNode(node *RadixNode) *MediaItem {
	if node == nil {
		return nil
	}
	if node.Media != nil {
		return node.Media
	}
	return a.titleToMedia[node.FullString]
}

func (a *App) selectedBrowseMedia() *MediaItem {
	if a.nav.isAtRoot() {
		return nil
	}
	child := a.nav.getSelectedChild()
	if child == nil || !child.IsTerminal {
		return nil
	}
	return a.mediaForNode(child)
}

func (a *App) applyLibrarySnapshot(snapshot LibrarySnapshot) {
	a.movieRadix = snapshot.movieRadix
	a.showRadix = snapshot.showRadix
	a.titleToMedia = snapshot.titleToMedia
	a.movieCount = snapshot.movieCount
	a.showCount = snapshot.showCount
}

func (a *App) snapshotForMode(mode ListingMode) LibrarySnapshot {
	if mode == ListingModeDateAdded {
		return a.client.snapshotFor(dateAddedListingBuilder{})
	}
	return a.client.getSnapshot()
}

func (a *App) applyListingMode(mode ListingMode) {
	wasAtRoot := a.nav.isAtRoot()
	section := a.nav.section
	homeCursor := a.nav.cursor

	snapshot := a.snapshotForMode(mode)
	a.applyLibrarySnapshot(snapshot)
	a.listingMode = mode
	a.nav = newNavigationState()
	if wasAtRoot {
		a.nav.cursor = homeCursor
	} else if section == SectionShows {
		a.nav.resetToSection(SectionShows, a.showRadix.Root())
	} else {
		a.nav.resetToSection(SectionMovies, a.movieRadix.Root())
	}
	a.detail = nil
}

func newApp(w *app.Window, client *APIClient, sseCh <-chan struct{}, config *Config) *App {
	snapshot := client.getSnapshot()
	th := material.NewTheme()

	keyTag := new(bool)
	mpvPath := "mpv"
	playerFullscreen := false
	if config != nil {
		if strings.TrimSpace(config.MPVPath) != "" {
			mpvPath = strings.TrimSpace(config.MPVPath)
		}
		playerFullscreen = config.PlayerFullscreen
	}

	app := &App{
		client:           client,
		movieRadix:       snapshot.movieRadix,
		showRadix:        snapshot.showRadix,
		titleToMedia:     snapshot.titleToMedia,
		movieCount:       snapshot.movieCount,
		showCount:        snapshot.showCount,
		nav:              newNavigationState(),
		listingMode:      ListingModeTitle,
		browseFocus:      browseFocusContent,
		playerStatus:     PlayerStatusIdle,
		mpvPath:          mpvPath,
		playerFullscreen: playerFullscreen,
		resumePos:        make(map[string]float64),
		keyTag:           keyTag,
		w:                w,
		th:               th,
		leftList:         layout.List{Axis: layout.Vertical},
		rightList:        layout.List{Axis: layout.Vertical},
		playerLinesList:  layout.List{Axis: layout.Vertical},
		detailList:       layout.List{Axis: layout.Vertical},
		images:           newAsyncCache[paint.ImageOp](3, 24),
		nfos:             newAsyncCache[*polochon.Movie](1, 4),
		sseCh:            sseCh,
	}
	app.images.onDone = w.Invalidate
	app.nfos.onDone = w.Invalidate
	return app
}

// ── Main event loop ───────────────────────────────────────────────────────────

func run(w *app.Window, client *APIClient, sseCh <-chan struct{}, config *Config) error {
	a := newApp(w, client, sseCh, config)
	var ops op.Ops
	requestFocus := true // grab focus on the very first frame

	for {
		switch e := w.Event().(type) {
		case app.DestroyEvent:
			return e.Err

		case app.FrameEvent:
			ops.Reset()
			gtx := app.NewContext(&ops, e)

			// Scale UI proportionally to window width.
			// Reference: 1920dp (full HD TV). Smaller windows shrink, larger grow.
			const refWidth = 1920.0
			widthDp := float32(gtx.Constraints.Max.X) / gtx.Metric.PxPerDp
			scale := widthDp / refWidth
			if scale < 0.4 {
				scale = 0.4
			}
			if scale > 2.0 {
				scale = 2.0
			}
			gtx.Metric.PxPerDp *= scale
			gtx.Metric.PxPerSp *= scale

			// Paint background
			paint.Fill(gtx.Ops, colorBg)

			// Drain async channels before handling input
			a.drainChannels()

			// Register for key events over the whole window
			area := clip.Rect(image.Rectangle{Max: gtx.Constraints.Max}).Push(gtx.Ops)
			event.Op(gtx.Ops, a.keyTag)
			if requestFocus {
				gtx.Execute(key.FocusCmd{Tag: a.keyTag})
				requestFocus = false
			}
			a.handleKeys(gtx)
			area.Pop()

			// Draw the UI
			a.layout(gtx)

			e.Frame(gtx.Ops)
		}
	}
}

// drainChannels checks async goroutine results without blocking.
func (a *App) drainChannels() {
	if a.playerDone != nil {
		select {
		case err := <-a.playerDone:
			a.applyPlaybackSnapshot(a.playbackTracker)
			if err != nil {
				a.playerStatus = PlayerStatusError
				a.playerError = err.Error()
			} else {
				a.playerStatus = PlayerStatusIdle
				a.playerError = ""
			}
			a.playerDone = nil
			a.playbackTracker = nil
		default:
		}
	}

	a.drainRefresh()

	// Drain SSE events: each signal means the server library changed.
	if a.sseCh != nil {
		select {
		case _, ok := <-a.sseCh:
			if ok {
				a.client.log.Info("SSE: library changed, triggering refresh")
				a.startRefresh()
			}
		default:
		}
	}

	a.drainSubtitlePopup()

	// Drain all pending image/NFO fetch results.
	a.images.drain()
	a.nfos.drain()
}

// ── Key handling ──────────────────────────────────────────────────────────────

// keyFilters lists every key we want delivered — one Filter per key name.
// Optional: ModShift so that e.g. Shift+K still fires (we ignore it in logic).
var keyFilters = []event.Filter{
	key.Filter{Focus: nil, Name: key.NameUpArrow},
	key.Filter{Focus: nil, Name: key.NameDownArrow},
	key.Filter{Focus: nil, Name: key.NameLeftArrow},
	key.Filter{Focus: nil, Name: key.NameRightArrow},
	key.Filter{Focus: nil, Name: key.NameReturn},
	key.Filter{Focus: nil, Name: key.NameEnter},
	key.Filter{Focus: nil, Name: key.NameEscape},
	key.Filter{Focus: nil, Name: "H", Optional: key.ModShift},
	key.Filter{Focus: nil, Name: "J", Optional: key.ModShift},
	key.Filter{Focus: nil, Name: "K", Optional: key.ModShift},
	key.Filter{Focus: nil, Name: "L", Optional: key.ModShift},
	key.Filter{Focus: nil, Name: "Q", Optional: key.ModShift},
	key.Filter{Focus: nil, Name: "R", Optional: key.ModShift},
	key.Filter{Focus: nil, Name: "S", Optional: key.ModShift},
	key.Filter{Focus: nil, Name: key.NameTab},
}

type appAction int

const (
	actionUnknown appAction = iota
	actionUp
	actionDown
	actionLeft
	actionRight
	actionConfirm
	actionDismiss
	actionToggleSection
	actionRefresh
	actionSubtitles
	actionQuit
)

func actionForKey(name key.Name) appAction {
	switch name {
	case key.NameUpArrow, "K":
		return actionUp
	case key.NameDownArrow, "J":
		return actionDown
	case key.NameLeftArrow, "H":
		return actionLeft
	case key.NameRightArrow, "L":
		return actionRight
	case key.NameReturn, key.NameEnter:
		return actionConfirm
	case key.NameEscape:
		return actionDismiss
	case key.NameTab:
		return actionToggleSection
	case "R":
		return actionRefresh
	case "S":
		return actionSubtitles
	case "Q":
		return actionQuit
	default:
		return actionUnknown
	}
}

func (a *App) handleKeys(gtx layout.Context) {
	for {
		e, ok := gtx.Event(keyFilters...)
		if !ok {
			break
		}
		ke, ok := e.(key.Event)
		if !ok || ke.State != key.Press {
			continue
		}
		// Ignore modified keys (Ctrl+X, Alt+X, etc.) except allow plain Shift
		if ke.Modifiers & ^key.ModShift != 0 {
			continue
		}

		// Subtitle popup intercepts all keys when active.
		if a.subPopup != nil {
			a.handlePopupKeys(ke)
			continue
		}

		if a.detail != nil {
			a.handleDetailKeys(ke)
			continue
		}

		a.handleBrowseKeys(ke)
	}
}

func (a *App) handlePopupKeys(ke key.Event) {
	switch actionForKey(ke.Name) {
	case actionUp:
		if a.subPopup.cursor > 0 {
			a.subPopup.cursor--
		}
	case actionDown:
		if a.subPopup.state == SubtitlePopupReady && a.subPopup.cursor < len(a.subPopup.entries)-1 {
			a.subPopup.cursor++
		}
	case actionConfirm, actionRight:
		a.subPopupSelect()
	case actionDismiss, actionLeft:
		a.closeSubtitlePopup()
	}
}

func (a *App) handleBrowseKeys(ke key.Event) {
	switch actionForKey(ke.Name) {
	case actionUp:
		a.navigateUp()
	case actionDown:
		a.navigateDown()
	case actionLeft:
		a.navigateLeft()
	case actionDismiss:
		a.navigateEscape()
	case actionRight:
		a.navigateRight()
	case actionConfirm:
		a.activateBrowseSelection()
	case actionToggleSection:
		a.toggleSection()
	case actionRefresh:
		a.startRefresh()
	case actionSubtitles:
		a.openSubtitlePopup()
	case actionQuit:
		a.w.Perform(system.ActionClose)
	}
}

func (a *App) handleDetailKeys(ke key.Event) {
	d := a.detail
	if d.kind == detailShow {
		a.handleShowKeys(ke, d.show)
		return
	}

	// Movie detail page.
	switch actionForKey(ke.Name) {
	case actionConfirm, actionRight:
		a.launchMovie(&MediaItem{ID: d.movieID, Title: d.movieTitle, Type: mediaKindMovie})
	case actionDismiss, actionLeft:
		a.popDetail()
	case actionUp:
		a.scrollDetail(-1)
	case actionDown:
		a.scrollDetail(1)
	case actionSubtitles:
		a.openSubtitlePopup()
	case actionQuit:
		a.w.Perform(system.ActionClose)
	}
}

func (a *App) handleShowKeys(ke key.Event, s *showPage) {
	switch actionForKey(ke.Name) {
	case actionUp:
		s.up()
	case actionDown:
		s.down()
	case actionLeft:
		if s.focus == focusTabs {
			s.left()
		} else {
			a.popDetail()
		}
	case actionRight:
		s.right()
	case actionConfirm:
		if s.focus == focusTabs {
			s.focus = focusEpisodes
			return
		}
		if ep := s.currentEpisode(); ep != nil {
			a.launchEpisode(ep)
		}
	case actionDismiss:
		a.popDetail()
	case actionSubtitles:
		a.openSubtitlePopup()
	case actionQuit:
		a.w.Perform(system.ActionClose)
	}
}

// scrollDetail scrolls the movie detail page content with the keyboard.
func (a *App) scrollDetail(dir int) {
	const step = 160 // px
	a.detailList.Position.Offset += dir * step
	if a.detailList.Position.Offset < 0 {
		a.detailList.Position.Offset = 0
	}
}

// ── Browse navigation ─────────────────────────────────────────────────────────

func (a *App) navigateUp() {
	if a.browseFocus == browseFocusModeRail {
		if a.listingMode == ListingModeTitle {
			a.applyListingMode(listingModeCount - 1)
		} else {
			a.applyListingMode(a.listingMode - 1)
		}
		return
	}
	if a.nav.isAtRoot() {
		if a.nav.cursor == 0 {
			a.nav.cursor = 1
		} else {
			a.nav.cursor = 0
		}
	} else {
		a.nav.moveCursorUp(a.nav.getChildrenCount())
	}
}

func (a *App) navigateDown() {
	if a.browseFocus == browseFocusModeRail {
		a.applyListingMode((a.listingMode + 1) % listingModeCount)
		return
	}
	if a.nav.isAtRoot() {
		if a.nav.cursor == 1 {
			a.nav.cursor = 0
		} else {
			a.nav.cursor = 1
		}
	} else {
		a.nav.moveCursorDown(a.nav.getChildrenCount())
	}
}

func (a *App) navigateLeft() {
	if a.browseFocus == browseFocusModeRail {
		return
	}
	if a.nav.isAtRoot() {
		a.browseFocus = browseFocusModeRail
		return
	}
	if a.nav.isAtSectionRoot() {
		a.nav.returnToHome()
		return
	}
	a.nav.goBack()
}

func (a *App) navigateEscape() {
	if a.browseFocus == browseFocusModeRail {
		a.browseFocus = browseFocusContent
		return
	}
	if a.nav.isAtSectionRoot() {
		a.nav.returnToHome()
		return
	}
	a.nav.goBack()
}

func (a *App) navigateRight() {
	if a.browseFocus == browseFocusModeRail {
		a.browseFocus = browseFocusContent
		return
	}
	a.activateContentSelection()
}

func (a *App) activateBrowseSelection() {
	if a.browseFocus == browseFocusModeRail {
		a.navigateRight()
		return
	}
	a.activateContentSelection()
}

func (a *App) activateContentSelection() {
	if a.nav.isAtRoot() {
		if a.nav.cursor == 0 {
			a.nav.resetToSection(SectionMovies, a.movieRadix.Root())
		} else {
			a.nav.resetToSection(SectionShows, a.showRadix.Root())
		}
		return
	}

	child := a.nav.getSelectedChild()
	if child == nil {
		return
	}

	if child.IsTerminal {
		media := a.mediaForNode(child)
		if media == nil {
			return
		}
		if media.Type == mediaKindShow {
			a.pushShowDetail(media)
			return
		}
		a.pushMovieDetail(media)
		return
	}

	a.nav.goForward(child)
}

func (a *App) toggleSection() {
	a.browseFocus = browseFocusContent
	if a.nav.section == SectionMovies {
		a.nav.resetToSection(SectionShows, a.showRadix.Root())
	} else {
		a.nav.resetToSection(SectionMovies, a.movieRadix.Root())
	}
}

// ── Player ────────────────────────────────────────────────────────────────────

// ── Root layout ───────────────────────────────────────────────────────────────

func (a *App) layout(gtx C) D {
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx C) D {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Flexed(1, func(gtx C) D {
					if a.detail != nil {
						return a.layoutDetail(gtx)
					}
					return a.layoutBrowse(gtx)
				}),
				layout.Rigid(a.layoutHelp),
			)
		}),
		layout.Stacked(func(gtx C) D {
			if a.subPopup == nil {
				return D{}
			}
			return a.layoutSubtitlePopup(gtx)
		}),
	)
}

// ── Format helpers (ported from ui.go, no lipgloss) ──────────────────────────

// ── MPV helpers (ported from ui.go) ──────────────────────────────────────────
