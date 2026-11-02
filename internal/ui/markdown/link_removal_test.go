package markdownui

import (
	"strings"
	"testing"

	code "github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown/parser"
)

func TestUnlinkPreservesHTMLAcrossInlineDelimiterCombinations(t *testing.T) {
	const destination = "](https://example.com)"
	boundaries := []string{"", "*", "**", "***", "_", "__", "~~", "word ", "\\\\", "(", "[", "[[", "](/other)"}
	labels := []string{"x", "x*", "*x*", "**x**", "x_", "_x_", "~~x~~", "**x***", "x\\*", "x\\\\*", "x&#42;", "`x*`", "**A [B]**"}
	checked := 0
	for _, prefix := range boundaries {
		for _, label := range labels {
			for _, suffix := range boundaries {
				source := prefix + "[" + label + destination + suffix + "\n\nuntouched"
				doc := parser.Parse(source)
				var target *parser.Inline
				for block := 1; block < doc.Len(); block++ {
					for _, in := range doc.Inlines(block) {
						if in.Kind == parser.Link && !in.Auto && in.Start == len(prefix) && in.End == len(prefix)+1+len(label)+len(destination) {
							copy := in
							target = &copy
						}
					}
				}
				if target == nil {
					continue // surrounding syntax can make the input not a link
				}
				checked++
				edit, err := unlinkInContext(code.NewBufferPreservingLineEndings(source).Snapshot(), target.Start, target.End, label)
				if err != nil {
					t.Fatalf("cannot unwrap valid link %q: %v", source, err)
				}
				got := source[:edit.start] + edit.text + source[edit.end:]
				wantHTML := strings.NewReplacer("<a href=\"https://example.com\">", "", "</a>", "").Replace(doc.HTML())
				gotHTML := strings.ReplaceAll(parser.Parse(got).HTML(), "<i></i>", "")
				if gotHTML != wantHTML {
					t.Fatalf("unlink %q:\n got %q\nwant %q\nsource %q", source, gotHTML, wantHTML, got)
				}
			}
		}
	}
	if checked < 1000 {
		t.Fatalf("insufficient actual links checked: %d", checked)
	}
	t.Logf("checked %d valid links", checked)
}
