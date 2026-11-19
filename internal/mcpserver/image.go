package mcpserver

import (
	"bytes"
	"image"
	"image/draw"
	"image/jpeg"
	_ "image/png" // a device screenshot is a PNG
)

// maxEdge is the longest side a model is shown. Claude scales anything larger down to about this
// before reading it, so the extra pixels only cost the agent's context.
const maxEdge = 1568

// fitForModel shrinks a screenshot whose longest side is over maxEdge by a whole factor and returns
// it as a JPEG, which is several times smaller than the PNG for a phone screen. A smaller image, or
// one that cannot be read, is returned unchanged.
func fitForModel(png []byte) ([]byte, string) {
	src, _, err := image.Decode(bytes.NewReader(png))
	if err != nil {
		return png, "image/png"
	}
	bounds := src.Bounds()
	long := max(bounds.Dx(), bounds.Dy())
	if long <= maxEdge {
		return png, "image/png"
	}
	factor := (long + maxEdge - 1) / maxEdge

	rgba := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(rgba, rgba.Bounds(), src, bounds.Min, draw.Src)
	small := shrink(rgba, factor)

	var out bytes.Buffer
	if err := jpeg.Encode(&out, small, &jpeg.Options{Quality: 85}); err != nil {
		return png, "image/png"
	}
	return out.Bytes(), "image/jpeg"
}

// shrink averages each factor x factor block of src into one pixel. A partial block at the right
// or bottom edge is dropped.
func shrink(src *image.RGBA, factor int) *image.RGBA {
	w, h := src.Rect.Dx()/factor, src.Rect.Dy()/factor
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	area := uint32(factor * factor)
	for y := range h {
		for x := range w {
			var sum [4]uint32
			for dy := range factor {
				row := src.Pix[(y*factor+dy)*src.Stride:]
				for dx := range factor {
					p := row[(x*factor+dx)*4:]
					for c := range sum {
						sum[c] += uint32(p[c])
					}
				}
			}
			d := dst.Pix[y*dst.Stride+x*4:]
			for c := range sum {
				d[c] = uint8(sum[c] / area)
			}
		}
	}
	return dst
}
