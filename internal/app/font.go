package app

import (
	_ "embed"
	"fmt"

	"gioui.org/font"
	"gioui.org/font/opentype"
	"gioui.org/text"
	"gioui.org/widget/material"
)

//go:embed embed/font.ttf
var embeddedFont []byte

var embeddedFontFaces = parseEmbeddedFont(embeddedFont)

func parseEmbeddedFont(data []byte) []font.FontFace {
	faces, err := opentype.ParseCollection(data)
	if err != nil {
		panic(fmt.Errorf("parse embedded font: %w", err))
	}
	if len(faces) == 0 {
		panic("parse embedded font: no faces found")
	}
	return faces
}

func newAppTheme() *material.Theme {
	th := material.NewTheme()
	th.Shaper = text.NewShaper(text.NoSystemFonts(), text.WithCollection(embeddedFontFaces))
	th.Face = embeddedFontFaces[0].Font.Typeface
	return th
}
