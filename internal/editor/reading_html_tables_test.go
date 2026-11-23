package editor

import (
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/egoist/mygo/ui/markdown/parser"
)

func TestProjectHTMLTablesReadsMixedFormatting(t *testing.T) {
	const before = "# 需求原文\r\n\r\n"
	const table = "<table title='a > b'>\r\n<thead><tr><th>\r\n\r\n**规则编号**\r\n\r\n</th><th>处理结果</th></tr></thead>\r\n" +
		"<tbody><tr><td><strong>BR-01</strong></td><td>中文甲 | 中文乙<br>[详情](<https://example.test/one>) 与 " +
		"<a href='https://example.test/two'>说明</a></td></tr></tbody>\r\n</table>"
	const after = "\r\n\r\n后文 [原链接](https://example.test/original)\r\n"
	source := before + table + after
	projected, original := ProjectHTMLTables(source)
	if original == nil || !strings.HasPrefix(projected, before) || !strings.HasSuffix(projected, after) {
		t.Fatalf("projection did not preserve the surrounding Markdown: %q", projected)
	}
	d := parser.Parse(projected)
	var cells, links []string
	var tables int
	for id := 1; id < d.Len(); id++ {
		if d.Block(id).Kind == parser.Table {
			tables++
		}
		if d.Block(id).Kind == parser.TableCell {
			var cell strings.Builder
			for _, inline := range d.Inlines(id) {
				if inline.Kind == parser.Text || inline.Kind == parser.Code {
					cell.WriteString(inline.Text)
				}
				if inline.Kind == parser.Link {
					links = append(links, inline.Dest)
				}
			}
			cells = append(cells, cell.String())
		}
	}
	if tables != 1 || !slices.Equal(cells, []string{"规则编号", "处理结果", "BR-01", "中文甲 | 中文乙详情 与 说明"}) ||
		!slices.Equal(links, []string{"https://example.test/one", "https://example.test/two"}) || !strings.Contains(projected, "**规则编号**") {
		t.Fatalf("unexpected native table: %d, %q, %q in %s", tables, cells, links, projected)
	}
	if original(0) != 0 || original(len(projected)) != len(source) {
		t.Fatal("whole Markdown copy no longer covers the original")
	}
	for _, fragment := range []string{"规则编号", "BR-01", "中文乙", "详情", "说明", "后文", "原链接"} {
		at := strings.Index(projected, fragment)
		start, end := original(at), original(at+len(fragment))
		if got := source[start:end]; got != fragment {
			t.Errorf("selection %q mapped to %q", fragment, got)
		}
	}
	last := 0
	for at := range len(projected) {
		if got := original(at); got < last || got < 0 || got > len(source) ||
			utf8.RuneStart(projected[at]) && got < len(source) && !utf8.RuneStart(source[got]) {
			t.Fatalf("invalid source offset %d at %d", got, at)
		} else {
			last = got
		}
	}
}

func TestProjectHTMLTablesExcludesUnsupportedAndLiteralTables(t *testing.T) {
	for _, source := range []string{
		"| Existing | Table |\n|---|---|\n| 1 | 2 |",
		"```html\n<table><tr><td>fenced</td></tr></table>\n```",
		"    <table><tr><td>indented code</td></tr></table>",
		"\\<table><tr><td>escaped example</td></tr></table>",
		"`<table><tr><td>inline code</td></tr></table>`",
		"<table><tr><td>unterminated table</td></tr>",
		"<table>\n\n<table><tr><td>nested under an unclosed table</td></tr></table>",
		"<table><tr><td>missing cell end</tr></table>",
		"<table><tr><td colspan='2'>merged cell</td></tr></table>",
		"<table><tr><td rowspan='2'>merged cell</td></tr></table>",
		"<table>\n<tr><td>\n\n<table><tr><td>nested</td></tr></table>\n</td></tr></table>",
		"<table><tr><td>same-line suffix</td></tr></table> text",
		"<table><tr><td><script>Example</script></td></tr></table>",
	} {
		if got, original := ProjectHTMLTables(source); got != source || original != nil {
			t.Errorf("changed unsupported or literal source %q to %q", source, got)
		}
	}
}
