package app

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"

	"gioui.org/op/paint"
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
		v := linearChannel(f)*a + linearChannel(b)*(1-a)
		if v <= 0.0031308 {
			v *= 12.92
		} else {
			v = 1.055*math.Pow(v, 1/2.4) - 0.055
		}
		return uint8(math.Round(v * 255))
	}
	return color.NRGBA{
		R: blend(fg.R, bg.R),
		G: blend(fg.G, bg.G),
		B: blend(fg.B, bg.B),
		A: 0xff,
	}
}

func contrastRatio(a, b color.NRGBA) float64 {
	la, lb := relativeLuminance(a), relativeLuminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func linearChannel(v uint8) float64 {
	n := float64(v) / 255
	if n <= 0.04045 {
		return n / 12.92
	}
	return math.Pow((n+0.055)/1.055, 2.4)
}

func relativeLuminance(c color.NRGBA) float64 {
	return 0.2126*linearChannel(c.R) + 0.7152*linearChannel(c.G) + 0.0722*linearChannel(c.B)
}

// Exercise the actual compositor, including the gradient's plateau and fade.
// This renders only the surface over white, not a mock catalog/detail page.
func TestDetailReadingScrimRenderedContrast(t *testing.T) {
	for _, width := range []int{1920, 1280} {
		edge := width * 1248 / 1920
		d, err := driver.New(driver.Config{Width: width, Height: width * 9 / 16, PxPerDp: 1, PxPerSp: 1}, func(gtx C) D {
			paint.Fill(gtx.Ops, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
			gtx = scaleContext(gtx)
			detailScrim(gtx, edge)
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
		img, err := png.Decode(file)
		_ = file.Close()
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range []image.Point{image.Pt(1, 1), image.Pt(edge/2, 1), image.Pt(edge-1, 1), image.Pt(edge-1, img.Bounds().Dy()/2)} {
			bg := color.NRGBAModel.Convert(img.At(p.X, p.Y)).(color.NRGBA)
			for _, fg := range []color.NRGBA{colorFg, colorSubtle, colorPrimary} {
				if got := contrastRatio(fg, bg); got < 4.5 {
					t.Fatalf("width %d, pixel %v: %v on %v = %.2f:1, want 4.5:1", width, p, fg, bg, got)
				}
			}
		}
		protected := color.NRGBAModel.Convert(img.At(edge-1, 1)).(color.NRGBA)
		exposed := color.NRGBAModel.Convert(img.At(width-1, 1)).(color.NRGBA)
		if relativeLuminance(exposed) <= relativeLuminance(protected) {
			t.Fatal("scrim does not fade into the exposed artwork")
		}
	}
}
