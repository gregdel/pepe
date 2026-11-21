package app

import (
	"image"
	"image/color"
	"image/draw"
	"testing"
	"time"

	"gioui.org/gpu/headless"
	"gioui.org/io/input"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/paint"
	"gioui.org/unit"
	polochon "github.com/odwrtw/polochon/lib"
	"github.com/odwrtw/polochon/lib/papi"
)

type detailFadeFrame struct {
	pixels *image.RGBA
	bounds map[string]image.Rectangle
	wakeup bool
}

// Unlike the interactive driver, this renderer never advances or refreshes the
// clock when reading pixels. Each frame uses exactly the supplied Gio time.
func renderDetailFadeFrame(t *testing.T, window *headless.Window, router *input.Router, now time.Time, widget layout.Widget, labels ...string) detailFadeFrame {
	t.Helper()
	var ops op.Ops
	gtx := C{
		Ops:         &ops,
		Source:      router.Source(),
		Now:         now,
		Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
		Constraints: layout.Exact(window.Size()),
	}
	widget(gtx)
	if err := window.Frame(&ops); err != nil {
		t.Fatalf("render detail frame: %v", err)
	}
	router.Frame(&ops)
	_, wakeup := router.WakeupTime()
	frame := detailFadeFrame{pixels: image.NewRGBA(image.Rectangle{Max: window.Size()}), wakeup: wakeup}
	if err := window.Screenshot(frame.pixels); err != nil {
		t.Fatalf("read detail frame: %v", err)
	}
	frame.bounds = make(map[string]image.Rectangle, len(labels))
	for _, node := range router.AppendSemantics(nil) {
		for _, label := range labels {
			if node.Desc.Label == label && frame.bounds[label].Empty() {
				frame.bounds[label] = node.Desc.Bounds.Intersect(frame.pixels.Bounds())
			}
		}
	}
	for _, label := range labels {
		if frame.bounds[label].Empty() {
			t.Fatalf("%q has no visible semantic bounds", label)
		}
	}
	return frame
}

func detailFadeLuminance(c color.RGBA) float64 {
	return linearLuminance(color.NRGBA(c))
}

func detailFadePixelNear(a, b color.RGBA, tolerance int) bool {
	diff := func(x, y uint8) int {
		d := int(x) - int(y)
		if d < 0 {
			return -d
		}
		return d
	}
	return diff(a.R, b.R) <= tolerance && diff(a.G, b.G) <= tolerance &&
		diff(a.B, b.B) <= tolerance && a.A == b.A
}

// Verify fully covered foreground pixels stay opaque even while the artwork
// beneath them changes. Semantic bounds keep the samples on the real control.
func assertDetailFadeForeground(t *testing.T, label string, col color.NRGBA, frames ...detailFadeFrame) {
	t.Helper()
	want := color.RGBA(col)
	first := frames[0]
	bounds := first.bounds[label]
	count := 0
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			p := first.pixels.RGBAAt(x, y)
			if !detailFadePixelNear(p, want, 1) {
				continue
			}
			count++
			for i, frame := range frames[1:] {
				if !image.Pt(x, y).In(frame.bounds[label]) || !detailFadePixelNear(frame.pixels.RGBAAt(x, y), p, 2) {
					t.Fatalf("%q pixel (%d,%d) changed from %v to %v in frame %d", label, x, y, p, frame.pixels.RGBAAt(x, y), i+1)
				}
			}
		}
	}
	if count < 10 {
		t.Fatalf("%q has only %d opaque foreground pixels in %v", label, count, bounds)
	}
}

func TestDetailArtworkEntranceFade(t *testing.T) {
	const movieID, showID = "fade-movie", "fade-show"
	for _, tc := range []struct {
		name, id, title, action, posterKey string
		kind                               MediaKind
		posterWidth                        unit.Dp
	}{
		{"movie", movieID, "LUMINOUS MOVIE", "Play movie", "thumb:" + movieID, mediaKindMovie, moviePosterWidth},
		{"show", showID, "LUMINOUS SHOW", "Season 1", "poster:" + showID, mediaKindShow, showPosterWidth},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := NewEmptyAPIClient(nil)
			client.movies[movieID] = &papi.Movie{Movie: &polochon.Movie{ImdbID: movieID, Title: "LUMINOUS MOVIE", Year: 2020}}
			show := &papi.Show{Show: &polochon.Show{ImdbID: showID, Title: "LUMINOUS SHOW", Year: 2020}}
			show.Seasons = map[int]*papi.Season{1: {Episodes: map[int]*papi.Episode{1: {
				ShowEpisode: &polochon.ShowEpisode{Season: 1, Episode: 1, Title: "Pilot"},
			}}}}
			client.shows[showID] = show
			a := New(client, nil, nil, nil, nil)
			t.Cleanup(a.Close)

			white := image.NewNRGBA(image.Rect(0, 0, 8, 12))
			draw.Draw(white, white.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
			art := paint.NewImageOp(white)
			a.backdrops.store("fanart:"+tc.id, backdrop{sharp: art})
			a.images.store(tc.posterKey, art)
			media := &MediaItem{ID: tc.id, Title: tc.title, Type: tc.kind}
			if tc.kind == mediaKindMovie {
				a.pushMovieDetail(media)
			} else {
				a.pushShowDetail(media)
			}
			if a.detail == nil {
				t.Fatal("fixture did not open detail")
			}
			entered := time.Unix(1_700_000_000, 0)
			a.detail.enteredAt = entered

			window, err := headless.NewWindow(1280, 720)
			if err != nil {
				t.Fatalf("create headless window: %v", err)
			}
			t.Cleanup(window.Release)
			var router input.Router
			drawDetail := func(gtx C) D {
				paint.Fill(gtx.Ops, colorBg)
				gtx = scaleContext(gtx)
				a.viewportInsets = layout.UniformInset(dp24)
				return a.layoutDetail(gtx)
			}
			frame := func(elapsed time.Duration) detailFadeFrame {
				return renderDetailFadeFrame(t, window, &router, entered.Add(elapsed), drawDetail, tc.title, tc.action)
			}

			before := frame(-detailFadeDuration)
			start := frame(0)
			middle := frame(detailFadeDuration / 2)
			end := frame(detailFadeDuration)
			after := frame(2 * detailFadeDuration)
			if !start.wakeup || !middle.wakeup || end.wakeup || after.wakeup {
				t.Errorf("fade redraws: start=%t middle=%t end=%t after=%t", start.wakeup, middle.wakeup, end.wakeup, after.wakeup)
			}
			assertDetailFadeForeground(t, tc.title, colorFg, start, middle, end)
			assertDetailFadeForeground(t, tc.action, colorSelectBg, start, middle, end)

			// Render the same entry frame with resolved, empty artwork. This
			// distinguishes zero opacity from a merely dim initial image.
			images, backdrops := a.images, a.backdrops
			a.images = newWeightedAsyncCache[paint.ImageOp](1, 0, imageCacheBudget, imageOpWeight)
			a.backdrops = newWeightedAsyncCache[backdrop](1, 0, backdropCacheBudget, backdropWeight)
			a.backdrops.store("fanart:"+tc.id, backdrop{})
			a.images.store(tc.posterKey, paint.ImageOp{})
			noArtwork := frame(0)
			a.images, a.backdrops = images, backdrops

			// Render the detail with explicit full artwork opacity as a
			// reference independent of layoutDetail's transition clock.
			fullyVisible := renderDetailFadeFrame(t, window, &router, entered, func(gtx C) D {
				paint.Fill(gtx.Ops, colorBg)
				gtx = scaleContext(gtx)
				a.viewportInsets = layout.UniformInset(dp24)
				if tc.kind == mediaKindMovie {
					return a.layoutMovieDetail(gtx, a.detail, 1)
				}
				return a.layoutShowDetail(gtx, a.detail.show, 1)
			}, tc.title, tc.action)

			// Both samples are away from controls and rounded poster corners.
			scale := float32(window.Size().X) / 1920
			poster := image.Pt(int(float32(dp24+tc.posterWidth/2)*scale), window.Size().Y/2)
			if tc.kind == mediaKindShow {
				poster.Y = int(float32(dp24+tc.posterWidth*3/4) * scale)
			}
			fanart := image.Pt(window.Size().X-2, window.Size().Y/4)
			// Sample the reading scrim outside the poster and any local
			// content surfaces, which may extend up to the top edge.
			protected := image.Pt(2, 2)
			for _, sample := range []struct {
				name   string
				point  image.Point
				minEnd float64
			}{
				{"fanart", fanart, 0.15},
				{"poster", poster, 0.6},
			} {
				p := sample.point
				beforePixel := before.pixels.RGBAAt(p.X, p.Y)
				startPixel := start.pixels.RGBAAt(p.X, p.Y)
				middlePixel := middle.pixels.RGBAAt(p.X, p.Y)
				endPixel := end.pixels.RGBAAt(p.X, p.Y)
				afterPixel := after.pixels.RGBAAt(p.X, p.Y)
				blank, full := noArtwork.pixels.RGBAAt(p.X, p.Y), fullyVisible.pixels.RGBAAt(p.X, p.Y)
				if !detailFadePixelNear(startPixel, blank, 2) || !detailFadePixelNear(endPixel, full, 2) {
					t.Errorf("%s endpoints: start=%v without artwork=%v end=%v fully visible=%v", sample.name, startPixel, blank, endPixel, full)
				}
				if !detailFadePixelNear(beforePixel, startPixel, 2) || !detailFadePixelNear(endPixel, afterPixel, 2) {
					t.Errorf("%s did not clamp outside the transition: before=%v start=%v end=%v after=%v", sample.name, beforePixel, startPixel, endPixel, afterPixel)
				}
				low, mid, high := detailFadeLuminance(startPixel), detailFadeLuminance(middlePixel), detailFadeLuminance(endPixel)
				if high-low < sample.minEnd {
					t.Fatalf("%s did not become visible: start=%v end=%v", sample.name, startPixel, endPixel)
				}
				if progress := (mid - low) / (high - low); progress < 0.4 || progress > 0.6 {
					t.Errorf("%s midpoint progress = %.3f, want approximately 0.5 (pixels %v, %v, %v)", sample.name, progress, startPixel, middlePixel, endPixel)
				}
			}

			if p := end.pixels.RGBAAt(poster.X, poster.Y); !detailFadePixelNear(p, color.RGBA{R: 255, G: 255, B: 255, A: 255}, 2) {
				t.Errorf("settled white poster = %v, want opaque white", p)
			}

			// Reference composition keeps the scrim at full opacity while only
			// its white fanart layer fades. The protected sample has no controls.
			for i, alpha := range []float32{0, 0.5, 1} {
				reference := renderDetailFadeFrame(t, window, &router, entered, func(gtx C) D {
					paint.Fill(gtx.Ops, colorBg)
					gtx = scaleContext(gtx)
					stack := paint.PushOpacity(gtx.Ops, alpha)
					paint.Fill(gtx.Ops, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
					stack.Pop()
					// The sampled point is inside the full-strength reading area.
					detailScrim(gtx, gtx.Constraints.Max.X)
					return D{Size: gtx.Constraints.Max}
				})
				actual := []detailFadeFrame{start, middle, end}[i].pixels.RGBAAt(protected.X, protected.Y)
				want := reference.pixels.RGBAAt(protected.X, protected.Y)
				if !detailFadePixelNear(actual, want, 2) {
					t.Errorf("scrim at alpha %.1f = %v, want opaque-scrim composition %v", alpha, actual, want)
				}
			}
			bg := end.pixels.RGBAAt(protected.X, protected.Y)
			for _, fg := range []color.NRGBA{colorFg, colorSubtle, colorPrimary} {
				l1 := detailFadeLuminance(color.RGBA(fg))
				l2 := detailFadeLuminance(bg)
				ratio := (max(l1, l2) + 0.05) / (min(l1, l2) + 0.05)
				if ratio < 4.5 {
					t.Errorf("settled %v on composited background %v has %.2f:1 contrast, want 4.5:1", fg, bg, ratio)
				}
			}

			// Embedded callers may omit Gio's frame clock. They must still reach
			// the settled artwork rather than continuously invalidating.
			a.detail.enteredAt = time.Now().Add(-2 * detailFadeDuration)
			_ = renderDetailFadeFrame(t, window, &router, time.Time{}, drawDetail)
			noClock := renderDetailFadeFrame(t, window, &router, time.Time{}, drawDetail)
			if noClock.wakeup || !detailFadePixelNear(noClock.pixels.RGBAAt(fanart.X, fanart.Y), fullyVisible.pixels.RGBAAt(fanart.X, fanart.Y), 2) {
				t.Error("zero Gio frame time did not settle artwork")
			}
			a.detail.enteredAt = entered

			// Playback changes the detail layout, so compare frames with the
			// same player state rather than comparing them to the idle layout.
			a.playerStatus = PlayerStatusRunning
			runningStart := frame(0)
			runningAgain := frame(0)
			runningEnd := frame(detailFadeDuration)
			if runningAgain.wakeup || runningEnd.wakeup {
				t.Errorf("playback bypass requested fade redraws: start=%t end=%t", runningAgain.wakeup, runningEnd.wakeup)
			}
			assertDetailFadeForeground(t, tc.title, colorFg, runningStart, runningEnd)
			assertDetailFadeForeground(t, tc.action, colorSelectBg, runningStart, runningEnd)
			for _, sample := range []struct {
				name  string
				point image.Point
			}{{"fanart", fanart}, {"poster", poster}} {
				p := sample.point
				s, e := runningStart.pixels.RGBAAt(p.X, p.Y), runningEnd.pixels.RGBAAt(p.X, p.Y)
				full := fullyVisible.pixels.RGBAAt(p.X, p.Y)
				if !detailFadePixelNear(s, e, 2) || !detailFadePixelNear(s, full, 2) {
					t.Errorf("running %s did not bypass the fade: start=%v end=%v fully visible=%v", sample.name, s, e, full)
				}
			}
		})
	}
}
