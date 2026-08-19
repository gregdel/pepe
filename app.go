package main

import (
	"image"
	"strings"
	"time"

	"gioui.org/app"
	"gioui.org/io/event"
	"gioui.org/io/key"
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

	// browse owns the active catalog view and all browse-screen navigation.
	browse browseState

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

type libraryView struct {
	LibrarySnapshot
	mode ListingMode
}

type browseState struct {
	library libraryView
	nav     *NavigationState
	focus   browseFocus
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
		section:   a.browse.nav.section,
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

	a.browse.nav.section = sel.section
	if !a.browse.nav.navigateToMedia(a.sectionRoot(sel.section), sel.mediaType, sel.mediaID) {
		a.browse.nav = newNavigationState()
	}
}

func (a *App) sectionRoot(section Section) *RadixNode {
	if section == SectionShows {
		return a.browse.library.showRadix.Root()
	}
	return a.browse.library.movieRadix.Root()
}

func (a *App) mediaForNode(node *RadixNode) *MediaItem {
	if node == nil {
		return nil
	}
	if node.Media != nil {
		return node.Media
	}
	return a.browse.library.titleToMedia[node.FullString]
}

func (a *App) selectedBrowseMedia() *MediaItem {
	if a.browse.nav.isAtRoot() {
		return nil
	}
	child := a.browse.nav.getSelectedChild()
	if child == nil || !child.IsTerminal {
		return nil
	}
	return a.mediaForNode(child)
}

func (a *App) applyLibrarySnapshot(snapshot LibrarySnapshot) {
	a.browse.library.LibrarySnapshot = snapshot
}

func (a *App) snapshotForMode(mode ListingMode) LibrarySnapshot {
	if mode == ListingModeDateAdded {
		return a.client.snapshotFor(dateAddedListingBuilder{})
	}
	return a.client.getSnapshot()
}

func (a *App) applyListingMode(mode ListingMode) {
	wasAtRoot := a.browse.nav.isAtRoot()
	section := a.browse.nav.section
	homeCursor := a.browse.nav.cursor

	snapshot := a.snapshotForMode(mode)
	a.applyLibrarySnapshot(snapshot)
	a.browse.library.mode = mode
	a.browse.nav = newNavigationState()
	if wasAtRoot {
		a.browse.nav.cursor = homeCursor
	} else {
		a.browse.nav.resetToSection(section, a.sectionRoot(section))
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
		browse:           browseState{library: libraryView{LibrarySnapshot: snapshot, mode: ListingModeTitle}, nav: newNavigationState(), focus: browseFocusContent},
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

// scrollDetail scrolls the movie detail page content with the keyboard.
func (a *App) scrollDetail(dir int) {
	const step = 160 // px
	a.detailList.Position.Offset += dir * step
	if a.detailList.Position.Offset < 0 {
		a.detailList.Position.Offset = 0
	}
}

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
