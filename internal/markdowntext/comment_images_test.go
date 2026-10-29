package markdowntext

import (
	"strings"
	"testing"
)

const commentTestToken = "938E4C1A-D9AE-40CF-870D-2BE95C99DA0B"
const commentTestURL = "https://project.feishu.cn/goapi/v5/platform/file/stream/download/" + commentTestToken

func TestCommentImagesKeepOriginalRangesAndExcludeExamples(t *testing.T) {
	image := "![真实图片](" + commentTestURL + ` "标题")`
	source := "\xef\xbb\xbf---\r\ncover: ![metadata](metadata.png)\r\n---\r\n\r\n" +
		"中文前缀 " + image + "<!--" + commentTestToken + "-->\r\n\r\n" +
		"```markdown\r\n![fenced](fenced.png)\r\n```\r\n\r\n" +
		"    ![indented](indented.png)\r\n\r\n" +
		"`![inline](inline.png)`\r\n\r\n" +
		"\\![escaped](escaped.png)\r\n\r\n" +
		"<div>![opaque](opaque.png)</div>\r\n"
	images := CommentImages(source)
	if len(images) != 1 {
		t.Fatalf("expected only the rendered image, got %#v", images)
	}
	node := images[0]
	if node.Destination != commentTestURL || node.Title != "标题" || source[node.Range.Start:node.Range.End] != image {
		t.Fatalf("image lost its original UTF-8 byte range or attributes: %#v", node)
	}
	if got := RewriteCommentImages(source, nil); got != source {
		t.Fatal("an empty replacement changed BOM, CRLF, or untouched source")
	}
	for _, example := range []string{"fenced.png", "indented.png", "inline.png", "escaped.png", "opaque.png", "metadata.png"} {
		replacement := "changed.png"
		if got := RewriteCommentImages(source, map[string]*string{example: &replacement}); got != source {
			t.Fatalf("an image example authorized rewriting %q", example)
		}
	}
}

func TestRewriteCommentImagesPreservesBytesAndPairsOnlyOwnedAnnotations(t *testing.T) {
	image := "![图片](" + commentTestURL + ` "原始标题")`
	matching := "<!--" + commentTestToken + "-->"
	foreign := "<!--00000000-0000-0000-0000-000000000000-->"
	replacement := "https://example.test/new-image.png"
	for _, test := range []struct {
		name, gap, annotation, tail string
		consumeAnnotation           bool
	}{
		{name: "same line", annotation: matching, tail: "\r\n后续文字  ", consumeAnnotation: true},
		{name: "next line", gap: " \r\n\t", annotation: matching, tail: "\r\n后续文字  ", consumeAnnotation: true},
		{name: "foreign annotation", annotation: foreign, tail: "\r\n后续文字  "},
		{name: "separated annotation", gap: "\r\n\r\n", annotation: matching, tail: "\r\n后续文字  "},
	} {
		t.Run(test.name, func(t *testing.T) {
			prefix := "原始前缀  \r\n\r\n"
			source := prefix + image + test.gap + test.annotation + test.tail
			wantTail := test.gap + test.annotation + test.tail
			if test.consumeAnnotation {
				wantTail = test.tail
			}
			got := RewriteCommentImages(source, map[string]*string{commentTestURL: &replacement}, map[string]string{commentTestURL: "new-upload-token"})
			want := prefix + strings.Replace(image, commentTestURL, replacement, 1) + "<!--new-upload-token-->" + wantTail
			if got != want {
				t.Fatalf("rewrite changed bytes outside its image/annotation: got %q, want %q", got, want)
			}
			if got := RewriteCommentImages(source, map[string]*string{commentTestURL: nil}); got != prefix+wantTail {
				t.Fatalf("removal consumed unrelated bytes: %q", got)
			}
			unchanged := commentTestURL
			if got := RewriteCommentImages(source, map[string]*string{commentTestURL: &unchanged}); got != source {
				t.Fatalf("same-destination rewrite normalized source: %q", got)
			}
		})
	}
}

func TestParseDocumentAndReadingProjectionPreserveSourceBoundaries(t *testing.T) {
	source := "\xef\xbb\xbf---\r\n标题: 保留\r\n---\r\n\r\n" +
		"# 标题\r\n\r\n| 名称 | 值 |\r\n| --- | --- |\r\n| 图片 | `a|b` |\r\n\r\n" +
		"```text\r\n原始内容  \r\n\r\n```\r\n"
	parts, _ := ParseDocument(source)
	var joined strings.Builder
	for _, part := range parts {
		if part.Range.Start < 0 || part.Range.End < part.Range.Start || part.Range.End > len(source) || source[part.Range.Start:part.Range.End] != part.Source {
			t.Fatalf("part escaped its original byte range: %#v", part)
		}
		joined.WriteString(part.Source)
	}
	if joined.String() != source {
		t.Fatal("parsing normalized or omitted original source bytes")
	}
	tableSource := "| 名称 | 值 |\r\n| --- | --- |\r\n| 图片 | `a|b` |\r\n"
	cells := TableCells(tableSource)
	if len(cells) != 2 || len(cells[1]) != 2 || tableSource[cells[1][1].Range.Start:cells[1][1].Range.End] != "`a|b`" {
		t.Fatalf("table source ranges split code or UTF-8 text: %#v", cells)
	}
	annotated := "前缀 ![](" + commentTestURL + ")<!--" + commentTestToken + "--> 后缀\r\n"
	projection := ProjectReading(annotated)
	if projection.Source != strings.Replace(annotated, "<!--"+commentTestToken+"-->", "", 1) || len(projection.Removed) != 1 {
		t.Fatalf("reading projection changed more than its recognized annotation: %#v", projection)
	}
	removed := projection.Removed[0]
	if annotated[removed.Start:removed.End] != "<!--"+commentTestToken+"-->" || projection.OriginalOffset(removed.Start) != removed.End {
		t.Fatalf("projection offsets no longer map to the original source: %#v", projection)
	}
}
