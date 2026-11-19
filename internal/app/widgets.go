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

// keepSelectedVisible adjusts a list before layout, including partially clipped
// first and last rows. The caller supplies the constraints at its layout point.
func keepSelectedVisible(list *layout.List, cursor int, size image.Point, previousSize *image.Point) {
	pos := list.Position
	last := pos.First + pos.Count - 1
	if *previousSize != size || pos.Count == 0 || cursor < pos.First || (cursor == pos.First && pos.Offset > 0) {
		list.ScrollTo(cursor)
	} else if cursor > last || (cursor == last && pos.OffsetLast < 0) {
		first := cursor - pos.Count + 1
		if pos.OffsetLast < 0 {
			first++
		}
		list.ScrollTo(first)
	}
	*previousSize = size
}

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

// markSelected exposes D-pad focus to the semantic tree, so headless tools
// can find the focused control without a screenshot. Callers push it before
// painting the focused content, so that content becomes its semantic child, and
// pop it afterwards. A plain clip area adds no pointer handling; it clips to
// size, which callers measure from the content itself, so focused rows render
// as before (headless screenshots are pixel-identical without it). Under the
// subtitle popup the area is pushed unmarked, so the popup owns the selection.
func (a *App) markSelected(gtx C, size image.Point) clip.Stack {
	stack := clip.Rect{Max: size}.Push(gtx.Ops)
	if !a.selectionHidden {
		semantic.SelectedOp(true).Add(gtx.Ops)
	}
	return stack
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
	paintGradient(gtx, image.Rectangle{Max: gtx.Constraints.Max},
		f32.Pt(0, 0), f32.Pt(0, float32(gtx.Constraints.Max.Y)),
		color.NRGBA{R: 0x08, G: 0x08, B: 0x0a, A: 0x40},
		color.NRGBA{R: 0x08, G: 0x08, B: 0x0a, A: 0xee},
	)
}

func detailBackdrop(gtx C) {
	paint.Fill(gtx.Ops, color.NRGBA{R: 0x08, G: 0x08, B: 0x0a, A: 0x40})
	paintGradient(gtx, image.Rectangle{Max: gtx.Constraints.Max},
		f32.Pt(0, 0), f32.Pt(0, float32(gtx.Constraints.Max.Y)),
		color.NRGBA{R: 0x08, G: 0x08, B: 0x0a, A: 0x00},
		color.NRGBA{R: 0x08, G: 0x08, B: 0x0a, A: 0x60},
	)
}

// detailScrim protects the full-height reading area and eases into the fanart
// only beyond its edge. Keep the feather wide even when the viewport clips it.
func detailScrim(gtx C, readingEdge int) {
	size := gtx.Constraints.Max
	readingEdge = min(max(readingEdge, 0), size.X)
	defer clip.Rect(image.Rectangle{Max: size}).Push(gtx.Ops).Pop()
	detailBackdrop(gtx)
	paint.FillShape(gtx.Ops, colorDetailPanel, clip.Rect(image.Rect(0, 0, readingEdge, size.Y)).Op())
	fade := gtx.Dp(unit.Dp(720))
	if fade > 0 {
		paintDetailFeather(gtx, image.Rect(readingEdge, 0, readingEdge+fade, size.Y),
			f32.Pt(float32(readingEdge), 0), f32.Pt(float32(readingEdge+fade), 0))
	}
}

// Two identical feather layers give the opaque edge a gentler perceptual
// slope in linear-light compositing. Together their initial alpha matches the
// reading surface; neither layer extends into the text area by itself.
func paintDetailFeather(gtx C, bounds image.Rectangle, from, to f32.Point) {
	start := colorDetailPanel
	start.A = 0xdb
	clear := start
	clear.A = 0
	for range 2 {
		paintGradient(gtx, bounds, from, to, start, clear)
	}
}

// paintGradient fills bounds with a linear gradient between from and to.
func paintGradient(gtx C, bounds image.Rectangle, from, to f32.Point, fromColor, toColor color.NRGBA) {
	paint.LinearGradientOp{Stop1: from, Stop2: to, Color1: fromColor, Color2: toColor}.Add(gtx.Ops)
	stack := clip.Rect(bounds).Push(gtx.Ops)
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

// scheduleArtworkRetry redraws when a visible transient failure may be
// retried; nothing else would wake an idle TV UI to fetch it again.
func (a *App) scheduleArtworkRetry(gtx C, cacheKey string) {
	if at, ok := a.images.retryAt(cacheKey); ok {
		gtx.Execute(op.InvalidateCmd{At: at})
	}
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
	a.scheduleArtworkRetry(gtx, cacheKey)
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
		a.scheduleArtworkRetry(gtx, cacheKey)
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
		if focused {
			defer a.markSelected(gtx, dims.Size).Pop()
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
