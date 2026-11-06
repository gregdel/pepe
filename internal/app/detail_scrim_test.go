package app

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gioui.org/op/paint"
	"github.com/odwrtw/polochon/lib/papi"
)

func detailScreenshot(t *testing.T, f *headlessFixture) image.Image {
	t.Helper()
	path := filepath.Join(t.TempDir(), "detail.png")
	if err := f.driver.Screenshot(path); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	img, err := png.Decode(file)
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func TestShowScrimStaysFullHeight(t *testing.T) {
	for _, size := range []image.Point{image.Pt(1920, 1080), image.Pt(1280, 800)} {
		t.Run(size.String(), func(t *testing.T) {
			f := newHeadlessFixture(t)
			show := f.app.client.shows["show-one"]
			shortPlot := "A short episode synopsis."
			longPlot := strings.Repeat("A longer synopsis must remain protected while its content scrolls. ", 80)
			show.Seasons[1].Episodes[1].Plot = shortPlot
			show.Seasons[1].Episodes[2].Plot = longPlot
			for ep := 3; ep <= 25; ep++ {
				show.Seasons[1].Episodes[ep] = headlessEpisode(1, ep, "Episode "+fmt.Sprint(ep))
			}
			show.Seasons[3] = &papi.Season{Episodes: map[int]*papi.Episode{}}

			white := image.NewNRGBA(image.Rect(0, 0, 16, 16))
			draw.Draw(white, white.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
			for _, key := range []string{"fanart:show-one", "poster:show-one"} {
				f.app.images.store(key, paint.NewImageOp(white))
			}
			f.app.pushShowDetail(&MediaItem{ID: "show-one", Title: "Fixture Show", Type: mediaKindShow})
			f.app.detail.enteredAt = time.Now().Add(-detailFadeDuration)
			if err := f.driver.Resize(size.X, size.Y); err != nil {
				t.Fatal(err)
			}

			checkText := func(img image.Image, labels ...string) {
				t.Helper()
				for _, label := range labels {
					nodes := headlessLabelNodes(f.driver.Tree(), label)
					if len(nodes) == 0 {
						t.Fatalf("missing label %q", label)
					}
					b := nodes[0].Bounds
					// Just left of the label is background, not an antialiased glyph.
					p := image.Pt(b.MinX-2, b.MinY+3)
					if !p.In(img.Bounds()) {
						t.Fatalf("label %q is outside the viewport: %+v", label, b)
					}
					assertDetailContrast(t, img, p)
				}
			}

			short := detailScreenshot(t, f)
			checkText(short, "Fixture Show", shortPlot)
			// This blank strip is just beyond the synopsis's right edge. Its
			// lower reading surface must not change with episode content.
			p := image.Pt(size.X*1602/1920, size.Y*4/5)
			background := color.RGBAModel.Convert(short.At(p.X, p.Y)).(color.RGBA)
			checkBackground := func(img image.Image) {
				t.Helper()
				assertDetailContrast(t, img, p)
				got := color.RGBAModel.Convert(img.At(p.X, p.Y)).(color.RGBA)
				if !detailFadePixelNear(got, background, 1) {
					t.Fatalf("reading surface changed with content at %v: %v / %v", p, got, background)
				}
			}
			checkBackground(short)
			headlessKey(t, f, "DownArrow")
			headlessKey(t, f, "DownArrow")
			if s := f.app.detail.show; s.focus != focusEpisodes || s.epCursor != 1 {
				t.Fatalf("episode focus = %v, cursor = %d", s.focus, s.epCursor)
			}
			long := detailScreenshot(t, f)
			checkText(long, "Fixture Show", longPlot)

			checkBackground(long)

			s := f.app.detail.show
			s.infoList.Position.Offset += 240
			if err := f.driver.Frame(); err != nil {
				t.Fatal(err)
			}
			if pos := s.infoList.Position; pos.First == 0 && pos.Offset == 0 {
				t.Fatal("long episode synopsis did not scroll")
			}
			checkBackground(detailScreenshot(t, f))

			for range 23 {
				headlessKey(t, f, "DownArrow")
			}
			last := detailScreenshot(t, f)
			checkBackground(last)
			rows := headlessLabelNodes(f.driver.Tree(), "Episode 25")
			if len(rows) == 0 || rows[0].Bounds.MinY >= size.Y {
				t.Fatal("last episode is not visible after scrolling")
			}
			// The blank strip just beyond the list's right edge shares the
			// full-height protection with the last visible row.
			rowEdge := image.Pt(size.X*810/1920, rows[0].Bounds.MinY+3)
			assertDetailContrast(t, last, rowEdge)

			f.app.playerStatus = PlayerStatusRunning
			if err := f.driver.Frame(); err != nil {
				t.Fatal(err)
			}
			playing := detailScreenshot(t, f)
			checkText(playing, "Fixture Show", "Episode details for Episode 25")
			assertDetailContrast(t, playing, p)

			f.app.playerStatus = PlayerStatusIdle
			s.focus, s.seasonCursor, s.epCursor = focusTabs, 2, 0
			if err := f.driver.Frame(); err != nil {
				t.Fatal(err)
			}
			empty := detailScreenshot(t, f)
			checkText(empty, "Fixture Show", "No episodes")
			checkBackground(empty)
		})
	}
}
