package desktop

import (
	"fmt"
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestServiceEditorFormModeLabelCentered(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, page := range []string{"commands", "copies"} {
			t.Run(fmt.Sprintf("%s-dark-%t", page, dark), func(t *testing.T) {
				s := &app{serviceEditor: &serviceEditorState{commandsMode: "form", copiesMode: "form"}}
				tester := ui.NewTester(func(c *ui.Context) {
					p := setTheme(c, dark)
					ui.Column(c).Fill().Padding(12).Children(func() { s.renderBootstrapEditorModes(c, p, page) })
				}, 480, 150)
				button, ok := tester.Find("表单配置")
				if !ok {
					t.Fatal("form button missing")
				}
				minX, maxX := 10000, -1
				for y := int(button.Y) + 12; y < int(button.Y+button.H)-12; y++ {
					for x := int(button.X) + 12; x < int(button.X+button.W)-12; x++ {
						pixel := tester.Image().RGBAAt(x, y)
						if pixel.R > 230 && pixel.G > 230 && pixel.B > 230 {
							minX = min(minX, x)
							maxX = max(maxX, x)
						}
					}
				}
				if maxX < minX || math.Abs(float64(float32(minX+maxX)/2-(button.X+button.W/2))) > 2 {
					t.Fatalf("label is not centered: button=%+v white pixels=%d..%d", button, minX, maxX)
				}
			})
		}
	}
}
