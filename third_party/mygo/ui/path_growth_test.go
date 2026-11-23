package ui

import (
	"math"
	"testing"
)

func TestColdAtlasPathsReturnToOneMask(t *testing.T) {
	const paths, samples = 6, 6001
	frame, show := 0, false
	point := func(path, i int) [2]float32 {
		return [2]float32{
			10 + (760+float32(frame)*0.37)*float32(i)/(samples-1),
			30 + float32(path)*60 + 20*float32(math.Sin(float64(i)*0.03+float64(frame)*0.02)),
		}
	}
	points := make([][2]float32, samples)
	tt := coreNewTester(func(c *context) {
		coreBox(c).Fill().Background(RGB(255, 255, 255)).Draw(func(p *Painter, r Rect) {
			// A small mask first keeps the atlas from growing on its first allocation.
			var small Path
			small.MoveTo(2, 2).LineTo(5, 2).LineTo(5, 5).Close()
			p.FillPath(&small, RGB(20, 60, 230))
			if !show {
				return
			}
			for path := range paths {
				for i := range points {
					points[i] = point(path, i)
				}
				p.StrokePolyline(points, 1.625, RGB(20, 60, 230))
			}
		})
	}, 800, 360)
	tt.SetScale(2)
	before := tt.rt.text.MaskAtlas.W * tt.rt.text.MaskAtlas.H
	if before != 256*256 {
		t.Fatalf("the fixture did not start with a cold atlas: area %d", before)
	}
	show = true
	for ; frame < 6; frame++ {
		tt.Frame()
		if tt.rt.text.Full() {
			t.Fatalf("frame %d left masks out after making room", frame)
		}
		if n := len(tt.rt.scene.Glyphs); n != paths+1 {
			t.Fatalf("frame %d: %d paths draw from %d masks after growth; want one each", frame, paths+1, n)
		}
		img := tt.Image()
		for path := range paths {
			for _, i := range []int{0, 1500, 3000, 4500, 6000} {
				at, found := point(path, i), false
				for dy := -2; dy <= 2 && !found; dy++ {
					for dx := -2; dx <= 2 && !found; dx++ {
						c := img.RGBAAt(int(2*at[0])+dx, int(2*at[1])+dy)
						found = c.B > 150 && c.R < 120
					}
				}
				if !found {
					t.Errorf("frame %d: path %d is missing sample %d", frame, path, i)
				}
			}
		}
	}
	if after := tt.rt.text.MaskAtlas.W * tt.rt.text.MaskAtlas.H; after <= before {
		t.Fatal("the fixture did not exercise atlas growth")
	}
}

// Clipping makes these paths' tiles cheap, although their whole masks
// would crowd even the largest atlas. Trying whole masks after every
// growth can consume both recovery paints before the last path draws.
func TestColdAtlasCrowdedPathsRemainVisible(t *testing.T) {
	show := false
	tt := coreNewTester(func(c *context) {
		coreBox(c).Fill().Background(RGB(255, 255, 255)).Draw(func(p *Painter, r Rect) {
			if !show {
				return
			}
			p.Clip(r, 0, func() {
				var small Path
				small.MoveTo(0, 0).LineTo(1022, 0).LineTo(1022, 12).LineTo(0, 12).Close()
				p.FillPath(&small, RGB(20, 60, 230))
				for i := range 11 {
					y := float32(20 + i*15)
					var line Path
					line.MoveTo(1, 1).LineTo(1, 351)
					line.MoveTo(1, y).LineTo(200, y).LineTo(3801, 351)
					p.StrokePath(&line, 2, RGB(20, 60, 230))
				}
			})
		})
	}, 200, 200)
	if a := tt.rt.text.MaskAtlas; a.W != 256 || a.H != 256 {
		t.Fatalf("the fixture did not start with a cold atlas: %dx%d", a.W, a.H)
	}
	show = true
	tt.Frame()
	img := tt.Image()
	for i := range 11 {
		if c := img.RGBAAt(100, 20+i*15); c.B < 150 || c.R > 120 {
			t.Errorf("the first frame is missing clipped path %d: %v", i, c)
		}
	}
}

// Cached masks drawn after newly visible paths must be released when a
// recovery paint switches to tiles. Cached tiles also need to go: their
// lasting shelves otherwise take room that transient tiles cannot use.
func TestAtlasGrowthReleasesCachedMasks(t *testing.T) {
	for _, cached := range []string{"whole", "tiles"} {
		t.Run(cached, func(t *testing.T) {
			const paths = 56
			show := false
			tt := coreNewTester(func(c *context) {
				coreBox(c).Fill().Background(RGB(255, 255, 255)).Draw(func(p *Painter, r Rect) {
					p.Clip(r, 0, func() {
						if show {
							for i := range paths {
								y := float32(4 + i*3)
								var line Path
								line.MoveTo(1, 1).LineTo(1, 3999)
								line.MoveTo(1, y).LineTo(200, y).LineTo(2999, 3999)
								p.StrokePath(&line, 2, RGB(20, 60, 230))
							}
						}
						var warm Path
						warm.MoveTo(1, 1).LineTo(1, 509)
						warm.MoveTo(1, 190).LineTo(200, 190).LineTo(899, 509)
						p.StrokePath(&warm, 2, RGB(230, 20, 60))
						if cached == "tiles" {
							var tiled Path
							tiled.MoveTo(1, 1).LineTo(1, 4997)
							tiled.MoveTo(1, 180).LineTo(200, 180).LineTo(998, 4997)
							p.StrokePath(&tiled, 2, RGB(60, 230, 20))
						}
					})
				})
			}, 200, 200)
			want := 1
			if cached == "tiles" {
				tt.Frame()
				want = 2
			} else {
				// Let the initial tiled mode, if any, expire and cache the whole mask.
				for range pathCrowdedPaints + 2 {
					tt.Frame()
				}
			}
			if n := len(tt.rt.scene.Glyphs); n != want {
				t.Fatalf("warm paths draw %d masks; want %d", n, want)
			}
			show = true
			tt.Frame()
			img := tt.Image()
			for i := range paths {
				if c := img.RGBAAt(100, 4+i*3); c.B < 150 || c.R > 120 {
					t.Errorf("path %d missing: %v", i, c)
				}
			}
			if cached == "tiles" {
				if c := img.RGBAAt(100, 180); c.G < 150 || c.B > 120 {
					t.Errorf("cached tile missing: %v", c)
				}
			}
			if c := img.RGBAAt(100, 190); c.R < 150 || c.B > 120 {
				t.Errorf("warm path missing: %v", c)
			}
		})
	}
}

// A whole mask can fill a shelf as tall as the largest atlas after it
// grows. Failed tiles or icons must then release it too, although no
// whole path mask failed in that paint.
func TestAtlasGrowthLeavesRoomForTilesAndIcons(t *testing.T) {
	for _, kind := range []string{"tiles", "icon"} {
		t.Run(kind, func(t *testing.T) {
			show := false
			icon := MustParseSVG([]byte(`<svg viewBox="0 0 20 20"><path d="M0 0H20V20H0Z"/></svg>`))
			tt := coreNewTester(func(c *context) {
				coreBox(c).Fill().Background(RGB(255, 255, 255)).Draw(func(p *Painter, r Rect) {
					if !show {
						return
					}
					p.Clip(r, 0, func() {
						draw := func(x, y float32) {
							var line Path
							line.MoveTo(1, 1).LineTo(1, 3999)
							line.MoveTo(1, y).LineTo(200, y).LineTo(x, 3999)
							p.StrokePath(&line, 2, RGB(20, 60, 230))
						}
						draw(2999, 60)
						if kind == "tiles" {
							draw(4999, 120)
						} else {
							p.Icon(icon, Rect{90, 110, 20, 20}, RGB(20, 60, 230))
						}
					})
				})
			}, 200, 200)
			show = true
			tt.Frame()
			img := tt.Image()
			for _, y := range []int{60, 120} {
				if c := img.RGBAAt(100, y); c.B < 150 || c.R > 120 {
					t.Errorf("drawing at y=%d is missing: %v", y, c)
				}
			}
		})
	}
}

// Interleaved wide and narrow tiles fit once cached masks are compacted.
// Evicting that cache on every recovery repeats the same fragmentation.
func TestAtlasGrowthKeepsTilesPacked(t *testing.T) {
	full, narrow, short := [2]int{1024, 1024}, [2]int{256, 1024}, [2]int{1024, 251}
	sizes := [][2]int{short}
	for range 6 {
		sizes = append(sizes, narrow)
		for range 7 {
			sizes = append(sizes, full)
		}
	}
	for range 5 {
		sizes = append(sizes, narrow)
	}
	for range 6 {
		sizes = append(sizes, full)
	}
	show := false
	tt := coreNewTester(func(c *context) {
		coreBox(c).Fill().Background(RGB(255, 255, 255)).Draw(func(p *Painter, r Rect) {
			p.Clip(r, 0, func() {
				for i, size := range sizes {
					if !show && i != 10 { // warm one path before the others appear
						continue
					}
					y := float32(3 + i*3)
					var line Path
					line.MoveTo(1, 1).LineTo(1, float32(size[1]))
					line.MoveTo(1, y).LineTo(200, y).LineTo(float32(size[0]), float32(size[1]))
					p.StrokePath(&line, 2, RGB(20, 60, 230))
				}
			})
		})
	}, 200, 200)
	for range 2 {
		tt.Frame()
	}
	show = true
	tt.Frame()
	for frame := range 3 {
		tt.Frame()
		img := tt.Image()
		for i := range sizes {
			if c := img.RGBAAt(100, 3+i*3); c.B < 150 || c.R > 120 {
				t.Errorf("frame %d: path %d is missing after compaction: %v", frame, i, c)
			}
		}
		if tt.rt.text.Full() {
			t.Errorf("frame %d: the compacted tiles still leave masks out", frame)
		}
	}
}

// One small mask shares a lasting shelf with a cached tile. When the
// tile is released for recovery, it must still be able to share that
// shelf: the other 56 tiles need the atlas's seven remaining rows.
func TestAtlasGrowthReusesSharedLastingShelf(t *testing.T) {
	show := false
	tt := coreNewTester(func(c *context) {
		coreBox(c).Fill().Background(RGB(255, 255, 255)).Draw(func(p *Painter, r Rect) {
			p.Clip(r, 0, func() {
				draw := func(i int, width, height float32) {
					y := float32(3 + i*3)
					var path Path
					path.MoveTo(1, 1).LineTo(1, height)
					path.MoveTo(1, y).LineTo(200, y).LineTo(width, height)
					p.StrokePath(&path, 2, RGB(20, 60, 230))
				}
				draw(0, 255, 509)   // 257×511: a small mask, kept during recovery.
				draw(1, 5000, 5000) // One visible 511×511 tile, cached too.
				if show {
					for i := 2; i < 58; i++ {
						draw(i, 1024, 1024)
					}
				}
			})
		})
	}, 200, 200)
	for range 2 {
		tt.Frame()
	}
	show = true
	tt.Frame()
	img := tt.Image()
	for i := range 58 {
		if c := img.RGBAAt(100, 3+i*3); c.B < 150 || c.R > 120 {
			t.Errorf("path %d disappeared during recovery: %v", i, c)
		}
	}
	if tt.rt.text.Full() {
		t.Error("recovery left out a mask although all 58 masks fit")
	}
}
