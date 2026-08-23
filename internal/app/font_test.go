package app

import (
	"testing"

	"gioui.org/font"
)

func TestEmbeddedRobotoFaces(t *testing.T) {
	if len(embeddedFontFaces) != 4 {
		t.Fatalf("embedded font faces = %d, want 4", len(embeddedFontFaces))
	}

	styles := map[font.Style]bool{}
	symbolFamilies := map[font.Typeface]bool{}
	for _, face := range embeddedFontFaces {
		switch face.Font.Typeface {
		case "Roboto":
			styles[face.Font.Style] = true
		case "Noto Sans Symbols", "Noto Sans Symbols 2":
			symbolFamilies[face.Font.Typeface] = true
		default:
			t.Errorf("unexpected embedded typeface %q", face.Font.Typeface)
		}
	}
	if !styles[font.Regular] || !styles[font.Italic] {
		t.Fatalf("embedded styles = %v, want regular and italic", styles)
	}
	if !symbolFamilies["Noto Sans Symbols"] || !symbolFamilies["Noto Sans Symbols 2"] {
		t.Fatalf("embedded symbol fallbacks = %v, want both Noto Sans Symbols families", symbolFamilies)
	}
	for _, r := range []rune{'↑', '↓', '←', '→', '▶', '·', '…'} {
		found := false
		for _, face := range embeddedFontFaces {
			if _, ok := face.Face.Face().NominalGlyph(r); ok {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("embedded collection does not cover %q", r)
		}
	}
	if got := newAppTheme().Face; got != "Roboto" {
		t.Fatalf("theme face = %q, want Roboto", got)
	}
}
