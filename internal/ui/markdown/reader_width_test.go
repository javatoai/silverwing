package markdownui

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestReaderBodyWrapsToInternalScrollViewport(t *testing.T) {
	body := strings.Repeat("长正文需要遵循阅读区域的宽度并自然换行。", 12)
	for _, prefix := range []string{"", "---\ntags: [业务/支付]\n---\n"} {
		reader := NewReaderState(Document{ID: "bounded-reader", Source: prefix + body})
		view := ui.NewTester(func(c *ui.Context) { MarkdownReader(c, reader, ReaderOptions{}).Fill() }, 280, 400)
		view.Frame()
		view.Frame()
		if rect, ok := view.Find(body); !ok || rect.W > 280 || rect.H < 40 {
			t.Fatalf("reader body remains unbounded: prefix=%t rect=%+v found=%t", prefix != "", rect, ok)
		}
		if reader.scroll.MaxX > 1 {
			t.Fatal("paragraph created horizontal scroll", reader.scroll.MaxX)
		}
		code := "SELECT " + strings.Repeat("long_column_name, ", 30) + "id FROM sample;\n"
		reader.SetDocument(Document{ID: "bounded-reader", Source: prefix + "```sql\n" + code + "```\n"})
		view.Frame()
		view.Frame()
		if rect, ok := view.Find("Copy"); !ok || rect.X+rect.W > 280 {
			t.Fatal("code copy control extends past reader", rect, ok)
		}
		if err := view.Click("Copy"); err != nil {
			t.Fatal(err)
		}
		if view.Clipboard() != code {
			t.Fatal("viewport constraint changed code clipboard content")
		}
	}
}
