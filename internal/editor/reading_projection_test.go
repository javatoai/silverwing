package editor

import (
	"strings"
	"testing"
)

func TestReadingProjectionKeepsCodeAndUnknownAnnotations(t *testing.T) {
	const mention = `<!-- mention:{"id":"ou_test","blockId":"block","cn_name":"张三"} -->`
	const image = `![](assets/a.png)<!-- image:{"width":820,"uuid":"abc"} -->`
	const preview = `[需求文档](https://project.feishu.cn/wiki/abc)<!-- linkPreview -->`
	source := "@张三" + mention + "\r\n\r\n" + image + "\r\n\r\n" + preview + "\r\n\r\n<!-- unknown -->\r\n\r\n```md\r\n" + preview + "\r\n" + image + "\r\n```\r\n"
	doc := NewDocument(source, "projection")
	got := ProjectReading(doc.Source())
	if strings.Contains(got.Source, "@张三<!--") || strings.Contains(got.Source, "![](assets/a.png)<!-- image:") && strings.Count(got.Source, "<!-- image:") != 1 {
		t.Fatalf("valid metadata leaked: %q", got.Source)
	}
	if strings.Count(got.Source, "<!-- linkPreview -->") != 1 || strings.Count(got.Source, "<!-- image:") != 1 || !strings.Contains(got.Source, "<!-- unknown -->") {
		t.Fatal("code or unknown annotations were changed")
	}
	if len(got.LinkPreviews) != 1 || got.LinkPreviews[0].Destination != "https://project.feishu.cn/wiki/abc" {
		t.Fatalf("preview metadata = %#v", got.LinkPreviews)
	}
	if doc.Source() != source {
		t.Fatal("projection changed source bytes")
	}
	for _, literal := range []string{"<!-- unknown -->", "```md"} {
		index := strings.Index(got.Source, literal)
		if raw := got.OriginalOffset(index); !strings.HasPrefix(source[raw:], literal) {
			t.Fatalf("source mapping lost %q at %d", literal, raw)
		}
	}
	for _, counterexample := range []string{"`@张三" + mention + "`", "[x](https://example.test) <!-- linkPreview -->", "@李四" + mention, "![](assets/a.png)<!-- unknown -->"} {
		if got := ProjectReading(counterexample); got.Source != counterexample {
			t.Fatalf("nonmetadata changed: %q => %q", counterexample, got.Source)
		}
	}
}

func TestCommentReadingProjectionStripsOnlyASTSpanTokens(t *testing.T) {
	const openOuter = `<span style="background-color: rgb(255, 255, 255)">`
	const openInner = `<span style='font-size: 14px; color: #000000'>`
	const source = "前置\r\n\r\n1. 中文 " + openOuter + openInner + "needle内容</span></span> ![截图](https://example.test/image.png)\r\n\r\n" +
		"行内代码：`<span>代码</span>`\r\n\r\n```html\r\n<span>代码块</span>\r\n```\r\n\r\n" +
		`转义：\<span class="escaped">标签\</span>` + "\r\n\r\n" +
		`未知标签：<div>保留为文本</div>；注释：<!-- <span>不改注释</span> -->` + "\r\n\r\n" +
		"[需求文档](https://project.feishu.cn/wiki/example)<!-- linkPreview -->\r\n"

	doc := NewDocument(source, "comment-reading-projection")
	got := ProjectCommentReading(doc.Source())
	want := strings.Replace(source, openOuter, "", 1)
	want = strings.Replace(want, openInner, "", 1)
	want = strings.Replace(want, "</span>", "", 2)
	want = strings.Replace(want, "<!-- linkPreview -->", "", 1)
	if got.Source != want {
		t.Fatalf("comment projection changed unexpected source bytes:\n got %q\nwant %q", got.Source, want)
	}
	if doc.Source() != source || strings.Contains(got.Source, openOuter) {
		t.Fatal("comment projection changed the source document or left presentation spans")
	}
	if !strings.Contains(got.Source, "![截图](https://example.test/image.png)") ||
		!strings.Contains(got.Source, "`<span>代码</span>`") ||
		!strings.Contains(got.Source, "<span>代码块</span>") ||
		!strings.Contains(got.Source, `\<span class="escaped">标签\</span>`) ||
		!strings.Contains(got.Source, "<div>保留为文本</div>") ||
		!strings.Contains(got.Source, "<!-- <span>不改注释</span> -->") {
		t.Fatal("projection changed an image, code, escaped tag, or unsupported HTML fallback")
	}
	if len(got.LinkPreviews) != 1 || got.LinkPreviews[0].Destination != "https://project.feishu.cn/wiki/example" {
		t.Fatalf("existing link preview projection was lost: %#v", got.LinkPreviews)
	}
	if ProjectReading(source).Source == got.Source || !strings.Contains(ProjectReading(source).Source, openOuter) {
		t.Fatal("comment span handling leaked into ordinary Markdown projection")
	}

	for offset := 0; offset < len(got.Source); offset++ {
		original := got.OriginalOffset(offset)
		if original >= len(source) || source[original] != got.Source[offset] {
			t.Fatalf("original byte mapping failed at projected byte %d: original=%d", offset, original)
		}
	}
	if end := got.OriginalOffset(len(got.Source)); end != len(source) {
		t.Fatalf("end-of-source mapping = %d, want %d", end, len(source))
	}
}

func TestCommentReadingProjectionRecognizesOnlySpanTags(t *testing.T) {
	for _, token := range []string{`<span>`, `</span>`, `<SPAN class="x > y">`, "<span\nstyle=\"color: red\">", `<span/>`} {
		if !isHTMLSpanToken([]byte(token)) {
			t.Errorf("valid span token was not recognized: %q", token)
		}
	}
	for _, token := range []string{`<spanish>`, `<span class=x`, `</span class="x">`, `<div>`, `<!-- <span> -->`} {
		if isHTMLSpanToken([]byte(token)) {
			t.Errorf("unsupported token was treated as a span tag: %q", token)
		}
	}
}

func TestCommentReadingProjectionMultilineSpanAttributes(t *testing.T) {
	for _, token := range []string{"<span\nstyle=\"color: red\">", "<span title=\"中文\n属性\">"} {
		source := "文字 " + token + "正文</span>"
		projection := ProjectCommentReading(source)
		if projection.Source != "文字 正文" {
			t.Errorf("multiline span projection=%q", projection.Source)
		}
		for offset := range len(projection.Source) {
			if source[projection.OriginalOffset(offset)] != projection.Source[offset] {
				t.Fatalf("multiline span offset mapping failed at %d", offset)
			}
		}
	}
}

func TestCommentReadingProjectionReparsesSpanHTMLBlocksSafely(t *testing.T) {
	const outer = `<span class="outer">`
	const inner = `<span style='color: red'>`
	const image = `![截图](https://example.test/image.png)`
	const source = outer + "\r\n" + inner + "\r\n" +
		"1. 中文列表 " + image + "\r\n" +
		"</span>\r\n```html\r\n<span>代码字面量</span>\r\n</span>\r\n```\r\n</span>\r\n\r\n" +
		"<div>\r\n<span>不支持的 HTML 回退</span>\r\n</div>\r\n"

	doc := NewDocument(source, "comment-reading-html-block")
	got := ProjectCommentReading(doc.Source())
	want := strings.Replace(source, outer, "", 1)
	want = strings.Replace(want, inner, "", 1)
	want = strings.Replace(want, "\r\n</span>\r\n```html", "\r\n\r\n```html", 1)
	want = strings.Replace(want, "\r\n</span>\r\n\r\n<div>", "\r\n\r\n\r\n<div>", 1)
	if got.Source != want {
		t.Fatalf("standalone span projection changed unexpected source bytes:\n got %q\nwant %q", got.Source, want)
	}
	if !strings.Contains(got.Source, "1. 中文列表 "+image) ||
		!strings.Contains(got.Source, "<span>代码字面量</span>\r\n</span>") ||
		!strings.Contains(got.Source, "<div>\r\n<span>不支持的 HTML 回退</span>\r\n</div>") {
		t.Fatal("projection changed Markdown content, code literals, or unsupported HTML")
	}
	if doc.Source() != source {
		t.Fatal("comment projection mutated the original document source")
	}
	for offset := 0; offset < len(got.Source); offset++ {
		original := got.OriginalOffset(offset)
		if original >= len(source) || source[original] != got.Source[offset] {
			t.Fatalf("original byte mapping failed at projected byte %d: original=%d", offset, original)
		}
	}
	if end := got.OriginalOffset(len(got.Source)); end != len(source) {
		t.Fatalf("end-of-source mapping = %d, want %d", end, len(source))
	}
}
