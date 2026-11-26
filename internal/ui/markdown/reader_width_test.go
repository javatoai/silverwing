package markdownui

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	md "github.com/egoist/mygo/ui/markdown"
)

func TestReaderBodyWrapsToInternalScrollViewport(t *testing.T) {
	const narrowWidth = 280
	const wideWidth = 1440
	const properties = "---\ntags: [业务/支付]\n---\n"
	prose := strings.Repeat("长正文需要遵循阅读区域的宽度并自然换行。", 12)
	link := "https://example.test/" + strings.Repeat("longpathsegment", 12)
	for _, fixture := range []struct{ name, source, text string }{
		{"paragraph", prose, prose},
		{"paragraph with properties", properties + prose, prose},
		{"long link", "<" + link + ">", link},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			reader := NewReaderState(Document{ID: "bounded-reader", Source: fixture.source})
			t.Cleanup(reader.Close)
			var drawn []ui.Rect
			native := &md.Options{OnText: func(_ *ui.Context, el ui.Element, _, _ int, spans []ui.Span) {
				if len(spans) == 1 && spans[0].Text == fixture.text {
					el.Draw(func(_ *ui.Painter, rect ui.Rect) { drawn = append(drawn, rect) })
				}
			}}
			view := ui.NewTester(func(c *ui.Context) {
				MarkdownReader(c, reader, ReaderOptions{Native: native}).Fill()
			}, narrowWidth, 500)
			assertNarrow := func(stage string) {
				t.Helper()
				rect, ok := view.Find(fixture.text)
				if !ok || rect.W > narrowWidth || rect.H < 40 {
					t.Fatalf("%s: did not wrap to the narrow viewport: rect=%+v found=%t", stage, rect, ok)
				}
				if fixture.text == prose && (len(drawn) == 0 || drawn[0].W > narrowWidth || drawn[0].H < 40) {
					t.Fatalf("%s: first frame did not wrap to the narrow viewport: %v", stage, drawn)
				}
				if reader.scroll.MaxX > 1 || reader.Document().Source != fixture.source {
					t.Fatalf("%s: reader overflowed by %.1f DIPs or changed the original source", stage, reader.scroll.MaxX)
				}
			}
			assertNarrow("open")
			drawn = nil
			view.SetSize(wideWidth, 500)
			// Wrapped inline links expose fragment bounds through Find. Use the
			// paragraph's own paint bounds for the wide reading measure.
			if fixture.text == prose && (len(drawn) == 0 || drawn[0].W > 1000 || drawn[0].H < 40) {
				t.Fatalf("wide viewport stretched prose too far: %v", drawn)
			}
			drawn = nil
			view.SetSize(narrowWidth, 500)
			assertNarrow("resize")
		})
	}
}

func TestReaderWideBlocksScrollWithoutMovingPage(t *testing.T) {
	const width = 1280
	const paragraph = "表格和代码里的内容可以各自横向查看。"
	const firstColumn = "功能"
	wideCell := strings.Repeat("版本切换与配置核对记录", 5)
	table := "| " + firstColumn + " | 环境 | 配置路径 | 核查方法 | 验收标准 | 备注 |\n" +
		"| --- | --- | --- | --- | --- | --- |\n" +
		"| 核查 | " + strings.Repeat(wideCell+" | ", 5) + "\n"
	code := "SELECT " + strings.Repeat("long_column_name, ", 80) + "id FROM sample;"
	source := paragraph + "\n\n" + table + "\n```sql\n" + code + "\n```\n"
	reader := NewReaderState(Document{ID: "wide-blocks", Source: source})
	t.Cleanup(reader.Close)
	view := ui.NewTester(func(c *ui.Context) { MarkdownReader(c, reader, ReaderOptions{}).Fill() }, width, 520)
	paragraphRect, ok := view.Find(paragraph)
	if !ok {
		t.Fatal("missing prose above wide blocks")
	}
	for _, target := range []string{firstColumn, code} {
		before, ok := view.Find(target)
		if !ok {
			t.Fatal("missing scroll target", target[:min(30, len(target))])
		}
		view.Scroll(before.X+min(before.W/2, 40), before.Y+before.H/2, 12, 0)
		after, ok := view.Find(target)
		if !ok || after.X >= before.X {
			t.Fatalf("wide block did not move horizontally: before=%+v after=%+v found=%t", before, after, ok)
		}
	}
	if rect, _ := view.Find(paragraph); rect != paragraphRect || reader.scroll.MaxX > 1 {
		t.Fatalf("scrolling within a wide block moved the page: before=%+v after=%+v maxX=%v", paragraphRect, rect, reader.scroll.MaxX)
	}
	const narrowWidth = 280
	view.SetSize(narrowWidth, 520)
	if rect, ok := view.Find("Copy"); !ok || rect.X+rect.W > narrowWidth {
		t.Fatalf("code copy control extends past reader: rect=%+v found=%t", rect, ok)
	}
	if err := view.Click("Copy"); err != nil {
		t.Fatal(err)
	}
	if view.Clipboard() != code+"\n" {
		t.Fatal("viewport constraint changed code clipboard content")
	}
}
