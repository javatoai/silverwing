package desktop

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/media"
)

func TestNativeImageLightboxInteraction(t *testing.T) {
	for _, window := range [][2]int{{1440, 900}, {480, 600}} {
		for _, dimensions := range [][2]int{{1200, 500}, {300, 1600}} {
			t.Run(fmt.Sprintf("%dx%d-image-%dx%d", window[0], window[1], dimensions[0], dimensions[1]), func(t *testing.T) {
				pixels := image.NewRGBA(image.Rect(0, 0, dimensions[0], dimensions[1]))
				for y := range dimensions[1] {
					for x := range dimensions[0] {
						pixels.SetRGBA(x, y, color.RGBA{R: 230, G: 20, B: 130, A: 255})
					}
				}
				bitmap := ui.NewBitmap(pixels)
				m := &nativeMediaState{kind: "image", imageInfo: media.ImageInfo{Width: dimensions[0], Height: dimensions[1]}, imageZoom: 1}
				s := &app{}
				backgroundClicks, backgroundInput := 0, ""
				tester := ui.NewTester(func(c *ui.Context) {
					p := setTheme(c, false)
					ui.Column(c).Fill().Background(p.surface).Children(func() {
						if ui.Button(c, "打开测试图片").Clicked() {
							m.imageExpandedBitmap = bitmap
							s.openNativeImageViewer(m)
						}
						if ui.Button(c, "背景操作").Clicked() {
							backgroundClicks++
						}
						ui.TextInput(c, &backgroundInput).Label("背景输入")
					})
					s.renderNativeImageViewer(c, p, m)
				}, window[0], window[1])
				if err := tester.Click("打开测试图片"); err != nil {
					t.Fatal(err)
				}
				canvas, found := tester.Find("图片画布")
				if !found || canvas.W != float32(window[0]-32) || canvas.H != float32(window[1]-32) {
					t.Fatalf("lightbox did not use available canvas: %+v", canvas)
				}
				for _, removed := range []string{"图片预览", "适配窗口", "原始尺寸", "滚轮或", "100%"} {
					if tester.HasText(removed) {
						t.Fatalf("removed toolbar text still visible: %s", removed)
					}
				}
				if directory := os.Getenv("SILVERWING_LIGHTBOX_EVIDENCE_DIR"); directory != "" {
					if err := os.MkdirAll(directory, 0700); err != nil {
						t.Fatal(err)
					}
					file, err := os.Create(filepath.Join(directory, fmt.Sprintf("lightbox-%dx%d-image-%dx%d.png", window[0], window[1], dimensions[0], dimensions[1])))
					if err != nil {
						t.Fatal(err)
					}
					err = png.Encode(file, tester.Image())
					closeErr := file.Close()
					if err != nil || closeErr != nil {
						t.Fatalf("save fixture: %v / %v", err, closeErr)
					}
				}
				for range 4 {
					tester.Key(0, ui.KeyTab)
				}
				tester.Type("must not reach background")
				if backgroundInput != "" || tester.Focused("背景输入") || tester.Focused("背景操作") {
					t.Fatal("keyboard focus escaped lightbox")
				}
				centerX, centerY := canvas.X+canvas.W/2, canvas.Y+canvas.H/2
				tester.ClickAt(centerX, centerY)
				if !m.imageExpanded {
					t.Fatal("clicking the image dismissed it")
				}
				// Zoom enough to exceed both canvas axes, then prove custom Draw is clipped.
				m.imageZoom = 8
				tester.Frame()
				frame := tester.Image()
				for y := 0; y < frame.Bounds().Dy(); y++ {
					for x := 0; x < frame.Bounds().Dx(); x++ {
						pixel := frame.RGBAAt(x, y)
						if pixel.R > 220 && pixel.G < 30 && pixel.B > 120 && pixel.B < 140 &&
							(float32(x) < canvas.X || float32(y) < canvas.Y || float32(x) >= canvas.X+canvas.W || float32(y) >= canvas.Y+canvas.H) {
							t.Fatalf("zoomed image escaped canvas at %d,%d", x, y)
						}
					}
				}
				anchorX, anchorY := canvas.W*.6, canvas.H*.6
				fit := nativeImageFitScale(float32(dimensions[0]), float32(dimensions[1]), canvas.W, canvas.H)
				beforeX := (anchorX - canvas.W/2 - m.imageOffsetX) / (fit * m.imageZoom)
				beforeY := (anchorY - canvas.H/2 - m.imageOffsetY) / (fit * m.imageZoom)
				tester.Scroll(canvas.X+anchorX, canvas.Y+anchorY, 0, -120)
				afterX := (anchorX - canvas.W/2 - m.imageOffsetX) / (fit * m.imageZoom)
				afterY := (anchorY - canvas.H/2 - m.imageOffsetY) / (fit * m.imageZoom)
				if math.Abs(float64(beforeX-afterX)) > .01 || math.Abs(float64(beforeY-afterY)) > .01 {
					t.Fatal("wheel zoom lost the pointer anchor")
				}
				tester.Press(centerX, centerY)
				tester.Move(canvas.X-10, canvas.Y-10)
				tester.Release(canvas.X-10, canvas.Y-10)
				if !m.imageExpanded {
					t.Fatal("drag release outside the image dismissed lightbox")
				}
				tester.Key(0, ui.KeyEscape)
				if m.imageExpanded || !tester.Focused("打开测试图片") || backgroundClicks != 0 {
					t.Fatal("Escape did not restore focus without background action")
				}
				if err := tester.Click("打开测试图片"); err != nil {
					t.Fatal(err)
				}
				canvas, _ = tester.Find("图片画布")
				// The corner is outside either portrait or landscape image at fitted size.
				tester.Press(canvas.X+2, canvas.Y+2)
				tester.Move(canvas.X+12, canvas.Y+12)
				tester.Release(canvas.X+12, canvas.Y+12)
				if !m.imageExpanded {
					t.Fatal("dragging blank space was treated as a dismissal click")
				}
				tester.ClickAt(canvas.X+2, canvas.Y+2)
				if m.imageExpanded || backgroundClicks != 0 {
					t.Fatal("blank-space click failed to dismiss safely")
				}
				if err := tester.Click("打开测试图片"); err != nil {
					t.Fatal(err)
				}
				tester.ClickAt(3, 3)
				if m.imageExpanded || backgroundClicks != 0 {
					t.Fatal("outer backdrop padding did not dismiss safely")
				}
				if err := tester.Click("打开测试图片"); err != nil {
					t.Fatal(err)
				}
				m.imageExpandedBitmap, m.imageExpandedError = nil, "fixture decode error"
				tester.Frame()
				if err := tester.Click("重试读取"); err != nil {
					t.Fatal(err)
				}
				if !m.imageExpanded {
					t.Fatal("retry button was consumed as a backdrop click")
				}
			})
		}
	}
}
