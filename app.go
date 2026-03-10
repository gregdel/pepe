package main

import (
	"fmt"
	"image"
	"image/color"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"gioui.org/app"
	"gioui.org/font"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"github.com/dustin/go-humanize"
	polochon "github.com/odwrtw/polochon/lib"
	"github.com/odwrtw/polochon/lib/papi"
)

// ── Colour palette (matches the original lipgloss TUI) ──────────────────────

var (
	colorBg         = color.NRGBA{R: 0x1c, G: 0x1c, B: 0x1c, A: 0xff}
	colorFg         = color.NRGBA{R: 0xd0, G: 0xd0, B: 0xd0, A: 0xff}
	colorSubtle     = color.NRGBA{R: 0xa8, G: 0xa8, B: 0xa8, A: 0xff} // secondary text on glass card
	colorPrimary    = color.NRGBA{R: 0x87, G: 0x5f, B: 0xff, A: 0xff} // 256-colour 99  (purple)
	colorMuted      = color.NRGBA{R: 0x60, G: 0x60, B: 0x60, A: 0xff} // 256-colour 240 (grey)
	colorTerminal   = color.NRGBA{R: 0xff, G: 0x69, B: 0xb4, A: 0xff} // 256-colour 212 (pink)
	colorSelectedBg = color.NRGBA{R: 0x00, G: 0x5f, B: 0xaf, A: 0xff} // 256-colour 25  (blue bg)
	colorSelectedFg = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	colorDivider    = color.NRGBA{R: 0x33, G: 0x33, B: 0x33, A: 0xff}

	// badge backgrounds
	colorBadgeBg      = color.NRGBA{R: 0x44, G: 0x44, B: 0x44, A: 0xff}
	colorSizeBadgeBg  = color.NRGBA{R: 0xd7, G: 0x00, B: 0x87, A: 0xff} // 256-colour 162 (pink)
	colorQualBadgeBg  = color.NRGBA{R: 0x87, G: 0x5f, B: 0xff, A: 0xff} // purple
	colorCodecBadgeBg = color.NRGBA{R: 0xff, G: 0xaf, B: 0x00, A: 0xff} // 256-colour 214 (orange)
	colorBadgeFg      = color.NRGBA{R: 0x00, G: 0x00, B: 0x00, A: 0xff}
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

	// player
	playerStatus  PlayerStatus
	playerMode    PlayerMode
	playerError   string
	playerCommand string
	playerDone    chan error // goroutine → event loop

	// library refresh
	refreshing  bool
	refreshDone chan LibrarySnapshot // goroutine → event loop
	sseCh       <-chan struct{}      // SSE events from server

	// image cache (thumbs, fanarts, posters — all media types)
	images *imageCache

	// nfo cache — parsed NFO data per movie ID
	nfos *nfoCache

	// Gio persistent list state (must survive between frames)
	leftList        layout.List
	rightList       layout.List
	playerLinesList layout.List

	// key input tag — a stable pointer used as event.Tag
	keyTag *bool

	w  *app.Window
	th *material.Theme
}

func newApp(w *app.Window, client *APIClient, sseCh <-chan struct{}) *App {
	snapshot := client.getSnapshot()
	th := material.NewTheme()

	keyTag := new(bool)

	return &App{
		client:          client,
		movieRadix:      snapshot.movieRadix,
		showRadix:       snapshot.showRadix,
		titleToMedia:    snapshot.titleToMedia,
		movieCount:      snapshot.movieCount,
		showCount:       snapshot.showCount,
		nav:             newNavigationState(),
		playerStatus:    PlayerStatusIdle,
		keyTag:          keyTag,
		w:               w,
		th:              th,
		leftList:        layout.List{Axis: layout.Vertical},
		rightList:       layout.List{Axis: layout.Vertical},
		playerLinesList: layout.List{Axis: layout.Vertical},
		images:          newImageCache(),
		nfos:            newNFOCache(),
		sseCh:           sseCh,
	}
}

// ── Main event loop ───────────────────────────────────────────────────────────

func run(w *app.Window, client *APIClient, sseCh <-chan struct{}) error {
	a := newApp(w, client, sseCh)
	var ops op.Ops
	requestFocus := true // grab focus on the very first frame

	for {
		switch e := w.Event().(type) {
		case app.DestroyEvent:
			return e.Err

		case app.FrameEvent:
			ops.Reset()
			gtx := app.NewContext(&ops, e)

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
			if err != nil {
				a.playerStatus = PlayerStatusError
				a.playerError = err.Error()
			} else {
				a.playerStatus = PlayerStatusIdle
				a.playerError = ""
			}
			a.playerDone = nil
		default:
		}
	}

	if a.refreshDone != nil {
		select {
		case snapshot := <-a.refreshDone:
			a.movieRadix = snapshot.movieRadix
			a.showRadix = snapshot.showRadix
			a.titleToMedia = snapshot.titleToMedia
			a.movieCount = snapshot.movieCount
			a.showCount = snapshot.showCount
			a.nav = newNavigationState()
			a.refreshing = false
			a.refreshDone = nil
			// invalidate cached images since the library has changed
			a.images.reset()
			a.nfos.reset()
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

	// Drain all pending image fetch results.
	if a.images.drain() {
		a.w.Invalidate()
	}

	// Drain all pending NFO fetch results.
	if a.nfos.drain() {
		a.w.Invalidate()
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
	key.Filter{Focus: nil, Name: "H", Optional: key.ModShift},
	key.Filter{Focus: nil, Name: "J", Optional: key.ModShift},
	key.Filter{Focus: nil, Name: "K", Optional: key.ModShift},
	key.Filter{Focus: nil, Name: "L", Optional: key.ModShift},
	key.Filter{Focus: nil, Name: "Q", Optional: key.ModShift},
	key.Filter{Focus: nil, Name: "R", Optional: key.ModShift},
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

		switch ke.Name {
		case key.NameUpArrow, "K":
			a.navigateUp()
		case key.NameDownArrow, "J":
			a.navigateDown()
		case key.NameLeftArrow, "H":
			a.navigateLeft()
		case key.NameRightArrow, "L", key.NameReturn, key.NameEnter:
			a.navigateRight()
		case key.NameTab:
			a.toggleSection()
		case "R":
			a.startRefresh()
		case "Q":
			a.w.Perform(system.ActionClose)
		}
	}
}

// ── Navigation ────────────────────────────────────────────────────────────────

func (a *App) navigateUp() {
	if a.nav.isAtRoot() {
		if a.nav.cursor > 0 {
			a.nav.cursor = 0
		}
	} else {
		a.nav.moveCursorUp()
	}
}

func (a *App) navigateDown() {
	if a.nav.isAtRoot() {
		if a.nav.cursor < 1 {
			a.nav.cursor = 1
		}
	} else {
		a.nav.moveCursorDown(a.nav.getChildrenCount())
	}
}

func (a *App) navigateLeft() {
	if a.nav.isAtSectionRoot() {
		a.nav.path = []*RadixNode{}
		a.nav.cursor = int(a.nav.section)
	} else {
		a.nav.goBack()
	}
}

func (a *App) navigateRight() {
	if a.nav.isAtRoot() {
		if a.nav.cursor == 0 {
			a.nav.resetToSection(SectionMovies, a.movieRadix.Root())
		} else {
			a.nav.resetToSection(SectionShows, a.showRadix.Root())
		}
		return
	}

	if a.nav.inDynamicNav {
		child := a.nav.getSelectedDynamicChild()
		if child == nil {
			return
		}
		if child.IsTerminalNode() {
			a.launchEpisode(child)
			return
		}
		a.nav.goForwardDynamic(child)
		return
	}

	child := a.nav.getSelectedChild()
	if child == nil {
		return
	}

	if child.IsTerminal {
		media, ok := a.titleToMedia[child.FullString]
		if !ok {
			return
		}
		if media.Type == "show" {
			show, ok := a.client.getShow(media.ID)
			if !ok {
				return
			}
			seasonNodes := createSeasonNodes(show)
			if len(seasonNodes) > 0 {
				a.nav.enterDynamicNav(seasonNodes, show.Title)
			}
			return
		}
		// movie
		a.launchMovie(media)
		return
	}

	a.nav.goForward(child)
}

func (a *App) toggleSection() {
	if a.nav.section == SectionMovies {
		a.nav.resetToSection(SectionShows, a.showRadix.Root())
	} else {
		a.nav.resetToSection(SectionMovies, a.movieRadix.Root())
	}
}

// ── Player ────────────────────────────────────────────────────────────────────

func (a *App) launchMovie(media *MediaItem) {
	_, execArgs, mode, ok := a.buildMovieCommand(media)
	if !ok {
		a.playerStatus = PlayerStatusError
		a.playerError = "failed to build movie command"
		return
	}
	a.startPlayer(execArgs, mode)
}

func (a *App) launchEpisode(node *DynamicNode) {
	_, execArgs, mode, ok := a.buildEpisodeCommand(node)
	if !ok {
		a.playerStatus = PlayerStatusError
		a.playerError = "failed to build episode command"
		return
	}
	a.startPlayer(execArgs, mode)
}

func (a *App) startPlayer(execArgs []string, mode PlayerMode) {
	if len(execArgs) == 0 {
		return
	}
	a.playerStatus = PlayerStatusRunning
	a.playerMode = mode
	a.playerCommand = buildMPVCommand(execArgs)
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

// ── Layout ────────────────────────────────────────────────────────────────────

type (
	C = layout.Context
	D = layout.Dimensions
)

const (
	leftFlex  = 0.38
	rightFlex = 0.62

	// Font sizes — calibrated for 10-foot / TV viewing distance at 1080p.
	sp      = unit.Sp(20) // default body text (KV rows, help, nav)
	spBody  = unit.Sp(22) // plot, subtitles, multi-line text
	spTitle = unit.Sp(36) // movie / show title
	spMeta  = unit.Sp(20) // year · runtime · genres line
	spBadge = unit.Sp(16) // tech badge pills

	// Spacing
	dp1  = unit.Dp(1)
	dp4  = unit.Dp(4)
	dp8  = unit.Dp(8)
	dp12 = unit.Dp(12)
	dp16 = unit.Dp(16)
	dp20 = unit.Dp(20)
	dp24 = unit.Dp(24)
)

func (a *App) layout(gtx C) D {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Flexed(1, func(gtx C) D {
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
				layout.Flexed(leftFlex, a.layoutLeftPanel),
				layout.Rigid(func(gtx C) D {
					return divider(gtx)
				}),
				layout.Flexed(rightFlex, a.layoutRightPanel),
			)
		}),
		layout.Rigid(func(gtx C) D {
			return a.layoutHelp(gtx)
		}),
	)
}

// divider draws a 1dp vertical separator.
func divider(gtx C) D {
	size := image.Point{X: gtx.Dp(unit.Dp(1)), Y: gtx.Constraints.Max.Y}
	defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, colorDivider)
	return D{Size: size}
}

// ── Left panel ────────────────────────────────────────────────────────────────

func (a *App) layoutLeftPanel(gtx C) D {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			return a.layoutPanelTitle(gtx)
		}),
		layout.Flexed(1, func(gtx C) D {
			return a.layoutLeftList(gtx)
		}),
	)
}

func (a *App) layoutPanelTitle(gtx C) D {
	title := "Polochon"
	if !a.nav.isAtRoot() {
		if a.nav.section == SectionMovies {
			title = "Polochon — Movies"
		} else {
			title = "Polochon — Shows"
		}
	}
	return layout.UniformInset(dp8).Layout(gtx, func(gtx C) D {
		lbl := material.Label(a.th, unit.Sp(16), title)
		lbl.Color = colorPrimary
		lbl.Font.Weight = font.Bold
		return lbl.Layout(gtx)
	})
}

func (a *App) layoutLeftList(gtx C) D {
	inset := layout.Inset{Left: dp8, Right: dp8, Top: dp4, Bottom: dp4}

	if a.nav.isAtRoot() {
		items := []struct {
			name  string
			count int
		}{
			{"Movies", a.movieCount},
			{"Shows", a.showCount},
		}
		return a.leftList.Layout(gtx, len(items), func(gtx C, i int) D {
			return inset.Layout(gtx, func(gtx C) D {
				return a.drawNavItem(gtx, items[i].name, items[i].count, i == a.nav.cursor, false)
			})
		})
	}

	if a.nav.inDynamicNav {
		current := a.nav.getCurrentDynamicNode()
		if current == nil || len(current.Children) == 0 {
			return D{}
		}
		children := current.Children
		return a.leftList.Layout(gtx, len(children), func(gtx C, i int) D {
			return inset.Layout(gtx, func(gtx C) D {
				node := children[i]
				isSelected := i == a.nav.cursor
				label := node.Label
				if a.nav.dynamicTitle != "" {
					label = a.nav.dynamicTitle + " · " + node.Label
				}
				count := 0
				if !node.IsTerminalNode() {
					count = node.SubtreeCount
				}
				return a.drawNavItem(gtx, label, count, isSelected, node.IsTerminalNode())
			})
		})
	}

	// Radix tree navigation
	current := a.nav.getCurrentNode()
	if current == nil {
		return D{}
	}
	currentPath := a.nav.getCurrentPath()
	children := current.Children
	return a.leftList.Layout(gtx, len(children), func(gtx C, i int) D {
		return inset.Layout(gtx, func(gtx C) D {
			node := children[i]
			isSelected := i == a.nav.cursor
			isTerminal := node.IsTerminal && node.SubtreeCount == 1

			var label string
			if node.IsTerminal && node.FullString != "" {
				label = node.FullString
			} else if currentPath != "" {
				label = currentPath + node.Label
			} else {
				label = node.Label
			}

			count := 0
			if !isTerminal {
				count = node.SubtreeCount
			}

			return a.drawNavItemWithPrefix(gtx, label, currentPath, count, isSelected, isTerminal)
		})
	})
}

// drawNavItem renders a simple navigation row (section root or dynamic nodes).
func (a *App) drawNavItem(gtx C, label string, count int, selected, isTerminal bool) D {
	text := label
	if count > 0 {
		text = fmt.Sprintf("%s (%d)", label, count)
	}
	return a.drawNavRow(gtx, text, selected, isTerminal, "")
}

// drawNavItemWithPrefix renders a radix-tree nav row, highlighting the
// accumulated prefix in primary colour.
func (a *App) drawNavItemWithPrefix(gtx C, label, prefix string, count int, selected, isTerminal bool) D {
	text := label
	if count > 0 {
		text = fmt.Sprintf("%s (%d)", label, count)
	}
	return a.drawNavRow(gtx, text, selected, isTerminal, prefix)
}

// drawNavRow is the low-level row renderer.
func (a *App) drawNavRow(gtx C, text string, selected, isTerminal bool, highlightPrefix string) D {
	bg := colorBg
	fg := colorFg
	if selected {
		bg = colorSelectedBg
		fg = colorSelectedFg
	} else if isTerminal {
		fg = colorTerminal
	}

	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx C) D {
			defer clip.Rect{Max: gtx.Constraints.Min}.Push(gtx.Ops).Pop()
			paint.Fill(gtx.Ops, bg)
			return D{Size: gtx.Constraints.Min}
		}),
		layout.Stacked(func(gtx C) D {
			return layout.Inset{Top: dp4, Bottom: dp4, Left: dp8, Right: dp8}.Layout(gtx, func(gtx C) D {
				if highlightPrefix != "" && !selected && strings.HasPrefix(text, highlightPrefix) {
					// Two-part label: highlighted prefix + normal remainder
					return layout.Flex{}.Layout(gtx,
						layout.Rigid(func(gtx C) D {
							lbl := material.Label(a.th, sp, highlightPrefix)
							lbl.Color = colorPrimary
							lbl.Font.Weight = font.Bold
							return lbl.Layout(gtx)
						}),
						layout.Rigid(func(gtx C) D {
							remainder := text[len(highlightPrefix):]
							lbl := material.Label(a.th, sp, remainder)
							lbl.Color = fg
							return lbl.Layout(gtx)
						}),
					)
				}
				lbl := material.Label(a.th, sp, text)
				lbl.Color = fg
				if selected {
					lbl.Font.Weight = font.Bold
				}
				return lbl.Layout(gtx)
			})
		}),
	)
}

// ── Right panel ───────────────────────────────────────────────────────────────

func (a *App) layoutRightPanel(gtx C) D {
	inset := layout.UniformInset(dp12)
	return inset.Layout(gtx, func(gtx C) D {
		return a.layoutRightContent(gtx)
	})
}

func (a *App) layoutRightContent(gtx C) D {
	if a.nav.isAtRoot() {
		if a.nav.cursor == 0 {
			return a.renderSectionInfo(gtx, "Movies", a.movieCount)
		}
		return a.renderSectionInfo(gtx, "Shows", a.showCount)
	}

	if a.nav.inDynamicNav {
		child := a.nav.getSelectedDynamicChild()
		if child == nil {
			return a.renderText(gtx, colorMuted, "No item selected")
		}
		if child.Type == DynamicNodeSeason {
			return a.renderSeasonDetails(gtx, child)
		}
		return a.renderEpisodeDetails(gtx, child)
	}

	child := a.nav.getSelectedChild()
	if child == nil {
		return a.renderText(gtx, colorMuted, "No item selected")
	}
	if child.IsTerminal {
		media, ok := a.titleToMedia[child.FullString]
		if !ok {
			return a.renderText(gtx, colorMuted, "Media details not found")
		}
		return a.renderMediaDetails(gtx, media)
	}
	return a.renderNodeChildren(gtx, child)
}

// ── Right panel: section info ─────────────────────────────────────────────────

func (a *App) renderSectionInfo(gtx C, name string, count int) D {
	lines := []labelLine{
		{text: name, color: colorPrimary, size: unit.Sp(18), weight: font.Bold},
		{text: "", color: colorFg, size: sp},
		{text: fmt.Sprintf("Total items: %d", count), color: colorFg, size: sp},
		{text: "", color: colorFg, size: sp},
		{text: "Press Enter or → to browse", color: colorMuted, size: sp},
	}
	return a.renderLines(gtx, lines)
}

// ── Right panel: radix non-terminal node ──────────────────────────────────────

func (a *App) renderNodeChildren(gtx C, node *RadixNode) D {
	currentPath := a.nav.getCurrentPath()
	selectedPrefix := currentPath + node.Label

	var lines []labelLine
	lines = append(lines, labelLine{text: "Children", color: colorPrimary, size: unit.Sp(16), weight: font.Bold})
	lines = append(lines, labelLine{text: ""})
	lines = append(lines, labelLine{text: fmt.Sprintf("%d items in this group:", node.SubtreeCount), color: colorFg, size: sp})
	lines = append(lines, labelLine{text: ""})

	maxShow := 20
	for i, child := range node.Children {
		if i >= maxShow {
			lines = append(lines, labelLine{
				text:  fmt.Sprintf("… and %d more", len(node.Children)-maxShow),
				color: colorMuted, size: sp,
			})
			break
		}
		label := child.Label
		if child.IsTerminal && child.FullString != "" {
			label = child.FullString
		} else if selectedPrefix != "" {
			label = selectedPrefix + child.Label
		}
		lines = append(lines, labelLine{
			text:  fmt.Sprintf("  %s (%d)", label, child.SubtreeCount),
			color: colorFg, size: sp,
		})
	}
	return a.renderLines(gtx, lines)
}

// ── Right panel: media details (movie) ───────────────────────────────────────

func (a *App) renderMediaDetails(gtx C, media *MediaItem) D {
	movie, ok := a.client.getMovie(media.ID)
	if !ok {
		return a.renderText(gtx, colorMuted, "Movie details not available")
	}

	movieID := movie.ImdbID
	id := media.ID

	// Kick off async NFO fetch if not yet cached.
	nfoKey := "nfo:" + movieID
	a.nfos.fetch(nfoKey, func() (*polochon.Movie, error) {
		return a.client.fetchMovieNFO(movieID)
	}, a.w)

	// Merge NFO data with API data — NFO wins for text fields.
	// nfoData is nil until the fetch completes (or permanently nil on error).
	nfoData, _ := a.nfos.get(nfoKey)

	// Resolved display values: prefer NFO when available.
	plot := movie.Plot
	tagline := movie.Tagline
	runtime := movie.Runtime
	genres := movie.Genres
	if nfoData != nil {
		if nfoData.Plot != "" {
			plot = nfoData.Plot
		}
		if nfoData.Tagline != "" {
			tagline = nfoData.Tagline
		}
		if nfoData.Runtime != 0 {
			runtime = nfoData.Runtime
		}
		if len(nfoData.Genres) > 0 {
			genres = nfoData.Genres
		}
	}

	// Build the info-column sections (right side of the two-column layout).
	var sections []func(C) D

	// Title — large, bold
	sections = append(sections, func(gtx C) D {
		lbl := material.Label(a.th, spTitle, media.Title)
		lbl.Color = colorFg
		lbl.Font.Weight = font.Bold
		lbl.MaxLines = 0
		return lbl.Layout(gtx)
	})
	sections = append(sections, spacerFn(dp8))

	// Tagline — italic, muted, below title
	if tagline != "" {
		tl := tagline
		sections = append(sections, func(gtx C) D {
			lbl := material.Label(a.th, spBody, tl)
			lbl.Color = colorSubtle
			lbl.Font.Style = font.Italic
			lbl.MaxLines = 0
			return lbl.Layout(gtx)
		})
		sections = append(sections, spacerFn(dp8))
	}

	// Meta line: "1999  ·  2h 16m  ·  Action, Sci-Fi"
	if metaLine := buildMetaLine(movie.Year, runtime, genres); metaLine != "" {
		sections = append(sections, func(gtx C) D {
			lbl := material.Label(a.th, spMeta, metaLine)
			lbl.Color = colorSubtle
			return lbl.Layout(gtx)
		})
		sections = append(sections, spacerFn(dp12))
	}

	// Tech badges
	if badges := a.buildMovieBadges(movie); len(badges) > 0 {
		sections = append(sections, func(gtx C) D {
			return a.renderBadgeRow(gtx, badges)
		})
		sections = append(sections, spacerFn(dp20))
	}

	// Plot
	if plot != "" {
		p := plot
		sections = append(sections, func(gtx C) D {
			return a.renderBody(gtx, p)
		})
		sections = append(sections, spacerFn(dp16))
	}

	// Subtitles
	subText := formatSubtitles(movie.EmbeddedSubtitles, movie.Subtitles)
	sections = append(sections, func(gtx C) D {
		return a.renderBody(gtx, subText)
	})
	sections = append(sections, spacerFn(dp12))

	// Release group + date added (compact KV)
	if movie.ReleaseGroup != "" {
		rg := movie.ReleaseGroup
		sections = append(sections, func(gtx C) D {
			return a.renderKV(gtx, "Release", rg)
		})
	}
	if d := formatDateAdded(movie.DateAdded); d != "" {
		sections = append(sections, func(gtx C) D {
			return a.renderKV(gtx, "Added", d)
		})
	}

	// Player status
	sections = append(sections, spacerFn(dp12))
	sections = append(sections, func(gtx C) D {
		return a.renderPlayerStatus(gtx)
	})

	// Two-column layout: [poster | info] floating on a glass card over the fanart.
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx C) D {
			return a.renderFanartBg(gtx, movieID)
		}),
		layout.Stacked(func(gtx C) D {
			return layout.Inset{
				Top: dp24, Bottom: dp24, Left: dp24, Right: dp24,
			}.Layout(gtx, func(gtx C) D {
				return a.renderGlassCard(gtx, func(gtx C) D {
					return layout.Inset{
						Top: dp24, Bottom: dp24, Left: dp24, Right: dp24,
					}.Layout(gtx, func(gtx C) D {
						return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Start}.Layout(gtx,
							// Left column: poster
							layout.Rigid(func(gtx C) D {
								return a.renderThumbColumn(gtx, id)
							}),
							layout.Rigid(layout.Spacer{Width: dp24}.Layout),
							// Right column: scrollable info
							layout.Flexed(1, func(gtx C) D {
								return a.renderSections(gtx, sections)
							}),
						)
					})
				})
			})
		}),
	)
}

// buildMetaLine returns "1999  ·  2h 16m  ·  Action, Sci-Fi" from resolved values.
func buildMetaLine(year, runtime int, genres []string) string {
	var parts []string
	if year > 0 {
		parts = append(parts, fmt.Sprintf("%d", year))
	}
	if runtime > 0 {
		h, m := runtime/60, runtime%60
		if h > 0 {
			parts = append(parts, fmt.Sprintf("%dh %dm", h, m))
		} else {
			parts = append(parts, fmt.Sprintf("%dm", m))
		}
	}
	if g := formatGenres(genres); g != "" {
		parts = append(parts, g)
	}
	return strings.Join(parts, "  ·  ")
}

func (a *App) buildMovieBadges(movie *papi.Movie) []badge {
	var badges []badge
	if movie.Size > 0 {
		badges = append(badges, badge{text: humanize.Bytes(uint64(movie.Size)), bg: colorSizeBadgeBg})
	}
	if q := formatQualityBadge(movie.Quality); q != "" {
		badges = append(badges, badge{text: q, bg: colorQualBadgeBg})
	}
	if c := formatCodecs(movie.VideoCodec, movie.AudioCodec); c != "" {
		badges = append(badges, badge{text: c, bg: colorCodecBadgeBg})
	}
	if ct := formatContainer(movie.Container); ct != "" {
		badges = append(badges, badge{text: ct, bg: colorBadgeBg})
	}
	return badges
}

// renderThumb displays the movie poster thumbnail.
// If the image is cached it renders it immediately; otherwise it kicks off an
// async fetch and shows a muted placeholder rectangle in the meantime.
func (a *App) renderThumb(gtx C, movieID string) D {
	const maxHeightDp = unit.Dp(300)
	maxH := gtx.Dp(maxHeightDp)

	// Constrain height to maxH while keeping full available width.
	thumbConstraints := gtx.Constraints
	if thumbConstraints.Max.Y > maxH {
		thumbConstraints.Max.Y = maxH
	}
	thumbConstraints.Min = image.Point{}

	if img, ok := a.images.get("thumb:" + movieID); ok {
		if img == nil {
			// Fetch failed permanently — render nothing.
			return D{}
		}
		// Image is ready — render it.
		gtx2 := gtx
		gtx2.Constraints = thumbConstraints
		return widget.Image{
			Src:      paint.NewImageOp(img),
			Fit:      widget.Contain,
			Position: layout.NW,
		}.Layout(gtx2)
	}

	// Not cached yet — kick off fetch if not already in flight.
	a.images.fetch("thumb:"+movieID, func() (image.Image, error) {
		return a.client.fetchMovieImage(movieID, func(m *papi.Movie) *papi.File { return m.Thumb })
	}, a.w)

	// Placeholder: a muted-colour rectangle at the constrained size.
	w := thumbConstraints.Max.X
	h := maxH
	// Estimate poster size (2:3 ratio) so placeholder doesn't take full width.
	if w > h*2/3 {
		w = h * 2 / 3
	}
	sz := image.Pt(w, h)
	defer clip.Rect{Max: sz}.Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, colorMuted)
	return D{Size: sz}
}

// renderGlassCard draws a semi-transparent dark panel with rounded corners and
// a subtle white border, then renders content inside it. This gives a
// "frosted glass card" look floating over the fanart background.
func (a *App) renderGlassCard(gtx C, content layout.Widget) D {
	r := gtx.Dp(unit.Dp(12))
	border := gtx.Dp(dp1)
	bounds := image.Rectangle{Max: gtx.Constraints.Max}

	// Outer ring: border colour (~20% white).
	rrOuter := clip.RRect{
		Rect: bounds,
		NE:   r, NW: r, SE: r, SW: r,
	}
	defer rrOuter.Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x33})

	// Inner fill: ~80% opaque dark, lets just a whisper of fanart tint through.
	inner := image.Rectangle{
		Min: image.Pt(border, border),
		Max: image.Pt(bounds.Max.X-border, bounds.Max.Y-border),
	}
	ri := r - border
	if ri < 0 {
		ri = 0
	}
	rrInner := clip.RRect{
		Rect: inner,
		NE:   ri, NW: ri, SE: ri, SW: ri,
	}
	defer rrInner.Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, color.NRGBA{R: 0x12, G: 0x12, B: 0x12, A: 0xcc})

	return content(gtx)
}

// renderFanartBg fills the right panel with the movie's fanart image and a
// light dark tint. The glass card drawn on top provides contrast for text.
func (a *App) renderFanartBg(gtx C, movieID string) D {
	if img, ok := a.images.get("fanart:" + movieID); ok {
		if img != nil {
			// Draw fanart covering the full panel (cropped, cinema feel).
			widget.Image{
				Src:      paint.NewImageOp(img),
				Fit:      widget.Cover,
				Position: layout.Center,
			}.Layout(gtx)
			// Light tint (~25% black) to tone down very bright fanarts.
			defer clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops).Pop()
			paint.Fill(gtx.Ops, color.NRGBA{A: 0x40})
		}
		return D{Size: gtx.Constraints.Max}
	}

	// Not cached yet — kick off fetch if not already in flight.
	a.images.fetch("fanart:"+movieID, func() (image.Image, error) {
		return a.client.fetchMovieImage(movieID, func(m *papi.Movie) *papi.File { return m.Fanart })
	}, a.w)
	return D{Size: gtx.Constraints.Max}
}

// renderThumbColumn renders the movie poster in a fixed-width column (~200dp).
// The poster fills the column width and is constrained to a 2:3 aspect ratio height.
// Used as the left column in the two-column movie details layout.
func (a *App) renderThumbColumn(gtx C, movieID string) D {
	const colWidthDp = unit.Dp(200)
	colW := gtx.Dp(colWidthDp)
	colH := colW * 3 / 2 // 2:3 poster ratio

	gtx2 := gtx
	gtx2.Constraints = layout.Exact(image.Pt(colW, colH))

	if img, ok := a.images.get("thumb:" + movieID); ok {
		if img == nil {
			// Fetch failed permanently — render a translucent rounded placeholder.
			defer clip.RRect{
				Rect: image.Rectangle{Max: image.Pt(colW, colH)},
				NE:   gtx.Dp(unit.Dp(8)), NW: gtx.Dp(unit.Dp(8)),
				SE: gtx.Dp(unit.Dp(8)), SW: gtx.Dp(unit.Dp(8)),
			}.Push(gtx.Ops).Pop()
			paint.Fill(gtx.Ops, color.NRGBA{R: 0x33, G: 0x33, B: 0x33, A: 0x80})
			return D{Size: image.Pt(colW, colH)}
		}
		// Clip poster to rounded corners before drawing.
		defer clip.RRect{
			Rect: image.Rectangle{Max: image.Pt(colW, colH)},
			NE:   gtx.Dp(unit.Dp(8)), NW: gtx.Dp(unit.Dp(8)),
			SE: gtx.Dp(unit.Dp(8)), SW: gtx.Dp(unit.Dp(8)),
		}.Push(gtx.Ops).Pop()
		return widget.Image{
			Src:      paint.NewImageOp(img),
			Fit:      widget.Contain,
			Position: layout.N,
		}.Layout(gtx2)
	}

	// Not cached yet — kick off fetch.
	a.images.fetch("thumb:"+movieID, func() (image.Image, error) {
		return a.client.fetchMovieImage(movieID, func(m *papi.Movie) *papi.File { return m.Thumb })
	}, a.w)

	// Loading placeholder: dim rounded rect at poster proportions.
	defer clip.RRect{
		Rect: image.Rectangle{Max: image.Pt(colW, colH)},
		NE:   gtx.Dp(unit.Dp(8)), NW: gtx.Dp(unit.Dp(8)),
		SE: gtx.Dp(unit.Dp(8)), SW: gtx.Dp(unit.Dp(8)),
	}.Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, color.NRGBA{R: 0x33, G: 0x33, B: 0x33, A: 0x80})
	return D{Size: image.Pt(colW, colH)}
}

// ── Right panel: season details ───────────────────────────────────────────────

func (a *App) renderSeasonDetails(gtx C, season *DynamicNode) D {
	var lines []labelLine
	lines = append(lines, labelLine{text: season.Label, color: colorPrimary, size: unit.Sp(16), weight: font.Bold})
	lines = append(lines, labelLine{text: ""})
	lines = append(lines, labelLine{text: fmt.Sprintf("%d episodes", len(season.Children)), color: colorFg, size: sp})
	lines = append(lines, labelLine{text: ""})

	maxShow := 20
	for i, ep := range season.Children {
		if i >= maxShow {
			lines = append(lines, labelLine{
				text:  fmt.Sprintf("… and %d more", len(season.Children)-maxShow),
				color: colorMuted, size: sp,
			})
			break
		}
		lines = append(lines, labelLine{text: "  " + ep.Label, color: colorFg, size: sp})
	}
	return a.renderLines(gtx, lines)
}

// ── Right panel: episode details ─────────────────────────────────────────────

func (a *App) renderEpisodeDetails(gtx C, episodeNode *DynamicNode) D {
	ep := episodeNode.Episode
	if ep == nil {
		return a.renderText(gtx, colorMuted, "Episode details not available")
	}

	showTitle := ep.ShowTitle
	if showTitle == "" {
		showTitle = a.nav.dynamicTitle
	}

	var titleParts []string
	if showTitle != "" {
		titleParts = append(titleParts, showTitle)
	}
	if ep.Season > 0 {
		titleParts = append(titleParts, fmt.Sprintf("Season %d", ep.Season))
	}
	if ep.Episode > 0 {
		titleParts = append(titleParts, fmt.Sprintf("Episode %d", ep.Episode))
	}
	if len(titleParts) == 0 {
		titleParts = append(titleParts, episodeNode.Label)
	}
	title := strings.Join(titleParts, " · ")

	var sections []func(C) D

	sections = append(sections, func(gtx C) D {
		lbl := material.Label(a.th, unit.Sp(18), title)
		lbl.Color = colorFg
		lbl.Font.Weight = font.Bold
		return lbl.Layout(gtx)
	})
	sections = append(sections, spacerFn(dp8))

	// Badges
	if badges := a.buildEpisodeBadges(ep); len(badges) > 0 {
		sections = append(sections, func(gtx C) D {
			return a.renderBadgeRow(gtx, badges)
		})
		sections = append(sections, spacerFn(dp8))
	}

	if showTitle != "" {
		st := showTitle
		sections = append(sections, func(gtx C) D {
			return a.renderKV(gtx, "Show", st)
		})
	}
	if ep.Aired != "" {
		aired := ep.Aired
		sections = append(sections, func(gtx C) D {
			return a.renderKV(gtx, "Aired", aired)
		})
	}
	if ep.Runtime > 0 {
		rt := ep.Runtime
		sections = append(sections, func(gtx C) D {
			return a.renderKV(gtx, "Runtime", fmt.Sprintf("%d min", rt))
		})
	}
	if ep.ReleaseGroup != "" {
		rg := ep.ReleaseGroup
		sections = append(sections, func(gtx C) D {
			return a.renderKV(gtx, "Release", rg)
		})
	}
	if d := formatDateAdded(ep.DateAdded); d != "" {
		sections = append(sections, func(gtx C) D {
			return a.renderKV(gtx, "Added", d)
		})
	}

	subText := formatSubtitles(ep.EmbeddedSubtitles, ep.Subtitles)
	sections = append(sections, spacerFn(dp4))
	sections = append(sections, func(gtx C) D {
		return a.renderMultiline(gtx, subText)
	})

	if ep.Plot != "" {
		sections = append(sections, spacerFn(dp8))
		plot := ep.Plot
		sections = append(sections, func(gtx C) D {
			return a.renderMultiline(gtx, plot)
		})
	}

	sections = append(sections, spacerFn(dp8))
	sections = append(sections, func(gtx C) D {
		return a.renderPlayerStatus(gtx)
	})

	return a.renderSections(gtx, sections)
}

func (a *App) buildEpisodeBadges(ep *papi.Episode) []badge {
	var badges []badge
	if ep.Size > 0 {
		badges = append(badges, badge{text: humanize.Bytes(uint64(ep.Size)), bg: colorSizeBadgeBg})
	}
	if q := formatQualityBadge(ep.Quality); q != "" {
		badges = append(badges, badge{text: q, bg: colorQualBadgeBg})
	}
	if c := formatCodecs(ep.VideoCodec, ep.AudioCodec); c != "" {
		badges = append(badges, badge{text: c, bg: colorCodecBadgeBg})
	}
	if ct := formatContainer(ep.Container); ct != "" {
		badges = append(badges, badge{text: ct, bg: colorBadgeBg})
	}
	return badges
}

// ── Right panel: player status ────────────────────────────────────────────────

func (a *App) renderPlayerStatus(gtx C) D {
	status := a.playerStatus
	if status == "" {
		status = PlayerStatusIdle
	}

	var lines []labelLine
	lines = append(lines, labelLine{text: "Player", color: colorPrimary, size: unit.Sp(14), weight: font.Bold})
	lines = append(lines, labelLine{text: fmt.Sprintf("Status: %s", status), color: colorFg, size: sp})
	if a.playerMode != "" {
		lines = append(lines, labelLine{text: fmt.Sprintf("Mode: %s", a.playerMode), color: colorFg, size: sp})
	}
	if status == PlayerStatusError && a.playerError != "" {
		lines = append(lines, labelLine{text: "Error:", color: colorMuted, size: sp})
		lines = append(lines, labelLine{text: a.playerError, color: colorTerminal, size: sp})
	}
	if status == PlayerStatusRunning && a.playerCommand != "" {
		lines = append(lines, labelLine{text: "Command:", color: colorMuted, size: sp})
		lines = append(lines, labelLine{text: a.playerCommand, color: colorFg, size: sp})
	}
	return a.playerLinesList.Layout(gtx, len(lines), func(gtx C, i int) D {
		l := lines[i]
		if l.text == "" {
			return layout.Spacer{Height: dp4}.Layout(gtx)
		}
		sz := l.size
		if sz == 0 {
			sz = sp
		}
		col := l.color
		if col == (color.NRGBA{}) {
			col = colorFg
		}
		return layout.Inset{Bottom: unit.Dp(2)}.Layout(gtx, func(gtx C) D {
			lbl := material.Label(a.th, sz, l.text)
			lbl.Color = col
			if l.weight != 0 {
				lbl.Font.Weight = l.weight
			}
			lbl.MaxLines = 0
			lbl.Alignment = text.Start
			return lbl.Layout(gtx)
		})
	})
}

// ── Help bar ──────────────────────────────────────────────────────────────────

func (a *App) layoutHelp(gtx C) D {
	helpText := "hjkl/arrows: navigate  ·  enter/l: select  ·  h: back  ·  tab: toggle  ·  r: refresh  ·  q: quit"
	if a.refreshing {
		helpText += "  ·  refreshing…"
	}
	if a.playerStatus == PlayerStatusRunning {
		helpText += "  ·  player: running"
	}

	return layout.Inset{Left: dp8, Right: dp8, Top: dp4, Bottom: dp4}.Layout(gtx, func(gtx C) D {
		lbl := material.Label(a.th, unit.Sp(12), helpText)
		lbl.Color = colorMuted
		return lbl.Layout(gtx)
	})
}

// ── Rendering helpers ─────────────────────────────────────────────────────────

type labelLine struct {
	text   string
	color  color.NRGBA
	size   unit.Sp
	weight font.Weight
}

type badge struct {
	text string
	bg   color.NRGBA
}

// renderLines lays out a vertical list of label lines using layout.List (scrollable).
func (a *App) renderLines(gtx C, lines []labelLine) D {
	return a.rightList.Layout(gtx, len(lines), func(gtx C, i int) D {
		l := lines[i]
		if l.text == "" {
			return layout.Spacer{Height: dp4}.Layout(gtx)
		}
		sz := l.size
		if sz == 0 {
			sz = sp
		}
		col := l.color
		if col == (color.NRGBA{}) {
			col = colorFg
		}
		return layout.Inset{Bottom: unit.Dp(2)}.Layout(gtx, func(gtx C) D {
			lbl := material.Label(a.th, sz, l.text)
			lbl.Color = col
			if l.weight != 0 {
				lbl.Font.Weight = l.weight
			}
			lbl.MaxLines = 0
			lbl.Alignment = text.Start
			return lbl.Layout(gtx)
		})
	})
}

// renderSections renders a vertical list of section widgets.
func (a *App) renderSections(gtx C, sections []func(C) D) D {
	return a.rightList.Layout(gtx, len(sections), func(gtx C, i int) D {
		return sections[i](gtx)
	})
}

// renderText draws a single muted/coloured line.
func (a *App) renderText(gtx C, col color.NRGBA, s string) D {
	lbl := material.Label(a.th, sp, s)
	lbl.Color = col
	return lbl.Layout(gtx)
}

// renderKV renders "Key: Value" on one line.
func (a *App) renderKV(gtx C, k, v string) D {
	return layout.Flex{}.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			lbl := material.Label(a.th, sp, k+": ")
			lbl.Color = colorSubtle
			return lbl.Layout(gtx)
		}),
		layout.Rigid(func(gtx C) D {
			lbl := material.Label(a.th, sp, v)
			lbl.Color = colorFg
			return lbl.Layout(gtx)
		}),
	)
}

// renderMultiline renders a pre-formatted multi-line string as individual labels.
func (a *App) renderMultiline(gtx C, s string) D {
	lbl := material.Label(a.th, sp, s)
	lbl.Color = colorFg
	lbl.MaxLines = 0
	return lbl.Layout(gtx)
}

// renderBody renders body text at the larger spBody size for comfortable TV reading.
func (a *App) renderBody(gtx C, s string) D {
	lbl := material.Label(a.th, spBody, s)
	lbl.Color = colorFg
	lbl.MaxLines = 0
	return lbl.Layout(gtx)
}

// renderBadgeRow draws a horizontal row of coloured badge pills.
func (a *App) renderBadgeRow(gtx C, badges []badge) D {
	children := make([]layout.FlexChild, 0, len(badges)*2)
	for i, b := range badges {
		if i > 0 {
			children = append(children, layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout))
		}
		b := b // capture
		children = append(children, layout.Rigid(func(gtx C) D {
			return a.drawBadge(gtx, b)
		}))
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
}

func (a *App) drawBadge(gtx C, b badge) D {
	inset := layout.Inset{Left: dp8, Right: dp8, Top: dp4, Bottom: dp4}
	// Measure text first
	macro := op.Record(gtx.Ops)
	dims := inset.Layout(gtx, func(gtx C) D {
		lbl := material.Label(a.th, spBadge, b.text)
		lbl.Color = colorBadgeFg
		return lbl.Layout(gtx)
	})
	call := macro.Stop()

	// Draw background
	defer clip.RRect{
		Rect: image.Rectangle{Max: dims.Size},
		NE:   gtx.Dp(unit.Dp(4)), NW: gtx.Dp(unit.Dp(4)),
		SE: gtx.Dp(unit.Dp(4)), SW: gtx.Dp(unit.Dp(4)),
	}.Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, b.bg)

	// Replay text
	call.Add(gtx.Ops)
	return dims
}

// spacerFn returns a widget function that draws a vertical spacer.
func spacerFn(h unit.Dp) func(C) D {
	return func(gtx C) D {
		return layout.Spacer{Height: h}.Layout(gtx)
	}
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

func buildMPVArgs(title, videoURL string, subtitleURLs []string, mode PlayerMode) []string {
	args := []string{"mpv"}
	args = append(args, "--http-header-fields=X-Auth-Token: $POLOCHON_TOKEN")
	if mode == PlayerModeKMS {
		args = append(args, "--vo=gpu", "--gpu-context=drm", "--opengl-es=yes", "--fs", "--no-border", "--ontop")
	}
	if title != "" {
		args = append(args, "--title="+title)
	}
	for _, sub := range subtitleURLs {
		if sub != "" {
			args = append(args, "--sub-file="+sub)
		}
	}
	args = append(args, videoURL)
	return args
}

func addQueryParam(rawURL, k, v string) string {
	if v == "" {
		return rawURL
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	q := parsed.Query()
	q.Set(k, v)
	parsed.RawQuery = q.Encode()
	return parsed.String()
}

func quoteArg(arg string) string {
	if arg == "" {
		return `""`
	}
	if strings.ContainsAny(arg, " \t\n\"'\\") {
		return `"` + strings.ReplaceAll(arg, `"`, `\"`) + `"`
	}
	return arg
}

func buildMPVCommand(args []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = quoteArg(a)
	}
	return strings.Join(parts, " ")
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

func (a *App) buildMovieCommand(media *MediaItem) (string, []string, PlayerMode, bool) {
	movie, ok := a.client.getMovie(media.ID)
	if !ok {
		return "", nil, "", false
	}
	videoURL, err := a.client.getDownloadURL(movie)
	if err != nil {
		return "", nil, "", false
	}
	videoURL = addQueryParam(videoURL, "title", media.Title)

	var subs []string
	for _, sub := range movie.Subtitles {
		if sub == nil || sub.Embedded {
			continue
		}
		u, err := a.client.getDownloadURL(sub)
		if err != nil {
			continue
		}
		subs = append(subs, addQueryParam(u, "lang", string(sub.Lang)))
	}

	mode := detectPlayerMode()
	execArgs := buildMPVArgs(media.Title, videoURL, subs, mode)
	execArgs = replaceMpvToken(execArgs, a.client.getToken())
	return buildMPVCommand(execArgs), execArgs, mode, true
}

func (a *App) buildEpisodeCommand(node *DynamicNode) (string, []string, PlayerMode, bool) {
	if node == nil || node.Episode == nil {
		return "", nil, "", false
	}
	videoURL, err := a.client.getDownloadURL(node.Episode)
	if err != nil {
		return "", nil, "", false
	}
	videoURL = addQueryParam(videoURL, "title", node.Label)

	var subs []string
	for _, sub := range node.Episode.Subtitles {
		if sub == nil || sub.Embedded {
			continue
		}
		u, err := a.client.getDownloadURL(sub)
		if err != nil {
			continue
		}
		subs = append(subs, addQueryParam(u, "lang", string(sub.Lang)))
	}

	mode := detectPlayerMode()
	execArgs := buildMPVArgs(node.Label, videoURL, subs, mode)
	execArgs = replaceMpvToken(execArgs, a.client.getToken())
	return buildMPVCommand(execArgs), execArgs, mode, true
}
