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
