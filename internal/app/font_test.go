package app

import (
	"testing"

	"gioui.org/font"
)

func TestEmbeddedRobotoFaces(t *testing.T) {
	if len(embeddedFontFaces) != 2 {
		t.Fatalf("embedded font faces = %d, want 2", len(embeddedFontFaces))
	}

	styles := map[font.Style]bool{}
	for _, face := range embeddedFontFaces {
		if face.Font.Typeface != "Roboto" {
			t.Errorf("unexpected embedded typeface %q", face.Font.Typeface)
		}
		styles[face.Font.Style] = true
	}
	if !styles[font.Regular] || !styles[font.Italic] {
		t.Fatalf("embedded styles = %v, want regular and italic", styles)
	}
	covered := func(r rune) bool {
		_, ok := embeddedFontFaces[0].Face.Face().NominalGlyph(r)
		return ok
	}
	// Punctuation used in UI text must come from Roboto: there is no fallback.
	for _, r := range []rune{'·', '…', '—', '×', '›', 'é'} {
		if !covered(r) {
			t.Errorf("Roboto does not cover %q", r)
		}
	}
	// Keycap arrows are icons because Roboto lacks them; keep the map exact.
	for _, r := range []rune{'↑', '↓', '←', '→'} {
		if keycapArrows[r] == nil {
			t.Errorf("no keycap icon for %q", r)
		}
		if covered(r) {
			t.Errorf("Roboto now covers %q; the keycap icon may be unnecessary", r)
		}
	}
	if got := newAppTheme().Face; got != "Roboto" {
		t.Fatalf("theme face = %q, want Roboto", got)
	}
}
