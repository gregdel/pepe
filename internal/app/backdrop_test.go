package app

import (
	"image"
	"image/color"
	"image/draw"
	"strings"
	"testing"
	"time"

	"gioui.org/op/paint"
)

// storeArtwork seeds resolved artwork: fanart into the backdrop cache,
// posters into the image cache.
func storeArtwork(a *App, key string, img image.Image) {
	if strings.HasPrefix(key, "fanart:") {
		a.backdrops.store(key, newBackdrop(img, image.Point{}))
		return
	}
	a.images.store(key, paint.NewImageOp(img))
}

func uniformImage(w, h int, c color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.NewUniform(c), image.Point{}, draw.Src)
	return img
}

// The ambient layer stays dark enough for browse text whatever the artwork,
// and the veil is opaque wherever browse text may sit.
func TestAmbientLuminanceAndVeil(t *testing.T) {
	checker := image.NewNRGBA(image.Rect(0, 0, 97, 61))
	for y := range 61 {
		for x := range 97 {
			if (x/3+y/3)%2 == 0 {
				checker.SetNRGBA(x, y, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff})
			} else {
				checker.SetNRGBA(x, y, color.NRGBA{R: 0xff, B: 0xff, A: 0xff})
			}
		}
	}
	for name, img := range map[string]image.Image{
		"white":    uniformImage(64, 36, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}),
		"yellow":   uniformImage(64, 36, color.NRGBA{R: 0xff, G: 0xff, A: 0xff}),
		"blue":     uniformImage(64, 36, color.NRGBA{B: 0xff, A: 0xff}),
		"black":    uniformImage(64, 36, color.NRGBA{A: 0xff}),
		"checker":  checker,
		"pixel":    uniformImage(1, 1, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}),
		"portrait": uniformImage(30, 90, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}),
		"wide":     uniformImage(400, 10, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}),
	} {
		t.Run(name, func(t *testing.T) {
			ambient, veils := ambientImages(img)
			if ambient.Bounds().Size() != image.Pt(ambientWidth, ambientHeight) || veils[0].Bounds() != ambient.Bounds() || veils[1].Bounds() != ambient.Bounds() {
				t.Fatalf("ambient sizes = %v, %v, %v", ambient.Bounds(), veils[0].Bounds(), veils[1].Bounds())
			}
			for y := range ambientHeight {
				for x := range ambientWidth {
					c := ambient.NRGBAAt(x, y)
					if l := linearLuminance(c); l > ambientMaxLuminance || c.A != 0xff {
						t.Fatalf("ambient %d,%d = %v, luminance %.4f", x, y, c, l)
					}
					for i, veil := range veils {
						v := veil.NRGBAAt(x, y)
						if v.R != c.R || v.G != c.G || v.B != c.B {
							t.Fatalf("veil colour %v differs from ambient %v at %d,%d", v, c, x, y)
						}
						// Bilinear upscaling reads the next texel: it must
						// be opaque too for the list edge to hold.
						u := (float32(x) + 0.5) / ambientWidth
						if u <= catalogListEdge(i == 1)+1.0/ambientWidth && v.A != 0xff {
							t.Fatalf("veil %d texel %d,%d alpha = %d, want opaque", i, x, y, v.A)
						}
					}
				}
			}
			for _, veil := range veils {
				// The veil is clear a fade width past the list edge.
				x := int((catalogListEdge(true)+veilFadeWidth)*ambientWidth) + 2
				for _, y := range []int{0, ambientHeight - 1} {
					if a := veil.NRGBAAt(x, y).A; a != 0 {
						t.Fatalf("veil hides the sharp fanart: alpha = %d at %d,%d", a, x, y)
					}
				}
			}
		})
	}
}

func TestBrowseTextOverAmbient(t *testing.T) {
	v := uint8(linearToSRGB(ambientMaxLuminance)*255 + 1) // round up: worst case
	bg := color.NRGBA{R: v, G: v, B: v, A: 0xff}
	for _, fg := range []color.NRGBA{colorFg, colorSubtle, colorPrimary, colorError} {
		if got := contrastRatio(fg, bg); got < 4.5 {
			t.Fatalf("%v on brightest ambient %v = %.2f:1, want 4.5:1", fg, bg, got)
		}
	}
}

// Render the catalog backdrop over white fanart: the veil's opaque regions
// keep text contrast, and the sharp fanart remains visible top-right.
func TestCatalogBackdropRenderedContrast(t *testing.T) {
	item := newMovieListingItem(testMovie("tt001", "Bright", 2001))
	a := newModeTestApp([]*listingItem{item}, nil)
	a.backdrops = newWeightedAsyncCache[backdrop](1, 0, backdropCacheBudget, backdropWeight)
	storeArtwork(a, "fanart:tt001", uniformImage(64, 36, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}))

	for _, view := range []struct{ sharp, rail bool }{{false, false}, {true, false}, {true, true}} {
		sharp := view.sharp
		if sharp {
			a.browse.nav.resetToSection(SectionMovies, a.sectionRoot(SectionMovies))
		}
		if media, got := a.wantedBackdrop(); media == nil || media.ID != "tt001" || got != sharp {
			t.Fatalf("wanted backdrop = (%v, %t), want tt001 sharp=%t", media, got, sharp)
		}
		// Shown already, so no cross-fade is in progress.
		art, _ := a.backdrops.get("fanart:tt001")
		a.browse.backdrop = backdropState{cur: backdropView{key: "fanart:tt001", sharp: sharp}, curArt: art}
		a.browse.focus = browseFocusContent
		if view.rail {
			a.browse.focus = browseFocusModeRail
		}
		edge := catalogListEdge(view.rail)
		for _, size := range []image.Point{image.Pt(1920, 1080), image.Pt(1280, 800), image.Pt(1920, 700)} {
			img := renderDetailScrim(t, size, func(gtx C) { a.layoutBackdrop(gtx) })
			for y := 0; y < size.Y; y += 7 {
				for x := 0; x < size.X; x += 11 {
					u := float32(x) / float32(size.X)
					if !sharp || u <= edge {
						assertDetailContrast(t, img, image.Pt(x, y))
					}
				}
			}
			if sharp {
				for _, p := range []image.Point{image.Pt(size.X-2, 2), image.Pt(size.X-2, size.Y-2)} {
					c := color.NRGBAModel.Convert(img.At(p.X, p.Y)).(color.NRGBA)
					if linearLuminance(c) < 0.8 {
						t.Fatalf("%v: sharp fanart hidden at %v: %v", size, p, c)
					}
				}
			}
		}
	}
}

// blockBackdrop marks key as loading until the returned function releases
// it with art, so a test controls when artwork arrives.
func blockBackdrop(t *testing.T, a *App, key string, art backdrop) func() {
	t.Helper()
	release := make(chan struct{})
	a.backdrops.fetch(key, func() (backdrop, error) {
		<-release
		return art, nil
	})
	var released bool
	t.Cleanup(func() {
		if !released {
			close(release)
		}
	})
	return func() {
		released = true
		close(release)
	}
}

// renderBackdrop renders the backdrop on the window background, as Layout
// does.
func renderBackdrop(t *testing.T, a *App, size image.Point) image.Image {
	t.Helper()
	return renderDetailScrim(t, size, func(gtx C) {
		paint.Fill(gtx.Ops, colorBg)
		a.layoutBackdrop(gtx)
	})
}

// assertBackdropReadable checks that unscrimmed preview text would keep its
// contrast anywhere over the rendered backdrop.
func assertBackdropReadable(t *testing.T, img image.Image, size image.Point) {
	t.Helper()
	for y := 1; y < size.Y; y += 23 {
		for x := 1; x < size.X; x += 29 {
			assertDetailContrast(t, img, image.Pt(x, y))
		}
	}
}

// Group, section and media-less previews draw no scrim: leaving a sharp
// title for one must not keep its sharp fanart while the next artwork loads,
// nor fade it out under their text.
func TestBackdropNeverSharpWithoutScrim(t *testing.T) {
	size := image.Pt(1280, 720)
	white := uniformImage(64, 36, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff})
	setup := func(t *testing.T) (*App, *RadixNode, int, int) {
		t.Helper()
		a := newModeTestApp([]*listingItem{
			newMovieListingItem(testMovie("tt001", "Alpha", 2001)),
			newMovieListingItem(testMovie("tt002", "Alpine", 2002)),
			newMovieListingItem(testMovie("tt003", "Beta", 2003)),
		}, nil)
		a.backdrops = newWeightedAsyncCache[backdrop](2, 4, backdropCacheBudget, backdropWeight)
		storeArtwork(a, "fanart:tt003", white)
		root := a.sectionRoot(SectionMovies)
		group, title := -1, -1
		for i, child := range root.Children {
			if child.IsTerminal && child.SubtreeCount == 1 {
				title = i
			} else {
				group = i
			}
		}
		if group < 0 || title < 0 {
			t.Fatalf("fixture needs a group and a title: %+v", root.Children)
		}
		a.browse.nav.resetToSection(SectionMovies, root)
		a.browse.nav.cursor = title
		renderBackdrop(t, a, size)
		a.browse.backdrop.since = time.Time{} // fade done
		if cur := a.browse.backdrop.cur; cur != (backdropView{key: "fanart:tt003", sharp: true}) {
			t.Fatalf("title backdrop = %+v", cur)
		}
		return a, root, group, title
	}

	t.Run("group loading", func(t *testing.T) {
		a, root, group, _ := setup(t)
		next := a.firstMedia(root.Children[group])
		blockBackdrop(t, a, "fanart:"+next.ID, backdrop{})
		a.browse.nav.cursor = group
		img := renderBackdrop(t, a, size)
		st := a.browse.backdrop
		if st.cur != (backdropView{key: "fanart:tt003"}) || !st.since.IsZero() {
			t.Fatalf("backdrop while the group loads = %+v since %v, want the title's ambient copy at once", st.cur, st.since)
		}
		assertBackdropReadable(t, img, size)
	})
	t.Run("group loaded", func(t *testing.T) {
		a, root, group, _ := setup(t)
		storeArtwork(a, "fanart:"+a.firstMedia(root.Children[group]).ID, white)
		a.browse.nav.cursor = group
		img := renderBackdrop(t, a, size)
		if st := a.browse.backdrop; st.cur.sharp || !st.since.IsZero() {
			t.Fatalf("backdrop for a loaded group = %+v since %v, want an instant ambient copy", st.cur, st.since)
		}
		assertBackdropReadable(t, img, size)
	})
	t.Run("no media", func(t *testing.T) {
		a, _, _, _ := setup(t)
		// The shows section is empty: its root preview has no title.
		a.browse.nav = newNavigationState()
		a.browse.nav.cursor = int(SectionShows)
		img := renderBackdrop(t, a, size)
		if st := a.browse.backdrop; st.cur != (backdropView{}) || !st.since.IsZero() {
			t.Fatalf("backdrop without media = %+v since %v, want none at once", st.cur, st.since)
		}
		assertBackdropReadable(t, img, size)
	})
}

// A refresh resets the artwork cache; the shown fanart stays until it is
// fetched again, then any new artwork replaces it.
func TestBackdropSurvivesRefresh(t *testing.T) {
	size := image.Pt(1280, 720)
	a := newModeTestApp([]*listingItem{newMovieListingItem(testMovie("tt001", "Bright", 2001))}, nil)
	a.backdrops = newWeightedAsyncCache[backdrop](2, 4, backdropCacheBudget, backdropWeight)
	storeArtwork(a, "fanart:tt001", uniformImage(64, 36, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}))
	a.browse.nav.resetToSection(SectionMovies, a.sectionRoot(SectionMovies))
	renderBackdrop(t, a, size)
	a.browse.backdrop.since = time.Time{}

	art := func(img image.Image) float64 {
		return linearLuminance(color.NRGBAModel.Convert(img.At(size.X-2, 2)).(color.NRGBA))
	}
	a.backdrops.reset()
	grey := newBackdrop(uniformImage(64, 36, color.NRGBA{R: 0x80, G: 0x80, B: 0x80, A: 0xff}), image.Point{})
	release := blockBackdrop(t, a, "fanart:tt001", grey)
	if l := art(renderBackdrop(t, a, size)); l < 0.9 {
		t.Fatalf("refresh blanked the backdrop: luminance %.2f", l)
	}

	release()
	waitForCache(t, func() bool {
		a.backdrops.drain()
		_, ok := a.backdrops.get("fanart:tt001")
		return ok
	})
	renderBackdrop(t, a, size)
	if st := a.browse.backdrop; st.curArt != grey || st.prevArt.sharp.Size().X == 0 {
		t.Fatal("refetched artwork did not replace the shown one with a cross-fade")
	}
	a.browse.backdrop.since = time.Time{}
	if l := art(renderBackdrop(t, a, size)); l < 0.15 || l > 0.3 {
		t.Fatalf("refetched artwork not shown: luminance %.2f", l)
	}
}

func TestFanartSizeCoversWindow(t *testing.T) {
	for _, tc := range []struct {
		name              string
		src, window, want image.Point
	}{
		{"unknown window", image.Pt(3840, 2160), image.Point{}, image.Pt(fanartMaxWidth, fanartMaxHeight)},
		{"4K art, 1080p", image.Pt(3840, 2160), image.Pt(1920, 1080), image.Pt(1920, 1080)},
		{"4K art, Deck", image.Pt(3840, 2160), image.Pt(1280, 800), image.Pt(1423, 800)},
		{"4K art, 4K window", image.Pt(3840, 2160), image.Pt(3840, 2160), image.Pt(fanartMaxWidth, fanartMaxHeight)},
		{"1080p art, 1080p", image.Pt(1920, 1080), image.Pt(1920, 1080), image.Pt(fanartMaxWidth, fanartMaxHeight)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := fanartSize(tc.src, tc.window); got != tc.want {
				t.Fatalf("fanartSize(%v, %v) = %v, want %v", tc.src, tc.window, got, tc.want)
			}
		})
	}
}

func TestFanartRefetchedWhenWindowGrows(t *testing.T) {
	a := &App{backdrops: newWeightedAsyncCache[backdrop](2, 4, backdropCacheBudget, backdropWeight)}
	art := uniformImage(64, 36, color.NRGBA{R: 0x80, G: 0x80, B: 0x80, A: 0xff})
	cached := func() bool { _, ok := a.backdrops.cache["fanart:tt001"]; return ok }

	a.trackFanartWindow(image.Pt(1280, 800)) // first known size: the baseline
	storeArtwork(a, "fanart:tt001", art)
	for _, tc := range []struct {
		name   string
		window image.Point
		kept   bool
		base   image.Point
	}{
		{"small growth", image.Pt(1300, 810), true, image.Pt(1280, 800)},
		{"docked to a TV", image.Pt(1920, 1080), false, image.Pt(1920, 1080)},
		{"shrink", image.Pt(1280, 800), true, image.Pt(1920, 1080)},
		{"back to the baseline", image.Pt(1920, 1080), true, image.Pt(1920, 1080)},
		{"4K window", image.Pt(3840, 2160), false, image.Pt(3840, 2160)},
		{"beyond the cap", image.Pt(7680, 4320), true, image.Pt(3840, 2160)},
	} {
		if !cached() {
			storeArtwork(a, "fanart:tt001", art)
		}
		a.trackFanartWindow(tc.window)
		if cached() != tc.kept || a.fanartWindow != tc.base {
			t.Fatalf("%s: kept=%v base=%v, want kept=%v base=%v", tc.name, cached(), a.fanartWindow, tc.kept, tc.base)
		}
	}
}
