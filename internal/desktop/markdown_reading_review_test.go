package desktop

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/editor"

	"github.com/egoist/mygo/ui"
)

func TestMarkdownMermaidCompactActions(t *testing.T) {
	if icons["expand"] == nil {
		t.Fatal("expand action has no visible icon")
	}
	const source = "sequenceDiagram\r\n    Alice->>Bob: 原始源码  \r\n"
	for _, width := range []int{420, 1000} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%v", width, dark), func(t *testing.T) {
				state := newReadonlyMarkdownState("mermaid-review", "```mermaid\r\n"+source+"\r\n```\r\n", nil)
				t.Cleanup(func() { closeMarkdownPreviewState(state) })
				var part editor.Part
				for _, candidate := range state.doc.Parts() {
					if candidate.Kind == editor.PartMermaid {
						part = candidate
					}
				}
				if part.ID == 0 {
					t.Fatal("fixture did not parse a Mermaid fence")
				}
				pixels := image.NewRGBA(image.Rect(0, 0, 1200, 300))
				draw.Draw(pixels, pixels.Bounds(), image.NewUniform(color.RGBA{R: 40, G: 100, B: 200, A: 255}), image.Point{}, draw.Src)
				bitmap := ui.NewBitmap(pixels)
				key := source
				if dark {
					key += "\x00dark"
				}
				state.diagrams[part.ID] = &markdownDiagramState{key: key, source: source, bitmap: bitmap}
				s := &app{dark: dark, page: "tasks"}
				tester := ui.NewTester(func(c *ui.Context) {
					p := setTheme(c, dark)
					s.beginOverflowMenuFrame()
					s.renderMarkdownPreview(c, p, state)
					s.endOverflowMenuFrame()
				}, width, 340)
				expand, found := tester.Find("放大查看")
				more, hasMore := tester.Find("更多图表操作")
				if !found || !hasMore || expand.W != 30 || expand.H != 30 || more.X < expand.X+expand.W || more.X+more.W > float32(width) {
					t.Fatalf("compact controls overflow or are not 30 DIP: expand=%#v more=%#v", expand, more)
				}
				preview, hasPreview := tester.Find("Mermaid 图表预览")
				if !hasPreview || preview.X < 0 || preview.X+preview.W > float32(width) || preview.W != preview.H*4 {
					t.Fatalf("wide diagram must fit the reading viewport at its original aspect ratio: %v", preview)
				}
				if tester.HasText("复制图表源码") {
					t.Fatal("copy must only appear in the more menu")
				}
				if output := os.Getenv("SILVERWING_READING_REVIEW_OUTPUT"); output != "" {
					path := filepath.Join(output, fmt.Sprintf("mermaid-compact-%d-dark-%v.png", width, dark))
					file, err := os.Create(path)
					if err != nil {
						t.Fatal(err)
					}
					err = png.Encode(file, tester.Image())
					closeErr := file.Close()
					if err != nil || closeErr != nil {
						t.Fatalf("save fixture: %v %v", err, closeErr)
					}
				}
				if err := tester.Click("更多图表操作"); err != nil {
					t.Fatal(err)
				}
				if !tester.HasText("复制图表源码") {
					t.Fatal("copy source missing from open menu")
				}
				tester.Key(0, ui.KeyEnter)
				if tester.Clipboard() != source {
					t.Fatalf("keyboard menu copy altered source: %q", tester.Clipboard())
				}
				if err := tester.Click("放大查看"); err != nil {
					t.Fatal(err)
				}
				viewer := s.markdownBitmapViewer
				if viewer == nil || viewer.owner != state || viewer.media.imageExpandedBitmap != bitmap || !viewer.media.imageExpanded {
					t.Fatal("expand did not reuse the shared native image viewer")
				}
				if state.doc.Source() != "```mermaid\r\n"+source+"\r\n```\r\n" {
					t.Fatal("presentation actions changed Markdown source")
				}
			})
		}
	}
}

func TestRequirementFindCrossesIndependentCommentDocuments(t *testing.T) {
	s := &app{}
	reader := s.requirementReader("", "https://project.feishu.cn/test/story/detail/123")
	t.Cleanup(s.closeRequirementReaders)
	s.updateRequirementReaderBody(reader, application.RequirementContent{Body: "正文 needle"})
	s.updateRequirementReaderComments(reader, application.RequirementComments{Comments: []domain.RequirementComment{
		{ID: "first", Content: "第一个 needle"},
		{ID: "second", Content: "```go\r\nneedle()\r\n```"},
	}})
	controller := s.prepareRequirementMarkdownReader(reader)
	controller.findOpen = true
	controller.find.set(requirementReaderFindIdentity(reader, false), "needle", requirementFindBlocksForReader(reader, false))
	if len(controller.find.hits) != 3 {
		t.Fatalf("cross-document search found %d hits, want 3", len(controller.find.hits))
	}
	for _, section := range []string{"body", "comment:id:first", "comment:id:second", "body"} {
		s.prepareRequirementMarkdownReader(reader)
		hit, ok := controller.find.current()
		if !ok || !strings.HasPrefix(hit.block, section+"\x00") {
			t.Fatalf("find should enter %q, got %#v", section, hit)
		}
		for key, state := range reader.commentMarkdown {
			active, exists := state.readonlyReader.find.current()
			if !exists || (requirementCommentFindSection(key) == section) == (active.block == requirementReaderInactiveFindBlock) {
				t.Fatalf("comment %q has wrong active local hit: %#v", key, active)
			}
		}
		s.moveRequirementMarkdownFind(reader, 1)
	}
}

func TestReadingLateLoadRespectsUserSelection(t *testing.T) {
	root := t.TempDir()
	for _, interacted := range []bool{false, true} {
		t.Run(fmt.Sprint(interacted), func(t *testing.T) {
			s := &app{page: "tasks", tasks: []task{{id: "first", path: filepath.Join(root, "first")}, {id: "saved", path: filepath.Join(root, "saved")}}}
			s.readingSession = &desktopReadingSession{loaded: true, restoreLastTask: true, userInteracted: interacted,
				startupIdentity: readingUIIdentity{page: "tasks"},
				cache:           ReadingSnapshot{LastTaskPath: filepath.Join(root, "saved"), View: "MATERIALS"}}
			s.restoreReadingSessionOnSnapshot()
			if interacted {
				if s.selected != 0 || s.tab != 0 {
					t.Fatal("late cache load replaced a user selection")
				}
			} else if s.selected != 1 || s.tab != 1 {
				t.Fatal("empty startup identity prevented restoring the saved task")
			}
		})
	}
}

func TestMarkdownMermaidRejectsLateResults(t *testing.T) {
	state := newReadonlyMarkdownState("review", "```mermaid\nA-->B\n```", nil)
	t.Cleanup(func() { closeMarkdownPreviewState(state) })
	diagram := &markdownDiagramState{key: "current"}
	state.diagrams[1] = diagram
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if !markdownMermaidResultRelevant(state, nil, diagram, 1, "current", ctx, "") {
		t.Fatal("current detached preview was rejected")
	}
	if markdownMermaidResultRelevant(state, nil, diagram, 1, "old", ctx, "") {
		t.Fatal("old source/theme result was accepted")
	}
	state.diagrams[1] = &markdownDiagramState{key: "current"}
	if markdownMermaidResultRelevant(state, nil, diagram, 1, "current", ctx, "") {
		t.Fatal("replaced diagram result was accepted")
	}
	state.diagrams[1] = diagram
	cancel()
	if markdownMermaidResultRelevant(state, nil, diagram, 1, "current", ctx, "") {
		t.Fatal("cancelled result was accepted")
	}
	closeMarkdownPreviewState(state)
	if markdownMermaidResultRelevant(state, nil, diagram, 1, "current", context.Background(), "") {
		t.Fatal("closed preview accepted a late result")
	}
}
