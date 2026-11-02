package desktop

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
	md "github.com/egoist/mygo/ui/markdown"
	markdownui "silverwing-go/internal/ui/markdown"
)

func TestMarkdownComponentsPublicSurfaces(t *testing.T) {
	for _, width := range []int{600, 1100} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%t", width, dark), func(t *testing.T) {
				reader := markdownui.NewReaderState(markdownui.Document{ID: "public-reader", Source: "# 标准阅读\n\n1. 有序列表\n2. **重点**和引用\n\n> 引用内容\n\n| 名称 | 值 |\n|---|---|\n| 阅读表格 | 正常 |\n\n[阅读链接](https://example.com)\n"})
				defer reader.Close()
				editor := markdownui.NewEditorState(markdownui.Document{ID: "public-editor", Source: "# 实时编辑\n\n| 功能 | 状态 |\n|---|---|\n| 表格单元格 | 直接输入 |\n\n[编辑链接](https://example.com)\n"})
				defer editor.Close()
				editor.NativeState().Focus()
				view := ui.NewTester(func(c *ui.Context) {
					p := setTheme(c, dark)
					theme := &md.Theme{Font: markdownSansFontFamily, FontSize: 15, Text: p.text, Muted: p.muted, Link: p.blue, CodeFont: "monospace", TableBorder: p.line, TableHeader: p.soft}
					container := ui.Row(c).Fill().Padding(16).Gap(16).Background(p.surface)
					if width < 800 {
						container = container.Column()
					}
					container.Children(func() {
						markdownui.MarkdownReader(c, reader, markdownui.ReaderOptions{Theme: theme}).Grow(1)
						markdownui.MarkdownEditor(c, editor, markdownui.EditorOptions{ReaderOptions: markdownui.ReaderOptions{Theme: theme}}).Grow(1)
					})
				}, width, 850)
				for _, text := range []string{"标准阅读", "实时编辑", "阅读表格", "表格单元格", "直接输入", "编辑链接"} {
					if !view.HasText(text) {
						t.Fatalf("missing %q: %v", text, view.Texts())
					}
				}
				captureDesktopTestFrame(t, view, fmt.Sprintf("markdown-components-%d-dark-%t", width, dark))
				if err := view.Click("编辑链接"); err != nil {
					t.Fatal(err)
				}
				_, labelFound := view.Find("显示文字")
				_, urlFound := view.Find("链接地址")
				if !labelFound || !urlFound {
					t.Fatalf("missing popover: %v", view.Texts())
				}
				captureDesktopTestFrame(t, view, fmt.Sprintf("markdown-link-popover-%d-dark-%t", width, dark))
			})
		}
	}
}
