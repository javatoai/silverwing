package desktop

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

type singleNamingBackend struct{ calls int }

func (*singleNamingBackend) Snapshot(context.Context) (application.Snapshot, error) {
	return application.Snapshot{}, nil
}
func (b *singleNamingBackend) Dispatch(_ context.Context, action application.Action) (application.ActionResult, error) {
	if action.Kind == "requirements.ai-name" {
		b.calls++
		return application.ActionResult{Data: domain.RequirementAiNamingSuggestion{FolderName: "已存在名", BranchSuffix: "existing_name"}}, nil
	}
	return application.ActionResult{}, nil
}

func TestTaskNamingCollisionReturnsFirstSuggestionWithoutRetry(t *testing.T) {
	s, _ := taskCreationCompactFixture(t, 960, false, 1)
	form := s.taskUnit.state.taskCreation
	form.aiEligible = true
	root := *form.config.TaskRoot
	if err := os.MkdirAll(filepath.Join(root, "已存在名"), 0700); err != nil {
		t.Fatal(err)
	}
	backend := &singleNamingBackend{}
	request := &taskCreationAINamingRequest{form: form, token: form.token, ctx: context.Background(), action: application.Action{Kind: "requirements.ai-name", Payload: map[string]any{"requestLink": form.draft.RequirementLink}}}
	result, err := runTaskCreationAINamingRequest(backend, request)
	if err != nil || backend.calls != 1 {
		t.Fatalf("collision retried: calls=%d err=%v", backend.calls, err)
	}
	s.taskCreationActionResult(request.actionWithToken(), result)
	if form.draft.TaskName != "已存在名" {
		t.Fatalf("occupied suggestion was discarded: %s", form.draft.TaskName)
	}
	if got := checkTaskCreationDirectory(root, form.draft.TaskName); got != taskCreationDirectoryOccupied {
		t.Fatal(got)
	}
}

func (r *taskCreationAINamingRequest) actionWithToken() application.Action {
	a := r.action
	a.Payload = taskCreationActionPayload(a.Payload, r.token)
	return a
}

func TestTaskDirectoryCheckRejectsStaleResultsAndPreservesEdits(t *testing.T) {
	for _, change := range []string{"name", "root", "home", "closed"} {
		t.Run(change, func(t *testing.T) {
			s, _ := taskCreationCompactFixture(t, 960, false, 1)
			form := s.taskUnit.state.taskCreation
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			request := &taskCreationDirectoryCheck{form: form, signature: s.taskCreationDirectorySignature(), ctx: ctx, cancel: cancel, resume: true}
			form.directoryCheck = taskCreationDirectoryState{signature: request.signature, request: request}
			switch change {
			case "name":
				form.draft.TaskName = "手工新名"
			case "root":
				root := t.TempDir()
				form.config.TaskRoot = &root
			case "home":
				s.snapshot.Home += "-changed"
			case "closed":
				s.dialogOpen = false
			}
			s.finishTaskCreationDirectoryCheck(request, taskCreationDirectoryOccupied)
			if form.directoryCheck.message != "" || form.phase != "editing" {
				t.Fatal("late directory result changed form")
			}
		})
	}
}

func TestTaskDirectoryCollisionFeedbackAndCreatedWarning(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(map[bool]string{true: "dark", false: "light"}[dark], func(t *testing.T) {
			s, tester := taskCreationCompactFixture(t, 960, dark, 1)
			s.taskUnit.state.taskCreation.directoryCheck = taskCreationDirectoryState{signature: s.taskCreationDirectorySignature(), message: taskCreationDirectoryOccupied}
			tester.Frame()
			if !tester.HasText(taskCreationDirectoryOccupied) {
				t.Fatal("collision warning missing")
			}
			captureTaskCreationLeft(t, tester, "collision-"+map[bool]string{true: "dark", false: "light"}[dark])
			if message, _ := s.taskCreationValidationFailure(); message != taskCreationDirectoryOccupied {
				t.Fatal(message)
			}
			var toasts []ui.Toast
			action := application.Action{Kind: "task.create", Payload: map[string]any{"taskCreateToken": s.taskUnit.state.taskCreation.token}}
			s.taskCreationActionResult(action, application.ActionResult{Message: "任务已创建", Text: "工作区工具未全部打开：fixture"})
			if s.dialogOpen || s.taskUnit.state.taskCreation != nil || s.lastError != "" || s.taskUnit.state.taskCreationWarning == "" {
				t.Fatal("committed create left a retryable creation form")
			}
			ui.NewTester(func(c *ui.Context) {
				s.renderOperationNotifications(c)
				ui.ToastViewportBase(c, func(_ ui.Element, items []ui.Toast) { toasts = append([]ui.Toast(nil), items...) })
			}, 960, 800)
			found := false
			for _, toast := range toasts {
				if toast.Type == "warning" && toast.Title == s.taskUnit.state.taskCreationWarning {
					if toast.ContentMaxHeight != 360 {
						t.Fatal("long follow-up warnings must wrap and scroll")
					}
					found = true
				}
			}
			if !found {
				t.Fatal("follow-up did not use a separate warning toast")
			}
		})
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "同名文件"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if got := checkTaskCreationDirectory(root, "同名文件"); got != taskCreationDirectoryOccupied {
		t.Fatal("file was treated as an available directory")
	}
}
