package integrations

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"silverwing-go/internal/domain"
)

func TestCommentRetrySaveFailureRetainsPendingCredentials(t *testing.T) {
	f := newCommentFixture(t, buildCommentCLI(t))
	f.edit(t, "save", map[string]any{"content": "保留原文"})
	f.draft.Images = []domain.CommentDraftAsset{{ID: commentRandomID(), Upload: &domain.CommentUpload{Token: "previously-used-image", Scope: f.draft.Scope}}}
	f.draft.Attachment = &domain.CommentDraftAsset{ID: commentRandomID(), Upload: &domain.CommentUpload{Token: "previously-used-attachment", Scope: f.draft.Scope}}
	f.draft.Attempt = &domain.CommentPublishAttempt{ID: commentRandomID(), State: "unknown", AuthorKey: "user-1"}
	if err := f.s.saveCommentDraft(&f.draft); err != nil {
		t.Fatal(err)
	}
	dir, err := f.s.commentDirectory(f.draft)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "draft.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	// Reading is allowed, but replacing the durable journal is blocked.
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.CloseHandle(handle)
	p := f.payload("allow-retry")
	p["confirmed"], p["attemptID"] = true, f.draft.Attempt.ID
	if _, err := f.s.Execute(context.Background(), "requirements.comment-draft", p); err == nil {
		t.Fatal("retry ignored the journal replacement failure")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("failed retry altered the pending journal")
	}
	p = f.payload("")
	p["requestID"] = commentRandomID()
	value, err := f.s.PublishRequirementComment(context.Background(), p, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := value.(domain.CommentPublishResult)
	if result.Status != "unknown" || !result.Draft.Pending() || result.Draft.Images[0].Upload == nil || result.Draft.Attachment.Upload == nil {
		t.Fatal("save failure made the pending draft publishable or lost its matching credentials")
	}
	if creates, uploads := f.counts(t); creates != 0 || uploads != 0 {
		t.Fatalf("save failure started remote writes: %d creates %d uploads", creates, uploads)
	}
}
