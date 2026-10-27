package editor

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseFeishuCommentImageUUID(t *testing.T) {
	const link = "https://project.feishu.cn/goapi/v5/platform/file/stream/download/fixture-token"
	const annotation = "<!-- 938E4C1A-D9AE-40CF-870D-992EA4FD0E5C -->"
	image := "![](" + link + ")"
	for _, gap := range []string{"", "\r\n"} {
		source := "已完成验证\r\n\r\n" + image + gap + annotation + "\r\n"
		snapshot := Parse(source)
		if snapshot.Source != source || !reflect.DeepEqual(FeishuCommentImageURLs(source), []string{link}) {
			t.Fatalf("real comment image was not recognized or source changed: %#v", snapshot.Parts)
		}
		for _, part := range snapshot.Parts {
			if part.Kind == PartOriginal || containsParsedNode(part.Nodes, "html") || source[part.Range.Start:part.Range.End] != part.Source {
				t.Fatalf("annotation leaked into preview or ranges changed: %#v", part)
			}
		}
	}
	// Meegle strips the adjacent upload annotation when returning SW comments.
	// The rendered image and its strictly recognized download URL are enough;
	// HTML metadata is not a file-authorization credential.
	for _, source := range []string{image, "# 你好\n\n" + image, image + "\n\n" + annotation} {
		if urls := FeishuCommentImageURLs(source); !reflect.DeepEqual(urls, []string{link}) {
			t.Errorf("rendered unannotated comment image missing: %q", source)
		}
	}
	for name, source := range map[string]string{
		"invalid UUID":      image + "<!-- 938E4C1A-D9AE-40CF-870D-short -->",
		"unknown HTML":      image + "<!-- keep me -->",
		"intervening text":  image + "text" + annotation,
		"other host":        "![](https://example.invalid/image.png)" + annotation,
		"HTTP":              "![](http://project.feishu.cn/goapi/v5/platform/file/stream/download/token)",
		"other route":       "![](https://project.feishu.cn/private/token)",
		"userinfo":          "![](https://user:pass@project.feishu.cn/goapi/v5/platform/file/stream/download/token)",
		"extra path":        "![](" + link + "/more)",
		"encoded slash":     "![](" + link + "%2fmore)",
		"encoded backslash": "![](" + link + "%5cmore)",
		"fragment":          "![](" + link + "#fragment)",
		"plain URL":         link + annotation,
		"fenced":            "```md\n" + image + annotation + "\n```",
		"indented":          "    " + image + annotation,
		"inline":            "`" + image + annotation + "`",
		"escaped":           "\\" + image + annotation,
	} {
		t.Run(name, func(t *testing.T) {
			if urls := FeishuCommentImageURLs(source); len(urls) != 0 {
				t.Fatalf("non-image example authorized download: %d", len(urls))
			}
			if got := Parse(source); got.Source != source {
				t.Fatal("source changed")
			}
		})
	}
}

func TestParseAdjacentFeishuImageMetadataAsImage(t *testing.T) {
	// Feishu emits the metadata comment on the same line as the Markdown image.
	// Keep CRLF here to prove byte-for-byte ranges survive line normalization.
	source := "![](assets/x.jpg)<!-- image:{\"width\":820,\"uuid\":\"abc-123\"} -->\r\n"
	snapshot := Parse(source)
	if snapshot.Source != source {
		t.Fatal("parser changed source bytes")
	}
	imagePart, ok := firstPartOfKind(snapshot.Parts, PartImage)
	if !ok {
		t.Fatalf("expected image part, got %#v", snapshot.Parts)
	}
	if !strings.Contains(imagePart.Source, `<!-- image:{"width":820,"uuid":"abc-123"} -->`) {
		t.Fatal("image metadata was removed from source")
	}
	if !containsParsedNode(imagePart.Nodes, "image") || containsParsedNode(imagePart.Nodes, "html") {
		t.Fatalf("metadata should be hidden while preserving the image node: %#v", imagePart.Nodes)
	}
	imageNode, ok := parsedNodeOfKind(imagePart.Nodes, "image")
	if !ok || imageNode.Range.Start < 0 || imageNode.Range.End > len(source) || source[imageNode.Range.Start:imageNode.Range.End] != "![](assets/x.jpg)" {
		t.Fatalf("image node range no longer maps to exact Markdown bytes: %#v", imageNode)
	}
	for _, part := range snapshot.Parts {
		if part.Kind == PartOriginal {
			t.Fatalf("whitelisted image annotation was rendered as original Markdown: %#v", part)
		}
		if part.Range.Start < 0 || part.Range.End < part.Range.Start || part.Range.End > len(source) || source[part.Range.Start:part.Range.End] != part.Source {
			t.Fatalf("part range no longer maps to exact source: %#v", part)
		}
	}
}

func TestParseOnlyWhitelistedAdjacentImageMetadata(t *testing.T) {
	cases := []struct {
		name                  string
		source                string
		kind                  PartKind
		preservedOriginal     string
		wholeSourceOriginal   bool
		preservesRealImage    bool
		forbidsImageRendering bool
	}{
		{
			name: "unrelated comment", source: "![x](https://example.test/x.png)\n<!-- keep me -->",
			kind: PartImage, preservedOriginal: "<!-- keep me -->", preservesRealImage: true,
		},
		{
			name:   "unknown metadata field on image line",
			source: "![](assets/x.jpg)<!-- image:{\"width\":820,\"uuid\":\"abc\",\"extra\":true} -->",
			kind:   PartOriginal, wholeSourceOriginal: true,
		},
		{
			name:   "non-adjacent comment",
			source: "![x](https://example.test/x.png)\nnot metadata\n<!-- image:{\"width\":820,\"uuid\":\"abc\"} -->",
			kind:   PartImage, preservedOriginal: "<!-- image:{\"width\":820,\"uuid\":\"abc\"} -->", preservesRealImage: true,
		},
		{
			name: "fenced example", source: "```md\n![x](https://example.test/x.png)\n<!-- image:{\"width\":820,\"uuid\":\"abc\"} -->\n```\n",
			kind: PartCode, forbidsImageRendering: true,
		},
		{
			name: "indented code example", source: "    ![x](https://example.test/x.png)\n    <!-- image:{\"width\":820,\"uuid\":\"abc\"} -->\n",
			kind: PartOriginal, preservedOriginal: "![x](https://example.test/x.png)\n    <!-- image:{\"width\":820,\"uuid\":\"abc\"} -->\n", forbidsImageRendering: true,
		},
		{
			name: "inline code example", source: "`![x](https://example.test/x.png) <!-- image:{\"width\":820,\"uuid\":\"abc\"} -->`\n",
			kind: PartOriginal, preservedOriginal: "`![x](https://example.test/x.png) <!-- image:{\"width\":820,\"uuid\":\"abc\"} -->`\n", forbidsImageRendering: true,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			snapshot := Parse(test.source)
			if snapshot.Source != test.source {
				t.Fatal("parser changed source bytes")
			}
			part, ok := firstNonEmptyPart(snapshot.Parts)
			if !ok || part.Kind != test.kind {
				t.Fatalf("expected %s, got %#v", test.kind, part)
			}
			if test.wholeSourceOriginal && (part.Kind != PartOriginal || part.Source != test.source) {
				t.Fatalf("invalid same-line metadata should preserve the entire original source, got %#v", part)
			}
			if test.preservedOriginal != "" {
				assertOriginalPartSource(t, snapshot.Parts, test.source, test.preservedOriginal)
			}
			if test.preservesRealImage {
				if _, ok := firstPartOfKind(snapshot.Parts, PartImage); !ok {
					t.Fatalf("valid Markdown image should remain an image part: %#v", snapshot.Parts)
				}
			}
			if test.forbidsImageRendering {
				for _, parsed := range snapshot.Parts {
					if parsed.Kind == PartImage || containsParsedNode(parsed.Nodes, "image") {
						t.Fatalf("code example was mistaken for a real image: %#v", parsed)
					}
				}
			}
		})
	}
}

func assertOriginalPartSource(t *testing.T, parts []Part, document, expected string) {
	t.Helper()
	for _, part := range parts {
		if part.Kind != PartOriginal || part.Source != expected {
			continue
		}
		if part.Range.Start < 0 || part.Range.End < part.Range.Start || part.Range.End > len(document) || document[part.Range.Start:part.Range.End] != expected {
			t.Fatalf("original part range does not map to the exact preserved comment: %#v", part)
		}
		return
	}
	t.Fatalf("expected exact original part %q, got %#v", expected, parts)
}

func firstPartOfKind(parts []Part, kind PartKind) (Part, bool) {
	for _, part := range parts {
		if part.Kind == kind && part.Source != "" {
			return part, true
		}
	}
	return Part{}, false
}

func firstNonEmptyPart(parts []Part) (Part, bool) {
	for _, part := range parts {
		if strings.TrimSpace(part.Source) != "" {
			return part, true
		}
	}
	return Part{}, false
}

func containsParsedNode(nodes []Node, kind string) bool {
	for _, node := range nodes {
		if node.Kind == kind || containsParsedNode(node.Children, kind) {
			return true
		}
	}
	return false
}

func parsedNodeOfKind(nodes []Node, kind string) (Node, bool) {
	for _, node := range nodes {
		if node.Kind == kind {
			return node, true
		}
		if nested, ok := parsedNodeOfKind(node.Children, kind); ok {
			return nested, true
		}
	}
	return Node{}, false
}
