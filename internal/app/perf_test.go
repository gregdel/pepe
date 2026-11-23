package app

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"math"
	"math/rand/v2"
	"testing"

	"gioui.org/f32"
	"gioui.org/gpu/headless"
	"gioui.org/op"
	"gioui.org/op/paint"
)

// photoJPEG encodes a w×h image with photographic statistics (smooth shapes,
// edges and grain), so decode and resize cost what real TMDB artwork costs.
func photoJPEG(tb testing.TB, w, h int) []byte { return photoJPEGSeed(tb, w, h, 0) }

// photoJPEGSeed is photoJPEG with a distinct picture per seed.
func photoJPEGSeed(tb testing.TB, w, h int, seed uint64) []byte {
	tb.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	rng := rand.New(rand.NewPCG(1, 2+seed))
	ph := float64(seed)
	for y := range h {
		for x := range w {
			u, v := float64(x)/float64(w), float64(y)/float64(h)
			r := 0.5 + 0.4*math.Sin(u*7+v*3+ph)
			g := 0.4 + 0.3*math.Sin(u*3-v*5+1)
			b := 0.3 + 0.3*math.Cos(u*11*v)
			if math.Sin(u*40)*math.Cos(v*25) > 0.6 {
				r, g, b = r*0.4, g*0.4, b*0.4
			}
			n := (rng.Float64() - 0.5) * 0.05
			to8 := func(f float64) uint8 { return uint8(min(max((f+n)*255, 0), 255)) }
			img.SetNRGBA(x, y, color.NRGBA{R: to8(r), G: to8(g), B: to8(b), A: 0xff})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
		tb.Fatal(err)
	}
	return buf.Bytes()
}

var artworkSizes = []struct {
	name string
	w, h int
}{
	{"fanart1080", 1920, 1080},
	{"fanart2160", 3840, 2160},
	{"poster1000", 1000, 1500},
	{"poster2000", 2000, 3000},
	{"poster680", 680, 1000},
}

func BenchmarkArtworkDecode(b *testing.B) {
	for _, s := range artworkSizes {
		data := photoJPEG(b, s.w, s.h)
		b.Run(s.name, func(b *testing.B) {
			b.ReportMetric(float64(len(data))/1024, "KiB")
			for b.Loop() {
				if _, err := decodeArtwork(bytes.NewReader(data)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func decodedPhoto(b *testing.B, w, h int) image.Image {
	img, err := decodeArtwork(bytes.NewReader(photoJPEG(b, w, h)))
	if err != nil {
		b.Fatal(err)
	}
	return img
}

// BenchmarkArtworkProcess measures the work done on each decoded image before
// it reaches the cache: fanart display copy and ambient/veils, poster sizes.
func BenchmarkArtworkProcess(b *testing.B) {
	fan1080, fan2160 := decodedPhoto(b, 1920, 1080), decodedPhoto(b, 3840, 2160)
	poster, poster1000 := decodedPhoto(b, 2000, 3000), decodedPhoto(b, 1000, 1500)
	for _, bc := range []struct {
		name string
		fn   func()
	}{
		{"resizeFanart1080", func() { resizeArtwork(fan1080, fanartMaxWidth, fanartMaxHeight) }},
		{"resizeFanart2160", func() { resizeArtwork(fan2160, fanartMaxWidth, fanartMaxHeight) }},
		{"ambient1080", func() { ambientImages(fan1080) }},
		{"ambient2160", func() { ambientImages(fan2160) }},
		{"newBackdrop1080", func() { newBackdrop(fan1080, image.Point{}) }},
		{"newBackdrop2160", func() { newBackdrop(fan2160, image.Point{}) }},
		{"newBackdrop1080at1080p", func() { newBackdrop(fan1080, image.Pt(1920, 1080)) }},
		{"newBackdrop2160at1080p", func() { newBackdrop(fan2160, image.Pt(1920, 1080)) }},
		{"posterDetail1000", func() { resizeArtwork(poster1000, posterMaxSize.X, posterMaxSize.Y) }},
		{"posterDetail2000", func() { resizeArtwork(poster, posterMaxSize.X, posterMaxSize.Y) }},
		{"posterRow1000", func() { resizeArtwork(poster1000, rowPosterMaxSize.X, rowPosterMaxSize.Y) }},
		{"posterRow2000", func() { resizeArtwork(poster, rowPosterMaxSize.X, rowPosterMaxSize.Y) }},
	} {
		b.Run(bc.name, func(b *testing.B) {
			for b.Loop() {
				bc.fn()
			}
		})
	}
}

// BenchmarkImageOpUpload is the CPU side of handing a new image to Gio:
// NewImageOp copies non-RGBA images into an sRGB RGBA buffer.
func BenchmarkImageOpUpload(b *testing.B) {
	img := decodedPhoto(b, 1920, 1080)
	b.Run("ycbcr1080", func(b *testing.B) {
		for b.Loop() {
			paint.NewImageOp(img)
		}
	})
}

// BenchmarkTextureUpload measures what the GPU pays to show an image on a
// 1920×1080 headless window. Gio frees textures not drawn in the previous
// frame, so every newly shown backdrop is uploaded again, with mipmaps.
// "readback" is an empty frame; "cached" redraws one image (no upload);
// "new*" draw a new ImageOp of the same pixels each frame (upload + mipmaps).
// Each frame reads one pixel back so queued GPU work completes; subtract
// "cached" (or "readback") to get the upload cost. Uses the platform's
// renderer: set LIBGL_ALWAYS_SOFTWARE=0 for the GPU.
func BenchmarkTextureUpload(b *testing.B) {
	const w, h = 1920, 1080
	window, err := headless.NewWindow(w, h)
	if err != nil {
		b.Skipf("headless window: %v", err)
	}
	defer window.Release()
	pixel := image.NewRGBA(image.Rect(0, 0, 1, 1))
	var ops op.Ops
	frame := func(b *testing.B, img *paint.ImageOp) {
		ops.Reset()
		if img != nil {
			sz := img.Size()
			s := op.Affine(f32.Affine2D{}.Scale(f32.Point{}, f32.Pt(float32(w)/float32(sz.X), float32(h)/float32(sz.Y)))).Push(&ops)
			img.Add(&ops)
			paint.PaintOp{}.Add(&ops)
			s.Pop()
		}
		if err := window.Frame(&ops); err != nil {
			b.Fatal(err)
		}
		if err := window.Screenshot(pixel); err != nil {
			b.Fatal(err)
		}
	}
	b.Run("readback", func(b *testing.B) {
		for b.Loop() {
			frame(b, nil)
		}
	})
	src1080 := photoRGBA(b, w, h)
	cached := paint.NewImageOp(src1080)
	b.Run("cached1080", func(b *testing.B) {
		frame(b, &cached)
		for b.Loop() {
			frame(b, &cached)
		}
	})
	for _, s := range []struct {
		name string
		img  *image.RGBA
	}{
		{"new1080", src1080},
		{"new1152x1080", photoRGBA(b, 1152, 1080)},
		{"new1440", photoRGBA(b, 2560, 1440)},
	} {
		b.Run(s.name, func(b *testing.B) {
			b.ReportMetric(float64(len(s.img.Pix))/(1<<20), "MiB")
			for b.Loop() {
				img := paint.NewImageOp(s.img)
				frame(b, &img)
			}
		})
	}
}

// photoRGBA is a decoded photo as the *image.RGBA Gio uploads unchanged.
func photoRGBA(b *testing.B, w, h int) *image.RGBA {
	src := decodedPhoto(b, w, h)
	dst := image.NewRGBA(src.Bounds())
	draw.Draw(dst, dst.Bounds(), src, src.Bounds().Min, draw.Src)
	return dst
}
