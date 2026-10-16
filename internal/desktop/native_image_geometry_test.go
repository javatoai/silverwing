package desktop

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/media"
)

func TestNativeImageViewerUsesCanvasGeometry(t *testing.T) {
	for _, size := range [][2]int{{1440, 940}, {960, 760}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			pixels := image.NewRGBA(image.Rect(0, 0, 1136, 2690))
			for y := 0; y < pixels.Bounds().Dy(); y++ {
				for x := 0; x < pixels.Bounds().Dx(); x++ {
					pixels.SetRGBA(x, y, color.RGBA{R: 230, G: 20, B: 130, A: 255})
				}
			}
			m := &nativeMediaState{kind: "image", imageExpanded: true, imageExpandedBitmap: ui.NewBitmap(pixels), imageInfo: media.ImageInfo{Width: 1136, Height: 2690}, imageZoom: 1}
			s := &app{}
			tester := ui.NewTester(func(c *ui.Context) { s.renderNativeImageViewer(c, setTheme(c, false), m) }, size[0], size[1])
			canvas, found := tester.Find("图片画布")
			if !found || math.Abs(float64(canvas.W-m.imageViewportWidth)) > 1 || math.Abs(float64(canvas.H-m.imageViewportHeight)) > 1 {
				t.Fatalf("viewer viewport %vx%v does not match canvas %#v", m.imageViewportWidth, m.imageViewportHeight, canvas)
			}
			imageBounds := image.Rectangle{}
			frame := tester.Image()
			for y := 0; y < frame.Bounds().Dy(); y++ {
				for x := 0; x < frame.Bounds().Dx(); x++ {
					pixel := frame.RGBAAt(x, y)
					if pixel.R > 220 && pixel.G < 30 && pixel.B > 120 && pixel.B < 140 {
						imageBounds = imageBounds.Union(image.Rect(x, y, x+1, y+1))
					}
				}
			}
			centerX, centerY := float32(imageBounds.Min.X+imageBounds.Max.X)/2, float32(imageBounds.Min.Y+imageBounds.Max.Y)/2
			if imageBounds.Empty() || math.Abs(float64(centerX-(canvas.X+canvas.W/2))) > 1 || math.Abs(float64(centerY-(canvas.Y+canvas.H/2))) > 1 {
				t.Fatalf("image %#v is not centered in canvas %#v", imageBounds, canvas)
			}
			tester.Scroll(canvas.X+canvas.W/2, canvas.Y+canvas.H/2, 0, -120)
			if m.imageZoom <= 1 {
				t.Fatal("zoom did not update")
			}
			tester.Key(0, ui.Key1)
			fit := nativeImageFitScale(1136, 2690, canvas.W, canvas.H)
			if math.Abs(float64(m.imageZoom*fit-1)) > .001 || m.imageOffsetX != 0 || m.imageOffsetY != 0 {
				t.Fatal("original size did not use centered canvas geometry")
			}
			tester.SetSize(1200, 800)
			canvas, _ = tester.Find("图片画布")
			fit = nativeImageFitScale(1136, 2690, canvas.W, canvas.H)
			if math.Abs(float64(m.imageZoom*fit-1)) > .001 {
				t.Fatal("resizing changed original-size pixels")
			}
			x, y := canvas.X+canvas.W/2, canvas.Y+canvas.H/2
			tester.Press(x, y)
			tester.Move(x, y+35)
			tester.Release(x, y+35)
			if m.imageOffsetY <= 0 {
				t.Fatal("canvas-relative drag did not pan the original image")
			}
			tester.Key(0, ui.Key0)
			if m.imageZoom != 1 || m.imageOffsetX != 0 || m.imageOffsetY != 0 {
				t.Fatal("fit did not reset zoom and offsets")
			}
			tester.SetSize(1100, 820)
			canvas, _ = tester.Find("图片画布")
			if m.imageViewportWidth != canvas.W || m.imageViewportHeight != canvas.H {
				t.Fatal("resized viewport did not follow canvas")
			}
		})
	}
}
