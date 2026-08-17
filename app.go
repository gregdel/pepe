package main

import (
	"fmt"
	"image"
	"os"
	"os/exec"
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
	"github.com/dustin/go-humanize"
	polochon "github.com/odwrtw/polochon/lib"
	"github.com/odwrtw/polochon/lib/papi"
)

// ── Player state ─────────────────────────────────────────────────────────────

type PlayerStatus string

const (
	PlayerStatusIdle    PlayerStatus = "Idle"
	PlayerStatusRunning PlayerStatus = "Running"
	PlayerStatusError   PlayerStatus = "Error"
)

type PlayerMode string

const (
	PlayerModeGUI PlayerMode = "gui"
	PlayerModeKMS PlayerMode = "kms"
)

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
	refreshing  bool
	refreshDone chan LibrarySnapshot // goroutine → event loop
	sseCh       <-chan struct{}      // SSE events from server

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
	mediaType string
	mediaID   string
}

// captureSelection records what is currently selected, or returns nil if
// nothing meaningful is selected (e.g. browsing prefix nodes or at root).
func (a *App) captureSelection() *savedSelection {
	nav := a.nav
	if nav.isAtRoot() {
		return nil
	}
	child := nav.getSelectedChild()
	if child == nil || !child.IsTerminal {
		return nil
	}
	media := a.mediaForNode(child)
	if media == nil {
		return nil
	}
	return &savedSelection{
		section:   nav.section,
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

func (a *App) applyLibrarySnapshot(snapshot LibrarySnapshot) {
	a.movieRadix = snapshot.movieRadix
	a.showRadix = snapshot.showRadix
	a.titleToMedia = snapshot.titleToMedia
	a.movieCount = snapshot.movieCount
	a.showCount = snapshot.showCount
}

func (a *App) snapshotForMode(mode ListingMode) (LibrarySnapshot, error) {
	if mode == ListingModeDateAdded {
		return a.client.snapshotFor(dateAddedListingBuilder{})
	}
	return a.client.getSnapshot(), nil
}

func (a *App) applyListingMode(mode ListingMode) bool {
	wasAtRoot := a.nav.isAtRoot()
	section := a.nav.section
	homeCursor := a.nav.cursor

	snapshot, err := a.snapshotForMode(mode)
	if err != nil {
		return false
	}
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
	return true
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

	if a.refreshDone != nil {
		select {
		case snapshot := <-a.refreshDone:
			sel := a.captureSelection()
			if a.listingMode == ListingModeDateAdded {
				if dateSnapshot, err := a.snapshotForMode(a.listingMode); err == nil {
					snapshot = dateSnapshot
				}
			}
			a.applyLibrarySnapshot(snapshot)
			a.nav = newNavigationState()
			a.refreshing = false
			a.refreshDone = nil
			// invalidate cached images since the library has changed
			a.images.reset()
			a.nfos.reset()
			// pop any open detail page back to browse
			a.detail = nil
			a.restoreSelection(sel)
		default:
		}
	}

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

	// Drain subtitle popup channels.
	if a.subPopup != nil {
		select {
		case res := <-a.subPopup.resultCh:
			if res.err != nil {
				a.subPopup.state = SubtitlePopupError
				a.subPopup.errMsg = res.err.Error()
			} else {
				a.subPopup.state = SubtitlePopupReady
				a.subPopup.entries = res.entries
			}
		default:
		}
		if a.subPopup != nil && a.subPopup.downloadDone != nil {
			select {
			case res := <-a.subPopup.downloadDone:
				if res.err != nil {
					a.subPopup.state = SubtitlePopupError
					a.subPopup.errMsg = res.err.Error()
				} else {
					a.subPopup.state = SubtitlePopupDone
					a.subPopup.doneAt = time.Now()
				}
				a.subPopup.downloadDone = nil
			default:
			}
		}
		// Auto-close after showing "Downloaded!" for 1.5s.
		if a.subPopup != nil && a.subPopup.state == SubtitlePopupDone && time.Since(a.subPopup.doneAt) > 1500*time.Millisecond {
			a.subPopup = nil
		}
	}

	// Drain all pending image/NFO fetch results.
	a.images.drain()
	a.nfos.drain()
}

func (a *App) applyPlaybackSnapshot(tracker *playbackTracker) {
	if tracker == nil {
		return
	}
	tracker.wait(100 * time.Millisecond)
	mediaKey, pos, eof := tracker.snapshot()
	if mediaKey == "" {
		return
	}
	if eof {
		delete(a.resumePos, mediaKey)
		return
	}
	if pos > 30 {
		a.resumePos[mediaKey] = pos
	}
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
	switch ke.Name {
	case key.NameUpArrow, "K":
		if a.subPopup.cursor > 0 {
			a.subPopup.cursor--
		}
	case key.NameDownArrow, "J":
		if a.subPopup.state == SubtitlePopupReady && a.subPopup.cursor < len(a.subPopup.entries)-1 {
			a.subPopup.cursor++
		}
	case key.NameReturn, key.NameEnter, key.NameRightArrow, "L":
		a.subPopupSelect()
	case key.NameEscape, key.NameLeftArrow, "H":
		a.closeSubtitlePopup()
	}
}

func (a *App) handleBrowseKeys(ke key.Event) {
	switch ke.Name {
	case key.NameUpArrow, "K":
		a.navigateUp()
	case key.NameDownArrow, "J":
		a.navigateDown()
	case key.NameLeftArrow, "H":
		a.navigateLeft()
	case key.NameEscape:
		a.navigateEscape()
	case key.NameRightArrow, "L":
		a.navigateRight()
	case key.NameReturn, key.NameEnter:
		a.activateBrowseSelection()
	case key.NameTab:
		a.toggleSection()
	case "R":
		a.startRefresh()
	case "S":
		a.openSubtitlePopup()
	case "Q":
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
	switch ke.Name {
	case key.NameReturn, key.NameEnter, key.NameRightArrow, "L":
		a.launchMovie(&MediaItem{ID: d.movieID, Title: d.movieTitle, Type: "movie"})
	case key.NameEscape, key.NameLeftArrow, "H":
		a.popDetail()
	case key.NameUpArrow, "K":
		a.scrollDetail(-1)
	case key.NameDownArrow, "J":
		a.scrollDetail(1)
	case "S":
		a.openSubtitlePopup()
	case "Q":
		a.w.Perform(system.ActionClose)
	}
}

func (a *App) handleShowKeys(ke key.Event, s *showPage) {
	switch ke.Name {
	case key.NameUpArrow, "K":
		s.up()
	case key.NameDownArrow, "J":
		s.down()
	case key.NameLeftArrow, "H":
		if s.focus == focusTabs {
			s.left()
		} else {
			a.popDetail()
		}
	case key.NameRightArrow, "L":
		s.right()
	case key.NameReturn, key.NameEnter:
		if s.focus == focusTabs {
			s.focus = focusEpisodes
			return
		}
		if ep := s.currentEpisode(); ep != nil {
			a.launchEpisode(ep)
		}
	case key.NameEscape:
		a.popDetail()
	case "S":
		a.openSubtitlePopup()
	case "Q":
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
		a.nav.path = []*RadixNode{}
		a.nav.cursor = int(a.nav.section)
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
		a.nav.path = []*RadixNode{}
		a.nav.cursor = int(a.nav.section)
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
		if media.Type == "show" {
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

func (a *App) launchMovie(media *MediaItem) {
	mediaKey, sockPath, execArgs, mode, ok := a.buildMovieCommand(media)
	if !ok {
		a.playerStatus = PlayerStatusError
		a.playerError = "failed to build movie command"
		return
	}
	a.startPlayer(execArgs, mode)
	a.playbackTracker = startIPC(sockPath, mediaKey)
}

func (a *App) launchEpisode(node *DynamicNode) {
	mediaKey, sockPath, execArgs, mode, ok := a.buildEpisodeCommand(node)
	if !ok {
		a.playerStatus = PlayerStatusError
		a.playerError = "failed to build episode command"
		return
	}
	a.startPlayer(execArgs, mode)
	a.playbackTracker = startIPC(sockPath, mediaKey)
}

func (a *App) startPlayer(execArgs []string, mode PlayerMode) {
	if len(execArgs) == 0 {
		return
	}
	a.playerStatus = PlayerStatusRunning
	a.playerMode = mode
	a.playerError = ""
	a.playerDone = make(chan error, 1)

	go func() {
		cmd := exec.Command(execArgs[0], execArgs[1:]...) //nolint:gosec
		a.playerDone <- cmd.Run()
		a.w.Invalidate()
	}()
}

func (a *App) startRefresh() {
	if a.refreshing {
		return
	}
	a.refreshing = true
	a.refreshDone = make(chan LibrarySnapshot, 1)

	go func() {
		_ = a.client.fetchLibrary()
		a.refreshDone <- a.client.getSnapshot()
		a.w.Invalidate()
	}()
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

func formatQualityBadge(q polochon.Quality) string {
	return string(q)
}

func formatContainer(c string) string {
	return strings.ToUpper(c)
}

func formatCodecs(video, audio string) string {
	switch {
	case video == "" && audio == "":
		return ""
	case video == "":
		return audio
	case audio == "":
		return video
	default:
		return video + " + " + audio
	}
}

func formatDateAdded(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02")
}

func formatGenres(genres []string) string {
	return strings.Join(genres, ", ")
}

var languageNames = map[string]string{
	"en": "English",
	"fr": "French",
}

func formatLanguageName(lang polochon.Language) string {
	short := lang.ShortForm()
	if name, ok := languageNames[short]; ok {
		return fmt.Sprintf("%s (%s)", name, short)
	}
	return fmt.Sprintf("%s (%s)", string(lang), string(lang))
}

func formatSubtitles(embeddedLangs []polochon.Language, externalSubs []*papi.Subtitle) string {
	if len(embeddedLangs) == 0 && len(externalSubs) == 0 {
		return "Subtitles: None"
	}

	var b strings.Builder
	b.WriteString("Subtitles:\n")
	for _, lang := range embeddedLangs {
		fmt.Fprintf(&b, "  %s - embedded\n", formatLanguageName(lang))
	}
	for _, sub := range externalSubs {
		sizeStr := ""
		if sub.Size > 0 {
			sizeStr = fmt.Sprintf(", %s", humanize.Bytes(uint64(sub.Size)))
		}
		fmt.Fprintf(&b, "  %s - external%s\n", formatLanguageName(sub.Lang), sizeStr)
	}
	return strings.TrimRight(b.String(), "\n")
}

// ── MPV helpers (ported from ui.go) ──────────────────────────────────────────

func detectPlayerMode() PlayerMode {
	for _, name := range []string{"WAYLAND_DISPLAY", "DISPLAY"} {
		if os.Getenv(name) != "" {
			return PlayerModeGUI
		}
	}
	sessionType := strings.ToLower(strings.TrimSpace(os.Getenv("XDG_SESSION_TYPE")))
	if sessionType == "wayland" || sessionType == "x11" {
		return PlayerModeGUI
	}
	return PlayerModeKMS
}

func buildMPVArgs(mpvPath, title, videoURL string, subtitleURLs []string, mode PlayerMode, startPos float64, sockPath string, fullscreen bool) []string {
	if strings.TrimSpace(mpvPath) == "" {
		mpvPath = "mpv"
	}
	args := []string{mpvPath}
	args = append(args, "--http-header-fields=X-Auth-Token: $POLOCHON_TOKEN")
	if startPos > 30 {
		args = append(args, fmt.Sprintf("--start=%d", int(startPos)))
	}
	if mode == PlayerModeKMS {
		args = append(args, "--vo=gpu", "--gpu-context=drm", "--opengl-es=yes", "--fs", "--no-border", "--ontop")
	} else if fullscreen {
		args = append(args, "--fs", "--no-border")
	}
	if title != "" {
		args = append(args, "--title="+title)
	}
	for _, sub := range subtitleURLs {
		if sub != "" {
			args = append(args, "--sub-file="+sub)
		}
	}
	args = append(args, "--input-ipc-server="+sockPath)
	args = append(args, videoURL)
	return args
}

func replaceMpvToken(args []string, token string) []string {
	if token == "" {
		return args
	}
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = strings.ReplaceAll(a, "$POLOCHON_TOKEN", token)
	}
	return out
}

func (a *App) buildMovieCommand(media *MediaItem) (mediaKey, sockPath string, execArgs []string, mode PlayerMode, ok bool) {
	movie, ok := a.client.getMovie(media.ID)
	if !ok {
		return "", "", nil, "", false
	}
	videoURL, err := a.client.getDownloadURL(movie)
	if err != nil {
		return "", "", nil, "", false
	}

	var subs []string
	for _, sub := range movie.Subtitles {
		if sub == nil || sub.Embedded {
			continue
		}
		u, err := a.client.getDownloadURL(sub)
		if err != nil {
			continue
		}
		subs = append(subs, u)
	}

	mediaKey = "movie:" + movie.ImdbID
	sockPath = fmt.Sprintf("/tmp/polochon-mpv-%d.sock", os.Getpid())
	startPos := a.resumePos[mediaKey]
	mode = detectPlayerMode()
	execArgs = buildMPVArgs(a.mpvPath, media.Title, videoURL, subs, mode, startPos, sockPath, a.playerFullscreen)
	execArgs = replaceMpvToken(execArgs, a.client.getToken())
	return mediaKey, sockPath, execArgs, mode, true
}

func (a *App) buildEpisodeCommand(node *DynamicNode) (mediaKey, sockPath string, execArgs []string, mode PlayerMode, ok bool) {
	if node == nil || node.Episode == nil {
		return "", "", nil, "", false
	}
	ep := node.Episode
	videoURL, err := a.client.getDownloadURL(ep)
	if err != nil {
		return "", "", nil, "", false
	}

	var subs []string
	for _, sub := range ep.Subtitles {
		if sub == nil || sub.Embedded {
			continue
		}
		u, err := a.client.getDownloadURL(sub)
		if err != nil {
			continue
		}
		subs = append(subs, u)
	}

	mediaKey = fmt.Sprintf("episode:%s:%d:%d", node.ShowID, ep.Season, ep.Episode)
	sockPath = fmt.Sprintf("/tmp/polochon-mpv-%d.sock", os.Getpid())
	startPos := a.resumePos[mediaKey]
	mode = detectPlayerMode()
	execArgs = buildMPVArgs(a.mpvPath, node.Label, videoURL, subs, mode, startPos, sockPath, a.playerFullscreen)
	execArgs = replaceMpvToken(execArgs, a.client.getToken())
	return mediaKey, sockPath, execArgs, mode, true
}
