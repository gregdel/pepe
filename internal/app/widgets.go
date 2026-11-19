package app

import (
	"image"
	"image/color"
	"math"
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
	"golang.org/x/exp/shiny/materialdesign/icons"
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
// the same application actions used by keyboard navigation. The action is
// queued and runs before the next frame's layout, like key actions, so a frame
// is never drawn half with the old state and half with the new one.
func (a *App) clickableLayout(gtx C, id, label string, content layout.Widget, action func()) D {
	clickable := a.clickables[id]
	if clickable == nil {
		clickable = new(widget.Clickable)
		a.clickables[id] = clickable
	}
	if clickable.Clicked(gtx) && action != nil {
		a.pendingClick = action
		gtx.Execute(op.InvalidateCmd{})
	}
	return clickable.Layout(gtx, func(gtx C) D {
		semantic.Button.Add(gtx.Ops)
		semantic.LabelOp(label).Add(gtx.Ops)
		return content(gtx)
	})
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

const (
	// Gio blends in linear light: 0xe2 keeps colorFg at 4.5:1 even over white
	// artwork. Darker values hide the fanart without improving readability.
	scrimTextAlpha = 0xe2
	scrimTopAlpha  = 0x40
	scrimSegments  = 8
)

// scrim paints the browse preview gradient: an even band from just above
// textTop to the bottom, eased into the lightly tinted fanart above.
// Full-screen details use the independently tuned detailScrim.
func scrim(gtx C, textTop int) {
	size := gtx.Constraints.Max
	band := min(max(textTop-gtx.Dp(dp16), 0), size.Y)
	tint := func(a float32) color.NRGBA {
		return color.NRGBA{R: 0x08, G: 0x08, B: 0x0a, A: uint8(a + 0.5)}
	}
	paint.FillShape(gtx.Ops, tint(scrimTextAlpha), clip.Rect(image.Rect(0, band, size.X, size.Y)).Op())
	// 2t²−t⁴ keeps the upper fanart light and flattens out before the band,
	// so its edge does not show as a line.
	alpha := func(i int) float32 {
		t := float32(i) / scrimSegments
		return scrimTopAlpha + (scrimTextAlpha-scrimTopAlpha)*t*t*(2-t*t)
	}
	for i := range scrimSegments {
		y0, y1 := band*i/scrimSegments, band*(i+1)/scrimSegments
		if y1 <= y0 {
			continue
		}
		paintGradient(gtx, image.Rect(0, y0, size.X, y1),
			f32.Pt(0, float32(y0)), f32.Pt(0, float32(y1)),
			tint(alpha(i)), tint(alpha(i+1)))
	}
}

// detailScrim protects the full-height reading area and eases into the fanart
// beyond its edge, reaching fully clear at the window's right edge so the
// whole exposed width is used for the transition.
func detailScrim(gtx C, readingEdge int) {
	size := gtx.Constraints.Max
	readingEdge = min(max(readingEdge, 0), size.X)
	defer clip.Rect(image.Rectangle{Max: size}).Push(gtx.Ops).Pop()
	paint.FillShape(gtx.Ops, colorDetailPanel, clip.Rect(image.Rect(0, 0, readingEdge, size.Y)).Op())
	paintDetailFeather(gtx, readingEdge, size.X-readingEdge)
}

const detailFeatherSegments = 16

// paintDetailFeather eases from the reading surface at x0 to clear over width.
// Over white artwork its perceived (sRGB) brightness follows a smoothstep, so
// it leaves the flat surface and reaches the fanart with zero slope: a ramp
// starting at full slope reads as a hard edge. Alpha is solved per segment
// because Gio blends in linear light.
func paintDetailFeather(gtx C, x0, width int) {
	if width <= 0 {
		return
	}
	panel := linearLuminance(colorDetailPanel)
	surface := float64(colorDetailPanel.A) / 255
	start := linearToSRGB(surface*panel + 1 - surface)
	tint := func(i int) color.NRGBA {
		t := float64(i) / detailFeatherSegments
		v := start + (1-start)*t*t*(3-2*t)
		c := colorDetailPanel
		c.A = uint8(255*(1-srgbToLinear(v))/(1-panel) + 0.5)
		return c
	}
	size := gtx.Constraints.Max
	for i := range detailFeatherSegments {
		xa, xb := x0+width*i/detailFeatherSegments, x0+width*(i+1)/detailFeatherSegments
		if xb <= xa {
			continue
		}
		paintGradient(gtx, image.Rect(xa, 0, xb, size.Y),
			f32.Pt(float32(xa), 0), f32.Pt(float32(xb), 0), tint(i), tint(i+1))
	}
}

// linearLuminance is the relative luminance of c's colour, ignoring alpha.
func linearLuminance(c color.NRGBA) float64 {
	return 0.2126*srgbToLinear(float64(c.R)/255) + 0.7152*srgbToLinear(float64(c.G)/255) + 0.0722*srgbToLinear(float64(c.B)/255)
}

func srgbToLinear(v float64) float64 {
	if v <= 0.04045 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

func linearToSRGB(v float64) float64 {
	if v <= 0.0031308 {
		return v * 12.92
	}
	return 1.055*math.Pow(v, 1/2.4) - 0.055
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

// proseWidth keeps editorial descriptions near 55–70 characters per line at
// the body type size.
const proseWidth = unit.Dp(720)

// prose caps descriptions at proseWidth, independently of title and
// technical-metadata panel widths.
func (a *App) prose(gtx C, s string) D {
	gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(proseWidth))
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
	fanartMaxWidth  = 2560
	fanartMaxHeight = 1440
)

var (
	posterMaxSize = image.Pt(800, 1200)
	// Nav rows show 44dp posters: about 2× their size at 1080p, and 1:1 at
	// the largest UI scale. A separate small texture keeps the GPU from
	// minifying a full poster without mipmaps, which shimmers while scrolling.
	rowPosterMaxSize = image.Pt(96, 144)
)

func fetchImageOp(fetchFn func() (image.Image, error), maxWidth, maxHeight int) (paint.ImageOp, error) {
	img, err := fetchFn()
	if err != nil {
		return paint.ImageOp{}, err
	}
	if img == nil {
		return paint.ImageOp{}, nil
	}
	return paint.NewImageOp(resizeArtwork(img, maxWidth, maxHeight)), nil
}

// resizeArtwork shrinks img to fit within maxWidth×maxHeight, keeping its
// aspect ratio. Smaller images are returned unchanged.
func resizeArtwork(img image.Image, maxWidth, maxHeight int) image.Image {
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
	if width == bounds.Dx() && height == bounds.Dy() {
		return img
	}
	resized := image.NewRGBA(image.Rect(0, 0, width, height))
	// ApproxBiLinear samples only 4 source pixels and aliases beyond about
	// 2×; the kernel scaler widens its support when shrinking but costs
	// several times more CPU, so keep it for large reductions.
	var scaler draw.Scaler = draw.ApproxBiLinear
	if bounds.Dx() > 2*width {
		scaler = draw.BiLinear
	}
	scaler.Scale(resized, resized.Bounds(), img, bounds, draw.Src, nil)
	return resized
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
// maxSize bounds the cached texture; keys must differ between sizes.
func (a *App) posterImage(gtx C, cacheKey string, fetchFn func() (image.Image, error), width, radius unit.Dp, maxSize image.Point) D {
	w := gtx.Dp(width)
	size := image.Pt(w, w*3/2)

	gtx2 := gtx
	gtx2.Constraints = layout.Exact(size)

	img, ok := a.images.get(cacheKey)
	if !ok {
		a.fetchArtwork(cacheKey, fetchFn, maxSize.X, maxSize.Y)
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
	posterPlaceholder(gtx, size)
	return D{Size: size}
}

// posterPlaceholder marks where a poster belongs while it loads, or when the
// title has none; a large plain box reads as broken. Nav-row posters stay
// plain: an icon that small is only a smudge.
func posterPlaceholder(gtx C, size image.Point) {
	paint.Fill(gtx.Ops, colorSurface2)
	if size.X < gtx.Dp(posterPlaceholderMinWidth) {
		return
	}
	side := size.X / 3
	defer op.Offset(image.Pt((size.X-side)/2, (size.Y-side)/2)).Push(gtx.Ops).Pop()
	gtx.Constraints = layout.Exact(image.Pt(side, side))
	posterPlaceholderIcon.Layout(gtx, colorOutline)
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

// Preview and detail share full-size poster cache keys; nav rows use small
// "row" keys for the same files (see rowPosterMaxSize).
func (a *App) movieThumb(gtx C, id string, width, radius unit.Dp) D {
	return a.posterImage(gtx, "thumb:"+id, a.movieThumbFile(id), width, radius, posterMaxSize)
}

func (a *App) showPoster(gtx C, id string, width, radius unit.Dp) D {
	return a.posterImage(gtx, "poster:"+id, a.showPosterFile(id), width, radius, posterMaxSize)
}

func (a *App) movieThumbFile(id string) func() (image.Image, error) {
	return func() (image.Image, error) {
		return a.client.fetchMovieImage(id, func(m *papi.Movie) *papi.File { return m.ThumbFile })
	}
}

func (a *App) showPosterFile(id string) func() (image.Image, error) {
	return func() (image.Image, error) {
		return a.client.fetchShowImage(id, func(s *papi.Show) *papi.File { return s.PosterFile })
	}
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
// detailActionButton draws a pill button; icon, when set, precedes the text.
func (a *App) detailActionButton(gtx C, id, semanticLabel, text string, icon *widget.Icon, primary bool, action func()) D {
	return a.clickableLayout(gtx, id, semanticLabel, func(gtx C) D {
		inset := layout.Inset{Left: dp16, Right: dp16, Top: dp8, Bottom: dp8}
		macro := op.Record(gtx.Ops)
		dims := inset.Layout(gtx, func(gtx C) D {
			col := colorFg
			if primary {
				col = colorSelectFg
			}
			label := func(gtx C) D { return a.label(gtx, sp, text, col, font.Bold) }
			if icon == nil {
				return label(gtx)
			}
			measure := op.Record(gtx.Ops)
			line := label(gtx)
			measure.Stop()
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(inlineIcon(icon, line.Size.Y, line.Size.Y, col)),
				layout.Rigid(layout.Spacer{Width: dp6}.Layout),
				layout.Rigid(label),
			)
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

// Roboto, the only embedded font, has no arrows or play symbol, so these are
// drawn as icons.
var (
	keycapArrows = map[rune]*widget.Icon{
		'↑': mustIcon(icons.NavigationArrowUpward),
		'↓': mustIcon(icons.NavigationArrowDownward),
		'←': mustIcon(icons.NavigationArrowBack),
		'→': mustIcon(icons.NavigationArrowForward),
	}
	playIcon              = mustIcon(icons.AVPlayArrow)
	posterPlaceholderIcon = mustIcon(icons.AVMovie)
)

const posterPlaceholderMinWidth = unit.Dp(80)

// inlineIcon draws icon at most lineHeight tall, centred in a box exactly one
// text line high so it lines up with the labels beside it.
func inlineIcon(icon *widget.Icon, lineHeight, size int, col color.NRGBA) layout.Widget {
	return func(gtx C) D {
		defer op.Offset(image.Pt(0, (lineHeight-size)/2)).Push(gtx.Ops).Pop()
		gtx.Constraints = layout.Exact(image.Pt(size, size))
		icon.Layout(gtx, col)
		return D{Size: image.Pt(size, lineHeight)}
	}
}

// keycap renders a keyboard key as an outlined rounded cap, one Roboto line
// high whether it holds text, arrow icons or both. key is its accessible name.
func (a *App) keycap(gtx C, key string) D {
	gtx.Constraints.Min = image.Point{}
	measure := op.Record(gtx.Ops)
	line := a.label(gtx, spSmall, "M", colorFg, font.Medium)
	measure.Stop()

	var children []layout.FlexChild
	textStart := 0
	flush := func(end int) {
		if run := key[textStart:end]; run != "" {
			children = append(children, layout.Rigid(func(gtx C) D {
				return a.label(gtx, spSmall, run, colorFg, font.Medium)
			}))
		}
	}
	for i, r := range key {
		icon := keycapArrows[r]
		if icon == nil {
			continue
		}
		flush(i)
		textStart = i + len(string(r))
		children = append(children, layout.Rigid(inlineIcon(icon, line.Size.Y, line.Size.Y*4/5, colorFg)))
	}
	flush(len(key))

	inset := layout.Inset{Left: dp8, Right: dp8, Top: dp2, Bottom: dp2}
	macro := op.Record(gtx.Ops)
	dims := inset.Layout(gtx, func(gtx C) D {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
	})
	call := macro.Stop()

	defer clip.Rect{Max: dims.Size}.Push(gtx.Ops).Pop()
	semantic.LabelOp(key).Add(gtx.Ops)
	fillRounded(gtx, dims.Size, dp6, colorSurface2)
	strokeRounded(gtx, dims.Size, dp6, colorOutline)
	call.Add(gtx.Ops)
	return D{Size: dims.Size, Baseline: gtx.Dp(dp2) + line.Baseline}
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

// renderLines stacks a short, fixed set of label lines. It is not a scrollable
// list: inside another list it would get unbounded height and still capture
// wheel events. Lines past a bounded height are clipped by their labels.
func (a *App) renderLines(gtx C, lines []labelLine) D {
	children := make([]layout.FlexChild, len(lines))
	for i, l := range lines {
		children[i] = layout.Rigid(func(gtx C) D {
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
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

// spacerFn returns a widget function that draws a vertical spacer.
func spacerFn(h unit.Dp) func(C) D {
	return func(gtx C) D {
		return layout.Spacer{Height: h}.Layout(gtx)
	}
}
