package integrations

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"silverwing-go/internal/domain"
)

func TestCommentConfirmedRetryRefreshesConsumedUploads(t *testing.T) {
	cli := buildCommentCLI(t)
	for _, kind := range []string{"image", "attachment", "mixed"} {
		t.Run(kind, func(t *testing.T) {
			f := newCommentFixture(t, cli)
			if err := os.WriteFile(filepath.Join(f.root, "single-use-tokens"), []byte("true"), 0600); err != nil {
				t.Fatal(err)
			}
			f.edit(t, "save", map[string]any{"content": "# 保留原文"})
			if kind != "attachment" {
				path := filepath.Join(f.root, "image.png")
				var imageBytes bytes.Buffer
				if err := png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, imageBytes.Bytes(), 0600); err != nil {
					t.Fatal(err)
				}
				f.edit(t, "add-image", map[string]any{"sourcePath": path})
			}
			if kind != "image" {
				path := filepath.Join(f.root, "说明.txt")
				if err := os.WriteFile(path, []byte("attachment"), 0600); err != nil {
					t.Fatal(err)
				}
				f.edit(t, "add-attachment", map[string]any{"sourcePath": path})
			}
			// The server accepted the comment and consumed its tokens, but the
			// list is temporarily unavailable. Restart/check must not resend it.
			f.mode(t, "read-fail")
			pending, _ := f.publish(t, nil)
			if pending.Status != "unknown" || !pending.Draft.Pending() {
				t.Fatal("fixture did not retain the pending publication")
			}
			original := f.draft
			dir, err := f.s.commentDirectory(original)
			if err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(filepath.Join(dir, "draft.json"))
			if err != nil {
				t.Fatal(err)
			}
			for _, invalid := range []string{"cancelled", "attempt changed", "revision changed", "scope changed"} {
				p := f.payload("allow-retry")
				p["attemptID"], p["confirmed"] = original.Attempt.ID, true
				switch invalid {
				case "cancelled":
					p["confirmed"] = false
				case "attempt changed":
					p["attemptID"] = commentRandomID()
				case "revision changed":
					p["revision"] = original.Revision + 1
				case "scope changed":
					p["scope"] = "stale-scope"
				}
				if _, err := f.s.Execute(context.Background(), "requirements.comment-draft", p); err == nil {
					t.Fatalf("%s retry changed receipts", invalid)
				}
			}
			after, err := os.ReadFile(filepath.Join(dir, "draft.json"))
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("cancelled confirmation changed the durable pending attempt")
			}
			f.edit(t, "allow-retry", map[string]any{"confirmed": true, "attemptID": original.Attempt.ID})
			for _, a := range f.draft.Images {
				if a.Upload != nil {
					t.Fatal("confirmed retry retained an image receipt")
				}
				if _, err := os.Stat(a.LocalPath); err != nil {
					t.Fatal("confirmed retry lost a local image", err)
				}
			}
			if f.draft.Attachment != nil {
				if f.draft.Attachment.Upload != nil {
					t.Fatal("confirmed retry retained an attachment receipt")
				}
				if _, err := os.Stat(f.draft.Attachment.LocalPath); err != nil {
					t.Fatal("confirmed retry lost a local attachment", err)
				}
			}
			if f.draft.Content != original.Content || f.draft.ID != original.ID || len(f.draft.Images) != len(original.Images) {
				t.Fatal("confirmed retry changed the draft content or target")
			}
			assets := len(f.draft.Images)
			if f.draft.Attachment != nil {
				assets++
			}
			if creates, uploads := f.counts(t); creates != 1 || uploads != assets {
				t.Fatalf("local retry confirmation performed a remote write: %d creates %d uploads", creates, uploads)
			}
			f.s = NewService(f.s.paths, f.s.config, nil)
			f.edit(t, "load", nil)
			f.mode(t, "normal")
			result, request := f.publish(t, nil)
			if result.Status != "published" || result.CommentID != "comment-2" {
				t.Fatalf("retry reused consumed file credentials: %s %s", result.Status, result.Message)
			}
			if _, err := f.s.PublishRequirementComment(context.Background(), request, nil); err != nil {
				t.Fatal("duplicate click did not reuse the completed result", err)
			}
			if creates, uploads := f.counts(t); creates != 2 || uploads != 2*assets {
				t.Fatalf("retry must upload once per asset and issue exactly one create: %d creates %d uploads", creates, uploads)
			}
		})
	}
}

func TestCommentPendingCheckPreservesConsumedReceipt(t *testing.T) {
	cli := buildCommentCLI(t)
	f := newCommentFixture(t, cli)
	f.edit(t, "save", map[string]any{"content": "待核对"})
	f.draft.Images = []domain.CommentDraftAsset{{ID: commentRandomID(), Name: "image.png", Upload: &domain.CommentUpload{Token: "existing-token", URL: "https://project.feishu.cn/files/existing-token", Scope: f.draft.Scope}}}
	f.draft.Attempt = &domain.CommentPublishAttempt{ID: commentRandomID(), State: "unknown", AuthorKey: "user-1", Body: f.draft.Markdown(func(a domain.CommentDraftAsset) (string, string) { return a.Upload.URL, a.Upload.Token })}
	if err := f.s.saveCommentDraft(&f.draft); err != nil {
		t.Fatal(err)
	}
	value, err := f.s.Execute(context.Background(), "requirements.comments/check", f.payload("check"))
	if err != nil {
		t.Fatal(err)
	}
	result := value.(domain.CommentPublishResult)
	if result.Status != "unknown" || result.Draft.Images[0].Upload == nil || result.Draft.Images[0].Upload.Token != "existing-token" {
		t.Fatal("read-only check invalidated the pending attempt's matching credentials")
	}
	if creates, uploads := f.counts(t); creates != 0 || uploads != 0 {
		t.Fatalf("check started a remote write: %d creates %d uploads", creates, uploads)
	}
}
