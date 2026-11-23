package raster

import "github.com/egoist/mygo/internal/scene"

// imagePixels draws an unscaled image on whole pixels without bilinear
// sampling: every sample is exactly one texel. Clips and opacity still
// apply, and translucent pixels use the same blend as sampled images.
func (r *renderer) imagePixels(op *scene.Op, opacity float32) bool {
	img, src, dst := op.Image, op.Src, op.Rect
	if img.Surface != nil || op.Grayscale || op.Pixelated || op.Color != (scene.Color{}) ||
		op.Radii != ([4]float32{}) || src.W != dst.W || src.H != dst.H ||
		src.W <= 0 || src.H <= 0 || src.X < 0 || src.Y < 0 ||
		src.X+src.W > float32(img.W) || src.Y+src.H > float32(img.H) || len(img.Pix) < 4*img.W*img.H {
		return false
	}
	// Keep pixel centres exactly representable through the sampled path's
	// float32 subtractions and additions too. Very large coordinates keep
	// that path's rounding rather than changing it to integer arithmetic.
	whole := func(v float32) bool { return v > -(1<<22) && v < 1<<22 && v == float32(int(v)) }
	if !whole(dst.X) || !whole(dst.Y) || !whole(dst.W) || !whole(dst.H) ||
		!whole(src.X) || !whole(src.Y) || !whole(src.X+src.W) || !whole(src.Y+src.H) {
		return false
	}
	x0, y0, x1, y1 := r.pixelBounds(dst)
	if x0 >= x1 || y0 >= y1 {
		return true
	}
	dx, dy := int(src.X)-int(dst.X), int(src.Y)-int(dst.Y)
	for y := y0; y < y1; y++ {
		from := img.Pix[4*((y+dy)*img.W+x0+dx) : 4*((y+dy)*img.W+x1+dx)]
		to := r.dst.Pix[y*r.dst.Stride+4*x0 : y*r.dst.Stride+4*x1]
		cl, ch := r.clipSolid(y)
		for i := 0; i < len(from); i += 4 {
			p := from[i : i+4]
			if p[3] == 0 {
				continue
			}
			cov := opacity
			if x := x0 + i/4; x < cl || x >= ch {
				cov *= r.clipCoverage(x, y)
			}
			if cov <= 0 {
				continue
			}
			q := to[i : i+4]
			if p[3] == 255 && cov == 1 {
				q[0], q[1], q[2], q[3] = p[2], p[1], p[0], 255
			} else {
				blend(q, [4]float32{float32(p[0]) / 255, float32(p[1]) / 255, float32(p[2]) / 255, float32(p[3]) / 255}, cov)
			}
		}
	}
	return true
}
