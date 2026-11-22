package app

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"strings"

	"gioui.org/f32"
	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

// ── Browse screen ─────────────────────────────────────────────────────────────
//
// The catalog floats on the backdrop (backdrop.go): header, mode rail and nav
// list share the left column, the preview takes the rest. Rail pages keep a
// full-width header above the rail and the page.

func (a *App) layoutBrowse(gtx C) D {
	a.syncLogs()
	if !a.browse.onPage() {
		return a.layoutCatalog(gtx)
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(a.layoutHeader),
		layout.Rigid(hairline),
		layout.Flexed(1, func(gtx C) D {
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
				layout.Rigid(a.layoutModeRail),
				layout.Rigid(verticalHairline),
				layout.Flexed(1, func(gtx C) D {
					if a.browse.remote != nil {
						return a.layoutWebRemote(gtx)
					}
					if a.browse.logs != nil {
						return a.layoutLogs(gtx)
					}
					return a.layoutSettingsPage(gtx)
				}),
			)
		}),
	)
}

// layoutCatalog keeps the header, rail and list inside the backdrop veil's
// opaque left region; the preview covers the rest. The preview is laid out
// first: its text block places the scrim, which spans the whole window under
// the list too, so it has no edges over the full-bleed fanart.
func (a *App) layoutCatalog(gtx C) D {
	size := gtx.Constraints.Max
	rail := min(gtx.Dp(a.modeRailWidth()), size.X)
	left := int(catalogLeftWidth(float32(size.X), float32(rail)))

	a.browse.previewTextTop = -1
	pgtx := gtx
	pgtx.Constraints = layout.Exact(image.Pt(max(size.X-left, 0), size.Y))
	macro := op.Record(gtx.Ops)
	a.layoutPreview(pgtx)
	preview := macro.Stop()

	// Only a terminal preview shows the sharp fanart and the veil's fade,
	// which the selection fades out with; otherwise the preview's text starts
	// right of the list.
	a.browse.selectionReach = 0
	if a.browse.previewTextTop >= 0 {
		insetLeft := gtx.Dp(a.viewportInsets.Left)
		window := float32(size.X + insetLeft + gtx.Dp(a.viewportInsets.Right))
		veilClear := (veilFadeStart(catalogListEdge(a.browse.focus == browseFocusModeRail))+veilFadeWidth)*window - float32(insetLeft)
		a.browse.selectionReach = max(int(veilClear)-left, 0)
	}

	if top := a.browse.previewTextTop; top >= 0 {
		insets := a.viewportInsets
		area := image.Rect(-gtx.Dp(insets.Left), -gtx.Dp(insets.Top), size.X+gtx.Dp(insets.Right), size.Y)
		stack := op.Offset(area.Min).Push(gtx.Ops)
		sgtx := gtx
		sgtx.Constraints = layout.Exact(area.Size())
		scrim(sgtx, top-area.Min.Y)
		stack.Pop()
	}

	lgtx := gtx
	lgtx.Constraints = layout.Exact(image.Pt(left, size.Y))
	layout.Flex{Axis: layout.Vertical}.Layout(lgtx,
		layout.Rigid(a.layoutHeader),
		layout.Flexed(1, func(gtx C) D {
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
				layout.Rigid(a.layoutModeRail),
				layout.Flexed(1, func(gtx C) D {
					return layout.Inset{Top: dp8}.Layout(gtx, a.layoutNavList)
				}),
			)
		}),
	)
	stack := op.Offset(image.Pt(left, 0)).Push(gtx.Ops)
	preview.Add(gtx.Ops)
	stack.Pop()
	return D{Size: size}
}

func verticalHairline(gtx C) D {
	size := image.Point{X: gtx.Dp(1), Y: gtx.Constraints.Max.Y}
	defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, colorOutline)
	return D{Size: size}
}

// ── Mode rail ─────────────────────────────────────────────────────────────────

var (
	radixModeIcon     = mustIcon(icons.ActionList)
	dateAddedModeIcon = mustIcon(icons.ActionSchedule)
	webRemoteIcon     = mustIcon(icons.CommunicationPhoneLinkRing)
	logsIcon          = mustIcon(icons.ActionBugReport)
	settingsIcon      = mustIcon(icons.ActionSettings)
)

// railEntry is a mode rail row: the catalog listings, then pages that replace
// the catalog without changing its listing or navigation.
type railEntry int

const (
	railSmartList  = railEntry(ListingModeTitle)
	railLastAdded  = railEntry(ListingModeDateAdded)
	railWebRemote  = railLastAdded + 1
	railLogs       = railWebRemote + 1
	railSettings   = railLogs + 1
	railEntryCount = railSettings + 1
)

func (a *App) railOption() railEntry {
	switch {
	case a.browse.remote != nil:
		return railWebRemote
	case a.browse.logs != nil:
		return railLogs
	case a.browse.settings != nil:
		return railSettings
	}
	return railEntry(a.browse.library.mode)
}

func (a *App) selectRailOption(entry railEntry) {
	switch entry {
	case railWebRemote:
		a.openWebRemote()
	case railLogs:
		a.openLogs()
	case railSettings:
		a.openSettings()
	default:
		a.applyListingMode(ListingMode(entry))
	}
}

func railLabel(entry railEntry) string {
	switch entry {
	case railWebRemote:
		return "Web remote"
	case railLogs:
		return "Logs"
	case railSettings:
		return "Settings"
	}
	return listingModeLabel(ListingMode(entry))
}

func mustIcon(data []byte) *widget.Icon {
	icon, err := widget.NewIcon(data)
	if err != nil {
		panic(err)
	}
	return icon
}

// catalogLeftWidth is the width of the catalog's left column (rail, header
// and list) in a catalog of the given width; catalogListEdge shares it.
func catalogLeftWidth(width, rail float32) float32 {
	return rail + navFlex*(width-rail)
}

const modeRailExpandedWidth = unit.Dp(240)

// modeRailWidth widens the rail to show labels while it has focus.
func (a *App) modeRailWidth() unit.Dp {
	if a.browse.focus == browseFocusModeRail {
		return modeRailExpandedWidth
	}
	return modeRailWidth
}

// layoutModeRail draws no background: the window or the backdrop shows
// through.
func (a *App) layoutModeRail(gtx C) D {
	width := min(gtx.Dp(a.modeRailWidth()), gtx.Constraints.Max.X)
	gtx.Constraints.Min.X = width
	gtx.Constraints.Max.X = width
	gtx.Constraints.Min.Y = gtx.Constraints.Max.Y
	// Expanded, keep the selected pill clear of the divider.
	var inset layout.Inset
	if a.browse.focus == browseFocusModeRail {
		inset.Right = dp8
	}
	// The rail never scrolls; it shares the lists' focus motion through a
	// scratch list whose position nothing reads.
	focus := -1
	if a.browse.focus == browseFocusModeRail {
		focus = int(a.railOption())
	}
	m := &a.browse.railMotion
	var still layout.List
	if m.begin(gtx, &still, railMotionKey{}, focus, 0, int(railEntryCount), math.MaxInt32) {
		a.animating(gtx)
	}
	return inset.Layout(gtx, func(gtx C) D {
		return layout.Center.Layout(gtx, func(gtx C) D {
			var entries [railEntryCount]layout.FlexChild
			for entry := range railEntryCount {
				entries[entry] = layout.Rigid(func(gtx C) D {
					return focusListRow(gtx, layout.Vertical, m, int(entry), focus, rowFocus,
						func(gtx C, _ int) D { return a.layoutModeOption(gtx, entry) })
				})
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, entries[:]...)
		})
	})
}

const (
	modeIconSize = unit.Dp(36)
	// railRowInset is rowShell's total horizontal padding (outer + inner).
	railRowInset = 2 * (dp8 + dp12)
)

func (a *App) layoutModeOption(gtx C, mode railEntry) D {
	active := a.railOption() == mode
	focused := a.browse.focus == browseFocusModeRail && active
	icon := radixModeIcon
	switch mode {
	case railLastAdded:
		icon = dateAddedModeIcon
	case railWebRemote:
		icon = webRemoteIcon
	case railLogs:
		icon = logsIcon
	case railSettings:
		icon = settingsIcon
	}

	return a.clickableLayout(gtx, "mode:"+railLabel(mode), railLabel(mode), func(gtx C) D {
		return a.focusRowShell(gtx, focused, active, browseRowHeight, func(gtx C) D {
			iconColor := colorSubtle
			if active {
				iconColor = colorPrimary
			}
			if focused {
				iconColor = colorSelectFg
			}

			row := func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx C) D {
						size := gtx.Dp(modeIconSize)
						gtx.Constraints = layout.Exact(image.Pt(size, size))
						return icon.Layout(gtx, iconColor)
					}),
					layout.Rigid(func(gtx C) D {
						if a.browse.focus != browseFocusModeRail {
							return D{}
						}
						return layout.Inset{Left: dp12}.Layout(gtx, func(gtx C) D {
							return a.label(gtx, sp, railLabel(mode), iconColor, font.Medium)
						})
					}),
				)
			}
			if a.browse.focus != browseFocusModeRail {
				return layout.Center.Layout(gtx, row)
			}
			// Expanded: left-align so icons share an edge, offset to where
			// the collapsed rail centres them.
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.W.Layout(gtx, func(gtx C) D {
				return layout.Inset{Left: (modeRailWidth - railRowInset - modeIconSize) / 2}.Layout(gtx, row)
			})
		})
	}, func() {
		a.browse.focus = browseFocusModeRail
		a.selectRailOption(mode)
	})
}

// ── Header ────────────────────────────────────────────────────────────────────

func (a *App) layoutHeader(gtx C) D {
	return layout.Inset{Left: dp8, Right: dp8, Top: dp8, Bottom: dp8}.Layout(gtx, func(gtx C) D {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			// Active listing mode.
			layout.Rigid(func(gtx C) D {
				return a.label(gtx, spHero, railLabel(a.railOption()), colorPrimary, font.Bold)
			}),
			layout.Rigid(layout.Spacer{Width: dp16}.Layout),
			layout.Rigid(func(gtx C) D {
				size := image.Pt(gtx.Dp(unit.Dp(1)), gtx.Dp(dp32))
				defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
				paint.Fill(gtx.Ops, colorOutline)
				return D{Size: size}
			}),
			layout.Rigid(layout.Spacer{Width: dp16}.Layout),
			// Current section and accumulated title prefix.
			layout.Flexed(1, func(gtx C) D {
				return a.layoutSectionLabel(gtx)
			}),
			// Status + counts
			layout.Rigid(func(gtx C) D {
				return a.layoutHeaderStatus(gtx)
			}),
		)
	})
}

// layoutSectionLabel renders the current browse section in the header.
func (a *App) layoutSectionLabel(gtx C) D {
	if a.browse.remote != nil {
		status := "Unavailable"
		if len(a.browse.remote.urls) > 0 {
			status = "Scan to connect"
		}
		return a.label(gtx, spMeta, status, colorFg, font.Medium)
	}
	if a.browse.logs != nil {
		return a.label(gtx, spMeta, a.browse.logs.summary(), colorFg, font.Medium)
	}
	if a.browse.settings != nil {
		return a.label(gtx, spMeta, a.browse.settings.summary(), colorFg, font.Medium)
	}
	section := "Movies"
	if a.browse.nav.isAtRoot() {
		section = "Home"
	} else if a.browse.nav.section == SectionShows {
		section = "Shows"
	}
	if prefix := a.browse.nav.getCurrentPath(); prefix != "" && !a.browse.nav.isAtRoot() {
		section += " / " + prefix
	}
	return a.label(gtx, spMeta, section, colorFg, font.Medium)
}

func (a *App) layoutHeaderStatus(gtx C) D {
	var parts []layout.FlexChild

	if a.refreshing {
		parts = append(parts, layout.Rigid(func(gtx C) D {
			return a.label(gtx, spTiny, "Refreshing…", colorPrimary, font.Medium)
		}))
		parts = append(parts, layout.Rigid(layout.Spacer{Width: dp16}.Layout))
	} else if a.refreshError != nil {
		status := "Refresh failed"
		if a.loadPending {
			status = "Server unreachable · retrying"
		}
		parts = append(parts, layout.Rigid(func(gtx C) D {
			return a.label(gtx, spTiny, status, colorError, font.Medium)
		}))
		parts = append(parts, layout.Rigid(layout.Spacer{Width: dp16}.Layout))
	}
	parts = append(parts, layout.Rigid(func(gtx C) D {
		return a.label(gtx, spTiny,
			fmt.Sprintf("%d movies · %d shows", a.browse.library.movieCount, a.browse.library.showCount),
			colorSubtle, 0)
	}))

	return layout.Flex{Alignment: layout.Middle}.Layout(gtx, parts...)
}

func listingModeLabel(mode ListingMode) string {
	if mode == ListingModeDateAdded {
		return "Last added"
	}
	return "Smart list"
}

// ── Nav panel ─────────────────────────────────────────────────────────────────

// railMotionKey identifies the mode rail's focus motion; its entries never
// change.
type railMotionKey struct{}

// navMotionKey identifies the nav list's rows and width; the list snaps
// instead of gliding when they change (drill-in, back, refresh).
type navMotionKey struct {
	node *RadixNode // nil at home
	mode ListingMode
}

func (a *App) layoutNavList(gtx C) D {
	var current *RadixNode
	if !a.browse.nav.isAtRoot() {
		if current = a.browse.nav.getCurrentNode(); current == nil {
			return D{}
		}
	}
	key := navMotionKey{node: current, mode: a.browse.library.mode}
	focused := a.browse.focus == browseFocusContent

	// The list is widened by selectionReach so the selection can fade out
	// with the backdrop veil; rows keep the list's width.
	width, reach := gtx.Constraints.Max.X, a.browse.selectionReach
	gtx.Constraints.Max.X += reach
	gtx.Constraints.Min.X = min(gtx.Constraints.Min.X+reach, gtx.Constraints.Max.X)
	layoutRows := func(n int, row func(gtx C, i int) D) D {
		// The cursor stays centred (like scrolloff=999).
		dims := a.focusList(gtx, &a.leftList, &a.browse.navMotion, key, n, a.browse.nav.cursor, focused,
			focusStyle{
				paint:  func(gtx C, _ int, size image.Point) { paintCatalogSelection(gtx, size, reach) },
				centre: true,
				still:  true, // the fade's segments would show seams when scaled
			},
			func(gtx C, i int) D {
				gtx.Constraints.Max.X = width
				gtx.Constraints.Min.X = min(gtx.Constraints.Min.X, width)
				return row(gtx, i)
			})
		dims.Size.X = min(dims.Size.X, width)
		return dims
	}

	if a.browse.nav.isAtRoot() {
		items := []struct {
			name  string
			count int
		}{
			{"Movies", a.browse.library.movieCount},
			{"Shows", a.browse.library.showCount},
		}
		return layoutRows(len(items), func(gtx C, i int) D {
			selected := a.browse.focus == browseFocusContent && i == a.browse.nav.cursor
			item := items[i]
			return a.clickableLayout(gtx, "section:"+item.name, item.name, func(gtx C) D {
				return a.groupRow(gtx, "", item.name, item.count, selected)
			}, func() {
				a.browse.focus = browseFocusContent
				a.browse.nav.cursor = i
				section := SectionMovies
				if i == int(SectionShows) {
					section = SectionShows
				}
				a.browse.nav.resetToSection(section, a.sectionRoot(section))
			})
		})
	}

	currentPath := a.browse.nav.getCurrentPath()
	children := current.Children
	return layoutRows(len(children), func(gtx C, i int) D {
		node := children[i]
		selected := a.browse.focus == browseFocusContent && i == a.browse.nav.cursor
		label := node.Label
		if currentPath != "" {
			label = currentPath + node.Label
		}
		return a.clickableLayout(gtx, fmt.Sprintf("nav:%p", node), label, func(gtx C) D {
			if node.IsTerminal && node.SubtreeCount == 1 {
				return a.terminalRow(gtx, node, currentPath, selected)
			}
			return a.groupRow(gtx, currentPath, label, node.SubtreeCount, selected)
		}, func() {
			a.browse.focus = browseFocusContent
			a.browse.nav.cursor = i
			a.navigateForward()
		})
	})
}

// focusRowShell lays out a focusable list row (DESIGN.md, Focus). A selected
// row draws no fill of its own: its list paints the focus fill (focusList),
// so the fill can glide between rows. A current row, focused elsewhere, gets
// the quiet surface.
func (a *App) focusRowShell(gtx C, selected, current bool, minHeight unit.Dp, content layout.Widget) D {
	fill := color.NRGBA{}
	if current && !selected {
		fill = colorSurface2
	}
	return a.rowFrame(gtx, minHeight, fill, selected, false, content)
}

// rowFrame lays out a row of at least minHeight on an optional fill, inset
// like the focus fill (paintFocusRow). A focused row is marked selected; ring
// outlines it, which only a field being edited uses.
func (a *App) rowFrame(gtx C, minHeight unit.Dp, fill color.NRGBA, focused, ring bool, content layout.Widget) D {
	return layout.Inset{Left: dp8, Right: dp8, Top: dp2, Bottom: dp2}.Layout(gtx, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		if h := gtx.Dp(minHeight); gtx.Constraints.Min.Y < h {
			gtx.Constraints.Min.Y = h
		}
		return a.focusFrame(gtx, focused, ring, fill, func(gtx C) D {
			return layout.Center.Layout(gtx, func(gtx C) D {
				return layout.Inset{Left: dp12, Right: dp12, Top: dp6, Bottom: dp6}.Layout(gtx, content)
			})
		})
	})
}

// paintFocusRow paints the focus fill behind a row of size laid out by
// rowFrame.
func paintFocusRow(gtx C, size image.Point) {
	inset, insetY := gtx.Dp(dp8), gtx.Dp(dp2)
	defer op.Offset(image.Pt(inset, insetY)).Push(gtx.Ops).Pop()
	fillRounded(gtx, image.Pt(size.X-2*inset, size.Y-2*insetY), radiusRow, colorSelectBg)
}

const (
	// The selection is solid up to selectionFadeStart of the row's width,
	// then fades out where the backdrop veil clears (selectionReach).
	selectionFadeStart    = 0.45
	selectionFadeSegments = 8
)

// paintCatalogSelection paints the selection of a catalog row of size, inset
// like rowFrame, fading out by the row's right edge, or reach past
// the list's.
func paintCatalogSelection(gtx C, size image.Point, reach int) {
	inset, insetY := gtx.Dp(dp8), gtx.Dp(dp2)
	r := gtx.Dp(radiusRow)
	right := size.X - inset
	if reach > 0 {
		right = size.X + reach
	}
	area := image.Rect(inset, insetY, right, size.Y-insetY)
	defer clip.RRect{Rect: area, NW: r, SW: r}.Push(gtx.Ops).Pop()
	defer op.Offset(area.Min).Push(gtx.Ops).Pop()
	x0 := int(selectionFadeStart * float32(size.X-2*inset))
	paintSelectionFade(gtx, area.Size(), x0, colorSelectBg)
}

// paintSelectionFade fills size with col, fading out horizontally from x0 to
// its right edge. Gio blends in linear light, where a fading colour over a
// dark background lingers and then drops; squaring the ramp's complement
// (gamma 2.2) makes the perceived fade even.
func paintSelectionFade(gtx C, size image.Point, x0 int, col color.NRGBA) {
	paint.FillShape(gtx.Ops, col, clip.Rect(image.Rect(0, 0, x0, size.Y)).Op())
	tint := func(i int) color.NRGBA {
		t := float64(i) / selectionFadeSegments
		c := col
		c.A = uint8(float64(col.A)*math.Pow(1-t, 2.2) + 0.5)
		return c
	}
	width := size.X - x0
	for i := range selectionFadeSegments {
		xa, xb := x0+width*i/selectionFadeSegments, x0+width*(i+1)/selectionFadeSegments
		if xb <= xa {
			continue
		}
		paintGradient(gtx, image.Rect(xa, 0, xb, size.Y),
			f32.Pt(float32(xa), 0), f32.Pt(float32(xb), 0), tint(i), tint(i+1))
	}
}

// groupRow renders a radix prefix group: two-tone label + count pill.
func (a *App) groupRow(gtx C, prefix, label string, count int, selected bool) D {
	return a.focusRowShell(gtx, selected, false, browseRowHeight, func(gtx C) D {
		fg := colorFg
		prefixCol := colorPrimary
		if selected {
			fg = colorSelectFg
			prefixCol = colorSelectFg
		}
		rest := label
		if prefix != "" && strings.HasPrefix(label, prefix) {
			rest = label[len(prefix):]
		} else {
			prefix = ""
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx C) D {
				return a.twoTone(gtx, spBrowse, prefix, rest, fg, prefixCol)
			}),
			layout.Rigid(func(gtx C) D {
				return a.countPill(gtx, count, selected)
			}),
			layout.Rigid(func(gtx C) D {
				return layout.Inset{Left: dp12}.Layout(gtx, func(gtx C) D {
					return a.label(gtx, spBrowse, "›", fg, font.Bold)
				})
			}),
		)
	})
}

// terminalRow renders a playable movie/show with a poster thumbnail.
func (a *App) terminalRow(gtx C, node *RadixNode, currentPath string, selected bool) D {
	const thumbW = unit.Dp(44)
	fullString := node.FullString

	return a.focusRowShell(gtx, selected, false, thumbW*3/2, func(gtx C) D {
		titleCol := colorFg
		if selected {
			titleCol = colorSelectFg
		}

		media := a.mediaForNode(node)

		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				if media == nil {
					return D{}
				}
				return a.rowPoster(gtx, media)
			}),
			layout.Rigid(func(gtx C) D {
				if media == nil {
					return D{}
				}
				return layout.Spacer{Width: dp12}.Layout(gtx)
			}),
			layout.Flexed(1, func(gtx C) D {
				if selected {
					return a.label(gtx, spBrowse, fullString, titleCol, font.Bold)
				}
				if currentPath == "" || !strings.HasPrefix(fullString, currentPath) {
					return a.label(gtx, spBrowse, fullString, titleCol, 0)
				}
				return a.twoTone(gtx, spBrowse, currentPath, fullString[len(currentPath):], titleCol, colorPrimary)
			}),
		)
	})
}

// rowPoster renders the small poster thumbnail for a terminal nav row.
func (a *App) rowPoster(gtx C, media *MediaItem) D {
	const thumbW = unit.Dp(44)
	if media.Type == mediaKindShow {
		return a.posterImage(gtx, "rowposter:"+media.ID, a.showPosterFile(media.ID), thumbW, radiusThumb, rowPosterMaxSize)
	}
	return a.posterImage(gtx, "rowthumb:"+media.ID, a.movieThumbFile(media.ID), thumbW, radiusThumb, rowPosterMaxSize)
}
