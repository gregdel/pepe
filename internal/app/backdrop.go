package app

import (
	"image"
	"image/color"
	"math"
	"time"

	"gioui.org/f32"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"github.com/odwrtw/polochon/lib/papi"
	"golang.org/x/image/draw"
)

// ── Catalog backdrop ──────────────────────────────────────────────────────────
//
// The catalog draws the selected title's fanart full-bleed behind the list:
// the sharp image fills the full height right of the list, and a blurred,
// darkened "ambient" copy of it (the veil) takes over towards the list. The
// preview keeps its own scrim behind its text. Groups and the section root
// show only the ambient copy of a representative title, so the background
// stays calm while text covers it.
//
// The ambient layer never exceeds ambientMaxLuminance, so wherever the veil
// is opaque, text keeps WCAG AA contrast. The veil is opaque up to the list's
// right edge (catalogListEdge) and clears within veilFadeWidth, so as much
// artwork as possible stays visible; there is one veil per rail width.

const (
	// Window-relative geometry; the sharp fanart covers the box right of
	// backdropBoxLeft and above backdropBoxBottom, cropped to fit.
	backdropBoxLeft   = 0.40
	backdropBoxBottom = 1.0
	veilFadeWidth     = 0.05

	// The ambient canvas is tiny: it is heavily blurred, and the GPU's linear
	// filtering upscales it smoothly.
	ambientWidth      = 160
	ambientHeight     = 90
	ambientBlurRadius = 6
	ambientBlurPasses = 3
	// ambientMaxLuminance is the brightest linear luminance the ambient layer
	// may reach: colorPrimary, the dimmest essential browse text, keeps 4.5:1
	// over it (0.025 would do). Luminance rolls off towards it softly, with
	// ambientKnee setting how fast, so artwork keeps its variation instead of
	// flattening into a uniform grey; ambientEdgeDarken then takes the layer
	// towards black away from the sharp image, leaving a coloured glow beside
	// the artwork on a near-black screen.
	ambientMaxLuminance = 0.02
	ambientKnee         = 0.15
	ambientEdgeDarken   = 0.7

	backdropFade = 240 * time.Millisecond
)

// backdrop holds one title's fanart, its opaque ambient copy and the veils
// (ambient copy with the fade mask in its alpha channel), indexed by
// railExpanded.
type backdrop struct {
	sharp, ambient paint.ImageOp
	veils          [2]paint.ImageOp
}

func backdropWeight(b backdrop) int64 {
	return imageOpWeight(b.sharp) + imageOpWeight(b.ambient) + imageOpWeight(b.veils[0]) + imageOpWeight(b.veils[1])
}

// catalogListEdge is the window-relative right edge of the catalog's left
// column (header, rail and list) at the 1920dp reference width, inside the
// 24dp viewport insets. scaleContext clamps only very small or large
// windows, whose wider dp width moves the edge further left.
func catalogListEdge(railExpanded bool) float32 {
	const ref, inset = 1920, float32(dp24)
	rail := float32(modeRailWidth)
	if railExpanded {
		rail = float32(modeRailExpandedWidth)
	}
	return (inset + catalogLeftWidth(ref-2*inset, rail)) / ref
}

// backdropView is what the catalog backdrop shows: a fanart cache key, and
// whether the sharp image is shown or only its ambient copy.
type backdropView struct {
	key   string
	sharp bool
}

// backdropState holds the shown views and their artwork, so the backdrop
// does not depend on the cache: a refresh or an eviction does not blank it.
type backdropState struct {
	cur, prev       backdropView
	curArt, prevArt backdrop
	since           time.Time
}

// newBackdrop builds the display textures for one fanart image.
func newBackdrop(img image.Image) backdrop {
	ambient, veils := ambientImages(img)
	return backdrop{
		sharp:   paint.NewImageOp(resizeArtwork(img, fanartMaxWidth, fanartMaxHeight)),
		ambient: paint.NewImageOp(ambient),
		veils:   [2]paint.ImageOp{paint.NewImageOp(veils[0]), paint.NewImageOp(veils[1])},
	}
}

// ambientImages lays the image out on a small window-shaped canvas where the
// sharp box will be, extends its edges over the rest, blurs it and darkens
// it. The veils are the same canvas with each rail width's fade mask as
// alpha.
func ambientImages(img image.Image) (ambient *image.NRGBA, veils [2]*image.NRGBA) {
	const w, h = ambientWidth, ambientHeight
	boxLeft, boxBottom := float32(backdropBoxLeft), float32(backdropBoxBottom)
	boxX := int(boxLeft*w + 0.5)
	boxW, boxH := w-boxX, int(boxBottom*h+0.5)

	// Cover-crop the source to the box's aspect ratio, then downscale it with
	// a filter that averages every source pixel.
	src := img.Bounds()
	crop := src
	if src.Dx()*boxH > src.Dy()*boxW {
		cw := src.Dy() * boxW / boxH
		crop.Min.X += (src.Dx() - cw) / 2
		crop.Max.X = crop.Min.X + cw
	} else {
		ch := src.Dx() * boxH / boxW
		crop.Min.Y += (src.Dy() - ch) / 2
		crop.Max.Y = crop.Min.Y + ch
	}
	small := image.NewRGBA(image.Rect(0, 0, boxW, boxH))
	draw.BiLinear.Scale(small, small.Bounds(), img, crop, draw.Src, nil)

	// Linear-light canvas: the box holds the image, the rest extends its
	// nearest edge and eases towards its mean colour with distance, so the
	// extension does not streak.
	canvas := make([][3]float32, w*h)
	var mean [3]float32
	for y := range boxH {
		for x := range boxW {
			c := small.RGBAAt(x, y)
			px := [3]float32{srgb8ToLinear(c.R), srgb8ToLinear(c.G), srgb8ToLinear(c.B)}
			canvas[y*w+boxX+x] = px
			for i := range mean {
				mean[i] += px[i]
			}
		}
	}
	for i := range mean {
		mean[i] /= float32(boxW * boxH)
	}
	for y := range h {
		for x := range w {
			cx, cy := min(max(x, boxX), w-1), min(y, boxH-1)
			if cx == x && cy == y {
				continue
			}
			dist := max(float32(cx-x)/w, float32(y-cy)/h)
			t := smoothstep(0, 0.25, dist)
			edge := canvas[cy*w+cx]
			var px [3]float32
			for i := range px {
				px[i] = edge[i] + (mean[i]-edge[i])*t
			}
			canvas[y*w+x] = px
		}
	}
	for range ambientBlurPasses {
		boxBlur(canvas, w, h, ambientBlurRadius, 1, w)
		boxBlur(canvas, h, w, ambientBlurRadius, w, 1)
	}

	ambient = image.NewNRGBA(image.Rect(0, 0, w, h))
	edges := [2]float32{catalogListEdge(false), catalogListEdge(true)}
	for i := range veils {
		veils[i] = image.NewNRGBA(image.Rect(0, 0, w, h))
	}
	for y := range h {
		for x := range w {
			px := canvas[y*w+x]
			u, v := (float32(x)+0.5)/w, (float32(y)+0.5)/h
			if l := 0.2126*px[0] + 0.7152*px[1] + 0.0722*px[2]; l > 0 {
				// Away from the box, towards the left and bottom edges.
				dist := (boxLeft - u) / boxLeft
				if boxBottom < 1 {
					dist = max(dist, (v-boxBottom)/(1-boxBottom))
				}
				target := ambientMaxLuminance * float32(1-math.Exp(float64(-l/ambientKnee)))
				target *= 1 - ambientEdgeDarken*smoothstep(0, 1, dist)
				// target < l: ambientMaxLuminance/ambientKnee < 1, so no
				// channel grows past 1.
				for i := range px {
					px[i] *= target / l
				}
			}
			// Rounding down keeps the luminance under the limit.
			c := color.NRGBA{R: linearToSRGB8(px[0]), G: linearToSRGB8(px[1]), B: linearToSRGB8(px[2]), A: 0xff}
			ambient.SetNRGBA(x, y, c)
			for i, veil := range veils {
				c.A = veilAlpha(u, edges[i])
				veil.SetNRGBA(x, y, c)
			}
		}
	}
	return ambient, veils
}

// veilAlpha is the opacity at a window-relative texel centre of the veil
// that is opaque up to edge. The opaque region is widened by a texel and a
// half so the GPU's bilinear upscaling keeps it opaque up to edge. Gio blends
// in linear light, where a dark layer only reads as darker late in its ramp;
// the exponent makes the perceived fade follow the smoothstep.
func veilAlpha(u, edge float32) uint8 {
	start := edge + 1.5/ambientWidth
	s := 1 - smoothstep(start, start+veilFadeWidth, u)
	return uint8((1-math.Pow(float64(1-s), 2.2))*255 + 0.5)
}

func smoothstep(edge0, edge1, x float32) float32 {
	t := min(max((x-edge0)/(edge1-edge0), 0), 1)
	return t * t * (3 - 2*t)
}

// boxBlur blurs n lines of length samples each in place; stride steps along a
// line and lineStride between lines. Edges are clamped.
func boxBlur(px [][3]float32, length, n, radius, stride, lineStride int) {
	line := make([][3]float32, length)
	for l := range n {
		base := l * lineStride
		for i := range length {
			line[i] = px[base+i*stride]
		}
		var sum [3]float32
		for i := -radius; i <= radius; i++ {
			c := line[min(max(i, 0), length-1)]
			for k := range sum {
				sum[k] += c[k]
			}
		}
		scale := 1 / float32(2*radius+1)
		for i := range length {
			out := &px[base+i*stride]
			for k := range sum {
				out[k] = sum[k] * scale
			}
			add, drop := line[min(i+radius+1, length-1)], line[max(i-radius, 0)]
			for k := range sum {
				sum[k] += add[k] - drop[k]
			}
		}
	}
}

func srgb8ToLinear(v uint8) float32 { return float32(srgbToLinear(float64(v) / 255)) }

func linearToSRGB8(v float32) uint8 {
	return uint8(min(max(linearToSRGB(float64(v))*255, 0), 255))
}

// ── Fetching ──────────────────────────────────────────────────────────────────

// fetchBackdrop loads fanart for both the catalog backdrop and detail pages.
func (a *App) fetchBackdrop(key string, fetchFn func() (image.Image, error)) {
	a.backdrops.fetch(key, func() (backdrop, error) {
		img, err := fetchFn()
		if err != nil {
			a.logArtworkError(key, err)
			return backdrop{}, err
		}
		if img == nil {
			return backdrop{}, nil
		}
		return newBackdrop(img), nil
	})
}

// fanartSource returns the cache key and loader of a title's fanart.
func (a *App) fanartSource(media *MediaItem) (string, func() (image.Image, error)) {
	id := media.ID
	if media.Type == mediaKindShow {
		return "fanart:" + id, func() (image.Image, error) {
			return a.client.fetchShowImage(id, func(s *papi.Show) *papi.File { return s.FanartFile })
		}
	}
	return "fanart:" + id, func() (image.Image, error) {
		return a.client.fetchMovieImage(id, func(m *papi.Movie) *papi.File { return m.FanartFile })
	}
}

// ── Drawing ───────────────────────────────────────────────────────────────────

// wantedBackdrop picks the title behind the catalog: the selected title, or
// a representative of the selected group or section.
func (a *App) wantedBackdrop() (*MediaItem, bool) {
	nav := a.browse.nav
	if nav.isAtRoot() {
		section := SectionMovies
		if nav.cursor == int(SectionShows) {
			section = SectionShows
		}
		return a.firstMedia(a.sectionRoot(section)), false
	}
	child := nav.getSelectedChild()
	if child == nil {
		return nil, false
	}
	if child.IsTerminal && child.SubtreeCount == 1 {
		return a.mediaForNode(child), true
	}
	return a.firstMedia(child), false
}

// firstMedia follows the largest branches down to the first title.
func (a *App) firstMedia(node *RadixNode) *MediaItem {
	for node != nil {
		if node.IsTerminal {
			if media := a.mediaForNode(node); media != nil {
				return media
			}
		}
		if len(node.Children) == 0 {
			return nil
		}
		node = node.Children[0]
	}
	return nil
}

// layoutBackdrop fills the window behind the catalog. A new title, or new
// artwork for the shown one (refetched after a refresh), replaces the shown
// artwork once loaded, cross-fading, so neither fast scrolling nor a refresh
// flashes an empty background.
//
// Only a terminal preview draws a scrim behind its text, so sharp artwork is
// shown only while one is wanted: leaving it switches to the ambient copy at
// once, without waiting for the next artwork or fading over the text.
func (a *App) layoutBackdrop(gtx C) D {
	size := gtx.Constraints.Max
	st := &a.browse.backdrop

	view, art := st.cur, st.curArt
	if media, sharp := a.wantedBackdrop(); media != nil {
		key, fetchFn := a.fanartSource(media)
		if bd, ok := a.backdrops.get(key); ok {
			view, art = backdropView{key: key, sharp: sharp}, bd
		} else {
			a.fetchBackdrop(key, fetchFn)
			view.sharp = view.sharp && sharp
		}
		if at, ok := a.backdrops.retryAt(key); ok {
			gtx.Execute(op.InvalidateCmd{At: at})
		}
	} else {
		view, art = backdropView{}, backdrop{}
	}
	now := gtx.Now
	if now.IsZero() {
		now = time.Now()
	}
	if view != st.cur || art != st.curArt {
		instant := st.cur.sharp && !view.sharp
		st.prev, st.prevArt = st.cur, st.curArt
		st.cur, st.curArt = view, art
		st.since = now
		if instant {
			st.since = time.Time{}
		}
	}

	t := float32(1)
	if since := now.Sub(st.since); since < backdropFade {
		t = float32(since) / float32(backdropFade)
		gtx.Execute(op.InvalidateCmd{})
	}
	if t >= 1 {
		a.drawBackdrop(gtx, st.cur, st.curArt, size, 1)
		return D{Size: size}
	}
	// The incoming view is opaque wherever it has artwork; without artwork,
	// fade the outgoing one out instead.
	if a.drawBackdrop(gtx, st.cur, st.curArt, size, 0) {
		a.drawBackdrop(gtx, st.prev, st.prevArt, size, 1)
		a.drawBackdrop(gtx, st.cur, st.curArt, size, t)
	} else {
		a.drawBackdrop(gtx, st.prev, st.prevArt, size, 1-t)
	}
	return D{Size: size}
}

// drawBackdrop draws view's artwork bd at opacity alpha and reports whether
// it has any; alpha 0 only reports.
func (a *App) drawBackdrop(gtx C, view backdropView, bd backdrop, size image.Point, alpha float32) bool {
	if view.key == "" {
		return false
	}
	sharp := view.sharp && bd.sharp.Size().X > 0 && bd.sharp.Size().Y > 0
	if !sharp && bd.ambient.Size().X == 0 {
		return false
	}
	if alpha <= 0 {
		return true
	}
	if alpha < 1 {
		defer paint.PushOpacity(gtx.Ops, alpha).Pop()
	}
	full := image.Rectangle{Max: size}
	if sharp {
		box := image.Rect(int(backdropBoxLeft*float32(size.X)), 0, size.X, int(backdropBoxBottom*float32(size.Y)))
		drawImage(gtx, bd.sharp, box, true)
		veil := bd.veils[0]
		if a.browse.focus == browseFocusModeRail {
			veil = bd.veils[1]
		}
		drawImage(gtx, veil, full, false)
	} else {
		drawImage(gtx, bd.ambient, full, false)
	}
	return true
}

// drawImage stretches img over r, or covers r keeping its aspect ratio and
// centred. widget.Image rounds the image to whole pixels before scaling,
// which leaves a gap at the edge of a stretched small texture.
func drawImage(gtx C, img paint.ImageOp, r image.Rectangle, cover bool) {
	size := img.Size()
	if size.X <= 0 || size.Y <= 0 || r.Empty() {
		return
	}
	sx, sy := float32(r.Dx())/float32(size.X), float32(r.Dy())/float32(size.Y)
	offset := f32.Pt(float32(r.Min.X), float32(r.Min.Y))
	if cover {
		s := max(sx, sy)
		sx, sy = s, s
		offset.X += (float32(r.Dx()) - s*float32(size.X)) / 2
		offset.Y += (float32(r.Dy()) - s*float32(size.Y)) / 2
	}
	defer clip.Rect(r).Push(gtx.Ops).Pop()
	defer op.Affine(f32.AffineId().Scale(f32.Point{}, f32.Pt(sx, sy)).Offset(offset)).Push(gtx.Ops).Pop()
	img.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
}
