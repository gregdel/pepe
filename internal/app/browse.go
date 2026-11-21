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
	return inset.Layout(gtx, func(gtx C) D {
		return layout.Center.Layout(gtx, func(gtx C) D {
			var entries [railEntryCount]layout.FlexChild
			for entry := range railEntryCount {
				entries[entry] = layout.Rigid(func(gtx C) D { return a.layoutModeOption(gtx, entry) })
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
		return a.rowShell(gtx, focused, active, unit.Dp(64), func(gtx C) D {
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
			return a.label(gtx, spTiny, status, colorTerminal, font.Medium)
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

func (a *App) layoutNavList(gtx C) D {
	// Keep cursor centered in the visible area (like scrolloff=999).
	if pos := a.leftList.Position; pos.Count > 0 {
		target := max(0, a.browse.nav.cursor-pos.Count/2)
		if pos.First != target {
			a.leftList.ScrollTo(target)
		}
	}

	if a.browse.nav.isAtRoot() {
		items := []struct {
			name  string
			count int
		}{
			{"Movies", a.browse.library.movieCount},
			{"Shows", a.browse.library.showCount},
		}
		return a.leftList.Layout(gtx, len(items), func(gtx C, i int) D {
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

	current := a.browse.nav.getCurrentNode()
	if current == nil {
		return D{}
	}
	currentPath := a.browse.nav.getCurrentPath()
	children := current.Children
	return a.leftList.Layout(gtx, len(children), func(gtx C, i int) D {
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

// rowShell lays out a selectable row and its optional active-mode outline.
func (a *App) rowShell(gtx C, selected, active bool, minHeight unit.Dp, content layout.Widget) D {
	switch {
	case selected:
		return a.rowFrame(gtx, minHeight, colorSelectBg, true, content)
	case active:
		return a.rowFrame(gtx, minHeight, colorSurface2, false, content)
	}
	return a.rowFrame(gtx, minHeight, color.NRGBA{}, false, content)
}

// rowFrame lays out a row of at least minHeight on an optional fill; a
// focused row is marked selected and gets the focus ring.
func (a *App) rowFrame(gtx C, minHeight unit.Dp, fill color.NRGBA, focused bool, content layout.Widget) D {
	return layout.Inset{Left: dp8, Right: dp8, Top: dp2, Bottom: dp2}.Layout(gtx, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		if h := gtx.Dp(minHeight); gtx.Constraints.Min.Y < h {
			gtx.Constraints.Min.Y = h
		}
		return a.focusFrame(gtx, focused, fill, func(gtx C) D {
			return layout.Center.Layout(gtx, func(gtx C) D {
				return layout.Inset{Left: dp12, Right: dp12, Top: dp6, Bottom: dp6}.Layout(gtx, content)
			})
		})
	})
}

// catalogRowShell is rowShell for catalog list rows: the selection, without a
// focus ring, fades out to the right into the backdrop instead of ending on a
// hard edge.
func (a *App) catalogRowShell(gtx C, selected bool, minHeight unit.Dp, content layout.Widget) D {
	if !selected {
		return a.rowShell(gtx, false, false, minHeight, content)
	}
	return layout.Inset{Left: dp8, Right: dp8, Top: dp2, Bottom: dp2}.Layout(gtx, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		if h := gtx.Dp(minHeight); gtx.Constraints.Min.Y < h {
			gtx.Constraints.Min.Y = h
		}
		macro := op.Record(gtx.Ops)
		dims := layout.Center.Layout(gtx, func(gtx C) D {
			return layout.Inset{Left: dp12, Right: dp12, Top: dp6, Bottom: dp6}.Layout(gtx, content)
		})
		call := macro.Stop()

		defer a.markSelected(gtx, dims.Size).Pop()
		size := dims.Size
		r := gtx.Dp(radiusRow)
		rr := clip.RRect{Rect: image.Rectangle{Max: size}, NE: r, NW: r, SE: r, SW: r}
		fill := rr.Push(gtx.Ops)
		paintSelectionFade(gtx, size, colorSelectBg)
		fill.Pop()
		call.Add(gtx.Ops)
		return dims
	})
}

const (
	// The selection is solid up to selectionFadeStart of its width, then
	// fades out by its right edge.
	selectionFadeStart    = 0.45
	selectionFadeSegments = 8
)

// paintSelectionFade fills size with col fading out horizontally. Gio blends
// in linear light, where a fading colour over a dark background lingers and
// then drops; squaring the ramp's complement (gamma 2.2) makes the perceived
// fade even.
func paintSelectionFade(gtx C, size image.Point, col color.NRGBA) {
	x0 := int(selectionFadeStart * float32(size.X))
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
	return a.catalogRowShell(gtx, selected, browseRowHeight, func(gtx C) D {
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

	return a.catalogRowShell(gtx, selected, thumbW*3/2, func(gtx C) D {
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
