package desktop

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"image"
	"testing"
)

func TestMaterialsImageEditorSelectPopupAndResponsiveWidth(t *testing.T) {
	for _, size := range []int{120, 900} {
		t.Run(fmt.Sprintf("width-%d", size), func(t *testing.T) {
			const source = "Before\n\n![图片](shared.png)\n\nAfter"
			s, _, _, v := markdownLiveAcceptanceFixture(t, source, false, 1100)
			state := focusMarkdownLiveAcceptance(t, s, v)
			for _, resource := range state.inlineImages {
				if resource.cancel != nil {
					resource.cancel()
				}
				resource.bitmap = ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, size, size/5)))
				resource.loading, resource.error = false, ""
			}
			v.Frame()
			bounds, ok := v.Find("Markdown 图片：图片")
			if !ok || bounds.W <= 0 {
				t.Fatal("loaded image not rendered")
			}
			if size == 120 && bounds.W > 120.1 {
				t.Fatal("small image upscaled")
			}
			if size == 900 && bounds.W <= 520 {
				t.Fatalf("old 520px cap retained: %+v", bounds)
			}
			if err := v.Click("Markdown 图片：图片"); err != nil {
				t.Fatal(err)
			}
			if _, ok := state.live.SelectedImage(); !ok {
				t.Fatal("material image click failed to select")
			}
			if s.materialsUnit.state.markdownBitmapViewer != nil {
				t.Fatal("single click opened viewer")
			}
			if err := v.Click("编辑"); err != nil {
				t.Fatal(err)
			}
			if _, ok := v.Find("图片地址"); !ok {
				t.Fatal("common image editor popup missing")
			}
			if state.live.Buffer().OriginalText() != source {
				t.Fatal("select/edit rewrote source")
			}
			if err := v.Click("取消"); err != nil {
				t.Fatal(err)
			}
			if err := v.Click("Markdown 图片：图片"); err != nil {
				t.Fatal(err)
			}
			if err := v.Click("Markdown 图片：图片"); err != nil {
				t.Fatal(err)
			}
			if s.materialsUnit.state.markdownBitmapViewer == nil {
				t.Fatal("double click did not open image viewer")
			}
		})
	}
}
