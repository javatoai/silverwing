package desktop

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"

	"github.com/egoist/mygo/ui"
)

func requirementCommentCardFixture() []domain.RequirementComment {
	attachmentURL := "https://example.test/files/attachment.pdf"
	parentID := "short"
	longURL := "https://example.test/" + strings.Repeat("segment-", 18) + "resource"
	return []domain.RequirementComment{
		{ID: "short", AuthorName: "测试成员", CreatedAt: "2026-10-19 12:34:56 +08:00", Content: "短正文 **加粗**"},
		{ID: "url", AuthorName: "链接作者", CreatedAt: "2026-10-19 12:35:01 +08:00", Content: "长链接 " + longURL},
		{ID: "reply", AuthorName: "回复成员", CreatedAt: "2026-10-19 12:36:02 +08:00", ParentID: &parentID,
			Content: "回复正文\n\n| 名称 | 内容 |\n|---|---|\n| 表格行 | 表格内容 |\n\n```go\nfmt.Println(\"代码块内容\")\n```"},
		{ID: "long", AuthorName: strings.Repeat("长姓名成员", 8), CreatedAt: "2026-10-19 23:59:59.123456 +08:00",
			Content: strings.Repeat("长评论段落包含 **Markdown** 强调与换行布局校验。\n\n", 10) + "长评论末尾"},
		{ID: "attachment", AuthorName: "附件作者", CreatedAt: "2026-10-19 12:39:00 +08:00", AttachmentURL: &attachmentURL},
	}
}

func TestRequirementCommentCardsResponsiveLayoutAndContent(test *testing.T) {
	for _, width := range []int{360, 640, 960} {
		for _, dark := range []bool{false, true} {
			test.Run(fmt.Sprintf("width-%d-dark-%v", width, dark), func(test *testing.T) {
				appState := &app{dark: dark, page: "requirements"}
				reader := appState.requirementReader("", "https://project.feishu.cn/test/story/detail/123")
				test.Cleanup(appState.closeRequirementReaders)
				appState.updateRequirementReaderComments(reader, application.RequirementComments{Comments: requirementCommentCardFixture()})
				tester := ui.NewTester(func(context *ui.Context) {
					theme := setTheme(context, dark)
					appState.renderRequirementReadonlyMarkdownReader(context, theme, reader)
				}, width, 1900)

				comments := requirementCommentCardFixture()
				keys := []string{"id:short", "id:url", "id:reply", "id:long", "id:attachment"}
				cards := make([]ui.Rect, len(keys))
				for index, key := range keys {
					card, found := tester.Find("评论卡片 " + key)
					if !found {
						test.Fatalf("comment card %q missing at width %d", key, width)
					}
					cards[index] = card
					metadata, hasMetadata := tester.Find("评论元信息 " + key)
					if !hasMetadata {
						test.Fatalf("comment metadata %q missing", key)
					}
					divider, hasDivider := tester.Find("评论头部分隔线 " + key)
					if !hasDivider || divider.H != 1 || divider.X != metadata.X || divider.W != metadata.W {
						test.Errorf("card %q divider should be one DIP and match metadata width: metadata=%+v divider=%+v", key, metadata, divider)
					}
					body, hasBody := tester.Find("评论正文 " + comments[index].AuthorName)
					if !hasBody {
						test.Fatalf("comment body container %q missing", key)
					}
					if metadata.X < card.X+16 || metadata.X > card.X+18 || metadata.Y < card.Y+16 || metadata.Y > card.Y+18 {
						test.Errorf("card %q should have 16 DIP inner padding: card=%+v metadata=%+v", key, card, metadata)
					}
					metadataToDivider := divider.Y - metadata.Y - metadata.H
					if metadataToDivider < 11 || metadataToDivider > 13 {
						test.Errorf("card %q metadata/divider spacing=%v, want 12 DIP", key, metadataToDivider)
					}
					dividerToBody := body.Y - divider.Y - divider.H
					if body.X != metadata.X || dividerToBody < 15 || dividerToBody > 17 {
						test.Errorf("card %q divider/body spacing or alignment changed: divider=%+v body=%+v", key, divider, body)
					}
				}
				for index := 1; index < len(cards); index++ {
					if cards[index].X != cards[0].X || cards[index].W != cards[0].W {
						test.Errorf("comment cards must share a full-width column: first=%+v next=%+v", cards[0], cards[index])
					}
					gap := cards[index].Y - cards[index-1].Y - cards[index-1].H
					if gap < 11 || gap > 13 {
						test.Errorf("comment card gap=%v, want 12 DIP", gap)
					}
				}
				if cards[0].X < 25 || cards[0].X > 27 || cards[0].W < float32(width-64) || cards[0].W > float32(width-52) || cards[0].X+cards[0].W > float32(width) {
					test.Errorf("comment cards must fill the reading width without overflow: card=%+v viewport=%d", cards[0], width)
				}
				replyHint, hasReplyHint := tester.Find("回复 测试成员")
				replyMetadata, hasReplyMetadata := tester.Find("评论元信息 id:reply")
				replyDivider, hasReplyDivider := tester.Find("评论头部分隔线 id:reply")
				if !hasReplyHint || !hasReplyMetadata || !hasReplyDivider || replyHint.X != replyMetadata.X || replyHint.Y+replyHint.H > replyDivider.Y {
					test.Errorf("reply hint must align with metadata and precede its divider: hint=%+v metadata=%+v divider=%+v", replyHint, replyMetadata, replyDivider)
				}
				shortMetadata, hasShortMetadata := tester.Find("评论元信息 id:short")
				shortAuthor, hasShortAuthor := tester.Find("测试成员")
				shortTimestamp, hasShortTimestamp := tester.Find("2026-10-19 12:34:56 +08:00")
				if !hasShortMetadata || !hasShortAuthor || !hasShortTimestamp || shortAuthor.Y != shortTimestamp.Y || shortTimestamp.X+shortTimestamp.W < shortMetadata.X+shortMetadata.W-1 || shortTimestamp.X+shortTimestamp.W > shortMetadata.X+shortMetadata.W+1 {
					test.Errorf("short author/time row should remain single-line with the timestamp right-aligned: metadata=%+v author=%+v timestamp=%+v", shortMetadata, shortAuthor, shortTimestamp)
				}
				for _, visibleText := range []string{"短正文", "长链接", "回复正文", "表格内容", "fmt.Println", "长评论末尾", "查看评论附件"} {
					if !tester.HasText(visibleText) {
						test.Errorf("comment Markdown content %q is missing", visibleText)
					}
				}
				longName := comments[3].AuthorName
				longTimestamp := comments[3].CreatedAt
				nameBounds, hasName := tester.Find(longName)
				timestampBounds, hasTimestamp := tester.Find(longTimestamp)
				longCard, hasLongCard := tester.Find("评论卡片 id:long")
				if !hasName || !hasTimestamp || !hasLongCard || nameBounds.X < longCard.X || nameBounds.X+nameBounds.W > longCard.X+longCard.W || timestampBounds.X < longCard.X || timestampBounds.X+timestampBounds.W > longCard.X+longCard.W {
					test.Errorf("full author and timestamp must remain visible within the card: name=%+v timestamp=%+v card=%+v", nameBounds, timestampBounds, longCard)
				}
				saveFrameworkReadingFixture(test, tester, fmt.Sprintf("comment-cards-%d-dark-%v", width, dark))
			})
		}
	}
}

func TestRequirementCommentCardsZoomAndLongMetadata(test *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, zoom := range []float32{0.7, 1, 1.6} {
			test.Run(fmt.Sprintf("dark-%v-zoom-%0.1f", dark, zoom), func(test *testing.T) {
				appState := &app{dark: dark, page: "requirements"}
				reader := appState.requirementReader("", "https://project.feishu.cn/test/story/detail/123")
				test.Cleanup(appState.closeRequirementReaders)
				appState.updateRequirementReaderComments(reader, application.RequirementComments{Comments: requirementCommentCardFixture()})
				controller := appState.prepareRequirementMarkdownReader(reader)
				controller.zoom = zoom
				tester := ui.NewTester(func(context *ui.Context) {
					theme := setTheme(context, dark)
					appState.renderRequirementReadonlyMarkdownReader(context, theme, reader)
				}, 360, 1900)
				card, hasCard := tester.Find("评论卡片 id:long")
				metadata, hasMetadata := tester.Find("评论元信息 id:long")
				divider, hasDivider := tester.Find("评论头部分隔线 id:long")
				body, hasBody := tester.Find("评论正文 " + requirementCommentCardFixture()[3].AuthorName)
				longName := requirementCommentCardFixture()[3].AuthorName
				longTimestamp := requirementCommentCardFixture()[3].CreatedAt
				nameBounds, hasName := tester.Find(longName)
				timestampBounds, hasTimestamp := tester.Find(longTimestamp)
				if !hasCard || !hasMetadata || !hasDivider || !hasBody || !hasName || !hasTimestamp || divider.H != 1 || divider.X != metadata.X || divider.W != metadata.W || divider.Y-metadata.Y-metadata.H < 11 || divider.Y-metadata.Y-metadata.H > 13 || body.Y-divider.Y-divider.H < 15 || body.Y-divider.Y-divider.H > 17 || nameBounds.X < metadata.X || nameBounds.X+nameBounds.W > metadata.X+metadata.W || timestampBounds.X < metadata.X || timestampBounds.X+timestampBounds.W > metadata.X+metadata.W || card.X+card.W > 360 || divider.X+divider.W > card.X+card.W {
					test.Fatalf("zoom %0.1f clipped metadata or widened its card: card=%+v metadata=%+v divider=%+v body=%+v name=%+v timestamp=%+v", zoom, card, metadata, divider, body, nameBounds, timestampBounds)
				}
				if zoom == 1.6 {
					saveFrameworkReadingFixture(test, tester, fmt.Sprintf("comment-cards-long-metadata-360-dark-%v-zoom-1.6", dark))
				}
			})
		}
	}
}

func TestRequirementCommentCardsEmptyState(test *testing.T) {
	appState := &app{page: "requirements"}
	reader := appState.requirementReader("", "https://project.feishu.cn/test/story/detail/123")
	test.Cleanup(appState.closeRequirementReaders)
	appState.updateRequirementReaderComments(reader, application.RequirementComments{})
	tester := ui.NewTester(func(context *ui.Context) {
		theme := setTheme(context, false)
		appState.renderRequirementReadonlyMarkdownReader(context, theme, reader)
	}, 360, 480)
	if !tester.HasText("暂无评论。") {
		test.Fatal("empty comments state is missing")
	}
	for _, text := range tester.Texts() {
		if strings.HasPrefix(text, "评论卡片 ") || strings.HasPrefix(text, "评论元信息 ") {
			test.Fatalf("empty comment list rendered a card: %q", text)
		}
	}
}

func TestRequirementCommentCardStateReuseAndImageOwnership(test *testing.T) {
	appState := &app{page: "requirements"}
	reader := appState.requirementReader("", "https://project.feishu.cn/test/story/detail/123")
	test.Cleanup(appState.closeRequirementReaders)
	cachePath := filepath.Join(test.TempDir(), "comments.md")
	firstURL := "https://example.test/first.png"
	secondURL := "https://example.test/second.png"
	resources := []application.RequirementCommentResource{
		{CommentID: "first", URL: firstURL, Path: filepath.Join(test.TempDir(), "first.png"), Generation: "first-generation"},
		{CommentID: "second", URL: secondURL, Error: "temporary fixture failure", Generation: "second-generation"},
	}
	comments := []domain.RequirementComment{
		{ID: "first", AuthorName: "第一作者", Content: "![第一张](" + firstURL + ")"},
		{ID: "second", AuthorName: "第二作者", Content: "![第二张](" + secondURL + ")"},
	}
	appState.updateRequirementReaderComments(reader, application.RequirementComments{Comments: comments, CommentResources: resources, CachePath: cachePath})
	firstState := reader.commentMarkdown["id:first"]
	secondState := reader.commentMarkdown["id:second"]
	firstDocument, secondDocument := firstState.doc, secondState.doc
	firstSource, secondSource := firstDocument.Source(), secondDocument.Source()
	if firstState.path != cachePath || secondState.path != cachePath || reader.commentsMarkdown.path != cachePath {
		test.Fatal("comment state or aggregate source lost the comments cache path")
	}
	if firstState.trustedImages[firstURL] != resources[0].Path || secondState.trustedImages[firstURL] != "" || secondState.trustedImages[secondURL] != "" {
		test.Fatalf("comment images escaped their resource owner: first=%v second=%v", firstState.trustedImages, secondState.trustedImages)
	}
	comments = []domain.RequirementComment{comments[1], comments[0]}
	appState.updateRequirementReaderComments(reader, application.RequirementComments{Comments: comments, CommentResources: resources, CachePath: cachePath})
	if reader.commentMarkdown["id:first"] != firstState || reader.commentMarkdown["id:second"] != secondState || firstState.doc != firstDocument || secondState.doc != secondDocument {
		test.Fatal("reordering stable comment IDs recreated their Markdown state or parsed document")
	}
	if firstState.doc.Source() != firstSource || secondState.doc.Source() != secondSource || firstState.path != cachePath || secondState.path != cachePath {
		test.Fatal("reordering comments changed their source or cache path")
	}
	validAction := application.Action{Kind: "requirements.attachment", Payload: map[string]any{"commentID": "second", "commentGeneration": "second-generation", "fileUrl": secondURL}}
	if !requirementCommentResourceCurrent(reader, validAction) {
		test.Fatal("current failed image resource should remain eligible for retry")
	}
	validAction.Payload["commentID"] = "first"
	if requirementCommentResourceCurrent(reader, validAction) {
		test.Fatal("resource from another comment was accepted for retry")
	}

	tester := ui.NewTester(func(context *ui.Context) {
		theme := setTheme(context, false)
		appState.renderRequirementReaderActions(context, theme, reader, nil)
		appState.renderRequirementReadonlyMarkdownReader(context, theme, reader)
	}, 480, 700)
	if !tester.HasText("图片加载失败") {
		test.Fatal("failed attachment state is missing from its comment card")
	}
	if err := tester.Click("重试评论图片"); err != nil {
		test.Fatalf("local retry control is missing: %v", err)
	}
	if reader.commentResourceLoading != "" {
		test.Fatal("a nil backend unexpectedly started a resource request")
	}
}

func TestRequirementCommentCardsKeepOriginalCopyAndSourceMode(test *testing.T) {
	const source = "保留 **原文**\r\n\r\n| A | B |\r\n|---|---|\r\n| 一 | 二 |"
	appState := &app{page: "requirements"}
	reader := appState.requirementReader("", "https://project.feishu.cn/test/story/detail/123")
	test.Cleanup(appState.closeRequirementReaders)
	appState.updateRequirementReaderComments(reader, application.RequirementComments{
		Comments:  []domain.RequirementComment{{ID: "source", AuthorName: "源码作者", Content: source}},
		CachePath: filepath.Join(test.TempDir(), "comments.md"),
	})
	commentState := reader.commentMarkdown["id:source"]
	tester := ui.NewTester(func(context *ui.Context) {
		theme := setTheme(context, false)
		appState.renderRequirementReaderActions(context, theme, reader, nil)
		appState.renderRequirementReadonlyMarkdownReader(context, theme, reader)
	}, 640, 700)
	if err := tester.RightClick("保留 原文"); err != nil {
		test.Fatal(err)
	}
	if err := tester.ChooseMenuItem("Copy as Markdown"); err != nil {
		test.Fatal(err)
	}
	if tester.Clipboard() != source || commentState.doc.Source() != source {
		test.Fatalf("comment Markdown copy changed original source: clipboard=%q source=%q", tester.Clipboard(), commentState.doc.Source())
	}
	if err := tester.RightClick("保留 原文"); err != nil {
		test.Fatal(err)
	}
	if err := tester.ChooseMenuItem("查看 Markdown 源码"); err != nil {
		test.Fatal(err)
	}
	if !appState.prepareRequirementMarkdownReader(reader).sourceMode || reader.commentsMarkdown.doc.Source() == "" {
		test.Fatal("existing requirement source mode did not open the aggregate comments Markdown")
	}
	if tester.HasText("评论卡片 id:source") {
		test.Fatal("source mode should render the aggregate comments document instead of card mode")
	}
}
