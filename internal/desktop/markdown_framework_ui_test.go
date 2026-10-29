package desktop

import (
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func saveFrameworkReadingFixture(t *testing.T, tester *ui.Tester, name string) {
	t.Helper()
	if directory := os.Getenv("SILVERWING_READING_REVIEW_OUTPUT"); directory != "" {
		file, err := os.Create(filepath.Join(directory, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(file, tester.Image())
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			t.Fatalf("save screenshot: %v %v", err, closeErr)
		}
	}
}

func TestMyGOReadingLayoutCopyAndProjection(t *testing.T) {
	if mygo.Version != "0.5.0" {
		t.Fatalf("runtime version %s", mygo.Version)
	}
	const code = "fmt.Println(\"原始代码\")  \r\nnext()\r\n"
	const first = "第一项有 **中文重点** 和普通文字，编号与正文首行必须对齐。"
	source := "# 中文阅读\r\n\r\n7. " + first + "\r\n8. 第二项\r\n   - 嵌套项目\r\n\r\n| 名称 | 内容 |\r\n|---|---|\r\n| 表格 | needle |\r\n\r\n```go\r\n" + code + "```\r\n\r\n[需求文档](https://project.feishu.cn/wiki/abc)<!-- linkPreview -->\r\n\r\n<!-- unknown -->\r\n"
	for _, width := range []int{420, 960} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%v", width, dark), func(t *testing.T) {
				state := newReadonlyMarkdownState("framework-fixture", source, nil)
				t.Cleanup(func() { closeMarkdownPreviewState(state) })
				s := &app{dark: dark, page: "tasks"}
				tester := ui.NewTester(func(c *ui.Context) {
					p := setTheme(c, dark)
					ui.Column(c).Fill().Padding(16).Children(func() { s.renderMarkdownPreview(c, p, state) })
				}, width, 900)
				tester.SetLocale("zh-Hans")
				for _, text := range []string{"中文阅读", "7.", "8.", "第二项", "嵌套项目", "表格", "needle", "原始代码", "打开文档：需求文档", "<!-- unknown -->"} {
					if !tester.HasText(text) {
						t.Fatalf("missing %q in %#v", text, tester.Texts())
					}
				}
				if tester.HasText("代码只读预览") || tester.HasText("Markdown 编辑器") {
					t.Fatal("reading used an editor instead of the framework's code block")
				}
				marker, ok := tester.Find("7.")
				if !ok {
					t.Fatal("number missing")
				}
				paragraph, ok := tester.Find(strings.ReplaceAll(strings.ReplaceAll(first, "**", ""), "\r", ""))
				if !ok {
					t.Fatal("paragraph missing")
				}
				if math.Abs(float64(marker.Y-paragraph.Y)) > .5 || paragraph.X <= marker.X {
					t.Fatalf("list first-line geometry: marker=%+v text=%+v", marker, paragraph)
				}
				tester.ClickAt(paragraph.X+2, paragraph.Y+paragraph.H/2)
				tester.Command("selectAll")
				tester.Command("copy")
				if copied := tester.Clipboard(); !strings.Contains(copied, "中文阅读") || !strings.Contains(copied, `fmt.Println("原始代码")`) {
					t.Fatalf("default reader selection no longer spans body and code: %q", copied)
				}
				if err := tester.Click("复制代码"); err != nil {
					t.Fatal(err)
				}
				if tester.Clipboard() != strings.TrimSuffix(code, "\r\n") {
					t.Fatalf("code bytes changed: %q", tester.Clipboard())
				}
				if tester.HasText("<!-- linkPreview -->") {
					t.Fatal("recognized annotation leaked")
				}
				if state.doc.Source() != source {
					t.Fatal("render mutated original")
				}
				saveFrameworkReadingFixture(t, tester, fmt.Sprintf("mygo-reading-%d-dark-%v", width, dark))
			})
		}
	}
}

func TestMyGOReadingImageTrustAndLightbox(t *testing.T) {
	const target = "https://project.feishu.cn/goapi/v5/platform/file/stream/download/test-token"
	const source = "文字 ![行内图](" + target + ") 后续 needle\n\n![独立图](" + target + ")"
	state := newReadonlyMarkdownState("account-requirement-comment", source, nil)
	s := &app{page: "tasks"}
	t.Cleanup(func() {
		closeMarkdownPreviewState(state)
		drainMarkdownPreviewTestJobs(t, s)
	})
	tester := ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, false)
		s.renderMarkdownPreview(c, p, state)
		s.renderMarkdownBitmapViewer(c, p)
	}, 420, 600)
	if !tester.HasText("没有可信的本地图片映射") {
		t.Fatal("unmapped authenticated image was allowed")
	}
	if hits, _ := documentFindHits(frameworkMarkdownFindBlocks(state), "行内图", 10); len(hits) != 0 {
		t.Fatal("image placeholder text entered visible find")
	}
	bitmap := ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 240, 100)))
	seedMarkdownReadyImages(t, s, state, bitmap)
	tester.Frame()
	if err := tester.Click("Markdown 图片：独立图"); err != nil {
		t.Fatal(err)
	}
	if s.materialsUnit.state.markdownBitmapViewer == nil || s.materialsUnit.state.markdownBitmapViewer.owner != state {
		t.Fatal("image did not use shared lightbox")
	}
	if _, ok := tester.Find("图片画布"); !ok {
		t.Fatal("lightbox canvas missing")
	}
	tester.Key(0, ui.KeyEscape)
	if s.materialsUnit.state.markdownBitmapViewer != nil || state.doc.Source() != source {
		t.Fatal("lightbox close lost reader/source")
	}
}

func TestMyGOReadingSourceAndTableCopy(t *testing.T) {
	const source = "@张三<!-- mention:{\"id\":\"user\",\"blockId\":\"block\",\"cn_name\":\"张三\"} -->\r\n\r\n| A | B |\r\n|---|---|\r\n| 一 | 二 |"
	state := newReadonlyMarkdownState("copy", source, nil)
	t.Cleanup(func() { closeMarkdownPreviewState(state) })
	s := &app{}
	tester := ui.NewTester(func(c *ui.Context) { p := setTheme(c, false); s.renderMarkdownPreview(c, p, state) }, 500, 500)
	paragraph, ok := tester.Find("@张三")
	if !ok {
		t.Fatal("mention label missing")
	}
	tester.ClickAt(paragraph.X+2, paragraph.Y+paragraph.H/2)
	tester.Key(ui.Cmd|ui.Shift, ui.KeyC)
	if tester.Clipboard() != source {
		t.Fatalf("whole source copy changed bytes: %q", tester.Clipboard())
	}
	tester.Command("selectAll")
	tester.Command("copy")
	if tester.Clipboard() != "@张三\n\nA\tB\n一\t二" {
		t.Fatalf("native table text copy = %q", tester.Clipboard())
	}
	tester.Key(ui.Cmd|ui.Shift, ui.KeyC)
	if tester.Clipboard() != source {
		t.Fatalf("selected source copy changed annotations/line endings: %q", tester.Clipboard())
	}
}

func TestMyGOReadingCommentsBoundaryAndLongDocument(t *testing.T) {
	for _, width := range []int{420, 960} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%v", width, dark), func(t *testing.T) {
				s := &app{dark: dark, page: "requirements"}
				reader := s.requirementReader("", "https://project.feishu.cn/test/story/detail/123")
				t.Cleanup(s.requirementUnit.closeRequirementReaders)
				s.updateRequirementReaderBody(reader, application.RequirementContent{Body: "正文末尾 needle"})
				parent := "first"
				s.updateRequirementReaderComments(reader, application.RequirementComments{Comments: []domain.RequirementComment{
					{ID: parent, AuthorName: "张三", CreatedAt: "2026-10-15 14:20", Content: "第一条 needle"},
					{ID: "reply", AuthorName: "李四", CreatedAt: "2026-10-15 14:21", ParentID: &parent, Content: "- 回复第一项\n- 回复第二项"},
				}})
				tester := ui.NewTester(func(c *ui.Context) { p := setTheme(c, dark); s.renderRequirementReadonlyMarkdownReader(c, p, reader) }, width, 800)
				body, ok := tester.Find("正文末尾 needle")
				if !ok {
					t.Fatal("body missing")
				}
				header, ok := tester.Find("评论")
				if !ok {
					t.Fatal("section heading missing")
				}
				author, ok := tester.Find("张三")
				if !ok {
					t.Fatal("author missing")
				}
				if header.Y-(body.Y+body.H) < 28 || author.Y-(header.Y+header.H) < 12 {
					t.Fatalf("comments boundary has insufficient rhythm: body=%+v header=%+v author=%+v", body, header, author)
				}
				if !tester.HasText("暂无评论") && !tester.HasText("回复 张三") {
					t.Fatal("reply relationship missing")
				}
				saveFrameworkReadingFixture(t, tester, fmt.Sprintf("mygo-comments-%d-dark-%v", width, dark))
				s.updateRequirementReaderBody(reader, application.RequirementContent{Body: strings.Repeat("段落包含中文、列表相关说明与 needle 查找。\n\n", 100) + "正文末尾 needle"})
				controller := s.prepareRequirementMarkdownReader(reader)
				controller.findOpen = true
				controller.find.set(requirementReaderFindIdentity(reader, false), "needle", requirementFindBlocksForReader(reader, false))
				if len(controller.find.hits) != 102 {
					t.Fatalf("long body + comments find = %d", len(controller.find.hits))
				}
				controller.find.active = len(controller.find.hits) - 1
				controller.find.navigation++
				tester.Frame()
				if readonlyMarkdownReaderScroll(controller, false).Y <= 0 {
					t.Fatal("find did not reveal comment after long body")
				}
				comment, ok := tester.Find("第一条 needle")
				if !ok || comment.Y < 0 || comment.Y+comment.H > 800 {
					t.Fatalf("active comment is outside viewport: %+v", comment)
				}
				if reader.bodyMarkdown.frameworkMarkdown == nil || reader.commentMarkdown["id:first"].frameworkMarkdown == nil {
					t.Fatal("body/comments did not share framework entry")
				}
				s.updateRequirementReaderComments(reader, application.RequirementComments{})
				tester.Frame()
				if !tester.HasText("暂无评论。") {
					t.Fatal("empty comments state missing")
				}
			})
		}
	}
}

// Keep the new reader's geometry/search hooks tied to the actual MyGO output.
func TestMyGOReadingFindUsesRenderedText(t *testing.T) {
	state := newReadonlyMarkdownState("find", "<b>粗体</b> 后续 needle\n\n第一行  \n第二行 needle\n\n| A | B |\n|---|---|\n| needle | 末尾 |", nil)
	t.Cleanup(func() { closeMarkdownPreviewState(state) })
	find := &ensureReadonlyMarkdownReader(state).find
	find.set("find", "needle", frameworkMarkdownFindBlocks(state))
	if len(find.hits) != 3 {
		t.Fatalf("visible find hits=%d", len(find.hits))
	}
	s := &app{}
	tester := ui.NewTester(func(c *ui.Context) { p := setTheme(c, false); s.renderMarkdownPreview(c, p, state) }, 500, 800)
	if !tester.HasText("粗体 后续 needle") {
		t.Fatal("native formatting missing")
	}
	for _, block := range find.blocks {
		if strings.Contains(block.text, "<b>") {
			t.Fatal("find indexed hidden formatting syntax")
		}
	}
	if state.doc.Snapshot().Version != 0 || state.doc.Source() == "" {
		t.Fatal("reader edited source")
	}
}
