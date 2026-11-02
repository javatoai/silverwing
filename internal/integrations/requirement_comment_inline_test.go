package integrations

import (
	"context"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
)

func TestCommentInlineDraftMigrationStaleRevisionAndUndoCache(t *testing.T) {
	f := newCommentFixture(t, buildCommentCLI(t))
	path := filepath.Join(f.root, "旧图.png")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = png.Encode(file, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	f.edit(t, "save", map[string]any{"content": "前文"})
	f.edit(t, "add-image", map[string]any{"sourcePath": path})
	legacy := f.draft
	stale := f.payload("save")
	stale["content"] = "旧窗口覆盖"
	f.edit(t, "load", nil)
	a := f.draft.Images[0]
	want := "前文\n\n![旧图.png](" + domain.CommentImageMarkdownRef(a.ID) + ")"
	if f.draft.Content != want || a.MarkdownRef != domain.CommentImageMarkdownRef(a.ID) || f.draft.Revision != legacy.Revision+1 {
		t.Fatalf("load did not migrate one atomic draft revision: %#v", f.draft)
	}
	dir, err := f.s.commentDirectory(f.draft)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "draft.json"))
	if err != nil {
		t.Fatal(err)
	}
	var persisted domain.RequirementCommentDraft
	if err = json.Unmarshal(raw, &persisted); err != nil || persisted.Content != want || persisted.Images[0].MarkdownRef != a.MarkdownRef || persisted.Revision != f.draft.Revision {
		t.Fatal("content, references and revision were not persisted together", err)
	}
	if _, err = f.s.Execute(context.Background(), "requirements.comment-draft", stale); err == nil {
		t.Fatal("pre-migration revision replaced migrated body")
	}
	f.edit(t, "save", map[string]any{"content": "仅保留正文 ![手写](./not-staged.png)"})
	if len(f.draft.ActiveImages()) != 0 || len(f.draft.Images) != 1 {
		t.Fatal("deleted image stayed active or lost its undo metadata")
	}
	if _, err = f.s.commentAssetPath(f.draft, f.draft.Images[0]); err != nil {
		t.Fatal("deleted reference removed its undo cache", err)
	}
	f.edit(t, "save", map[string]any{"content": "恢复 ![图](" + a.MarkdownRef + ") 中间 ![同一图](" + a.MarkdownRef + ")"})
	if len(f.draft.ActiveImages()) != 1 {
		t.Fatal("repeated references did not share one active upload")
	}
	if creates, uploads := f.counts(t); creates != 0 || uploads != 0 {
		t.Fatal("draft migration/save uploaded or published remotely")
	}
	for _, pending := range []string{"publishing", "unknown"} {
		d := legacy
		d.Images = append([]domain.CommentDraftAsset(nil), legacy.Images...)
		d.Images[0].MarkdownRef = ""
		d.Attempt = &domain.CommentPublishAttempt{ID: commentRandomID(), State: pending, Body: "保留发布快照"}
		if err = f.s.saveCommentDraft(&d); err != nil {
			t.Fatal(err)
		}
		f.draft = d
		f.edit(t, "load", nil)
		if f.draft.Content != legacy.Content || f.draft.Images[0].MarkdownRef != "" || f.draft.Attempt.Body != "保留发布快照" || !f.draft.Pending() {
			t.Fatal("pending snapshot was normalized or made editable")
		}
	}
	f.edit(t, "allow-retry", map[string]any{"confirmed": true, "attemptID": f.draft.Attempt.ID})
	f.edit(t, "load", nil)
	ref := f.draft.Images[0].MarkdownRef
	f.edit(t, "save", map[string]any{"content": "前 ![图](" + ref + ") 中 ![重复](" + ref + ") 后 ![手写](./private.png)"})
	f.edit(t, "add-image", map[string]any{"sourcePath": path, "inlineImage": true})
	if len(f.draft.Images) != 2 || len(f.draft.ActiveImages()) != 1 {
		t.Fatal("an unused staged import became an appended image")
	}
	r, _ := f.publish(t, nil)
	if r.Status != "published" || strings.Contains(r.Draft.Attempt.Body, "#sw-comment-image/") || !strings.Contains(r.Draft.Attempt.Body, " 中 ") || !strings.Contains(r.Draft.Attempt.Body, "![手写](./private.png)") {
		t.Fatal("inline publication lost its position or uploaded an arbitrary Markdown file", r.Status, r.Message)
	}
	if creates, uploads := f.counts(t); creates != 1 || uploads != 1 {
		t.Fatalf("repeated asset was uploaded repeatedly or unused asset uploaded: creates %d uploads %d", creates, uploads)
	}
}

func TestCommentImageLimitCountsActiveAndPendingImages(t *testing.T) {
	d := domain.RequirementCommentDraft{}
	for i := 0; i < 25; i++ {
		id := strings.Repeat("a", i+1)
		d.Images = append(d.Images, domain.CommentDraftAsset{ID: id, MarkdownRef: domain.CommentImageMarkdownRef(id)})
		if i < 19 {
			d.Content += "![图](" + domain.CommentImageMarkdownRef(id) + ")\n"
		}
	}
	if !commentCanStageImage(d, map[string]any{"pendingImages": 1}) || commentCanStageImage(d, map[string]any{"pendingImages": 2}) {
		t.Fatal("inactive undo cache consumed slots, or pending imports exceeded twenty")
	}
	d.Content += "![重复](" + d.Images[0].MarkdownRef + ")"
	if !commentCanStageImage(d, nil) {
		t.Fatal("a repeated reference consumed another upload slot")
	}
}

func TestCommentMatchUsesInlineSequenceAndIgnoresUnusedImports(t *testing.T) {
	a := domain.CommentDraftAsset{ID: "one", MarkdownRef: domain.CommentImageMarkdownRef("one"), Upload: &domain.CommentUpload{URL: "https://example.com/one", Token: "token-one"}}
	d := domain.RequirementCommentDraft{Content: "前 ![图](" + a.MarkdownRef + ") 中 ![再用](" + a.MarkdownRef + ") 后", Images: []domain.CommentDraftAsset{a, {ID: "unused", MarkdownRef: domain.CommentImageMarkdownRef("unused")}}}
	body := d.Markdown(func(a domain.CommentDraftAsset) (string, string) { return a.Upload.URL, a.Upload.Token })
	d.Attempt = &domain.CommentPublishAttempt{State: "unknown", Body: body, CommentID: "comment", AuthorKey: "author"}
	c := domain.RequirementComment{ID: "comment", Content: body, AuthorKey: "author"}
	if matched, reason := matchPublishedComment(&d, []domain.RequirementComment{c}); matched.ID != "comment" {
		t.Fatal("duplicate occurrence/unused cache blocked reconciliation", reason)
	}
	c.Content = strings.Replace(c.Content, " 中 ", " 改动 ", 1)
	if matched, _ := matchPublishedComment(&d, []domain.RequirementComment{c}); matched.ID != "" {
		t.Fatal("body change was hidden by image reconciliation")
	}
}
