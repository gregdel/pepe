package app

import (
	"image"
	"image/color"
	"strconv"

	"gioui.org/f32"
	"gioui.org/font"
	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// ── Shared rendering primitives ───────────────────────────────────────────────

type (
	C = layout.Context
	D = layout.Dimensions
)

// clickableLayout adds a semantic button and routes pointer activation into
// the same application actions used by keyboard navigation.
func (a *App) clickableLayout(gtx C, id, label string, content layout.Widget, action func()) D {
	clickable := a.clickables[id]
	if clickable == nil {
		clickable = new(widget.Clickable)
		a.clickables[id] = clickable
	}
	clicked := clickable.Clicked(gtx)
	dims := clickable.Layout(gtx, func(gtx C) D {
		semantic.Button.Add(gtx.Ops)
		semantic.LabelOp(label).Add(gtx.Ops)
		return content(gtx)
	})
	if clicked && action != nil {
		action()
	}
	return dims
}

// fillRounded paints a rounded rectangle covering size.
func fillRounded(gtx C, size image.Point, radius unit.Dp, col color.NRGBA) {
	r := gtx.Dp(radius)
	defer clip.RRect{
		Rect: image.Rectangle{Max: size},
		NE:   r, NW: r, SE: r, SW: r,
	}.Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, col)
}

// strokeRounded paints a 1px rounded outline covering size.
func strokeRounded(gtx C, size image.Point, radius unit.Dp, col color.NRGBA) {
	r := gtx.Dp(radius)
	rr := clip.RRect{
		Rect: image.Rectangle{Max: size},
		NE:   r, NW: r, SE: r, SW: r,
	}
	defer clip.Stroke{Path: rr.Path(gtx.Ops), Width: float32(gtx.Dp(1))}.Op().Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, col)
}

// scrim paints the browse preview gradient. Full-screen details use the
// stronger, independently tuned detailScrim.
func scrim(gtx C) {
	paintGradient(gtx,
		f32.Pt(0, 0), f32.Pt(0, float32(gtx.Constraints.Max.Y)),
		color.NRGBA{R: 0x08, G: 0x08, B: 0x0a, A: 0x40},
		color.NRGBA{R: 0x08, G: 0x08, B: 0x0a, A: 0xee},
	)
}

// detailScrim preserves fanart as atmosphere while ensuring the areas around
// detail content start from a consistently dark foundation.
func detailScrim(gtx C) {
	defer clip.Rect(image.Rectangle{Max: gtx.Constraints.Max}).Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, color.NRGBA{R: 0x08, G: 0x08, B: 0x0a, A: 0x70})
	paintGradient(gtx,
		f32.Pt(0, 0), f32.Pt(float32(gtx.Constraints.Max.X), 0),
		color.NRGBA{R: 0x08, G: 0x08, B: 0x0a, A: 0xa8},
		color.NRGBA{R: 0x08, G: 0x08, B: 0x0a, A: 0x18},
	)
	paintGradient(gtx,
		f32.Pt(0, 0), f32.Pt(0, float32(gtx.Constraints.Max.Y)),
		color.NRGBA{R: 0x08, G: 0x08, B: 0x0a, A: 0x28},
		color.NRGBA{R: 0x08, G: 0x08, B: 0x0a, A: 0xd8},
	)
}

func paintGradient(gtx C, from, to f32.Point, fromColor, toColor color.NRGBA) {
	paint.LinearGradientOp{
		Stop1: from, Stop2: to,
		Color1: fromColor, Color2: toColor,
	}.Add(gtx.Ops)
	stack := clip.Rect(image.Rectangle{Max: gtx.Constraints.Max}).Push(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
	stack.Pop()
}

// detailPanel gives arbitrary artwork-backed content a predictable reading
// surface. The caller controls its outer size through the layout constraints.
func detailPanel(gtx C, inset layout.Inset, content layout.Widget) D {
	macro := op.Record(gtx.Ops)
	dims := inset.Layout(gtx, content)
	call := macro.Stop()
	fillRounded(gtx, dims.Size, radiusCard, colorDetailPanel)
	call.Add(gtx.Ops)
	return dims
}

// tvSafeHorizontalInset returns five percent of the viewport in density-
// independent units. scaleContext makes this approximately 96dp at 1080p.
func tvSafeHorizontalInset(gtx C) unit.Dp {
	return unit.Dp(float32(gtx.Constraints.Max.X) * 0.05 / gtx.Metric.PxPerDp)
}

func detailSafeInsets(gtx C) layout.Inset {
	horizontal := tvSafeHorizontalInset(gtx)
	vertical := unit.Dp(float32(gtx.Constraints.Max.Y) * 0.05 / gtx.Metric.PxPerDp)
	return layout.Inset{Top: vertical, Bottom: vertical, Left: horizontal, Right: horizontal}
}

// hairline paints a 1dp horizontal separator.
func hairline(gtx C) D {
	size := image.Point{X: gtx.Constraints.Max.X, Y: gtx.Dp(1)}
	defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, colorOutline)
	return D{Size: size}
}

// ── Text helpers ──────────────────────────────────────────────────────────────

// label renders a single styled text line.
func (a *App) label(gtx C, size unit.Sp, s string, col color.NRGBA, weight font.Weight) D {
	lbl := material.Label(a.th, size, s)
	lbl.Color = col
	if weight != 0 {
		lbl.Font.Weight = weight
	}
	lbl.MaxLines = 1
	return lbl.Layout(gtx)
}

// body renders multi-line body text.
func (a *App) body(gtx C, size unit.Sp, s string, col color.NRGBA, maxLines int) D {
	lbl := material.Label(a.th, size, s)
	lbl.Color = col
	lbl.MaxLines = maxLines
	lbl.Alignment = text.Start
	if size <= spBody {
		lbl.LineHeightScale = 1.4
	}
	return lbl.Layout(gtx)
}

// twoTone renders "prefix" in bold accent + "rest" in the given colour on one
// line — the radix prefix highlight used across nav rows and headers.
func (a *App) twoTone(gtx C, size unit.Sp, prefix, rest string, restCol color.NRGBA, prefixCol color.NRGBA) D {
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			return a.label(gtx, size, prefix, prefixCol, font.Bold)
		}),
		layout.Rigid(func(gtx C) D {
			return a.label(gtx, size, rest, restCol, 0)
		}),
	)
}

// ── Poster / fanart images ────────────────────────────────────────────────────

func fetchImageOp(fetchFn func() (image.Image, error)) (paint.ImageOp, error) {
	img, err := fetchFn()
	if err != nil {
		return paint.ImageOp{}, err
	}
	if img == nil {
		return paint.ImageOp{}, nil
	}
	return paint.NewImageOp(img), nil
}

// posterImage renders a 2:3 poster at the given width, asynchronously fetching
// it on first use. A placeholder surface is drawn while loading or on failure.
func (a *App) posterImage(gtx C, cacheKey string, fetchFn func() (image.Image, error), width, radius unit.Dp) D {
	w := gtx.Dp(width)
	size := image.Pt(w, w*3/2)

	gtx2 := gtx
	gtx2.Constraints = layout.Exact(size)

	if img, ok := a.images.get(cacheKey); ok {
		defer clip.RRect{
			Rect: image.Rectangle{Max: size},
			NE:   gtx.Dp(radius), NW: gtx.Dp(radius), SE: gtx.Dp(radius), SW: gtx.Dp(radius),
		}.Push(gtx.Ops).Pop()
		if img.Size().X == 0 || img.Size().Y == 0 {
			paint.Fill(gtx.Ops, colorSurface2)
			return D{Size: size}
		}
		return widget.Image{
			Src:      img,
			Fit:      widget.Cover,
			Position: layout.Center,
		}.Layout(gtx2)
	}

	a.images.fetch(cacheKey, func() (paint.ImageOp, error) { return fetchImageOp(fetchFn) })
	defer clip.RRect{
		Rect: image.Rectangle{Max: size},
		NE:   gtx.Dp(radius), NW: gtx.Dp(radius), SE: gtx.Dp(radius), SW: gtx.Dp(radius),
	}.Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, colorSurface2)
	return D{Size: size}
}

// fanart renders a full-bleed cover image filling the constraints.
func (a *App) fanart(gtx C, cacheKey string, fetchFn func() (image.Image, error)) D {
	if img, ok := a.images.get(cacheKey); ok {
		if img.Size().X != 0 && img.Size().Y != 0 {
			widget.Image{
				Src:      img,
				Fit:      widget.Cover,
				Position: layout.Center,
			}.Layout(gtx)
		}
		return D{Size: gtx.Constraints.Max}
	}
	a.images.fetch(cacheKey, func() (paint.ImageOp, error) { return fetchImageOp(fetchFn) })
	return D{Size: gtx.Constraints.Max}
}

// ── Count pill ────────────────────────────────────────────────────────────────

// countPill renders a muted rounded pill with a subtree count.
func (a *App) countPill(gtx C, n int, selected bool) D {
	inset := layout.Inset{Left: dp8, Right: dp8, Top: dp2, Bottom: dp2}
	macro := op.Record(gtx.Ops)
	dims := inset.Layout(gtx, func(gtx C) D {
		col := colorMuted
		if selected {
			col = colorSelectFg
		}
		return a.label(gtx, spTiny, strconv.Itoa(n), col, font.Medium)
	})
	call := macro.Stop()

	bg := colorSurface2
	if selected {
		bg = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x24}
	}
	fillRounded(gtx, dims.Size, dp12, bg)
	call.Add(gtx.Ops)
	return dims
}

// ── Detail actions ────────────────────────────────────────────────────────────

// detailActionButton exposes pointer and semantic activation without adding a
// D-pad focus zone; keyboard activation remains owned by the detail state.
func (a *App) detailActionButton(gtx C, id, semanticLabel, text string, primary bool, action func()) D {
	return a.clickableLayout(gtx, id, semanticLabel, func(gtx C) D {
		inset := layout.Inset{Left: dp16, Right: dp16, Top: dp8, Bottom: dp8}
		macro := op.Record(gtx.Ops)
		dims := inset.Layout(gtx, func(gtx C) D {
			col := colorFg
			if primary {
				col = colorSelectFg
			}
			return a.label(gtx, sp, text, col, font.Bold)
		})
		call := macro.Stop()

		bg := colorSurface2
		if primary {
			bg = colorSelectBg
		}
		fillRounded(gtx, dims.Size, radiusRow, bg)
		if !primary {
			strokeRounded(gtx, dims.Size, radiusRow, colorOutline)
		}
		call.Add(gtx.Ops)
		return dims
	}, action)
}

// ── Keycap hints ──────────────────────────────────────────────────────────────

type hint struct {
	key   string
	label string
}

// keycap renders a keyboard key as an outlined rounded cap.
func (a *App) keycap(gtx C, key string) D {
	inset := layout.Inset{Left: dp8, Right: dp8, Top: dp2, Bottom: dp2}
	macro := op.Record(gtx.Ops)
	dims := inset.Layout(gtx, func(gtx C) D {
		return a.label(gtx, spSmall, key, colorFg, font.Medium)
	})
	call := macro.Stop()

	fillRounded(gtx, dims.Size, dp6, colorSurface2)
	strokeRounded(gtx, dims.Size, dp6, colorOutline)
	call.Add(gtx.Ops)
	return dims
}

// hintItem renders "keycap + label" for the help bar.
func (a *App) hintItem(gtx C, h hint) D {
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			return a.keycap(gtx, h.key)
		}),
		layout.Rigid(layout.Spacer{Width: dp6}.Layout),
		layout.Rigid(func(gtx C) D {
			return a.label(gtx, spTiny, h.label, colorMuted, 0)
		}),
	)
}

// ── Legacy line/section list helpers ──────────────────────────────────────────

type labelLine struct {
	text   string
	color  color.NRGBA
	size   unit.Sp
	weight font.Weight
}

// renderLines lays out a vertical list of label lines using the given list.
func (a *App) renderLines(gtx C, list *layout.List, lines []labelLine) D {
	return list.Layout(gtx, len(lines), func(gtx C, i int) D {
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
		return layout.Inset{Bottom: dp2}.Layout(gtx, func(gtx C) D {
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

// renderKV renders "Key: Value" on one line.
func (a *App) renderKV(gtx C, k, v string) D {
	return layout.Flex{}.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			return a.label(gtx, sp, k+": ", colorSubtle, 0)
		}),
		layout.Rigid(func(gtx C) D {
			return a.label(gtx, sp, v, colorFg, 0)
		}),
	)
}

// spacerFn returns a widget function that draws a vertical spacer.
func spacerFn(h unit.Dp) func(C) D {
	return func(gtx C) D {
		return layout.Spacer{Height: h}.Layout(gtx)
	}
}
