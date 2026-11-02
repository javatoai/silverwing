package markdown

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown/parser"
)

func TestCodeCardBottomRoomBalancesHeaderWithoutChangingSource(t *testing.T) {
	for _, mode := range []string{"reader", "live", "readonly"} {
		for _, wrap := range []bool{false, true} {
			for _, size := range []float32{12, 18} {
				for _, content := range []string{"", "111111", "111111\n222222\n333333\n444444", "111111\n222222\n"} {
					t.Run(fmt.Sprintf("%s/wrap-%t/font-%g/body-%q", mode, wrap, size, content), func(t *testing.T) {
						source := "```text\n" + content + "\n```"
						if content == "" {
							source = "```text\n```"
						}
						source = "\ufeff" + strings.ReplaceAll(source, "\n", "\r\n")
						options := &Options{WrapCode: wrap, Theme: &Theme{FontSize: size}}
						var view *ui.Tester
						var state *EditorState
						if mode == "reader" {
							view, _ = rvView(&source, options)
						} else {
							state, view = liveTester(source, 0, EditorOptions{Options: options, InPlaceCode: true, ReadOnly: mode == "readonly"})
						}
						view.SetLocale("en")
						card, cardOK := view.Find("Code, text")
						shown := content
						if shown == "" {
							shown = " "
						}
						body, bodyOK := view.Find(shown)
						if !cardOK || !bodyOK {
							t.Fatalf("missing code geometry: card=%t body=%t labels=%q", cardOK, bodyOK, view.Texts())
						}
						top := body.Y - card.Y
						bottom := card.Y + card.H - body.Y - body.H
						if bottom+2 < top*.75 {
							t.Fatalf("code has room above but is cramped below: top=%g bottom=%g card=%+v body=%+v", top, bottom, card, body)
						}
						if body.X < card.X || body.X+body.W > card.X+card.W+.5 {
							t.Fatal("bottom room changed horizontal code containment")
						}
						if err := view.Click("Copy code"); err != nil {
							t.Fatal(err)
						}
						parsed := parser.Parse(normalizedText(source))
						if got, want := view.Clipboard(), parsed.Lines(1); got != want {
							t.Fatalf("layout changed copied code: %q != %q", got, want)
						}
						if state != nil && (state.Buffer().OriginalText() != source || state.Buffer().Version() != 0) {
							t.Fatal("layout or copy inserted blank source lines/history")
						}
					})
				}
			}
		}
	}
}
