package main

import "image"

// blurImage applies a fast separable box blur with the given radius to src
// and returns a new *image.NRGBA. Two passes (horizontal then vertical) give a
// reasonable approximation of a Gaussian blur with no extra dependencies.
func blurImage(src image.Image, radius int) *image.NRGBA {
	bounds := src.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	// Flatten src into a linear NRGBA buffer for fast random access.
	tmp := image.NewNRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, a := src.At(x, y).RGBA()
			// RGBA() returns pre-multiplied 16-bit values; convert to 8-bit.
			if a == 0 {
				continue
			}
			// Un-premultiply and downscale.
			tmp.Pix[(y-bounds.Min.Y)*tmp.Stride+(x-bounds.Min.X)*4+0] = uint8(r >> 8)
			tmp.Pix[(y-bounds.Min.Y)*tmp.Stride+(x-bounds.Min.X)*4+1] = uint8(g >> 8)
			tmp.Pix[(y-bounds.Min.Y)*tmp.Stride+(x-bounds.Min.X)*4+2] = uint8(b >> 8)
			tmp.Pix[(y-bounds.Min.Y)*tmp.Stride+(x-bounds.Min.X)*4+3] = uint8(a >> 8)
		}
	}

	// Horizontal pass: blur tmp → horiz.
	horiz := image.NewNRGBA(image.Rect(0, 0, w, h))
	boxBlurH(tmp, horiz, w, h, radius)

	// Vertical pass: blur horiz → out.
	out := image.NewNRGBA(image.Rect(0, 0, w, h))
	boxBlurV(horiz, out, w, h, radius)

	return out
}

// boxBlurH performs a horizontal box blur from src into dst.
func boxBlurH(src, dst *image.NRGBA, w, h, radius int) {
	for y := 0; y < h; y++ {
		var rSum, gSum, bSum, aSum, count int
		// Seed the initial window.
		for kx := 0; kx <= radius && kx < w; kx++ {
			i := y*src.Stride + kx*4
			rSum += int(src.Pix[i])
			gSum += int(src.Pix[i+1])
			bSum += int(src.Pix[i+2])
			aSum += int(src.Pix[i+3])
			count++
		}
		for x := 0; x < w; x++ {
			i := y*dst.Stride + x*4
			dst.Pix[i] = uint8(rSum / count)
			dst.Pix[i+1] = uint8(gSum / count)
			dst.Pix[i+2] = uint8(bSum / count)
			dst.Pix[i+3] = uint8(aSum / count)

			// Remove leftmost pixel of window.
			lx := x - radius
			if lx >= 0 {
				j := y*src.Stride + lx*4
				rSum -= int(src.Pix[j])
				gSum -= int(src.Pix[j+1])
				bSum -= int(src.Pix[j+2])
				aSum -= int(src.Pix[j+3])
				count--
			}
			// Add rightmost pixel of window.
			rx := x + radius + 1
			if rx < w {
				j := y*src.Stride + rx*4
				rSum += int(src.Pix[j])
				gSum += int(src.Pix[j+1])
				bSum += int(src.Pix[j+2])
				aSum += int(src.Pix[j+3])
				count++
			}
		}
	}
}

// boxBlurV performs a vertical box blur from src into dst.
func boxBlurV(src, dst *image.NRGBA, w, h, radius int) {
	for x := 0; x < w; x++ {
		var rSum, gSum, bSum, aSum, count int
		for ky := 0; ky <= radius && ky < h; ky++ {
			i := ky*src.Stride + x*4
			rSum += int(src.Pix[i])
			gSum += int(src.Pix[i+1])
			bSum += int(src.Pix[i+2])
			aSum += int(src.Pix[i+3])
			count++
		}
		for y := 0; y < h; y++ {
			i := y*dst.Stride + x*4
			dst.Pix[i] = uint8(rSum / count)
			dst.Pix[i+1] = uint8(gSum / count)
			dst.Pix[i+2] = uint8(bSum / count)
			dst.Pix[i+3] = uint8(aSum / count)

			ly := y - radius
			if ly >= 0 {
				j := ly*src.Stride + x*4
				rSum -= int(src.Pix[j])
				gSum -= int(src.Pix[j+1])
				bSum -= int(src.Pix[j+2])
				aSum -= int(src.Pix[j+3])
				count--
			}
			ry := y + radius + 1
			if ry < h {
				j := ry*src.Stride + x*4
				rSum += int(src.Pix[j])
				gSum += int(src.Pix[j+1])
				bSum += int(src.Pix[j+2])
				aSum += int(src.Pix[j+3])
				count++
			}
		}
	}
}
