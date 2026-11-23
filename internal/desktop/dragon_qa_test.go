package desktop

import (
	"context"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"

	"github.com/egoist/mygo/ui"
)

const dragonQATestRequirement = "https://project.feishu.cn/obt/userstory/detail/7160000000"
const dragonQATestApproval = "https://dragon.snowballtech.com/#/workorder/detail/1234"

type dragonQATestBackend struct {
	application.Backend
	status func(context.Context, application.DragonQATask) (application.DragonQAState, error)
	submit func(context.Context, application.DragonQASubmitRequest) (application.DragonQAState, error)
}

func (b *dragonQATestBackend) DragonQAStatus(ctx context.Context, task application.DragonQATask) (application.DragonQAState, error) {
	return b.status(ctx, task)
}

func (b *dragonQATestBackend) SubmitDragonQA(ctx context.Context, request application.DragonQASubmitRequest) (application.DragonQAState, error) {
	return b.submit(ctx, request)
}

func (*dragonQATestBackend) RecheckDragonQA(context.Context, application.DragonQATask, string) (application.DragonQAState, error) {
	panic("unexpected approval check")
}

func (*dragonQATestBackend) RetryDragonQAComment(context.Context, application.DragonQATask) (application.DragonQAState, error) {
	panic("unexpected comment retry")
}

func dragonQATestApp(t *testing.T) *app {
	t.Helper()
	home := t.TempDir()
	s := &app{backend: &dragonQATestBackend{}, page: "tasks", snapshot: application.Snapshot{Home: home}, inputs: make(map[string]*string)}
	s.taskUnit.state.tasks = []task{
		{id: "a", path: filepath.Join(home, "a"), createdAt: "created-a", title: "任务甲", groupID: "group"},
		{id: "b", path: filepath.Join(home, "b"), createdAt: "created-b", title: "任务乙"},
	}
	s.settingsUnit.state.configJSON = `{"tagEnabled":true}`
	seedPlatformConnectionsForDesktopTest(s, false, true)
	return s
}

func TestDragonQATaskBranchToolbar(t *testing.T) {
	s := dragonQATestApp(t)
	s.snapshot.Groups = []application.Group{{ID: "group"}}
	s.taskUnit.state.tasks[0].workspaces = []application.Workspace{{
		ServiceID: "svc", ModuleID: "module", Name: "Service", State: "READY",
		Record: domain.ServiceWorkspace{TagEnabled: true},
	}}
	tester := ui.NewTester(func(c *ui.Context) { s.taskWorkspaceDetail(c, setTheme(c, false)) }, 800, 560)
	entry, entryFound := tester.Find("青龙提测")
	batch, batchFound := tester.Find("批量构建 Tag")
	if !entryFound || !batchFound || entry.W != batch.W || entry.H != batch.H || entry.X < batch.X+batch.W || entry.X-batch.X-batch.W > 10 {
		t.Fatalf("toolbar entry not adjacent to batch Tag: Dragon %+v/%t, Tag %+v/%t", entry, entryFound, batch, batchFound)
	}
	if dragonQAIconBitmap() == nil {
		t.Fatal("the Dragon icon did not decode")
	}
	if err := tester.Click("青龙提测"); err != nil {
		t.Fatal(err)
	}
	if state := s.taskUnit.state.dragonQA; state == nil || !state.open {
		t.Fatal("clicking the Dragon icon did not open the QA dialog")
	}
	s.tab = taskTagTab
	tester.Frame()
	if tester.HasText("青龙提测") {
		t.Fatal("Tag tab exposed the Dragon QA action")
	}
	s.tab, s.page = 0, "archive"
	s.taskUnit.state.tasks[0].archived = true
	tester.Frame()
	if tester.HasText("青龙提测") {
		t.Fatal("archived task exposed the Dragon QA action")
	}
}

func TestDragonQAFormAndResultActions(t *testing.T) {
	s := dragonQATestApp(t)
	s.openDragonQA(s.taskUnit.currentTask())
	state := s.taskUnit.state.dragonQA
	state.loaded = true
	state.result = application.DragonQAState{TaskID: "a", RequirementURL: dragonQATestRequirement}
	first := application.DragonQAService{ID: "branch-suite", Name: "miXeD-名称.API"}
	second := application.DragonQAService{ID: "other", Name: "other"}
	state.setServices([]application.DragonQAService{first, first, second})
	tester := ui.NewTester(func(c *ui.Context) { s.renderDragonQADialog(c, setTheme(c, false)) }, 610, 550)
	if !state.syncComment || !state.canSubmit() || !reflect.DeepEqual(state.selectedServices(), []application.DragonQAService{first, second}) {
		t.Fatalf("incorrect form defaults: %+v", state)
	}
	if err := tester.Click("提测服务 miXeD-名称.API"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("同步审批链接到 Meego 需求评论"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(state.selectedServices(), []application.DragonQAService{second}) || state.syncComment {
		t.Fatalf("checkbox changes were not synchronized: %+v", state)
	}
	tester.SetSize(410, 430)
	if _, ok := tester.Find("提交提测"); !ok {
		t.Fatal("short window clipped the submit footer")
	}
	state.result = application.DragonQAState{
		TaskID: "a", Phase: application.DragonQAUnknown, Candidates: []application.DragonQACandidate{{
			ApprovalID: "900", ApprovalURL: "https://dragon.snowballtech.com/#/workorder/detail/900",
			Apps: []string{"other"}, CreatedAt: "2026-11-20T16:21:42+08:00",
		}},
	}
	tester.Frame()
	if !tester.HasText("核查审批") || !tester.HasText("确认为本次审批") || tester.HasText("提交提测") {
		t.Fatal("ambiguous approval should offer reconciliation only")
	}
	state.result = application.DragonQAState{
		TaskID: "a", Phase: application.DragonQAApproved, ApprovalID: "1234", ApprovalURL: dragonQATestApproval,
		CommentStatus: application.DragonQACommentUnknown,
	}
	tester.Frame()
	if !tester.HasText("打开审批") || !tester.HasText("核对评论") || tester.HasText("提交提测") || tester.HasText("核查审批") {
		t.Fatal("saved approval should offer its link and comment reconciliation only")
	}
	if err := tester.Click("打开审批"); err != nil {
		t.Fatal(err)
	}
	if urls := tester.OpenedURLs(); !reflect.DeepEqual(urls, []string{dragonQATestApproval}) {
		t.Fatalf("opened link differs from the stored receipt: %q", urls)
	}
	if err := tester.Click("复制链接"); err != nil {
		t.Fatal(err)
	}
	if copied := tester.Clipboard(); copied != dragonQATestApproval {
		t.Fatalf("copied link differs from the stored receipt: %q", copied)
	}
	state.result.CommentStatus = application.DragonQACommentFailed
	tester.Frame()
	if !tester.HasText("补发评论") || tester.HasText("提交提测") {
		t.Fatal("failed comment should only offer a separate comment retry")
	}
	state.result.CommentStatus = application.DragonQACommentSynced
	tester.Frame()
	if tester.HasText("补发评论") || tester.HasText("核对评论") {
		t.Fatal("synced comment still offers a retry")
	}
}

func TestDragonQABackgroundReceiptStaysWithOriginatingTask(t *testing.T) {
	s := dragonQATestApp(t)
	window := &refreshTestWindow{updates: make(chan func(), 4)}
	service := application.DragonQAService{ID: "svc", Name: "MiXeD 原服务"}
	form := func(id string) application.DragonQAState {
		return application.DragonQAState{TaskID: id, RequirementURL: dragonQATestRequirement, Services: []application.DragonQAService{service}}
	}
	receipt := form("a")
	receipt.Phase, receipt.ApprovalID, receipt.ApprovalURL = application.DragonQAApproved, "1234", dragonQATestApproval
	receipt.CommentStatus = application.DragonQACommentPending
	submitted := make(chan application.DragonQASubmitRequest, 2)
	finishComment := make(chan struct{})
	var releaseComment sync.Once
	release := func() { releaseComment.Do(func() { close(finishComment) }) }
	var receiptStored, commentSynced atomic.Bool
	t.Cleanup(release)
	s.backend = &dragonQATestBackend{
		status: func(_ context.Context, task application.DragonQATask) (application.DragonQAState, error) {
			if task.TaskID == "a" && receiptStored.Load() {
				result := receipt
				if commentSynced.Load() {
					result.CommentStatus = application.DragonQACommentSynced
				}
				return result, nil
			}
			return form(task.TaskID), nil
		},
		submit: func(_ context.Context, request application.DragonQASubmitRequest) (application.DragonQAState, error) {
			submitted <- request
			<-finishComment
			result := receipt
			result.CommentStatus = application.DragonQACommentSynced
			commentSynced.Store(true)
			return result, nil
		},
	}
	awaitUpdate := func() {
		t.Helper()
		select {
		case update := <-window.updates:
			update()
		case <-time.After(4 * time.Second):
			t.Fatal("Dragon QA did not complete its UI update")
		}
	}
	s.openDragonQA(s.taskUnit.currentTask())
	a := s.taskUnit.state.dragonQA
	s.startDragonQAJob(window, a, dragonQALoad, "")
	awaitUpdate()
	a.remark = "保留备注\n原内容"
	s.startDragonQAJob(window, a, dragonQASubmit, "")
	s.startDragonQAJob(window, a, dragonQASubmit, "")
	select {
	case request := <-submitted:
		if request.DragonQATask != a.scope.task || request.RequirementURL != dragonQATestRequirement ||
			request.Remark != a.remark || !request.SyncComment || !reflect.DeepEqual(request.Services, []application.DragonQAService{service}) {
			t.Fatalf("submission differs from visible form: %+v", request)
		}
	case <-time.After(time.Second):
		t.Fatal("selected payload did not reach backend")
	}
	receiptStored.Store(true)
	awaitUpdate()
	if a.result.ApprovalURL != dragonQATestApproval || a.result.CommentStatus != application.DragonQACommentPending ||
		s.taskUnit.state.dragonQAJobs[a.scope] == nil {
		t.Fatal("approval link was unavailable while Meego comment was pending")
	}
	s.taskUnit.state.selected = 1
	s.openDragonQA(s.taskUnit.currentTask())
	b := s.taskUnit.state.dragonQA
	s.startDragonQAJob(window, b, dragonQALoad, "")
	awaitUpdate()
	release()
	awaitUpdate()
	if s.taskUnit.state.dragonQA != b || b.result.TaskID != "b" || b.result.ApprovalURL != "" || !b.canSubmit() || len(submitted) != 0 {
		t.Fatalf("task A completion leaked into task B or resubmitted: %+v", b)
	}
	s.taskUnit.state.selected = 0
	s.openDragonQA(s.taskUnit.currentTask())
	a = s.taskUnit.state.dragonQA
	s.startDragonQAJob(window, a, dragonQALoad, "")
	awaitUpdate()
	if a.result.ApprovalURL != dragonQATestApproval || a.result.CommentStatus != application.DragonQACommentSynced || a.canSubmit() {
		t.Fatalf("reopened task A lost its saved result: %+v", a)
	}
}
