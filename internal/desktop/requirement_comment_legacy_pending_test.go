package desktop

import (
	"encoding/json"
	"github.com/egoist/mygo/ui"
	"path/filepath"
	"silverwing-go/internal/domain"
	markdownui "silverwing-go/internal/ui/markdown"
	"testing"
)

func TestCommentLegacyPendingImagesRemainReadOnlyAndReachable(t *testing.T) {
	s, backend, _, state := commentComposerFixture(t)
	path := filepath.Join(t.TempDir(), "legacy.png")
	writeMarkdownImageFixture(t, path, 48, 24)
	state.body, state.draft.Content = "待核对正文", "待核对正文"
	state.draft.Images = []domain.CommentDraftAsset{{ID: "legacy", Name: "legacy.png", LocalPath: path, Upload: &domain.CommentUpload{Token: "old-token", URL: "https://example.com/legacy"}}}
	state.draft.Attempt = &domain.CommentPublishAttempt{State: "unknown", Body: "legacy publish snapshot"}
	before, _ := json.Marshal(state.draft)
	tester := ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, false)
		ui.Column(c).Fill()
		s.renderRequirementCommentComposer(c, p)
		s.renderCommentDraftBitmapViewer(c, p)
	}, 960, 900)
	if err := tester.Click("待核对图片：legacy.png"); err != nil {
		t.Fatal(err)
	}
	viewer := s.materialsUnit.state.markdownBitmapViewer
	if viewer == nil || viewer.composer != state || viewer.sourcePath != path {
		t.Fatal("legacy pending image has no safe read-only viewer")
	}
	after, _ := json.Marshal(state.draft)
	if string(before) != string(after) || state.body != state.draft.Content || backend.count.Load() != 1 {
		t.Fatal("viewing pending image migrated or republished draft")
	}
	tester.Key(0, ui.KeyEscape)
	if !state.open || s.materialsUnit.state.markdownBitmapViewer != nil {
		t.Fatal("legacy image dismissal closed composer")
	}
}

func TestCommentLegacyPendingSourceViewerOnlyAcceptsKnownAsset(t *testing.T) {
	s, backend, _, state := commentComposerFixture(t)
	path := filepath.Join(t.TempDir(), "legacy.png")
	writeMarkdownImageFixture(t, path, 48, 24)
	state.body, state.draft.Content = "pending body", "pending body"
	state.draft.Images = []domain.CommentDraftAsset{{ID: "legacy", Name: "legacy.png", LocalPath: path}}
	state.draft.Attempt = &domain.CommentPublishAttempt{State: "unknown", Body: "original publication"}
	owner := s.ensureCommentMarkdownEditor(state)
	if err := owner.componentEditor.SetMode(markdownui.Source); err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(state.draft)
	view := ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, false)
		ui.Column(c).Fill()
		s.renderRequirementCommentComposer(c, p)
		s.renderCommentDraftBitmapViewer(c, p)
	}, 960, 900)
	if err := view.Click("待核对图片：legacy.png"); err != nil {
		t.Fatal(err)
	}
	viewer := s.materialsUnit.state.markdownBitmapViewer
	if viewer == nil || !s.commentComposerImageCurrent(viewer) {
		t.Fatal("known legacy asset cannot open from read-only source")
	}
	copyViewer := *viewer
	copyViewer.sourcePath = filepath.Join(filepath.Dir(path), "unrelated.png")
	if s.commentComposerImageCurrent(&copyViewer) {
		t.Fatal("source viewer accepted unrelated path")
	}
	state.draft.Attempt = nil
	if s.commentComposerImageCurrent(viewer) {
		t.Fatal("source viewer survived pending-state change")
	}
	state.draft.Attempt = &domain.CommentPublishAttempt{State: "unknown", Body: "original publication"}
	after, _ := json.Marshal(state.draft)
	if string(before) != string(after) || backend.count.Load() != 1 {
		t.Fatal("viewing legacy source migrated or published draft")
	}
}
