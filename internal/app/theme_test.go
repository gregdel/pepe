package app

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"

	"gioui.org/layout"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"github.com/gregdel/pepe/internal/driver"
)

func TestReadableTextContrast(t *testing.T) {
	brightArtwork := color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	worstCasePanel := composite(colorDetailPanel, brightArtwork)

	tests := []struct {
		name string
		fg   color.NRGBA
		bg   color.NRGBA
		want float64
	}{
		{name: "primary on bright-backed detail panel", fg: colorFg, bg: worstCasePanel, want: 4.5},
		{name: "secondary on bright-backed detail panel", fg: colorSubtle, bg: worstCasePanel, want: 4.5},
		{name: "hint on surface", fg: colorMuted, bg: colorSurface, want: 4.5},
		{name: "selection", fg: colorSelectFg, bg: colorSelectBg, want: 4.5},
		{name: "essential hint on background", fg: colorSubtle, bg: colorBg, want: 4.5},
		{name: "prefix on nav surface", fg: colorPrimary, bg: colorSurface, want: 4.5},
		{name: "accent on bright-backed detail panel", fg: colorPrimary, bg: worstCasePanel, want: 4.5},
		{name: "primary action on bright-backed panel", fg: colorSelectFg, bg: colorSurface2, want: 4.5},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := contrastRatio(test.fg, test.bg); got < test.want {
				t.Fatalf("contrast ratio = %.2f:1, want at least %.1f:1", got, test.want)
			}
		})
	}
}

func composite(fg, bg color.NRGBA) color.NRGBA {
	a := float64(fg.A) / 255
	// Gio composites premultiplied colors in linear space, not sRGB.
	blend := func(f, b uint8) uint8 {
		v := srgbToLinear(float64(f)/255)*a + srgbToLinear(float64(b)/255)*(1-a)
		return uint8(math.Round(linearToSRGB(v) * 255))
	}
	return color.NRGBA{
		R: blend(fg.R, bg.R),
		G: blend(fg.G, bg.G),
		B: blend(fg.B, bg.B),
		A: 0xff,
	}
}

func contrastRatio(a, b color.NRGBA) float64 {
	la, lb := linearLuminance(a), linearLuminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// Render the actual compositor over worst-case white artwork without text.
func renderDetailScrim(t *testing.T, size image.Point, scrim func(C)) image.Image {
	t.Helper()
	d, err := driver.New(driver.Config{Width: size.X, Height: size.Y, PxPerDp: 1, PxPerSp: 1}, func(gtx C) D {
		paint.Fill(gtx.Ops, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
		gtx = scaleContext(gtx)
		scrim(gtx)
		return D{Size: gtx.Constraints.Max}
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	path := filepath.Join(t.TempDir(), "scrim.png")
	if err := d.Screenshot(path); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	img, err := png.Decode(file)
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func assertDetailContrast(t *testing.T, img image.Image, p image.Point) {
	t.Helper()
	bg := color.NRGBAModel.Convert(img.At(p.X, p.Y)).(color.NRGBA)
	for _, fg := range []color.NRGBA{colorFg, colorSubtle, colorPrimary, colorTerminal} {
		if got := contrastRatio(fg, bg); got < 4.5 {
			t.Fatalf("pixel %v: %v on %v = %.2f:1, want 4.5:1", p, fg, bg, got)
		}
	}
}

func TestDetailReadingScrimRenderedContrast(t *testing.T) {
	for _, size := range []image.Point{image.Pt(1920, 1080), image.Pt(1280, 800), image.Pt(900, 600), image.Pt(640, 480)} {
		t.Run(size.String(), func(t *testing.T) {
			for _, tc := range []struct {
				name  string
				width unit.Dp
			}{
				{"movie", moviePosterWidth + detailColumnGap + movieDetailWidth},
				{"show", showPosterWidth + detailColumnGap + showDetailWidth},
			} {
				t.Run(tc.name, func(t *testing.T) {
					a := &App{viewportInsets: layout.UniformInset(dp24)}
					var edge int
					img := renderDetailScrim(t, size, func(gtx C) {
						edge = min(gtx.Dp(dp24+tc.width+dp12), size.X)
						empty := func(gtx C) D { return D{Size: gtx.Constraints.Max} }
						a.detailShell(gtx, 1, tc.width, empty, empty)
					})
					// The entire reading width is protected to the bottom,
					// including the last pixels before the feather begins.
					for _, p := range []image.Point{
						image.Pt(1, 1), image.Pt(edge-1, 1), image.Pt(edge-1, size.Y/2), image.Pt(edge-1, size.Y-1),
					} {
						assertDetailContrast(t, img, p)
					}
					for y := 1; y < size.Y; y += 37 {
						for x := 1; x < edge; x += 43 {
							assertDetailContrast(t, img, image.Pt(x, y))
						}
					}
					if edge < size.X {
						protected := color.NRGBAModel.Convert(img.At(edge-1, 1)).(color.NRGBA)
						exposed := color.NRGBAModel.Convert(img.At(size.X-1, 1)).(color.NRGBA)
						if linearLuminance(exposed) <= linearLuminance(protected) {
							t.Fatal("scrim does not fade into the exposed artwork")
						}
					}
				})
			}
		})
	}
}

// The reading surface is no darker than text contrast needs over white.
func TestDetailPanelIsMinimal(t *testing.T) {
	white := color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	if got := contrastRatio(colorPrimary, composite(colorDetailPanel, white)); got < 4.5 {
		t.Fatalf("panel contrast = %.2f:1, want 4.5:1", got)
	}
	lighter := colorDetailPanel
	lighter.A--
	if got := contrastRatio(colorPrimary, composite(lighter, white)); got >= 4.5 {
		t.Fatalf("a lighter panel still reaches %.2f:1; colorDetailPanel is darker than needed", got)
	}
}

func TestDetailScrimClampedEdges(t *testing.T) {
	size := image.Pt(1280, 800)
	for _, edge := range []int{-100, 0, size.X, size.X * 2} {
		img := renderDetailScrim(t, size, func(gtx C) { detailScrim(gtx, edge) })
		reference := renderDetailScrim(t, size, func(gtx C) { detailScrim(gtx, min(max(edge, 0), size.X)) })
		for _, p := range []image.Point{image.Pt(1, 1), image.Pt(size.X/2, size.Y/2), image.Pt(size.X-1, size.Y-1)} {
			if img.At(p.X, p.Y) != reference.At(p.X, p.Y) {
				t.Fatalf("reading edge %d was not clamped at %v", edge, p)
			}
			if edge >= size.X {
				assertDetailContrast(t, img, p)
			}
		}
	}
}

// The feather eases from the reading surface to fully clear at the window's
// right edge, without darkening or abrupt steps, for movie-like and narrower
// show-like exposed areas.
func TestDetailScrimFeatherSmoothness(t *testing.T) {
	for _, size := range []image.Point{image.Pt(1920, 1080), image.Pt(1280, 800)} {
		for _, edge := range []int{size.X * 13 / 20, size.X * 5 / 6} {
			t.Run(fmt.Sprintf("%v/edge=%d", size, edge), func(t *testing.T) {
				img := renderDetailScrim(t, size, func(gtx C) { detailScrim(gtx, edge) })
				// The fanart under the feather's end is not darkened.
				bare := renderDetailScrim(t, size, func(C) {})
				y := size.Y / 3
				previous := color.RGBAModel.Convert(img.At(edge-1, y)).(color.RGBA)
				for x := edge; x < size.X; x++ {
					current := color.RGBAModel.Convert(img.At(x, y)).(color.RGBA)
					if current.R < previous.R || current.G < previous.G || current.B < previous.B {
						t.Fatalf("feather darkened at x=%d: %v to %v", x, previous, current)
					}
					if !detailFadePixelNear(current, previous, 2) {
						t.Fatalf("abrupt feather transition at x=%d: %v to %v", x, previous, current)
					}
					previous = current
				}
				end := color.RGBAModel.Convert(bare.At(size.X-1, y)).(color.RGBA)
				if !detailFadePixelNear(previous, end, 1) {
					t.Fatalf("feather is not clear at the right edge: %v / %v", previous, end)
				}
				// Halfway, the feather is still clearly darker than the fanart.
				x := (edge + size.X) / 2
				actual := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
				base := color.NRGBAModel.Convert(bare.At(x, y)).(color.NRGBA)
				if linearLuminance(base)-linearLuminance(actual) < 0.05 {
					t.Fatal("feather ended before the middle of the exposed area")
				}
			})
		}
	}
}

// The browse preview text must stay readable over white artwork, without the
// scrim hiding the fanart above the text block.
func TestPreviewScrimRenderedContrast(t *testing.T) {
	for _, size := range []image.Point{image.Pt(1920, 1080), image.Pt(1280, 800)} {
		t.Run(size.String(), func(t *testing.T) {
			textTop := size.Y * 7 / 10
			img := renderDetailScrim(t, size, func(gtx C) { scrim(gtx, textTop) })
			at := func(x, y int) color.NRGBA { return color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA) }
			for y := textTop; y < size.Y; y += 7 {
				for _, x := range []int{1, size.X / 2, size.X - 1} {
					if got := contrastRatio(colorFg, at(x, y)); got < 4.5 {
						t.Fatalf("pixel %d,%d: %.2f:1, want 4.5:1", x, y, got)
					}
				}
			}
			if l := linearLuminance(at(size.X/2, 1)); l < 0.7 {
				t.Fatalf("top of the fanart darkened to luminance %.2f", l)
			}
			previous := color.RGBAModel.Convert(img.At(size.X/2, 0)).(color.RGBA)
			for y := 1; y < textTop; y++ {
				current := color.RGBAModel.Convert(img.At(size.X/2, y)).(color.RGBA)
				if current.R > previous.R || !detailFadePixelNear(current, previous, 3) {
					t.Fatalf("uneven fade at y=%d: %v to %v", y, previous, current)
				}
				previous = current
			}
		})
	}
}
