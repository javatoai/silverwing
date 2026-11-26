package desktop

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
)

type taskRequirementCommentSyncTestBackend struct {
	application.Backend
	status  application.TaskRequirementCommentSyncStatus
	path    string
	created string
	retries int
}

func (*taskRequirementCommentSyncTestBackend) StartTaskRequirementCommentSync() {}
func (*taskRequirementCommentSyncTestBackend) CloseTaskRequirementCommentSync(context.Context) error {
	return nil
}
func (*taskRequirementCommentSyncTestBackend) TaskRequirementCommentSyncChanges() <-chan struct{} {
	return nil
}
func (b *taskRequirementCommentSyncTestBackend) TaskRequirementCommentSyncStatus(path, created string) application.TaskRequirementCommentSyncStatus {
	if path == b.path && created == b.created {
		return b.status
	}
	return application.TaskRequirementCommentSyncStatus{}
}
func (b *taskRequirementCommentSyncTestBackend) RetryTaskRequirementCommentSync(path, created string) error {
	if path != b.path || created != b.created {
		panic("retry used the wrong task identity")
	}
	b.retries++
	b.status.State, b.status.Message = "pending", ""
	return nil
}

func taskRequirementCommentSyncFixture() (*app, *task, *taskRequirementCommentSyncTestBackend) {
	s := taskNavigationFixture()
	target := &s.taskUnit.state.tasks[1]
	target.path = "task-ui-path"
	target.requirementURL = "https://project.feishu.cn/obt/userstory/detail/1720002"
	backend := &taskRequirementCommentSyncTestBackend{path: target.path, created: target.createdAt,
		status: application.TaskRequirementCommentSyncStatus{State: "uncertain", RequirementURL: target.requirementURL, Message: "发送结果暂无法核对"}}
	s.backend = backend
	s.taskUnit.state.requirementCommentSync = &taskRequirementCommentSyncView{}
	return s, target, backend
}

func TestTaskRequirementCommentSyncRetryKeepsContextSelection(t *testing.T) {
	s, target, backend := taskRequirementCommentSyncFixture()
	s.observeTaskRequirementCommentSync()
	var toasts []ui.Toast
	window := ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, false)
		ui.Column(c).Fill().Children(func() { s.taskIndex(c, p) })
		s.renderTaskRequirementCommentSyncNotifications(c)
		ui.ToastViewportBase(c, func(_ ui.Element, shown []ui.Toast) { toasts = slices.Clone(shown) })
	}, 640, 500)
	if len(toasts) != 1 || toasts[0].Type != "error" || !strings.Contains(toasts[0].Title, target.title) || !strings.Contains(toasts[0].Title, backend.status.Message) {
		t.Fatalf("missing task-specific failure: %+v", toasts)
	}
	navigationChooseTaskMenu(t, window, target.title, "重试同步")
	if backend.retries != 1 || s.taskUnit.state.selected != 0 || len(toasts) != 0 {
		t.Fatalf("retry changed selection or kept the resolved toast: retries=%d selected=%d toasts=%+v", backend.retries, s.taskUnit.state.selected, toasts)
	}
	backend.status.State = "failed"
	if err := window.RightClick(target.title); err != nil {
		t.Fatal(err)
	}
	target.requirementURL = "https://project.feishu.cn/obt/userstory/detail/1720003"
	_ = window.ChooseMenuItem("重试同步")
	if backend.retries != 1 {
		t.Fatal("a stale menu retried after the task requirement changed")
	}
	backend.status.State, backend.status.Message = "conflict", "任务或关联需求已变化"
	s.observeTaskRequirementCommentSync()
	window.Frame()
	if len(toasts) != 1 || !strings.Contains(toasts[0].Title, backend.status.Message) {
		t.Fatalf("association conflict should remain visible: %+v", toasts)
	}
}

func TestTaskRequirementCommentSyncRetryIsAlwaysVisible(t *testing.T) {
	for _, tc := range []struct {
		name, state string
		unlinked    bool
		canChoose   bool
	}{
		{name: "never synced", canChoose: true},
		{name: "synced", state: "synced", canChoose: true},
		{name: "pending", state: "pending"},
		{name: "syncing", state: "syncing"},
		{name: "unlinked", unlinked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, target, backend := taskRequirementCommentSyncFixture()
			backend.status.State = tc.state
			if tc.unlinked {
				target.requirementURL = ""
			}
			window := taskNavigationTester(s, 640, 500)
			if err := window.RightClick(target.title); err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(window.Menu(), "重试同步") {
				t.Fatalf("missing retry item in menu: %v", window.Menu())
			}
			err := window.ChooseMenuItem("重试同步")
			if tc.canChoose {
				if err != nil || backend.retries != 1 {
					t.Fatalf("retry did not queue: %v, attempts %d", err, backend.retries)
				}
			} else if err == nil || backend.retries != 0 {
				t.Fatalf("unavailable retry was actionable: %v, attempts %d", err, backend.retries)
			}
		})
	}
}

func TestTaskRequirementCommentSyncRefreshesOnlyMatchingCommentReaders(t *testing.T) {
	s, target, backend := taskRequirementCommentSyncFixture()
	reader := s.requirementReader(target.id, target.requirementURL)
	other := s.requirementReader("other", "https://project.feishu.cn/obt/userstory/detail/1720004")
	sameLink := s.requirementReader("", target.requirementURL+"?view=test")
	reader.body = &application.RequirementContent{Body: "需求正文"}
	reader.comments, other.comments = &application.RequirementComments{}, &application.RequirementComments{}
	draft := &requirementCommentComposer{url: target.requirementURL, body: "未提交评论", dirty: true, open: true}
	s.requirementUnit.state.commentComposers = &requirementCommentComposers{active: draft}
	s.observeTaskRequirementCommentSync()
	backend.status.State, backend.status.CommentID, backend.status.Revision = "synced", "comment-1", 1
	s.observeTaskRequirementCommentSync()
	if !reader.commentsDirty || !sameLink.commentsDirty || other.commentsDirty || reader.body.Body != "需求正文" || !draft.open || !draft.dirty || draft.body != "未提交评论" {
		t.Fatal("confirmed sync did not selectively refresh matching comments while preserving the document and draft")
	}
	reader.commentsDirty = false
	s.observeTaskRequirementCommentSync()
	if reader.commentsDirty {
		t.Fatal("reading the same publish revision dirtied the comments again")
	}
	backend.status.Revision++
	s.observeTaskRequirementCommentSync()
	if !reader.commentsDirty || other.commentsDirty {
		t.Fatal("a later update reusing the same comment ID did not invalidate its comments")
	}
}
