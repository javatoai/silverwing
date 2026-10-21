package editor

import (
	"strings"
	"testing"
)

func TestParseAdjacentFeishuLinkPreviewAsNativeLinkNode(t *testing.T) {
	source := "See [需求文档](https://project.feishu.cn/wiki/abc)<!-- linkPreview --> after.\r\n"
	snapshot := Parse(source)
	if snapshot.Source != source {
		t.Fatal("parser changed source bytes")
	}
	part, ok := firstPartOfKind(snapshot.Parts, PartRich)
	if !ok || part.Source != source {
		t.Fatalf("expected the original paragraph to remain a rich part, got %#v", snapshot.Parts)
	}
	preview, ok := parsedNodeOfKind(part.Nodes, "link-preview")
	if !ok {
		t.Fatalf("expected a native link-preview node, got %#v", part.Nodes)
	}
	if preview.Destination != "https://project.feishu.cn/wiki/abc" || preview.Text != "需求文档" {
		t.Fatalf("unexpected preview destination or label: %#v", preview)
	}
	if preview.Range.Start < 0 || preview.Range.End > len(source) || source[preview.Range.Start:preview.Range.End] != "[需求文档](https://project.feishu.cn/wiki/abc)" {
		t.Fatalf("link preview range must point only at the Markdown link: %#v", preview.Range)
	}
	if containsParsedNode(part.Nodes, "html") {
		t.Fatalf("recognized preview marker leaked into rendered nodes: %#v", part.Nodes)
	}
}

func TestParseOnlyAdjacentRealFeishuDocumentLinksAsPreview(t *testing.T) {
	cases := []struct {
		name        string
		source      string
		preview     bool
		code        bool
		preserveRaw bool
	}{
		{name: "space before marker", source: "[doc](https://example.test/doc) <!-- linkPreview -->", preserveRaw: true},
		{name: "autolink", source: "https://example.test/doc<!-- linkPreview -->", preserveRaw: true},
		{name: "local path", source: "[doc](file:///tmp/doc)<!-- linkPreview -->", preserveRaw: true},
		{name: "invalid host", source: "[doc](https:missing-host)<!-- linkPreview -->", preserveRaw: true},
		{name: "unknown comment", source: "[doc](https://example.test/doc)<!-- other -->", preserveRaw: true},
		{name: "fenced example", source: "```md\n[doc](https://example.test/doc)<!-- linkPreview -->\n```", code: true},
		{name: "valid exact annotation", source: "[doc](https://example.test/doc)<!-- linkPreview -->", preview: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			snapshot := Parse(test.source)
			var gotPreview, gotCode, gotRaw bool
			for _, part := range snapshot.Parts {
				gotCode = gotCode || part.Kind == PartCode
				gotRaw = gotRaw || part.Kind == PartOriginal && strings.Contains(part.Source, "<!--")
				gotPreview = gotPreview || containsParsedNode(part.Nodes, "link-preview")
			}
			if gotPreview != test.preview || gotCode != test.code || test.preserveRaw && !gotRaw {
				t.Fatalf("preview=%v code=%v raw=%v, want preview=%v code=%v preserveRaw=%v; parts=%#v", gotPreview, gotCode, gotRaw, test.preview, test.code, test.preserveRaw, snapshot.Parts)
			}
		})
	}
}

func TestParseAdjacentFeishuMentionMetadataAsPlainText(t *testing.T) {
	source := `Reply @王震<!-- mention:{"blockId":"block-1","id":"person-1","cn_name":"王震","en_name":"Wang Zhen"} --> now.`
	snapshot := Parse(source)
	if snapshot.Source != source {
		t.Fatal("parser changed source bytes")
	}
	part, ok := firstPartOfKind(snapshot.Parts, PartRich)
	if !ok || part.Source != source {
		t.Fatalf("expected mention paragraph to remain rich, got %#v", snapshot.Parts)
	}
	if containsParsedNode(part.Nodes, "html") || strings.Contains(parsedInlineText(part.Nodes), "mention:") {
		t.Fatalf("valid mention metadata leaked into rendered text: %#v", part.Nodes)
	}
	if !strings.Contains(parsedInlineText(part.Nodes), "@王震") {
		t.Fatalf("mention label was not kept as normal text: %#v", part.Nodes)
	}
}

func TestParseOnlyValidAdjacentFeishuMentionMetadata(t *testing.T) {
	cases := []struct {
		name        string
		source      string
		preserveRaw bool
		code        bool
	}{
		{name: "invalid identity", source: `@王震<!-- mention:{"blockId":"block-1","cn_name":"王震"} -->`, preserveRaw: true},
		{name: "mismatched visible name", source: `@王震<!-- mention:{"blockId":"block-1","id":"person-1","cn_name":"张三"} -->`, preserveRaw: true},
		{name: "non-adjacent text", source: "@王震 other text <!-- mention:{\"blockId\":\"block-1\",\"id\":\"person-1\",\"cn_name\":\"王震\"} -->", preserveRaw: true},
		{name: "fenced example", source: "```md\n@王震<!-- mention:{\"blockId\":\"block-1\",\"id\":\"person-1\",\"cn_name\":\"王震\"} -->\n```", code: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			snapshot := Parse(test.source)
			var gotRaw, gotCode bool
			for _, part := range snapshot.Parts {
				gotCode = gotCode || part.Kind == PartCode
				gotRaw = gotRaw || part.Kind == PartOriginal && strings.Contains(part.Source, "<!-- mention:")
			}
			if gotRaw != test.preserveRaw || gotCode != test.code {
				t.Fatalf("raw=%v code=%v, want raw=%v code=%v; parts=%#v", gotRaw, gotCode, test.preserveRaw, test.code, snapshot.Parts)
			}
		})
	}
}

func parsedInlineText(nodes []Node) string {
	var out strings.Builder
	for _, node := range nodes {
		if node.Kind == "text" || node.Kind == "link-preview" {
			out.WriteString(node.Text)
		}
		out.WriteString(parsedInlineText(node.Children))
	}
	return out.String()
}

func TestParseNestedFeishuPresentationMetadata(t *testing.T) {
	for name, source := range map[string]string{
		"list link":       "- [需求文档](https://project.feishu.cn/wiki/abc)<!-- linkPreview -->\r\n",
		"emphasized link": "**[需求文档](https://project.feishu.cn/wiki/abc)<!-- linkPreview -->**",
		"list mention":    "- @王震<!-- mention:{\"blockId\":\"block-1\",\"id\":\"person-1\",\"cn_name\":\"王震\"} -->",
	} {
		t.Run(name, func(t *testing.T) {
			snapshot := Parse(source)
			if snapshot.Source != source {
				t.Fatal("parser changed original bytes")
			}
			for _, part := range snapshot.Parts {
				if part.Kind == PartOriginal || containsParsedNode(part.Nodes, "html") {
					t.Fatalf("valid nested metadata leaked into original preview: %#v", snapshot.Parts)
				}
			}
			if strings.Contains(source, "linkPreview") && !containsParsedNode(collectNodes(snapshot.Parts), "link-preview") {
				t.Fatal("nested link did not become a document card")
			}
		})
	}
}
