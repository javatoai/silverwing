package desktop

import (
	"context"
	"image"
	"testing"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"

	"github.com/egoist/mygo/ui"
)

func TestMarkdownCommentImageViewerClosesOnTaskSelection(t *testing.T) {
	const urlA = "https://project.feishu.cn/space/userstory/detail/101"
	const urlB = "https://project.feishu.cn/space/userstory/detail/102"
	s := newApp(nil, Options{CapturePath: "markdown-comment-viewer-task-selection"})
	s.page, s.tab = "tasks", 3
	s.tasks = []task{{id: "task-a", requirementURL: urlA}, {id: "task-b", requirementURL: urlB}}
	s.selected = 0
	_, owner := markdownNavigationCommentOwner(t, s, "task-a", urlA)
	viewer := markdownNavigationCommentViewer(t, s, owner, "cache/comment-a.png")

	if !markdownLocalImageViewerResultCurrent(s, viewer, viewer.generation) {
		t.Fatal("active task A comment viewer was rejected before switching")
	}
	currentTab := s.tab
	if !s.selectTaskIndex(1) {
		t.Fatalf("task selection failed: %s", s.lastError)
	}
	if s.currentTask().id != "task-b" || s.tab != currentTab {
		t.Fatal("task selection did not switch task while retaining the detail tab")
	}
	if viewer.requestCtx != nil || viewer.media.imageExpandedBitmap != nil || viewer.media.imageExpanded || viewer.media.imageExpandedLoading {
		t.Fatal("task switch did not cancel the original-image request and clear its bitmap")
	}
	if s.markdownBitmapViewer != nil || owner.closeBitmapViewer != nil {
		t.Fatal("task switch retained the comment image viewer or owner hook")
	}
	if markdownLocalImageViewerResultCurrent(s, viewer, viewer.generation) {
		t.Fatal("late result from task A remained current after selecting task B")
	}
	closeMarkdownPreviewState(owner)
}

func TestMarkdownCommentImageViewerClosesOnRequirementSelection(t *testing.T) {
	itemA := application.Requirement{ID: "101", ProjectKey: "space", Type: "userstory", Title: "需求 A", URL: "https://project.feishu.cn/space/userstory/detail/101"}
	itemB := application.Requirement{ID: "102", ProjectKey: "space", Type: "userstory", Title: "需求 B", URL: "https://project.feishu.cn/space/userstory/detail/102"}
	s := newApp(nil, Options{CapturePath: "markdown-comment-viewer-requirement-selection"})
	s.page = "requirements"
	s.requirements = []application.Requirement{itemA, itemB}
	s.selectedRequirement, s.selectedRequirementKey = 0, requirementKey(itemA)
	_, owner := markdownNavigationCommentOwner(t, s, "", itemA.URL)
	viewer := markdownNavigationCommentViewer(t, s, owner, "cache/requirement-comment-a.png")
	if !markdownLocalImageViewerResultCurrent(s, viewer, viewer.generation) {
		t.Fatal("active requirement A comment viewer was rejected before switching")
	}
	currentTab := s.tab

	tester := ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, false)
		ui.Column(c).Fill().Children(func() {
			for _, item := range s.requirements {
				s.requirementListItem(c, p, item, true)
			}
		})
	}, 640, 320)
	if err := tester.Click(itemB.Title); err != nil {
		t.Fatalf("select requirement B: %v", err)
	}
	if s.selectedRequirement != 1 || s.selectedRequirementKey != requirementKey(itemB) || s.tab != currentTab {
		t.Fatal("requirement list selection did not switch from A to B in the same tab")
	}
	if viewer.requestCtx != nil || viewer.media.imageExpandedBitmap != nil || viewer.media.imageExpanded || viewer.media.imageExpandedLoading {
		t.Fatal("requirement switch did not cancel the original-image request and clear its bitmap")
	}
	if s.markdownBitmapViewer != nil || owner.closeBitmapViewer != nil {
		t.Fatal("requirement switch retained the comment image viewer or owner hook")
	}
	if markdownLocalImageViewerResultCurrent(s, viewer, viewer.generation) {
		t.Fatal("late result from requirement A remained current after selecting requirement B")
	}
	closeMarkdownPreviewState(owner)
}

func TestMarkdownCommentImageViewerRejectsSelectedIdentityAndOwnerUpdates(t *testing.T) {
	itemA := application.Requirement{ID: "101", ProjectKey: "space", Type: "userstory", Title: "需求 A", URL: "https://project.feishu.cn/space/userstory/detail/101"}
	itemB := application.Requirement{ID: "102", ProjectKey: "space", Type: "userstory", Title: "需求 B", URL: "https://project.feishu.cn/space/userstory/detail/102"}
	s := newApp(nil, Options{CapturePath: "markdown-comment-viewer-stale-selection"})
	s.page = "requirements"
	s.requirements = []application.Requirement{itemA, itemB}
	s.selectedRequirementKey = requirementKey(itemA)
	_, owner := markdownNavigationCommentOwner(t, s, "", itemA.URL)
	viewer := markdownNavigationCommentViewer(t, s, owner, "cache/requirement-comment-a.png")

	s.selectedRequirementKey = requirementKey(itemB)
	if markdownLocalImageViewerResultCurrent(s, viewer, viewer.generation) {
		t.Fatal("viewer result was accepted after selected requirement identity changed")
	}
	s.selectedRequirementKey = requirementKey(itemA)
	s.tab++
	if markdownLocalImageViewerResultCurrent(s, viewer, viewer.generation) {
		t.Fatal("viewer result was accepted after tab changed")
	}
	s.tab--

	requestContext := viewer.requestCtx
	updateReadonlyMarkdownState(owner, "更新后的评论", nil)
	if requestContext == nil || requestContext.Err() == nil || viewer.media.imageExpandedBitmap != nil || viewer.media.imageExpanded || s.markdownBitmapViewer != nil {
		t.Fatal("comment document update did not cancel, detach and clear its previous viewer")
	}
	if markdownLocalImageViewerResultCurrent(s, viewer, viewer.generation) {
		t.Fatal("late result from the previous comment document remained current")
	}
	closeMarkdownPreviewState(owner)
}

func TestMarkdownCommentImageViewerClosesWhenRequirementRefreshChangesSelection(t *testing.T) {
	itemA := application.Requirement{ID: "101", ProjectKey: "space", Type: "userstory", Title: "需求 A", URL: "https://project.feishu.cn/space/userstory/detail/101"}
	itemB := application.Requirement{ID: "102", ProjectKey: "space", Type: "userstory", Title: "需求 B", URL: "https://project.feishu.cn/space/userstory/detail/102"}
	s := newApp(nil, Options{CapturePath: "markdown-comment-viewer-requirement-refresh"})
	s.page = "requirements"
	s.requirements = []application.Requirement{itemA, itemB}
	s.selectedRequirement, s.selectedRequirementKey = 1, requirementKey(itemB)
	_, owner := markdownNavigationCommentOwner(t, s, "", itemB.URL)
	viewer := markdownNavigationCommentViewer(t, s, owner, "cache/requirement-comment-b.png")
	if !markdownLocalImageViewerResultCurrent(s, viewer, viewer.generation) {
		t.Fatal("active requirement B comment viewer was rejected before refresh")
	}

	s.requirements = []application.Requirement{itemA}
	s.restoreRequirementListSelection(requirementKey(itemB))
	if s.selectedRequirementKey != requirementKey(itemA) {
		t.Fatal("requirement list refresh did not select its remaining item")
	}
	if viewer.requestCtx != nil || viewer.media.imageExpandedBitmap != nil || viewer.media.imageExpanded || s.markdownBitmapViewer != nil {
		t.Fatal("requirement refresh did not cancel and clear the previous selection viewer")
	}
	if markdownLocalImageViewerResultCurrent(s, viewer, viewer.generation) {
		t.Fatal("late result from the previous requirement selection remained current")
	}
	closeMarkdownPreviewState(owner)
}

func markdownNavigationCommentViewer(t *testing.T, s *app, owner *markdownEditorState, sourcePath string) *markdownBitmapViewerState {
	t.Helper()
	viewer := &markdownBitmapViewerState{
		owner: owner, document: owner.doc, source: owner.doc.Source(), sourcePath: sourcePath,
		page: s.page, settingsPage: s.settingsPage, tab: s.tab,
		media: &nativeMediaState{kind: "image", imageExpanded: true, imageExpandedBitmap: ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 4, 4)))},
	}
	if !s.bindMarkdownBitmapViewerOwner(viewer, owner) {
		t.Fatal("could not bind viewer to the active requirement reader")
	}
	s.markdownBitmapViewer = viewer
	owner.closeBitmapViewer = func() { s.closeMarkdownBitmapViewerForOwner(owner) }
	viewer.generation = 1
	viewer.requestCtx, viewer.cancel = context.WithCancel(context.Background())
	viewer.media.imageExpandedLoading = true
	viewer.media.imageExpandedCancel = viewer.cancel
	return viewer
}

func markdownNavigationCommentOwner(t *testing.T, s *app, taskID, url string) (*requirementReaderState, *markdownEditorState) {
	t.Helper()
	reader := s.requirementReader(taskID, url)
	s.updateRequirementReaderComments(reader, application.RequirementComments{
		Comments: []domain.RequirementComment{{ID: "comment-a", Content: "原评论"}},
	})
	owner := reader.commentMarkdown["id:comment-a"]
	if owner == nil || !owner.commentReading {
		t.Fatal("requirement reader did not create its comment Markdown owner")
	}
	return reader, owner
}
