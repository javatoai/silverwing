package desktop

import (
	"strings"
	"testing"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown/parser"
)

func TestRequirementCommentHTMLProjectionKeepsVisibleTextFindAndCopy(t *testing.T) {
	const imageURL = "https://project.feishu.cn/goapi/v5/platform/file/stream/download/synthetic-image"
	const source = "1. 中文 <span style=\"background-color: rgb(255, 255, 255)\"><span style=\"font-size: 14px; color: #000000\">needle评论正文</span></span> ![截图](" + imageURL + ")\r\n\r\n`<span>代码标签</span>`"
	const imagePath = "synthetic-local-comment-image.png"
	appState := &app{page: "requirements"}
	reader := appState.requirementReader("task", "https://project.feishu.cn/test/story/detail/123")
	t.Cleanup(appState.closeRequirementReaders)
	appState.updateRequirementReaderBody(reader, application.RequirementContent{Body: "需求正文 needle"})
	comments := []domain.RequirementComment{
		{ID: "html", AuthorName: "评论作者", Content: source},
		{ID: "other", AuthorName: "另一位作者", Content: "普通评论"},
	}
	resources := []application.RequirementCommentResource{{
		CommentID: "html", URL: imageURL, Path: imagePath, Generation: "synthetic-generation",
	}}
	appState.updateRequirementReaderComments(reader, application.RequirementComments{Comments: comments, CommentResources: resources})
	commentState := reader.commentMarkdown["id:html"]
	if commentState == nil || !commentState.commentReading {
		t.Fatal("individual comment state did not enter comment-reading mode")
	}
	originalDocument := commentState.doc
	native := ensureFrameworkMarkdown(commentState)
	if strings.Contains(native.projection.Source, "style=") || !strings.Contains(native.projection.Source, "needle评论正文") ||
		!strings.Contains(native.projection.Source, "![截图]("+imageURL+")") || !strings.Contains(native.projection.Source, "`<span>代码标签</span>`") {
		t.Fatalf("comment projection did not preserve only readable content: %q", native.projection.Source)
	}
	if native.commentReading != commentState.commentReading || native.original != source || commentState.doc.Source() != source {
		t.Fatal("comment projection changed the original comment source")
	}

	var imageNode *parser.Inline
	for index := 1; index < native.document.Len(); index++ {
		for _, inline := range native.document.Inlines(index) {
			if inline.Kind == parser.Image && inline.Dest == imageURL {
				imageCopy := inline
				imageNode = &imageCopy
				break
			}
		}
	}
	if imageNode == nil {
		t.Fatal("comment image was lost while projecting HTML")
	}
	imageStart := native.projection.OriginalOffset(imageNode.Start)
	imageEnd := native.projection.OriginalOffset(imageNode.End)
	if imageStart < 0 || imageEnd > len(source) || imageEnd <= imageStart {
		t.Fatalf("comment image source range is invalid: %d:%d", imageStart, imageEnd)
	}
	if got := source[imageStart:imageEnd]; got != "![截图]("+imageURL+")" {
		t.Fatalf("comment image source mapping changed: %d:%d %q", imageStart, imageEnd, got)
	}
	part := native.partAt(commentState, imageNode.Start)
	var sourcePartID uint64
	for _, sourcePart := range commentState.doc.Parts() {
		if imageStart >= sourcePart.Range.Start && imageStart < sourcePart.Range.End {
			sourcePartID = sourcePart.ID
			break
		}
	}
	if sourcePartID == 0 || part.ID != sourcePartID {
		t.Fatalf("comment image resolved to the wrong source part: got=%d want=%d", part.ID, sourcePartID)
	}

	combinedBlocks := requirementFindBlocksForReader(reader, false)
	hits, _ := documentFindHits(combinedBlocks, "needle", 10)
	if len(hits) != 2 {
		t.Fatalf("body and comment visible find hits=%d, want 2", len(hits))
	}
	var commentBlock string
	for _, block := range combinedBlocks {
		if strings.HasPrefix(block.key, "comment:id:html\x00") {
			commentBlock += block.text + "\n"
		}
	}
	if !strings.Contains(commentBlock, "needle评论正文") || strings.Contains(commentBlock, "style=") || !strings.Contains(commentBlock, "<span>代码标签</span>") {
		t.Fatalf("comment find index contains hidden HTML syntax: %q", commentBlock)
	}
	controller := appState.prepareRequirementMarkdownReader(reader)
	controller.findOpen = true
	controller.find.set(requirementReaderFindIdentity(reader, false), "needle", combinedBlocks)
	appState.moveRequirementMarkdownFind(reader, 1)
	appState.prepareRequirementMarkdownReader(reader)
	active, ok := controller.find.current()
	if !ok || !strings.HasPrefix(active.block, "comment:id:html\x00") {
		t.Fatalf("find navigation did not advance from the body into the comment: %#v", active)
	}

	tester := ui.NewTester(func(context *ui.Context) {
		theme := setTheme(context, false)
		appState.renderFrameworkMarkdown(context, theme, commentState)
	}, 640, 520)
	if !tester.HasText("needle评论正文") || tester.HasText(`style="background-color`) {
		t.Fatalf("comment HTML did not render as styled text: %#v", tester.Texts())
	}
	copySource := native.options.CopySource(0, len(native.projection.Source))
	if copySource != source || commentState.doc != originalDocument {
		t.Fatalf("Markdown copy/source identity changed: copied=%q", copySource)
	}

	comments = []domain.RequirementComment{comments[1], comments[0]}
	appState.updateRequirementReaderComments(reader, application.RequirementComments{Comments: comments, CommentResources: resources})
	if reader.commentMarkdown["id:html"] != commentState || commentState.doc != originalDocument ||
		commentState.trustedImages[imageURL] != imagePath || resources[0].CommentID != "html" ||
		resources[0].Generation != "synthetic-generation" || resources[0].URL != imageURL {
		t.Fatal("comment image URL, generation, cache owner, or parsed state changed after reordering")
	}
	if ensureFrameworkMarkdown(commentState) != native {
		t.Fatal("comment projection was rebuilt after an unchanged comment refresh")
	}

	state := newReadonlyMarkdownState("projection-cache-mode", source, nil)
	state.commentReading = true
	commentProjection := ensureFrameworkMarkdown(state)
	state.commentReading = false
	ordinaryProjection := ensureFrameworkMarkdown(state)
	if commentProjection == ordinaryProjection || strings.Contains(commentProjection.projection.Source, "style=") ||
		!strings.Contains(ordinaryProjection.projection.Source, "style=") {
		t.Fatal("framework Markdown cache reused a projection across reading modes")
	}
	if ordinaryProjection.original != source {
		t.Fatal("ordinary projection source changed")
	}
}
