package main

import (
	"image/color"

	"gioui.org/unit"
)

// ── Colour palette — refined purple on AMOLED black ─────────────────────────

var (
	colorBg       = color.NRGBA{R: 0x08, G: 0x08, B: 0x0a, A: 0xff} // window background
	colorSurface  = color.NRGBA{R: 0x10, G: 0x10, B: 0x14, A: 0xff} // nav panel
	colorSurface2 = color.NRGBA{R: 0x17, G: 0x17, B: 0x1d, A: 0xff} // cards, popup
	colorOutline  = color.NRGBA{R: 0x26, G: 0x26, B: 0x2e, A: 0xff} // hairlines

	colorFg     = color.NRGBA{R: 0xe8, G: 0xe8, B: 0xec, A: 0xff} // primary text
	colorSubtle = color.NRGBA{R: 0xa0, G: 0xa0, B: 0xab, A: 0xff} // secondary text
	colorMuted  = color.NRGBA{R: 0x6e, G: 0x6e, B: 0x78, A: 0xff} // hints, disabled

	colorPrimary  = color.NRGBA{R: 0x87, G: 0x5f, B: 0xff, A: 0xff} // accent (prefix, wordmark)
	colorSelectBg = color.NRGBA{R: 0x6a, G: 0x4f, B: 0xe0, A: 0xff} // selection pill (5.5:1 on white)
	colorSelectFg = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	colorTerminal = color.NRGBA{R: 0xff, G: 0x69, B: 0xb4, A: 0xff} // playable items (kept pink)

	// badge backgrounds
	colorBadgeBg      = color.NRGBA{R: 0x44, G: 0x44, B: 0x4c, A: 0xff}
	colorSizeBadgeBg  = color.NRGBA{R: 0xd7, G: 0x00, B: 0x87, A: 0xff} // pink
	colorQualBadgeBg  = color.NRGBA{R: 0x87, G: 0x5f, B: 0xff, A: 0xff} // purple
	colorCodecBadgeBg = color.NRGBA{R: 0xff, G: 0xaf, B: 0x00, A: 0xff} // orange
	colorBadgeFg      = color.NRGBA{R: 0x00, G: 0x00, B: 0x00, A: 0xff}
)

// ── Type scale — calibrated for 10-foot / TV viewing distance at 1080p ──────

const (
	spTiny  = unit.Sp(18) // count pills, hint labels, row meta
	spSmall = unit.Sp(20) // badges, keycaps
	sp      = unit.Sp(24) // nav rows, KV rows
	spBody  = unit.Sp(28) // plot, multi-line text
	spMeta  = unit.Sp(24) // year · runtime · genres line
	spTitle = unit.Sp(46) // detail page title
	spHero  = unit.Sp(30) // header wordmark
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
	radiusThumb  = unit.Dp(6)  // posters in nav rows
	radiusRow    = unit.Dp(10) // selection pill
	radiusPoster = unit.Dp(10) // detail page posters
	radiusCard   = unit.Dp(16) // popup, info cards
)

// ── Layout proportions ───────────────────────────────────────────────────────

const (
	modeRailWidth = unit.Dp(88)
	navFlex       = 0.38 // browse: nav list share of the width
	previewFlex   = 0.62 // browse: preview panel share
)
