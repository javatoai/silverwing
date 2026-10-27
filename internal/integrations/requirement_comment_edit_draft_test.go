package integrations

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"silverwing-go/internal/domain"
)

func TestCommentEditDraftReloadDiscardAndTargetValidation(t *testing.T) {
	f := newCommentFixture(t, buildCommentEditCLI(t))
	f.edit(t, "save", map[string]any{"content": "新增评论草稿仍保留"})
	creation := f.draft
	seedEditComment(t, f, true, "other", "parent")
	editDraft(t, f, "load", nil)
	baseline := f.draft
	editDraft(t, f, "save", map[string]any{"content": f.draft.Content + "\n本地修改"})
	f.s = NewService(f.s.paths, f.s.config, nil)
	editDraft(t, f, "load", nil)
	if f.draft.Content != baseline.Content+"\n本地修改" || f.draft.Edit.Original.Content != baseline.Content {
		t.Fatal("restart changed edited source or baseline")
	}
	for _, op := range []string{"reload", "discard"} {
		if _, err := f.s.Execute(context.Background(), "requirements.comment-draft", editPayload(f, op)); err == nil {
			t.Fatal("unconfirmed destructive draft action", op)
		}
	}
	bad := editPayload(f, "save")
	bad["editCommentID"] = "another-id"
	bad["content"] = "wrong"
	if _, err := f.s.Execute(context.Background(), "requirements.comment-draft", bad); err == nil {
		t.Fatal("draft identity was not bound to comment ID")
	}
	bad = editPayloadWithRequest(f)
	delete(bad, "editCommentID")
	if _, err := f.s.UpdateRequirementComment(context.Background(), bad, nil); err == nil {
		t.Fatal("update accepted an empty comment ID")
	}
	if _, err := f.s.PublishRequirementComment(context.Background(), editPayloadWithRequest(f), nil); err == nil {
		t.Fatal("creation accepted an edit draft")
	}
	seedEditComment(t, f, false, "other", "parent")
	editDraft(t, f, "load", nil)
	if !f.draft.Edit.Conflict || f.draft.Content != baseline.Content+"\n本地修改" {
		t.Fatal("fresh load overwrote local conflict")
	}
	editDraft(t, f, "reload", map[string]any{"confirmed": true})
	if f.draft.HasChanges() || f.draft.Edit.Conflict || f.draft.Content != "原始 **Markdown**" || len(f.draft.Images) != 0 || f.draft.Attachment != nil {
		t.Fatal("confirmed reload did not replace baseline/resources")
	}
	for _, a := range baseline.Images {
		if _, err := f.s.commentAssetPath(baseline, a); !os.IsNotExist(err) {
			t.Fatal("obsolete resource not removed", err)
		}
	}
	editDraft(t, f, "save", map[string]any{"content": "新编辑稿"})
	editDraft(t, f, "discard", map[string]any{"confirmed": true})
	if f.draft.HasContent() || f.draft.Edit.Original != nil {
		t.Fatal("discard retained edit content")
	}
	current, err := f.s.readCommentDraft(creation.Identity, creation.Item)
	if err != nil || current.Content != creation.Content || current.ID != creation.ID {
		t.Fatal("discard affected create draft", err)
	}
	if u, c, _, _ := commentEditCalls(t, f); u != 0 || c != 0 {
		t.Fatal("draft operations wrote remotely")
	}
}

func TestCommentEditConcurrentSameRequestOnlyUpdatesOnce(t *testing.T) {
	f := newCommentFixture(t, buildCommentEditCLI(t))
	seedEditComment(t, f, false, "user-1", "")
	editDraft(t, f, "load", nil)
	editDraft(t, f, "save", map[string]any{"content": "只保存一次"})
	p := editPayloadWithRequest(f)
	type outcome struct {
		v any
		e error
	}
	done := make(chan outcome, 2)
	for i := 0; i < 2; i++ {
		go func() { v, e := f.s.UpdateRequirementComment(context.Background(), p, nil); done <- outcome{v, e} }()
	}
	for i := 0; i < 2; i++ {
		r := <-done
		if r.e != nil || r.v.(domain.CommentPublishResult).Status != "updated" {
			t.Fatal("concurrent idempotent result", r.e)
		}
	}
	if u, c, _, _ := commentEditCalls(t, f); u != 1 || c != 0 {
		t.Fatalf("writes update=%d create=%d", u, c)
	}
}

func TestCommentEditDefiniteFailureRefreshesResourcesOnExplicitSave(t *testing.T) {
	f := newCommentFixture(t, buildCommentEditCLI(t))
	seedEditComment(t, f, true, "user-1", "")
	editDraft(t, f, "load", nil)
	editDraft(t, f, "save", map[string]any{"content": f.draft.Content + "\n修改"})
	_ = os.WriteFile(filepath.Join(f.root, "single-use-tokens"), nil, 0600)
	f.mode(t, "invalid-token")
	r, _ := updateDraft(t, f, nil)
	if r.Status != "failed" || r.Draft.Pending() {
		t.Fatal("definite rejection not editable")
	}
	for _, a := range f.draft.Images {
		if a.Upload != nil {
			t.Fatal("rejected request retained image receipt")
		}
	}
	if f.draft.Attachment.Upload != nil {
		t.Fatal("rejected request retained attachment receipt")
	}
	f.mode(t, "normal")
	r, _ = updateDraft(t, f, nil)
	if r.Status != "updated" {
		t.Fatal(r.Status, r.Message)
	}
	if u, c, uploads, _ := commentEditCalls(t, f); u != 2 || c != 0 || uploads != 6 {
		t.Fatalf("retry did not upload fresh resources: u=%d c=%d uploads=%d", u, c, uploads)
	}
}
