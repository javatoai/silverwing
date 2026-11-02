// Standalone native UI example: go run ./examples/markdown-components.
package main

import (
	"fmt"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	markdownui "silverwing-go/internal/ui/markdown"
)

func main() {
	const source = "# Markdown 公共组件\n\n1. 点击第二项末尾\n2. 按 Enter 自动续写第三项\n\n| 功能 | 状态 |\n|---|---|\n| 原位表格编辑 | 可编辑 |\n\n[链接](https://example.com)\n\n```sql\nSELECT id, name\nFROM materials\nWHERE status = 'ready';\n```\n\n点击代码正文直接编辑，右上角可选择代码语言。\n"
	reader := markdownui.NewReaderState(markdownui.Document{ID: "example-reader", Source: source})
	editor := markdownui.NewEditorState(markdownui.Document{ID: "example-editor", Source: source})
	notice := ""
	mygo.App.WhenReady(func() {
		window := mygo.NewWindow(mygo.WindowOptions{Title: "Markdown components", Width: 1100, Height: 720, Content: ui.View(func(c *ui.Context) {
			ui.Column(c).Fill().Padding(12).Gap(8).Children(func() {
				ui.Text(c, notice).FontSize(12)
				ui.Row(c).Grow(1).MinHeight(0).Gap(16).Children(func() {
					options := markdownui.ReaderOptions{OnNavigate: func(target markdownui.LinkTarget) { notice = "打开请求：" + target.Raw }}
					markdownui.MarkdownReader(c, reader, options).Grow(1)
					markdownui.MarkdownEditor(c, editor, markdownui.EditorOptions{ReaderOptions: options, OnChange: func(snapshot markdownui.Snapshot) {
						reader.SetDocument(markdownui.Document{ID: "example-reader", Source: snapshot.Source})
					}, OnSave: func(snapshot markdownui.Snapshot) {
						notice = fmt.Sprintf("保存请求：版本 %d，%d 字节", snapshot.Version, len(snapshot.Source))
					}}).Grow(1)
				})
			})
		})})
		window.OnClosed(func() { reader.Close(); editor.Close() })
	})
	if err := mygo.App.Run(); err != nil {
		panic(err)
	}
}
