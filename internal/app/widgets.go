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
	"github.com/odwrtw/polochon/lib/papi"
	"golang.org/x/image/draw"
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

// focusRing is inset so the outline remains within the control's bounds.
func focusRing(gtx C, size image.Point, radius unit.Dp) {
	r := gtx.Dp(radius)
	inset := gtx.Dp(dp2)
	rr := clip.RRect{Rect: image.Rect(inset, inset, size.X-inset, size.Y-inset), NE: r, NW: r, SE: r, SW: r}
	defer clip.Stroke{Path: rr.Path(gtx.Ops), Width: float32(gtx.Dp(dp2))}.Op().Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, colorSelectFg)
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

// detailScrim keeps a constant, contrast-safe surface under all text. The
// gradient starts beyond the reading edge, leaving exposed fanart on the right.
func detailScrim(gtx C, readingEdge int) {
	defer clip.Rect(image.Rectangle{Max: gtx.Constraints.Max}).Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, color.NRGBA{R: 0x08, G: 0x08, B: 0x0a, A: 0x40})
	clear := colorDetailPanel
	clear.A = 0
	paintGradient(gtx,
		f32.Pt(float32(readingEdge), 0), f32.Pt(float32(readingEdge+gtx.Dp(unit.Dp(320))), 0),
		colorDetailPanel, clear,
	)
	paintGradient(gtx,
		f32.Pt(0, 0), f32.Pt(0, float32(gtx.Constraints.Max.Y)),
		color.NRGBA{R: 0x08, G: 0x08, B: 0x0a, A: 0x00},
		color.NRGBA{R: 0x08, G: 0x08, B: 0x0a, A: 0x90},
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

// prose keeps editorial descriptions near 55–70 characters per line at the
// body type size, independently of title and technical-metadata panel widths.
func (a *App) prose(gtx C, s string) D {
	gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(unit.Dp(720)))
	gtx.Constraints.Min.X = min(gtx.Constraints.Min.X, gtx.Constraints.Max.X)
	return a.body(gtx, spBody, s, colorFg, 0)
}

// twoTone renders "prefix" in bold accent + "rest" in the given colour on one
// line — the radix prefix highlight used across nav rows and headers.
func (a *App) twoTone(gtx C, size unit.Sp, prefix, rest string, restCol color.NRGBA, prefixCol color.NRGBA) D {
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			return a.label(gtx, size, prefix, prefixCol, font.Bold)
		}),
		layout.Flexed(1, func(gtx C) D {
			return a.label(gtx, size, rest, restCol, 0)
		}),
	)
}

// ── Poster / fanart images ────────────────────────────────────────────────────

// Cache display-sized artwork; full-size posters can otherwise evict each
// other while still visible, triggering continuous refetch and redraw.
const (
	posterMaxWidth  = 800
	posterMaxHeight = 1200
	fanartMaxWidth  = 2560
	fanartMaxHeight = 1440
)

func fetchImageOp(fetchFn func() (image.Image, error), maxWidth, maxHeight int) (paint.ImageOp, error) {
	img, err := fetchFn()
	if err != nil {
		return paint.ImageOp{}, err
	}
	if img == nil {
		return paint.ImageOp{}, nil
	}
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width > maxWidth {
		height = max(1, height*maxWidth/width)
		width = maxWidth
	}
	if height > maxHeight {
		width = max(1, width*maxHeight/height)
		height = maxHeight
	}
	if width != bounds.Dx() || height != bounds.Dy() {
		resized := image.NewRGBA(image.Rect(0, 0, width, height))
		draw.ApproxBiLinear.Scale(resized, resized.Bounds(), img, bounds, draw.Src, nil)
		img = resized
	}
	return paint.NewImageOp(img), nil
}

// fetchArtwork keeps failures visible in the log even though the cache resolves
// them to empty images. Do not log request URLs, which may contain credentials.
func (a *App) fetchArtwork(cacheKey string, fetchFn func() (image.Image, error), maxWidth, maxHeight int) {
	a.images.fetch(cacheKey, func() (paint.ImageOp, error) {
		img, err := fetchImageOp(fetchFn, maxWidth, maxHeight)
		if err != nil && a.client != nil && a.client.log != nil {
			a.client.log.Warn("failed to load artwork", "key", cacheKey, "error", artworkLogReason(err))
		}
		return img, err
	})
}

// posterImage renders a 2:3 poster at the given width, asynchronously fetching
// it on first use. A placeholder surface is drawn while loading or on failure.
func (a *App) posterImage(gtx C, cacheKey string, fetchFn func() (image.Image, error), width, radius unit.Dp) D {
	w := gtx.Dp(width)
	size := image.Pt(w, w*3/2)

	gtx2 := gtx
	gtx2.Constraints = layout.Exact(size)

	img, ok := a.images.get(cacheKey)
	if !ok {
		a.fetchArtwork(cacheKey, fetchFn, posterMaxWidth, posterMaxHeight)
	}
	defer clip.RRect{
		Rect: image.Rectangle{Max: size},
		NE:   gtx.Dp(radius), NW: gtx.Dp(radius), SE: gtx.Dp(radius), SW: gtx.Dp(radius),
	}.Push(gtx.Ops).Pop()
	if ok && img.Size().X != 0 && img.Size().Y != 0 {
		return widget.Image{
			Src:      img,
			Fit:      widget.Cover,
			Position: layout.Center,
		}.Layout(gtx2)
	}
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
	a.fetchArtwork(cacheKey, fetchFn, fanartMaxWidth, fanartMaxHeight)
	return D{Size: gtx.Constraints.Max}
}

// Catalog artwork uses the same cache keys across browse, preview and detail.
func (a *App) movieThumb(gtx C, id string, width, radius unit.Dp) D {
	return a.posterImage(gtx, "thumb:"+id, func() (image.Image, error) {
		return a.client.fetchMovieImage(id, func(m *papi.Movie) *papi.File { return m.ThumbFile })
	}, width, radius)
}

func (a *App) showPoster(gtx C, id string, width, radius unit.Dp) D {
	return a.posterImage(gtx, "poster:"+id, func() (image.Image, error) {
		return a.client.fetchShowImage(id, func(s *papi.Show) *papi.File { return s.PosterFile })
	}, width, radius)
}

func (a *App) movieFanart(gtx C, id string) D {
	return a.fanart(gtx, "fanart:"+id, func() (image.Image, error) {
		return a.client.fetchMovieImage(id, func(m *papi.Movie) *papi.File { return m.FanartFile })
	})
}

func (a *App) showFanart(gtx C, id string) D {
	return a.fanart(gtx, "fanart:"+id, func() (image.Image, error) {
		return a.client.fetchShowImage(id, func(s *papi.Show) *papi.File { return s.FanartFile })
	})
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
		focused := primary && a.detail != nil && a.detail.kind == detailMovie
		if primary {
			bg = colorSelectBg
		}
		fillRounded(gtx, dims.Size, radiusRow, bg)
		if focused {
			focusRing(gtx, dims.Size, radiusRow)
		} else if !primary {
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
	color color.NRGBA // nonzero draws a remote bar; key remains its accessible name
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

// hintItem renders a keyboard keycap or remote colour bar beside its action.
func (a *App) hintItem(gtx C, h hint) D {
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			if h.color.A == 0 {
				return a.keycap(gtx, h.key)
			}
			size := gtx.Constraints.Constrain(image.Pt(gtx.Dp(unit.Dp(40)), gtx.Dp(dp8)))
			defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
			semantic.LabelOp(h.key).Add(gtx.Ops)
			fillRounded(gtx, size, dp2, h.color)
			return D{Size: size}
		}),
		layout.Rigid(layout.Spacer{Width: dp6}.Layout),
		layout.Rigid(func(gtx C) D {
			return a.label(gtx, spSmall, h.label, colorSubtle, 0)
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
		return layout.Inset{Bottom: dp2}.Layout(gtx, func(gtx C) D {
			lbl := material.Label(a.th, l.size, l.text)
			lbl.Color = l.color
			if l.weight != 0 {
				lbl.Font.Weight = l.weight
			}
			lbl.MaxLines = 0
			lbl.Alignment = text.Start
			return lbl.Layout(gtx)
		})
	})
}

// spacerFn returns a widget function that draws a vertical spacer.
func spacerFn(h unit.Dp) func(C) D {
	return func(gtx C) D {
		return layout.Spacer{Height: h}.Layout(gtx)
	}
}
