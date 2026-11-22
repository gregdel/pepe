package app

import (
	"fmt"
	"image/color"
	"strings"

	"gioui.org/unit"
)

// ── Colour palette — refined purple on AMOLED black ─────────────────────────

var (
	colorBg       = color.NRGBA{R: 0x08, G: 0x08, B: 0x0a, A: 0xff} // window background
	colorSurface  = color.NRGBA{R: 0x10, G: 0x10, B: 0x14, A: 0xff} // panels (playing bar)
	colorSurface2 = color.NRGBA{R: 0x17, G: 0x17, B: 0x1d, A: 0xff} // cards, popup
	colorOutline  = color.NRGBA{R: 0x26, G: 0x26, B: 0x2e, A: 0xff} // hairlines
	colorTrack    = color.NRGBA{R: 0x60, G: 0x60, B: 0x6a, A: 0xff} // progress track (3:1 on colorSurface)

	colorFg     = color.NRGBA{R: 0xe8, G: 0xe8, B: 0xec, A: 0xff} // primary text
	colorSubtle = color.NRGBA{R: 0xa0, G: 0xa0, B: 0xab, A: 0xff} // secondary text
	colorMuted  = color.NRGBA{R: 0x88, G: 0x88, B: 0x94, A: 0xff} // hints, disabled

	// The least alpha that keeps detail text (colorPrimary is the dimmest) at
	// 4.5:1 over white artwork after linear blending; less shows the fanart
	// more but fails contrast.
	colorDetailPanel = color.NRGBA{R: 0x10, G: 0x10, B: 0x14, A: 0xfa}

	colorPrimary  = color.NRGBA{R: 0x9c, G: 0x7f, B: 0xff, A: 0xff} // accent (prefix, wordmark)
	colorSelectBg = color.NRGBA{R: 0x6a, G: 0x4f, B: 0xe0, A: 0xff} // focus fill (5.5:1 on white)
	colorSelectFg = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	colorError    = color.NRGBA{R: 0xff, G: 0x69, B: 0xb4, A: 0xff} // errors and failure states
	colorWarn     = color.NRGBA{R: 0xff, G: 0xb4, B: 0x68, A: 0xff} // log warnings

	// The HDMI-CEC remote's colour keys, as help-bar swatches.
	colorRemoteRed    = color.NRGBA{R: 0xff, G: 0x8a, B: 0x80, A: 0xff}
	colorRemoteGreen  = color.NRGBA{R: 0x81, G: 0xd7, B: 0x9b, A: 0xff}
	colorRemoteYellow = color.NRGBA{R: 0xff, G: 0xd8, B: 0x6b, A: 0xff}
	colorRemoteBlue   = color.NRGBA{R: 0x68, G: 0xb4, B: 0xff, A: 0xff}
)

// ── Type scale — calibrated for 10-foot / TV viewing distance at 1080p ──────

const (
	spTiny   = unit.Sp(22) // count pills, row meta
	spSmall  = unit.Sp(24) // keycaps and essential hints
	spBrowse = unit.Sp(32) // primary browse labels
	sp       = unit.Sp(24) // controls, KV rows
	spBody   = unit.Sp(28) // plot, multi-line text
	spMeta   = unit.Sp(24) // year · runtime · genres line
	spTitle  = unit.Sp(46) // detail page title
	spHero   = unit.Sp(30) // header wordmark
)

// ── Spacing scale ────────────────────────────────────────────────────────────

const (
	dp2  = unit.Dp(2)
	dp4  = unit.Dp(4)
	dp6  = unit.Dp(6)
	dp8  = unit.Dp(8)
	dp12 = unit.Dp(12)
	dp16 = unit.Dp(16)
	dp20 = unit.Dp(20)
	dp24 = unit.Dp(24)
	dp32 = unit.Dp(32)
	dp48 = unit.Dp(48)
)

// ── Corner radii ─────────────────────────────────────────────────────────────

const (
	radiusThumb  = unit.Dp(6)  // row thumbnails
	radiusRow    = unit.Dp(10) // focusable rows, pills, buttons
	radiusPoster = unit.Dp(10) // detail page posters
	radiusCard   = unit.Dp(16) // popup, info cards
)

// ── Layout proportions ───────────────────────────────────────────────────────

const (
	modeRailWidth   = unit.Dp(80)
	browseRowHeight = unit.Dp(64)
	navFlex         = 0.38 // browse: nav list share of the width
)

// ── Other surfaces ───────────────────────────────────────────────────────────
//
// The mpv OSD and the web remote take their colours from the tokens above, so
// a palette change here reaches every surface (DESIGN.md, Other surfaces).

type themeToken struct {
	name  string
	color color.NRGBA
}

// osdColors are pepe.lua's colour options (pepe-color_<name>).
var osdColors = []themeToken{
	{"fg", colorFg},
	{"subtle", colorSubtle},
	{"accent", colorPrimary},
	{"panel", colorSurface},
	{"track", colorTrack},
	{"green", colorRemoteGreen},
	{"yellow", colorRemoteYellow},
	{"blue", colorRemoteBlue},
}

// assColor is c as an ASS colour, BGR hex.
func assColor(c color.NRGBA) string {
	return fmt.Sprintf("%02X%02X%02X", c.B, c.G, c.R)
}

// cssColor is c as a CSS hex colour, with alpha only when translucent.
func cssColor(c color.NRGBA) string {
	if c.A == 0xff {
		return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
	}
	return fmt.Sprintf("#%02x%02x%02x%02x", c.R, c.G, c.B, c.A)
}

// themeCSS is the web remote's tokens as CSS custom properties, served ahead
// of remote.css.
func themeCSS() string {
	var b strings.Builder
	b.WriteString(":root {\n")
	for _, t := range []themeToken{
		{"bg", colorBg},
		{"surface", colorSurface},
		{"surface-2", colorSurface2},
		{"outline", colorOutline},
		{"fg", colorFg},
		{"subtle", colorSubtle},
		{"muted", colorMuted},
		{"primary", colorPrimary},
		{"select-bg", colorSelectBg},
		{"select-fg", colorSelectFg},
		{"error", colorError},
		{"warn", colorWarn},
	} {
		fmt.Fprintf(&b, "  --%s: %s;\n", t.name, cssColor(t.color))
	}
	for _, r := range []struct {
		name   string
		radius unit.Dp
	}{{"row", radiusRow}, {"card", radiusCard}} {
		fmt.Fprintf(&b, "  --radius-%s: %gpx;\n", r.name, float32(r.radius))
	}
	b.WriteString("}\n")
	return b.String()
}
