package app

import (
	"context"
	"image"
	"strings"
	"time"

	gioapp "gioui.org/app"
	"gioui.org/font"
	"gioui.org/io/event"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
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
	listSize     image.Point
	resultCh     chan subPopupListResult
	downloadDone chan subPopupDownloadResult
}

// ── App ──────────────────────────────────────────────────────────────────────

const imageCacheBudget int64 = 128 << 20

func imageOpWeight(imageOp paint.ImageOp) int64 {
	size := imageOp.Size()
	return int64(size.X) * int64(size.Y) * 4
}

// App holds all application state across frames.
type App struct {
	client *APIClient

	// browse owns the active catalog view and all browse-screen navigation.
	browse            browseState
	dateAddedSnapshot *LibrarySnapshot // reused between mode switches until refresh

	// pushed full-screen detail page (nil = browse screen)
	detail *detailScreen
	// selectionHidden keeps markSelected out of the semantic tree while the
	// screen under the subtitle popup is drawn, so only the popup's focus is
	// exposed as selected.
	selectionHidden bool

	// Shared edge spacing; not an overscan reservation.
	viewportInsets layout.Inset
	controls       ControlProfile
	hideHelp       bool
	nativeOSD      bool

	// player
	playerStatus     PlayerStatus
	playerMode       PlayerMode
	playerTitle      string
	playerError      string
	playerFromWeb    bool
	playerStopping   bool
	playerSocket     string
	playerSession    uint64
	playerControls   *playerControls
	playerTracker    *playbackTracker
	pendingPlayback  *playbackSource
	playerStop       chan struct{}
	mpvPath          string
	playerFullscreen bool
	playerPolicy     playerPolicy
	playerCleanupDir string             // retained until launcher teardown is confirmed
	playerDone       chan playerResult  // goroutine → event loop
	resumePos        map[string]float64 // mediaKey -> last position (seconds)

	// opt-in HTTP transport; mutable app state stays on the event loop
	webUI           *webUI
	webCommandError string

	// library refresh
	refreshing       bool
	refreshPending   bool
	refreshError     error
	refreshDone      chan refreshResult // goroutine → event loop
	refreshLibraryFn func() error       // optional fetch override for tests
	sseCh            <-chan struct{}    // SSE events from server
	sseSignal        chan struct{}      // bounded wakeup for the event loop
	sseCancel        context.CancelFunc
	sseDone          <-chan struct{}

	// image cache (thumbs, fanarts, posters — all media types)
	images *asyncCache[paint.ImageOp]

	// subtitle search popup (nil = closed)
	subPopup *SubtitlePopup

	// Gio persistent list state (must survive between frames)
	clickables      map[string]*widget.Clickable
	pendingClick    func() // pointer action from the last frame, see clickableLayout
	leftList        layout.List
	rightList       layout.List
	playerLinesList layout.List
	detailList      layout.List

	// metric is the scaled metric of the last frame, for input handled
	// outside layout.
	metric unit.Metric

	// key input tag — a stable pointer used as event.Tag
	keyTag *bool

	invalidate  func()
	closeWindow func()
	th          *material.Theme
}

type libraryView struct {
	LibrarySnapshot
	mode ListingMode
}

type browseState struct {
	library libraryView
	nav     *NavigationState
	focus   browseFocus
	remote  *webRemotePage // separate from the catalog's listing mode
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
	a.clickables = make(map[string]*widget.Clickable)
}

func (a *App) snapshotForMode(mode ListingMode) LibrarySnapshot {
	if mode == ListingModeDateAdded {
		if a.dateAddedSnapshot == nil {
			snapshot := a.client.snapshotFor(dateAddedListingBuilder{})
			a.dateAddedSnapshot = &snapshot
		}
		return *a.dateAddedSnapshot
	}
	return a.client.getSnapshot()
}

func (a *App) applyListingMode(mode ListingMode) {
	a.browse.remote = nil
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

// New creates application state shared by the normal window and headless UI
// drivers. The callbacks are optional and are used for invalidation and quit.
func New(client *APIClient, sseCh <-chan struct{}, config *Config, invalidate, closeWindow func()) *App {
	if client == nil {
		client = NewEmptyAPIClient(nil)
	}
	snapshot := client.getSnapshot()
	th := newAppTheme()

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
		playerPolicy:     playerPolicyFromConfig(config),
		resumePos:        make(map[string]float64),
		keyTag:           keyTag,
		invalidate:       invalidate,
		closeWindow:      closeWindow,
		clickables:       make(map[string]*widget.Clickable),
		th:               th,
		leftList:         layout.List{Axis: layout.Vertical},
		rightList:        layout.List{Axis: layout.Vertical},
		playerLinesList:  layout.List{Axis: layout.Vertical},
		detailList:       layout.List{Axis: layout.Vertical},
		images:           newWeightedAsyncCache[paint.ImageOp](3, 24, imageCacheBudget, imageOpWeight),
		sseCh:            sseCh,
	}
	if config != nil {
		app.controls = config.Controls
		app.hideHelp = config.HelpBar != nil && !*config.HelpBar
		app.nativeOSD = config.MPVOSD == "native"
	}
	if sseCh != nil {
		ctx, cancel := context.WithCancel(context.Background())
		app.sseCancel = cancel
		app.sseSignal = make(chan struct{}, 1)
		done := make(chan struct{})
		app.sseDone = done
		go app.forwardSSE(ctx, done)
	}
	app.images.retryable = isTransientArtworkError
	if invalidate != nil {
		app.images.onDone = invalidate
	}
	return app
}

// forwardSSE wakes the Gio event loop without touching application state. The
// signal channel is bounded so bursts are coalesced until the next frame.
func (a *App) forwardSSE(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-a.sseCh:
			if !ok || ctx.Err() != nil {
				return
			}
			select {
			case a.sseSignal <- struct{}{}:
			default:
			}
			if a.invalidate != nil {
				a.invalidate()
			}
		}
	}
}

// Close stops the web server, control worker, and background event forwarding.
// Run calls it when the window exits;
// headless users should call it when their driver/app lifetime ends.
func (a *App) Close() {
	if a.webUI != nil {
		a.webUI.close()
	}
	if controls := a.playerControls; controls != nil {
		a.cancelPlayerControls()
		<-controls.done
	}
	if a.sseCancel != nil {
		a.sseCancel()
	}
	if a.sseDone != nil {
		<-a.sseDone
	}
}

// ── Main event loop ───────────────────────────────────────────────────────────

// Run drives the application in a normal Gio window.
func Run(w *gioapp.Window, client *APIClient, sseCh <-chan struct{}, config *Config) error {
	a := New(client, sseCh, config, w.Invalidate, func() { w.Perform(system.ActionClose) })
	defer a.Close()
	if config != nil {
		if err := a.startWebUI(config.WebUIAddr); err != nil {
			return err
		}
	}
	return a.run(w.Event)
}

func (a *App) run(nextEvent func() event.Event) error {
	var ops op.Ops
	for {
		switch e := nextEvent().(type) {
		case gioapp.DestroyEvent:
			return e.Err

		case gioapp.FrameEvent:
			ops.Reset()
			gtx := gioapp.NewContext(&ops, e)

			// Draw the shared UI and process its input.
			a.Layout(gtx)

			e.Frame(gtx.Ops)

		default:
			// Invalidate wakes inactive/occluded windows without a FrameEvent.
			// Keep remote commands and async playback results moving even then.
			a.drainChannels()
			a.drainWebCommands()
			a.publishWebStatus()
		}
	}
}

// drainChannels checks async goroutine results without blocking.
func (a *App) drainChannels() {
	if a.playerDone != nil {
		select {
		case result := <-a.playerDone:
			a.applyPlaybackSnapshot(result.tracker)
			if result.retainedRuntime != "" {
				a.playerCleanupDir = result.retainedRuntime
				a.pendingPlayback = nil
			}
			a.cancelPlayerControls()
			a.playerSocket = ""
			a.playerDone = nil
			a.playerTracker = nil
			a.playerStop = nil
			a.playerTitle = ""
			a.playerFromWeb = false
			a.playerStopping = false
			if next := a.pendingPlayback; next != nil {
				a.pendingPlayback = nil
				a.playerStatus = PlayerStatusIdle
				a.launchPlayback(*next)
			} else if result.err != nil {
				a.playerStatus = PlayerStatusError
				a.playerError = result.err.Error()
			} else {
				a.playerStatus = PlayerStatusIdle
				a.playerError = ""
			}
		default:
		}
	}

	a.updatePlayerTitle()
	a.drainPlayerControls()

	if a.sseSignal != nil {
		select {
		case <-a.sseSignal:
			a.libraryChanged()
		default:
		}
	}

	a.drainRefresh()

	a.drainSubtitlePopup()

	// Drain all pending image fetch results.
	a.images.drain()
}

// detailScrollStep is how far one Up/Down moves the movie detail page.
const detailScrollStep = unit.Dp(160)

// scrollDetail scrolls the movie detail page content with the keyboard.
func (a *App) scrollDetail(dir int) {
	step := a.metric.Dp(detailScrollStep)
	// Offset is relative to the first visible item. Gio traverses earlier
	// items for negative offsets and clamps the actual list boundaries.
	a.detailList.Position.Offset += dir * step
}

// ── Root layout ───────────────────────────────────────────────────────────────

// Layout renders one frame and processes input routed by the supplied
// layout context. It is intentionally independent of gioapp.Window.
// Loading reports whether the library or artwork is still being fetched, so
// the headless driver can wait for it before a screenshot. Call it from the
// goroutine that runs Layout.
func (a *App) Loading() bool {
	return a.refreshing || a.images.busy()
}

func (a *App) Layout(gtx C) D {
	defer a.publishWebStatus()
	gtx = scaleContext(gtx)
	a.metric = gtx.Metric
	// Run the last frame's click first: its closure captured that frame's
	// state, which refresh results or web commands may replace below.
	if click := a.pendingClick; click != nil {
		a.pendingClick = nil
		click()
	}
	a.drainChannels()
	a.drainWebCommands()
	paint.Fill(gtx.Ops, colorBg)
	area := clip.Rect(image.Rectangle{Max: gtx.Constraints.Max}).Push(gtx.Ops)
	event.Op(gtx.Ops, a.keyTag)
	a.handleKeys(gtx)
	area.Pop()

	return a.layout(gtx)
}

// scaleContext keeps the interface at the same proportional size in the
// production window and the headless driver. The reference width is a full-HD
// TV; the limits retain usability in unusually small and large windows.
func scaleContext(gtx C) C {
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
	return gtx
}

func (a *App) layoutPlayingBar(gtx C) D {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			return layout.Stack{}.Layout(gtx,
				layout.Expanded(func(gtx C) D {
					size := gtx.Constraints.Min
					defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
					paint.Fill(gtx.Ops, colorSurface)
					return D{Size: size}
				}),
				layout.Stacked(func(gtx C) D {
					return layout.Inset{Left: a.viewportInsets.Left, Right: a.viewportInsets.Right, Top: a.viewportInsets.Top, Bottom: dp8}.Layout(gtx, func(gtx C) D {
						status := "Now playing"
						if a.playerFromWeb {
							status = "Now playing from Web UI"
						}
						if a.playerStopping || a.pendingPlayback != nil {
							status = "Stopping playback"
						}
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(func(gtx C) D { return a.label(gtx, spMeta, status, colorPrimary, font.Bold) }),
							layout.Rigid(layout.Spacer{Width: dp12}.Layout),
							layout.Flexed(1, func(gtx C) D { return a.label(gtx, spMeta, a.playerTitle, colorFg, font.Medium) }),
						)
					})
				}),
			)
		}),
		layout.Rigid(hairline),
	)
}

func (a *App) layout(gtx C) D {
	a.viewportInsets = layout.UniformInset(dp24)
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx C) D {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					if a.playerStatus != PlayerStatusRunning {
						return D{}
					}
					return a.layoutPlayingBar(gtx)
				}),
				layout.Flexed(1, func(gtx C) D {
					a.selectionHidden = a.subPopup != nil
					defer func() { a.selectionHidden = false }()
					if a.detail != nil {
						return a.layoutDetail(gtx)
					}
					return layout.Inset{Top: a.viewportInsets.Top, Left: a.viewportInsets.Left, Right: a.viewportInsets.Right}.Layout(gtx, a.layoutBrowse)
				}),
				layout.Rigid(func(gtx C) D {
					if a.subPopup == nil {
						return a.layoutHelp(gtx)
					}
					// Reserve the footer once; draw only above the popup dimmer.
					record := op.Record(gtx.Ops)
					dims := a.layoutHelp(gtx)
					record.Stop()
					return dims
				}),
			)
		}),
		layout.Stacked(func(gtx C) D {
			if a.subPopup == nil {
				return D{}
			}
			return a.layoutSubtitlePopup(gtx)
		}),
		layout.Stacked(func(gtx C) D {
			if a.subPopup == nil {
				return D{}
			}
			// Popup hints must remain readable above the full-screen dimmer.
			gtx.Constraints.Min = gtx.Constraints.Max
			return layout.S.Layout(gtx, a.layoutHelp)
		}),
	)
}

// ── Format helpers (ported from ui.go, no lipgloss) ──────────────────────────

// ── MPV helpers (ported from ui.go) ──────────────────────────────────────────
