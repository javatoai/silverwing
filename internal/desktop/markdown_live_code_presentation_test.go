package desktop

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	markdownui "silverwing-go/internal/ui/markdown"
)

func TestMarkdownLiveCodePresentationAndLanguagePlacement(t *testing.T) {
	const body = "SELECT id, name\nFROM materials;"
	const source = "# 四要素齐全的有效支付配置\n\n```sql\n" + body + "\n```\n\n正文、表格和链接继续使用实时编辑。\n"
	for _, width := range []int{420, 960} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%t", width, dark), func(t *testing.T) {
				state := markdownui.NewEditorState(markdownui.Document{ID: "live-code-visual", Source: source})
				t.Cleanup(state.Close)
				view := ui.NewTester(func(c *ui.Context) {
					p := setTheme(c, dark)
					ui.Column(c).Fill().Padding(16).Background(p.bg).Children(func() {
						markdownui.MarkdownEditor(c, state, markdownui.EditorOptions{}).Grow(1)
					})
				}, width, 480)
				card, ok := view.Find("Code, sql")
				language, hasLanguage := view.Find("代码语言")
				if !ok || !hasLanguage || language.X < card.X+card.W/2 || language.X+language.W > card.X+card.W || language.Y < card.Y || language.Y > card.Y+50 {
					t.Fatalf("language selector must stay at the card's top right: card=%+v selector=%+v", card, language)
				}
				if err := view.Click(body); err != nil {
					t.Fatal(err)
				}
				view.Key(0, ui.KeyEnd)
				view.Type(" -- 实时编辑")
				if !strings.Contains(state.Snapshot().Source, " -- 实时编辑") || view.HasText("```sql") {
					t.Fatal("typing in the code card exposed or bypassed its rendered body")
				}
				captureDesktopTestFrame(t, view, fmt.Sprintf("live-code-%d-dark-%t-editing", width, dark))
				if err := view.Click("代码语言"); err != nil {
					t.Fatal(err)
				}
				if !view.HasText("Python") || !view.HasText("JavaScript") {
					t.Fatal("native language menu omitted the requested formats")
				}
				captureDesktopTestFrame(t, view, fmt.Sprintf("live-code-%d-dark-%t-language", width, dark))
				if err := view.Click("Python"); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(state.Snapshot().Source, "```python\n") || !strings.Contains(state.Snapshot().Source, " -- 实时编辑") {
					t.Fatal("language selection failed to retain the edited code body")
				}
				captureDesktopTestFrame(t, view, fmt.Sprintf("live-code-%d-dark-%t-python", width, dark))
			})
		}
	}
}
