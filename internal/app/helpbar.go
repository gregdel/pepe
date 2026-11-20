package app

import (
	"image"
	"image/color"
	"strings"

	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
)

// currentHints describes actions, independently of their remote presentation.
func (a *App) currentHints() []hint {
	if a.hideHelp {
		return nil
	}
	hints := a.contextHints()
	if a.controls != ControlsHDMICEC {
		return hints
	}
	remote := make([]hint, 0, len(hints)+1)
	colours := make([]hint, 0, 4)
	hasHome := false
	for _, h := range hints {
		switch h.key {
		case "q":
			continue // No remote Quit binding; TV power is not app power.
		case "s":
			h.key, h.color = "Blue", colorRemoteBlue
		case "r":
			h.key, h.color = "Green", colorRemoteGreen
		case "tab":
			h.key, h.color = "Yellow", colorRemoteYellow
		case "home":
			h.key, h.color = "Red", colorRemoteRed
			hasHome = true
		default:
			h.key = strings.NewReplacer("enter", "OK", "esc", "Back").Replace(h.key)
		}
		if h.color.A != 0 {
			colours = append(colours, h)
		} else {
			remote = append(remote, h)
		}
	}
	if !hasHome && a.subPopup == nil && (a.detail != nil || a.browse.focus == browseFocusModeRail || !a.browse.nav.isAtRoot()) {
		colours = append(colours, hint{key: "Red", label: "home", color: colorRemoteRed})
	}
	// Match the physical remote's colour row, after the navigation hints.
	for _, key := range []string{"Red", "Green", "Yellow", "Blue"} {
		for _, h := range colours {
			if h.key == key {
				remote = append(remote, h)
			}
		}
	}
	return remote
}

var (
	colorRemoteBlue   = color.NRGBA{R: 0x68, G: 0xb4, B: 0xff, A: 0xff}
	colorRemoteRed    = color.NRGBA{R: 0xff, G: 0x8a, B: 0x80, A: 0xff}
	colorRemoteGreen  = color.NRGBA{R: 0x81, G: 0xd7, B: 0x9b, A: 0xff}
	colorRemoteYellow = color.NRGBA{R: 0xff, G: 0xd8, B: 0x6b, A: 0xff}
)

func (a *App) contextHints() []hint {
	if a.setup != nil {
		return setupHints()
	}
	if p := a.subPopup; p != nil {
		var hints []hint
		if p.state == SubtitlePopupReady && len(p.entries) > 0 {
			if len(p.entries) > 1 {
				hints = append(hints, hint{key: "↑↓", label: "navigate"})
			}
			hints = append(hints, hint{key: "enter", label: "download"})
		} else if p.state == SubtitlePopupDone || p.state == SubtitlePopupError {
			hints = append(hints, hint{key: "enter", label: "close"})
		}
		return append(hints, hint{key: "esc", label: "close"})
	}

	if d := a.detail; d != nil {
		hints := []hint{{key: "↑↓", label: "scroll"}, {key: "enter", label: "play"}}
		if d.kind == detailShow {
			if d.show.focus == focusTabs {
				hints = []hint{{key: "←→", label: "seasons"}, {key: "↓/enter", label: "episodes"}}
			} else {
				hints = []hint{{key: "↑↓", label: "episodes"}}
				if d.show.currentEpisode() != nil {
					hints = append(hints, hint{key: "enter", label: "play episode"})
				}
			}
		}
		if a.currentVideo() != nil {
			hints = append(hints, hint{key: "s", label: "subtitles"})
		}
		// Back always leaves details, unlike Left on later season tabs.
		return append(hints, hint{key: "esc", label: "back"})
	}

	if a.browse.onPage() {
		hints := []hint{{key: "←", label: "menu"}, {key: "esc", label: "back"}, {key: "home", label: "browse"}}
		if a.browse.focus == browseFocusModeRail {
			hints = append([]hint{{key: "↑↓", label: "change mode"}, {key: "→/enter", label: "content"}}, hints[1:]...)
		} else if a.browse.remote != nil && len(a.browse.remote.urls) > 1 {
			hints = append([]hint{{key: "↑↓", label: "address"}}, hints...)
		} else if a.browse.logs != nil {
			hints = append([]hint{{key: "↑↓", label: "scroll"}}, hints...)
		}
		return append(hints, hint{key: "q", label: "quit"})
	}

	if a.browse.focus == browseFocusModeRail {
		return []hint{
			{key: "↑↓", label: "change mode"},
			{key: "→/enter/esc", label: "content"},
			{key: "r", label: "refresh"},
			{key: "q", label: "quit"},
		}
	}

	if a.browse.nav.isAtRoot() {
		return []hint{
			{key: "↑↓", label: "navigate"},
			{key: "→/enter", label: "browse"},
			{key: "←", label: "sort mode"},
			{key: "tab", label: "movies/shows"},
			{key: "r", label: "refresh"},
			{key: "q", label: "quit"},
		}
	}

	hints := []hint{
		{key: "↑↓", label: "navigate"},
		{key: "←", label: "back"},
		{key: "esc", label: "back"},
		{key: "tab", label: "section"},
	}
	if media := a.selectedBrowseMedia(); media != nil {
		hints = append(hints, hint{key: "→", label: "details"})
	} else if a.browse.nav.getChildrenCount() > 0 {
		hints = append(hints, hint{key: "→/enter", label: "browse"})
	}
	if a.currentVideo() != nil {
		hints = append(hints, hint{key: "s", label: "subtitles"})
	}
	return append(hints, hint{key: "r", label: "refresh"}, hint{key: "q", label: "quit"})
}

// Wrap whole hints and vertically centre them within each row. Render with
// measured constraints so semantic bounds fit the footer as well as the pixels.
func (a *App) layoutHelp(gtx C) D {
	hints := a.currentHints()
	if len(hints) == 0 {
		return D{}
	}
	inset := a.viewportInsets
	width := gtx.Constraints.Max.X
	available := max(0, width-gtx.Dp(inset.Left+inset.Right))
	type placedHint struct {
		hint   hint
		size   image.Point
		x, row int
	}
	items := make([]placedHint, 0, len(hints))
	rowHeights := []int{0}
	for _, h := range hints {
		item := op.Record(gtx.Ops)
		itemGtx := gtx
		itemGtx.Constraints.Min = image.Point{}
		itemGtx.Constraints.Max.X = available
		dims := a.hintItem(itemGtx, h)
		item.Stop()
		items = append(items, placedHint{hint: h, size: dims.Size})
	}
	x, row := 0, 0
	for i := range items {
		item := &items[i]
		gap := gtx.Dp(dp20)
		needed := item.size.X
		if item.hint.color.A != 0 && (i == 0 || items[i-1].hint.color.A == 0) {
			// Keep the colour group on one row when it fits, with a little
			// extra separation from the navigation hints.
			gap = gtx.Dp(dp32)
			groupWidth := 0
			for _, colour := range items[i:] {
				groupWidth += colour.size.X + gtx.Dp(dp20)
			}
			groupWidth -= gtx.Dp(dp20)
			if groupWidth <= available {
				needed = groupWidth
			}
		}
		if x > 0 {
			x += gap
			if x+needed > available {
				x, row = 0, row+1
				rowHeights = append(rowHeights, 0)
			}
		}
		item.x, item.row = x, row
		x += item.size.X
		rowHeights[row] = max(rowHeights[row], item.size.Y)
	}
	rowOffsets := make([]int, len(rowHeights))
	y := gtx.Dp(dp8)
	for i, height := range rowHeights {
		rowOffsets[i] = y
		y += height + gtx.Dp(dp8)
	}
	record := op.Record(gtx.Ops)
	for _, item := range items {
		y := rowOffsets[item.row] + (rowHeights[item.row]-item.size.Y)/2
		stack := op.Offset(image.Pt(gtx.Dp(inset.Left)+item.x, y)).Push(gtx.Ops)
		itemGtx := gtx
		itemGtx.Constraints.Min = image.Point{}
		itemGtx.Constraints.Max = item.size
		a.hintItem(itemGtx, item.hint)
		stack.Pop()
	}
	call := record.Stop()
	size := gtx.Constraints.Constrain(image.Pt(width, y-gtx.Dp(dp8)+gtx.Dp(inset.Bottom)))
	defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, colorBg)
	hairline(gtx)
	call.Add(gtx.Ops)
	return D{Size: size}
}
