package integrations

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"silverwing-go/internal/domain"
)

func TestCommentEditJournalFailureNeverReplaysOrDropsDraft(t *testing.T) {
	cli := buildCommentEditCLI(t)
	for _, stage := range []string{"intent", "success", "rejection"} {
		t.Run(stage, func(t *testing.T) {
			f := newCommentFixture(t, cli)
			seedEditComment(t, f, false, "other", "")
			editDraft(t, f, "load", nil)
			editDraft(t, f, "save", map[string]any{"content": "本地修改必须保留"})
			dir, _ := f.s.commentDirectory(f.draft)
			journal := filepath.Join(dir, "draft.json")
			var handle syscall.Handle
			block := func() {
				name, err := syscall.UTF16PtrFromString(journal)
				if err != nil {
					t.Fatal(err)
				}
				handle, err = syscall.CreateFile(name, syscall.GENERIC_READ, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
				if err != nil {
					t.Fatal(err)
				}
			}
			defer func() {
				if handle != 0 {
					_ = syscall.CloseHandle(handle)
				}
			}()
			if stage == "intent" {
				block()
			}
			if stage == "rejection" {
				f.mode(t, "permission-denied")
			}
			r, _ := updateDraft(t, f, func(e domain.CommentPublishProgress) {
				if stage != "intent" && e.Stage == "保存修改" {
					block()
				}
			})
			if !r.Draft.HasContent() {
				t.Fatal("journal error dropped edit")
			}
			u, c, _, _ := commentEditCalls(t, f)
			if c != 0 || stage == "intent" && u != 0 || stage != "intent" && u != 1 {
				t.Fatal("incorrect update count", u, c)
			}
			if stage == "intent" {
				if r.Status != "failed" || r.Draft.Pending() {
					t.Fatal("unissued update is not editable")
				}
			} else if r.Status != "unknown" || !r.Draft.Pending() {
				t.Fatal("unpersisted outcome enabled another write")
			}
			_ = syscall.CloseHandle(handle)
			handle = 0
			if _, err := os.ReadFile(journal); err != nil {
				t.Fatal("durable journal missing", err)
			}
			if stage == "success" {
				v, err := f.s.Execute(context.Background(), "requirements.comments/check", editPayload(f, ""))
				if err != nil {
					t.Fatal(err)
				}
				if v.(domain.CommentPublishResult).Status != "updated" {
					t.Fatal("read-only recovery failed")
				}
				if updates, creates, _, _ := commentEditCalls(t, f); updates != 1 || creates != 0 {
					t.Fatal("check wrote again")
				}
			}
		})
	}
}
