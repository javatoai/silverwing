package editor

import (
	"github.com/egoist/mygo/ui/markdown/parser"
	"strings"
	"testing"
)

func TestClipboardHTMLIncomingFormattingTablesAndLiteralCode(t *testing.T) {
	source := "<h2>标题</h2><p>Hello <strong>粗体</strong> world <a href=\"https://example.com/a\">链接</a></p><table><tr><th>A</th><th>B</th></tr><tr><td>中文😀</td><td>1|2</td></tr></table><pre><code class=\"language-sql\">SELECT 1;\n\n\nSELECT 2;</code></pre><script>SECRET_SCRIPT</script>"
	got, err := ClipboardHTMLToMarkdown(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"## 标题", "Hello **粗体** world", "[链接](<https://example.com/a>)", "| A | B |", "1\\|2", "```sql\nSELECT 1;\n\n\nSELECT 2;\n```"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, "SECRET_SCRIPT") {
		t.Fatal("script body imported")
	}
}

func TestClipboardHTMLLiteralEntitiesAndCodeFallbackKeepOriginalCharacters(t *testing.T) {
	for _, body := range []string{" a\\|b\nc\t ", "a\\|b &copy; &#32;"} {
		encoded := strings.ReplaceAll(body, "&", "&amp;")
		got, err := ClipboardHTMLToMarkdown("<table><tr><th>A</th></tr><tr><td><code>" + encoded + "</code></td></tr></table>")
		if err != nil {
			t.Fatal(err)
		}
		d := parser.Parse(got)
		var cells []string
		for b := 1; b < d.Len(); b++ {
			if d.Block(b).Kind == parser.TableCell {
				var text strings.Builder
				for _, in := range d.Inlines(b) {
					if in.Kind == parser.Text || in.Kind == parser.Code {
						text.WriteString(in.Text)
					}
				}
				cells = append(cells, text.String())
			}
		}
		if len(cells) != 2 || cells[1] != body {
			t.Fatalf("literal fallback changed code characters: %q => %q", body, cells)
		}
	}
	got, err := ClipboardHTMLToMarkdown("<p>&amp;copy; <strong>&amp;copy;</strong> <a href=\"https://example.com/a&amp;copy;\">&amp;copy;</a><img src=\"a&amp;copy;.png\" alt=\"&amp;copy;\"></p>")
	if err != nil {
		t.Fatal(err)
	}
	d := parser.Parse(got)
	var text strings.Builder
	link, image := false, false
	for b := 1; b < d.Len(); b++ {
		for _, in := range d.Inlines(b) {
			if in.Kind == parser.Text {
				text.WriteString(in.Text)
			}
			link = link || in.Kind == parser.Link && in.Dest == "https://example.com/a&copy;"
			image = image || in.Kind == parser.Image && in.Dest == "a&copy;.png"
		}
	}
	if strings.Contains(text.String(), "©") || !strings.Contains(text.String(), "&copy;") || !link || !image {
		t.Fatalf("double-decoded incoming entities: %q %q", got, text.String())
	}
}

func TestClipboardHTMLOrderedListCanStartAtZero(t *testing.T) {
	source, err := ClipboardHTMLToMarkdown("<ol start=\"0\"><li>zero</li><li>one</li></ol>")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(source, "0. zero\n1. one") {
		t.Fatalf("lost HTML starting number: %q", source)
	}
	d := parser.Parse(source)
	found := false
	for b := 1; b < d.Len(); b++ {
		if d.Block(b).Kind == parser.List && d.Block(b).Ordered && d.Block(b).Number == 0 {
			found = true
		}
	}
	if !found {
		t.Fatal("zero list was not parsed as ordered")
	}
}

func TestClipboardHTMLPreservesInlineCodeCellBoundariesAndWordSpaces(t *testing.T) {
	fragment := "<table><tr><th>A</th><th>B</th></tr><tr><td><code>a|b</code></td><td>c</td></tr></table>"
	got, err := ClipboardHTMLToMarkdown(fragment)
	if err != nil {
		t.Fatal(err)
	}
	d := parser.Parse(got)
	var cells []string
	for i := 1; i < d.Len(); i++ {
		if d.Block(i).Kind == parser.TableCell {
			var text strings.Builder
			for _, in := range d.Inlines(i) {
				if in.Kind == parser.Text || in.Kind == parser.Code {
					text.WriteString(in.Text)
				}
			}
			cells = append(cells, text.String())
		}
	}
	if strings.Join(cells, ",") != "A,B,a|b,c" {
		t.Fatalf("lost table data: %q => %q", got, cells)
	}
	for _, body := range []string{"`a`", " a ", "a`", "`a"} {
		got, err = ClipboardHTMLToMarkdown("<p><code>" + body + "</code></p>")
		if err != nil {
			t.Fatal(err)
		}
		d = parser.Parse(got)
		found := false
		for i := 1; i < d.Len(); i++ {
			for _, in := range d.Inlines(i) {
				if in.Kind == parser.Code && in.Text == body {
					found = true
				}
			}
		}
		if !found {
			t.Fatalf("inline literal changed: %q => %q", body, got)
		}
	}
	for _, tag := range []string{"strong", "em", "s"} {
		got, err = ClipboardHTMLToMarkdown("Hello<" + tag + "> world </" + tag + ">again")
		if err != nil {
			t.Fatal(err)
		}
		d = parser.Parse(got)
		var text strings.Builder
		for i := 1; i < d.Len(); i++ {
			for _, in := range d.Inlines(i) {
				if in.Kind == parser.Text {
					text.WriteString(in.Text)
				}
			}
		}
		if text.String() != "Hello world again" {
			t.Fatalf("word spaces lost with %s: %q => %q", tag, got, text.String())
		}
	}
}

func TestClipboardHTMLNestedOrderedListKeepsChildOwnership(t *testing.T) {
	got, err := ClipboardHTMLToMarkdown("<ol start=\"9\"><li>parent<ul><li>child</li></ul></li></ol>")
	if err != nil {
		t.Fatal(err)
	}
	d := parser.Parse(got)
	parent, child := -1, -1
	for i := 1; i < d.Len(); i++ {
		b := d.Block(i)
		if b.Kind == parser.Item && b.Ordered {
			parent = i
		}
		if b.Kind == parser.Paragraph && strings.Contains(d.Lines(i), "child") {
			child = i
		}
	}
	if parent < 0 || child < 0 {
		t.Fatal("lost list text")
	}
	for child > 0 && child != parent {
		child = d.Block(child).Parent
	}
	if child != parent {
		t.Fatalf("HTML nested child escaped parent: %q", got)
	}
}

func TestClipboardHTMLUnrepresentableCodeCellKeepsAllLiteralText(t *testing.T) {
	// A code cell with an odd literal slash before | cannot retain both the
	// slash and a single code span under GFM's pre-inline pipe unescape.
	// The promised fallback is text preservation, never silent character loss.
	for _, body := range []string{`a\|b`, `a\\\|b`, ` a\|b `, "\ta\\|b\t"} {
		got, err := ClipboardHTMLToMarkdown("<table><tr><th>A</th></tr><tr><td><code>" + body + "</code></td></tr></table>")
		if err != nil {
			t.Fatal(err)
		}
		d := parser.Parse(got)
		var text strings.Builder
		for i := 1; i < d.Len(); i++ {
			if d.Block(i).Kind == parser.TableCell && !d.Block(d.Block(i).Parent).Header {
				for _, in := range d.Inlines(i) {
					if in.Kind == parser.Text || in.Kind == parser.Code {
						text.WriteString(in.Text)
					}
				}
			}
		}
		if text.String() != body {
			t.Fatalf("fallback lost literal code cell data: %q -> %q (%q)", body, text.String(), got)
		}
	}
}
