package richtext

import (
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestNestedQuoteListKeepsBothDepths(t *testing.T) {
	const src = `<blockquote><blockquote><ul><li>first<ul><li>second</li></ul></li></ul><blockquote><p>third</p></blockquote></blockquote></blockquote>`
	text := ParseHTML(src)
	assert := func(t *testing.T, text Text) {
		t.Helper()
		wantQuotes := []int{2, 2, 3}
		wantLists := []int{0, 1, 0}
		i := 0
		for _, p := range text.Paragraphs() {
			if i >= len(wantQuotes) {
				t.Fatal("extra paragraph")
			}
			if p.Style.quoteDepth() != wantQuotes[i] || p.Style.Indent != wantLists[i] {
				t.Errorf("paragraph %d: %+v", i, p.Style)
			}
			i++
		}
		if i != 3 {
			t.Fatalf("paragraphs=%d", i)
		}
	}
	assert(t, text)
	assert(t, ParseHTML(text.HTML()))
	assert(t, ParseMarkdown(text.Markdown()))
	data, err := json.Marshal(text)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Text
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	assert(t, decoded)
}

func TestQuoteDepthSurvivesSplitJoinAndUndo(t *testing.T) {
	for _, at := range []int{0, 2, 4} {
		t.Run(string(rune('0'+at)), func(t *testing.T) {
			d := NewDocument(TextOf(Paragraph{Style: ParagraphStyle{Quote: true, QuoteDepth: 3, List: Bullets, Indent: 1}, Runs: []Run{{Text: "abcd"}}}))
			d.SetSelection(at, at)
			d.InsertText("\n")
			for _, p := range d.Text().Paragraphs() {
				if p.Style.quoteDepth() != 3 || p.Style.Indent != 1 || p.Style.List != Bullets {
					t.Fatalf("split style=%+v", p.Style)
				}
			}
			d.Undo()
			if got := d.Text().String(); got != "abcd" {
				t.Fatalf("undo=%q", got)
			}
			d.Redo()
			d.SetSelection(at, at+1)
			d.InsertText("")
			if d.Text().ParagraphCount() != 1 || d.Text().ParagraphStyleAt(0).quoteDepth() != 3 {
				t.Fatal("join lost quote depth")
			}
			d.ToggleList(Numbers)
			if d.Text().ParagraphStyleAt(0).quoteDepth() != 3 {
				t.Fatal("list formatting changed quote depth")
			}
			d.ToggleQuote()
			if p := d.Text().ParagraphStyleAt(0); p.Quote || p.QuoteDepth != 0 {
				t.Fatalf("quote off=%+v", p)
			}
			d.Undo()
			if d.Text().ParagraphStyleAt(0).quoteDepth() != 3 {
				t.Fatal("undo lost quote depth")
			}
		})
	}
}

func TestQuoteDepthIsIndependentOfListIndentInLayout(t *testing.T) {
	v := &view{}
	v.look.indent = 20
	shallow := ParagraphStyle{Quote: true, List: Bullets, Indent: 2}
	deep := shallow
	deep.QuoteDepth = 3
	a, _ := v.insets(shallow)
	b, _ := v.insets(deep)
	if b-a != 30 {
		t.Fatalf("quote inset delta=%v", b-a)
	}
	if count := strings.Count(TextOf(Paragraph{Style: deep, Runs: []Run{{Text: "x"}}}).HTML(), "<blockquote>"); count != 3 {
		t.Fatalf("quote count=%d", count)
	}
}

func TestLeavingANestedQuoteClearsItsDepth(t *testing.T) {
	for _, key := range []ui.Key{ui.KeyBackspace, ui.KeyEnter} {
		doc := NewDocument(TextOf(Paragraph{Style: ParagraphStyle{Quote: true, QuoteDepth: 3}}))
		tt, _ := richTester(t, doc)
		tt.Key(0, key)
		if st := doc.Text().ParagraphStyleAt(0); st.Quote || st.QuoteDepth != 0 {
			t.Fatalf("key %v left style %+v", key, st)
		}
		doc.ToggleQuote()
		if st := doc.Text().ParagraphStyleAt(0); st.quoteDepth() != 1 {
			t.Fatalf("new quotation restored old depth: %+v", st)
		}
	}
}

func TestQuoteDepthKeepsLegacyQuotes(t *testing.T) {
	legacy := TextOf(Paragraph{Style: ParagraphStyle{Quote: true}, Runs: []Run{{Text: "quoted"}}})
	for _, depth := range []int{-1, 0, 1} {
		text := TextOf(Paragraph{Style: ParagraphStyle{Quote: true, QuoteDepth: depth}, Runs: []Run{{Text: "quoted"}}})
		if text.HTML() != legacy.HTML() || text.Markdown() != legacy.Markdown() {
			t.Fatalf("depth %d changed the legacy quotation", depth)
		}
	}
	const src = "> > first\n>\n> second\n>\n> > <blockquote><p>third</p></blockquote>"
	text := ParseMarkdown(src)
	for _, roundtrip := range []Text{text, ParseHTML(text.HTML()), ParseMarkdown(text.Markdown())} {
		i := 0
		for _, p := range roundtrip.Paragraphs() {
			if i >= 3 || p.Style.quoteDepth() != []int{2, 1, 3}[i] {
				t.Fatalf("paragraph %d: %+v", i, p.Style)
			}
			i++
		}
		if i != 3 {
			t.Fatalf("paragraph count=%d", i)
		}
	}
}
