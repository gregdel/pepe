package app

import (
	_ "embed"
	"fmt"

	"gioui.org/font"
	"gioui.org/font/opentype"
	"gioui.org/text"
	"gioui.org/widget/material"
)

//go:embed embed/roboto.ttf
var embeddedRoboto []byte

//go:embed embed/roboto-italic.ttf
var embeddedRobotoItalic []byte

//go:embed embed/noto-sans-symbols.ttf
var embeddedSymbols []byte

//go:embed embed/noto-sans-symbols2.ttf
var embeddedSymbols2 []byte

var embeddedFontFaces = parseEmbeddedFonts(embeddedRoboto, embeddedRobotoItalic, embeddedSymbols, embeddedSymbols2)

func parseEmbeddedFonts(collections ...[]byte) []font.FontFace {
	var all []font.FontFace
	for i, data := range collections {
		faces, err := opentype.ParseCollection(data)
		if err != nil {
			panic(fmt.Errorf("parse embedded font %d: %w", i, err))
		}
		if len(faces) == 0 {
			panic(fmt.Errorf("parse embedded font %d: no faces found", i))
		}
		all = append(all, faces...)
	}
	if len(all) == 0 {
		panic("parse embedded fonts: no collections")
	}
	return all
}

func newAppTheme() *material.Theme {
	th := material.NewTheme()
	th.Shaper = text.NewShaper(text.NoSystemFonts(), text.WithCollection(embeddedFontFaces))
	th.Face = embeddedFontFaces[0].Font.Typeface
	return th
}
