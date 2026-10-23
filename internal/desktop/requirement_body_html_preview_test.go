package desktop

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown/parser"
)

func TestRequirementBodyHTMLProjectionPreservesSource(t *testing.T) {
	const source = "# 测试数据\r\n\r\n订单号：<span style=\"background-color: rgb(255, 255, 255)\"><span style=\"font-size: 12px\"><span style=\"color: #515A6E\">**ORDER-001**</span></span></span>\r\n\r\n卡号：<span\r\n style='background-color: rgb(235, 247, 255)'>CARD-002</span>\r\n"
	const want = "# 测试数据\r\n\r\n订单号：**ORDER-001**\r\n\r\n卡号：CARD-002\r\n"
	s := &app{page: "requirements"}
	reader := s.requirementReader("task", "https://project.feishu.cn/test/bug/detail/123")
	t.Cleanup(s.closeRequirementReaders)
	s.updateRequirementReaderBody(reader, application.RequirementContent{Body: source})
	state := reader.bodyMarkdown
	native := ensureFrameworkMarkdown(state)
	if got := native.projection.Source; got != want {
		t.Fatalf("body HTML wrappers leaked into the reading projection:\n got %q\nwant %q", got, want)
	}
	if state.commentReading {
		t.Fatal("body incorrectly enabled comment image identity handling")
	}
	if reader.body.Body != source || state.doc.Source() != source || native.original != source || state.doc.Snapshot().Version != 0 {
		t.Fatal("reading projection modified the original body")
	}
	if hits, _ := documentFindHits(requirementFindBlocksForReader(reader, false), "ORDER-001", 10); len(hits) != 1 {
		t.Fatalf("visible identifier find hits=%d, want 1", len(hits))
	}
	for _, block := range requirementFindBlocksForReader(reader, false) {
		if strings.Contains(block.text, "style=") || strings.Contains(block.text, "<span") {
			t.Fatalf("reading search indexed hidden wrappers: %q", block.text)
		}
	}
}

func TestRequirementBodyHTMLSearchRefreshAndResourceIdentity(t *testing.T) {
	const imageURL = "https://project.feishu.cn/goapi/v5/platform/file/stream/download/synthetic-body"
	const image = "![截图](" + imageURL + ")"
	const link = "[需求文档](https://project.feishu.cn/wiki/synthetic)"
	const source = "正文 <span style='color: red'>needle一</span> " + image + "\r\n\r\n" + link + "<!-- linkPreview -->"
	path := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	s := &app{page: "requirements"}
	reader := s.requirementReader("a", "https://project.feishu.cn/test/story/detail/123")
	t.Cleanup(s.closeRequirementReaders)
	body := application.RequirementContent{Body: source, MarkdownPath: path, Generation: "one",
		Resources: []application.RequirementResource{{URL: imageURL, Path: "synthetic-body.png"}}}
	s.updateRequirementReaderBody(reader, body)
	s.updateRequirementReaderComments(reader, application.RequirementComments{Comments: []domain.RequirementComment{
		{ID: "comment", Content: "评论 <span style='color: red'>needle二</span>"},
	}})
	state := reader.bodyMarkdown
	doc := state.doc
	native := ensureFrameworkMarkdown(state)
	if state.commentReading || !reader.commentMarkdown["id:comment"].commentReading {
		t.Fatal("body/comment resource ownership changed")
	}
	if state.trustedImages[imageURL] != "synthetic-body.png" {
		t.Fatal("body image trust mapping was lost")
	}
	seenImage := false
	for index := 1; index < native.document.Len(); index++ {
		for _, inline := range native.document.Inlines(index) {
			if inline.Kind != parser.Image {
				continue
			}
			start, end := native.projection.OriginalOffset(inline.Start), native.projection.OriginalOffset(inline.End)
			if inline.Dest != imageURL || source[start:end] != image {
				t.Fatal("projected image no longer maps to its original URL/source")
			}
			part := native.partAt(state, inline.Start)
			if start < part.Range.Start || start >= part.Range.End {
				t.Fatal("projected image resolved to a different source part")
			}
			seenImage = true
		}
	}
	if !seenImage || len(native.projection.LinkPreviews) != 1 {
		t.Fatal("body image or link preview disappeared")
	}
	preview := native.projection.LinkPreviews[0]
	if source[preview.Range.Start:preview.Range.End] != link {
		t.Fatal("link preview lost its original source mapping")
	}
	controller := s.prepareRequirementMarkdownReader(reader)
	controller.findOpen = true
	controller.find.set(requirementReaderFindIdentity(reader, false), "needle", requirementFindBlocksForReader(reader, false))
	if len(controller.find.hits) != 2 {
		t.Fatalf("combined visible search hits=%d, want 2", len(controller.find.hits))
	}
	s.moveRequirementMarkdownFind(reader, 1)
	if active, ok := controller.find.current(); !ok || !strings.HasPrefix(active.block, "comment:id:comment\x00") {
		t.Fatal("search did not navigate from the body into the comment")
	}
	if hits, _ := documentFindHits(requirementFindBlocksForReader(reader, false), "style=", 10); len(hits) != 0 {
		t.Fatal("rendered search indexed removed styles")
	}
	if hits, _ := documentFindHits(requirementFindBlocksForReader(reader, true), "style=", 10); len(hits) != 2 {
		t.Fatal("source search lost the original body/comment wrappers")
	}
	s.closeRequirementMarkdownFind(reader)
	scroll := readonlyMarkdownReaderScroll(controller, false)
	scroll.Y = 123
	controller.sourceMode = true
	s.prepareRequirementMarkdownReader(reader)
	if !state.sourceMode || state.doc.Source() != source {
		t.Fatal("source mode did not preserve the original body")
	}
	controller.sourceMode = false
	s.prepareRequirementMarkdownReader(reader)
	s.updateRequirementReaderBody(reader, body)
	if ensureFrameworkMarkdown(state) != native || state.doc != doc || readonlyMarkdownReaderScroll(controller, false) != scroll || scroll.Y != 123 {
		t.Fatal("unchanged refresh/source toggle replaced the projection, document, or reading position")
	}
	other := s.requirementReader("b", "https://project.feishu.cn/test/story/detail/456")
	s.updateRequirementReaderBody(other, application.RequirementContent{Body: "另一个需求 <span>OTHER</span>"})
	s.prepareRequirementMarkdownReader(other)
	if other.bodyMarkdown == state || ensureFrameworkMarkdown(other.bodyMarkdown) == native || scroll.Y != 123 {
		t.Fatal("switching requirements shared body projection or reading state")
	}
	body.Body, body.Generation = strings.Replace(source, "needle一", "NEW", 1), "two"
	s.updateRequirementReaderBody(reader, body)
	updated := ensureFrameworkMarkdown(state)
	if updated == native || !strings.Contains(updated.projection.Source, "NEW") || strings.Contains(updated.projection.Source, "needle一") ||
		!strings.Contains(ensureFrameworkMarkdown(other.bodyMarkdown).projection.Source, "OTHER") {
		t.Fatal("body refresh reused old content or changed another requirement")
	}
	if contents, err := os.ReadFile(path); err != nil || string(contents) != source {
		t.Fatalf("presentation rewrote cached source: %v", err)
	}
}

func TestRequirementBodyHTMLNativeMarkdownRenderingAndCopy(t *testing.T) {
	const code = "fmt.Println(\"<span>代码原文</span>\")  \r\nnext()\r\n"
	const source = "# 需求正文\r\n\r\n订单号：<span style='color: red'><span style='font-size: 12px'>**ORDER-001**</span></span>\r\n\r\n" +
		"1. <span style='background-color: yellow'>列表中文</span>\r\n2. 普通项目\r\n\r\n" +
		"| 名称 | 内容 |\r\n|---|---|\r\n| 卡号 | <span>CARD-002</span> |\r\n\r\n" +
		"[需求链接](https://example.test/requirement)\r\n\r\n行内：`<span>代码标签</span>`\r\n\r\n" +
		"```go\r\n" + code + "```\r\n\r\n" +
		"转义：\\<span>保留标签\\</span>\r\n\r\n不支持：<spanish>原样</spanish>\r\n\r\n异常：<span title=\"未闭合\r\n"
	for _, width := range []int{420, 960} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%v", width, dark), func(t *testing.T) {
				s := &app{page: "requirements", dark: dark}
				reader := s.requirementReader("task", "https://project.feishu.cn/test/bug/detail/123")
				t.Cleanup(s.closeRequirementReaders)
				s.updateRequirementReaderBody(reader, application.RequirementContent{Body: source})
				state := reader.bodyMarkdown
				tester := ui.NewTester(func(c *ui.Context) {
					p := setTheme(c, dark)
					ui.Column(c).Fill().Padding(16).Children(func() { s.renderMarkdownPreview(c, p, state) })
				}, width, 1100)
				tester.SetLocale("zh-Hans")
				for _, text := range []string{"需求正文", "订单号：ORDER-001", "1.", "列表中文", "CARD-002", "需求链接", "<span>代码标签</span>", "<span>代码原文</span>", "<span>保留标签</span>", "<spanish>原样</spanish>", `<span title="未闭合`} {
					if !tester.HasText(text) {
						t.Fatalf("native reader lost %q: %#v", text, tester.Texts())
					}
				}
				if tester.HasText("style=") || tester.HasText("代码只读预览") {
					t.Fatal("HTML styles leaked or native code rendering was replaced")
				}
				native := ensureFrameworkMarkdown(state)
				if native.options.CopySource(0, len(native.projection.Source)) != source {
					t.Fatal("native source copy lost original wrappers or line endings")
				}
				paragraph, ok := tester.Find("订单号：ORDER-001")
				if !ok {
					t.Fatal("body paragraph missing")
				}
				tester.ClickAt(paragraph.X+2, paragraph.Y+paragraph.H/2)
				tester.Key(ui.Cmd|ui.Shift, ui.KeyC)
				if tester.Clipboard() != source {
					t.Fatal("copy Markdown source changed original HTML")
				}
				tester.Command("selectAll")
				tester.Command("copy")
				if copied := tester.Clipboard(); strings.Contains(copied, "style=") || !strings.Contains(copied, "ORDER-001") || !strings.Contains(copied, "卡号\tCARD-002") {
					t.Fatalf("native visible-text copy changed: %q", copied)
				}
				if err := tester.Click("复制代码"); err != nil {
					t.Fatal(err)
				}
				if tester.Clipboard() != strings.TrimSuffix(code, "\r\n") {
					t.Fatalf("native code copy changed: %q", tester.Clipboard())
				}
				if state.doc.Source() != source || state.doc.Snapshot().Version != 0 {
					t.Fatal("native rendering edited body source")
				}
				saveFrameworkReadingFixture(t, tester, fmt.Sprintf("requirement-body-html-%d-dark-%v", width, dark))
			})
		}
	}
}
