package app

import (
	"image/color"
	"math"
	"testing"
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
	blend := func(f, b uint8) uint8 {
		return uint8(math.Round(float64(f)*a + float64(b)*(1-a)))
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

func relativeLuminance(c color.NRGBA) float64 {
	linear := func(v uint8) float64 {
		n := float64(v) / 255
		if n <= 0.04045 {
			return n / 12.92
		}
		return math.Pow((n+0.055)/1.055, 2.4)
	}
	return 0.2126*linear(c.R) + 0.7152*linear(c.G) + 0.0722*linear(c.B)
}
