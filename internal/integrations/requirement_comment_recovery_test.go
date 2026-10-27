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

func TestCommentPublishLegacyUnknownReadOnlyRecovery(t *testing.T) {
	cli := buildCommentCLI(t)
	for _, mode := range []string{"confirmed", "save-failure", "read-account-change"} {
		t.Run(mode, func(t *testing.T) {
			f := newCommentFixture(t, cli)
			f.edit(t, "save", map[string]any{"content": "# 你好"})
			file := filepath.Join(f.root, "image.png")
			out, err := os.Create(file)
			if err != nil {
				t.Fatal(err)
			}
			if err = png.Encode(out, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
				t.Fatal(err)
			}
			_ = out.Close()
			f.edit(t, "add-image", map[string]any{"sourcePath": file})
			f.draft.Images[0].Upload = &domain.CommentUpload{Token: "image-token-1", URL: "https://project.feishu.cn/upload/image-token-1", Scope: f.draft.Scope}
			f.draft.Attempt = &domain.CommentPublishAttempt{ID: commentRandomID(), State: "unknown", StartedAt: "2026-10-27T01:52:57.8768215Z", AuthorKey: "user-1", BeforeIDs: []string{"old"}}
			f.draft.Attempt.Body = f.draft.Markdown(func(a domain.CommentDraftAsset) (string, string) { return a.Upload.URL, a.Upload.Token })
			if err = f.s.saveCommentDraft(&f.draft); err != nil {
				t.Fatal(err)
			}
			f.s = NewService(f.s.paths, f.s.config, nil)
			f.edit(t, "load", nil)
			if !f.draft.Pending() {
				t.Fatal("restart lost the pending attempt")
			}
			remote := map[string]any{"comment_id": "posted", "creator": "user-1", "content": "# 你好\n\n![](https://project.feishu.cn/goapi/v5/platform/file/stream/download/image-token-1)", "created_at": "2026-10-27 09:53:00"}
			raw, _ := json.Marshal(remote)
			if err = os.WriteFile(filepath.Join(f.root, "comment"), raw, 0600); err != nil {
				t.Fatal(err)
			}
			f.mode(t, mode)
			var value any
			if mode == "save-failure" {
				dir, e := f.s.commentDirectory(f.draft)
				if e != nil {
					t.Fatal(e)
				}
				if e = os.Rename(filepath.Join(dir, "draft.json"), filepath.Join(dir, "draft.bak")); e != nil {
					t.Fatal(e)
				}
				if e = os.Mkdir(filepath.Join(dir, "draft.json"), 0700); e != nil {
					t.Fatal(e)
				}
				value, err = f.s.reconcileCommentPublish(context.Background(), f.config, &f.draft)
			} else {
				value, err = f.s.Execute(context.Background(), "requirements.comments/check", f.payload("check"))
			}
			if err != nil {
				t.Fatal(err)
			}
			result := value.(domain.CommentPublishResult)
			if mode == "confirmed" {
				if result.Status != "published" || result.CommentID != "posted" || result.Draft.HasContent() {
					t.Fatalf("legacy recovery failed: %s %s", result.Status, result.Message)
				}
				saved, e := f.s.readCommentDraft(f.draft.Identity, f.draft.Item)
				if e != nil || saved.Attempt == nil || saved.Attempt.State != "published" {
					t.Fatal("success was not durable", e)
				}
			} else {
				if result.Status != "unknown" || !result.Draft.HasContent() || !result.Draft.Pending() {
					t.Fatal("failure cleared the draft")
				}
				if mode == "save-failure" && !strings.Contains(result.Message, "保存失败") {
					t.Fatal("missing save failure detail", result.Message)
				}
				if _, e := os.Stat(f.draft.Images[0].LocalPath); e != nil {
					t.Fatal("failure deleted staged image", e)
				}
			}
			if creates, uploads := f.counts(t); creates != 0 || uploads != 0 {
				t.Fatalf("check performed a remote write: %d creates %d uploads", creates, uploads)
			}
		})
	}
}

func TestCommentPublishRetainsRequestFeedbackAcrossChecks(t *testing.T) {
	cli := buildCommentCLI(t)
	for _, mode := range []string{"create-fail", "create-business-fail"} {
		t.Run(mode, func(t *testing.T) {
			f := newCommentFixture(t, cli)
			f.edit(t, "save", map[string]any{"content": "保留草稿"})
			f.mode(t, mode)
			r, _ := f.publish(t, nil)
			for i := 0; i < 3; i++ {
				if r.Status != "unknown" || !r.Draft.Pending() || !r.Draft.HasContent() || !strings.Contains(r.Draft.Attempt.Error, "fixture request feedback") {
					t.Fatalf("check %d erased the create feedback or draft: %s %s", i, r.Status, r.Message)
				}
				f.draft = r.Draft
				value, err := f.s.Execute(context.Background(), "requirements.comments/check", f.payload("check"))
				if err != nil {
					t.Fatal(err)
				}
				next := value.(domain.CommentPublishResult)
				if next.Draft.Attempt.Error != r.Draft.Attempt.Error {
					t.Fatal("repeated checks accumulated or changed the request feedback")
				}
				r = next
			}
			if creates, uploads := f.counts(t); creates != 1 || uploads != 0 {
				t.Fatalf("checks retried a write: %d creates %d uploads", creates, uploads)
			}
		})
	}
}
