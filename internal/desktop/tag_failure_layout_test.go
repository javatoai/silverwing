package desktop

import (
	"context"
	"fmt"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
)

func tagFailureFixture(t *testing.T) *app {
	t.Helper()
	s := tagLayoutFixture(t)
	s.snapshot.ConfigJSON = s.settingsUnit.state.configJSON
	s.backend = &tagLayoutCaptureBackend{actions: make(chan application.Action, 4)}
	s.snapshot.TagHistory = []application.TagRecord{
		{ID: "conflict", TaskID: "task-a", BatchID: "current-batch", ServiceID: "service-a", ModuleID: "module-a", ServiceName: "api-service", State: "CONFLICT", RetryKind: "RETRY_BUILD", Tag: "1.0.beta-1", SourceBranch: "feature/example", TargetBranch: "release/test", CreatedAt: "2026-10-15 12:00:00", Detail: "合并失败：" + strings.Repeat("完整错误包含中文与长分支名称，需要按需阅读。", 10), ConflictSummary: "我在服务 api-service 的 feature/example 分支合并到 origin/release/test 时遇到了冲突，请你解决。", ConflictFiles: []string{"src/示例/Example.kt", strings.Repeat("long-directory/", 12) + "Tail.kt"}, Guidance: "我在服务 api-service 的 feature/example 分支合并到 origin/release/test 时遇到了冲突，请你解决。\n解决冲突后提交并推送 `origin/release/test`，再在桌面端打开任务“支付渠道优化”的 Tag 历史，选择记录4557b63c-440...并点击“重试”。"},
		{ID: "dirty", TaskID: "task-a", BatchID: "current-batch", ServiceID: "service-b", ModuleID: "module-b", ServiceName: "example-payment-service", State: "FAILED", RetryKind: "RESUME_PARTIAL", Tag: "2.0.beta-2", SourceBranch: "feature/example", TargetBranch: "release/test", CreatedAt: "2026-10-15 12:00:00", Detail: "特性工作区存在未提交改动\n" + strings.Repeat(" M src/示例.go\n", 12)},
	}
	for _, record := range s.snapshot.TagHistory {
		path := filepath.Join(t.TempDir(), record.ID)
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		s.taskUnit.state.tasks[0].workspaces = append(s.taskUnit.state.tasks[0].workspaces, application.Workspace{ServiceID: record.ServiceID, ModuleID: record.ModuleID, Path: path})
	}
	return s
}

func captureTagFailure(t *testing.T, tester *ui.Tester, name string) {
	t.Helper()
	dir := os.Getenv("SILVERWING_TAG_FAILURE_EVIDENCE_DIR")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, tester.Image()); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestTagFailureCompactLayout(t *testing.T) {
	for _, width := range []int{1100, 360} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%t", width, dark), func(t *testing.T) {
				s := tagFailureFixture(t)
				tester := tagLayoutTester(s, width, dark)
				for _, record := range s.snapshot.TagHistory {
					if !tester.HasText(tagFailureSummary(record)) || tester.HasText(record.Detail) || tester.HasText(record.Guidance) && record.Guidance != "" {
						t.Fatal("recoverable failure did not separate concise cause from full details")
					}
					row, ok := tester.Find("Tag 恢复动作 · " + record.ID)
					if !ok || row.X < 0 || row.X+row.W > float32(width-28) || width == 1100 && math.Abs(float64(row.H-28)) > 1 {
						t.Fatalf("compact actions overflowed/were not 28 DIP: %+v", row)
					}
					for _, name := range []string{"检查工作区 · ", "重试 Tag · ", "Tag 失败详情 · "} {
						button, found := tester.Find(name + record.ID)
						if !found || math.Abs(float64(button.H-28)) > 1 || button.X < row.X || button.X+button.W > row.X+row.W+1 {
							t.Fatalf("action is not content-sized/in bounds: %s %+v row=%+v", name, button, row)
						}
					}
					for _, status := range []string{"合并分支 · ", "Tag 恢复操作 · "} {
						if _, ok := tester.Find(status + record.ID); !ok {
							t.Fatalf("record content lost: %s", status)
						}
					}
				}
				if !tester.HasText("继续构建Tag") || !tester.HasText("重试冲突操作") || tester.HasText("构建 未知") || tester.HasText("UAT 未知") || tester.HasText("复制构建记录") {
					t.Fatal("retry distinctions changed or unpublished Tags showed remote badges")
				}
				captureTagFailure(t, tester, fmt.Sprintf("failure-%d-dark-%t", width, dark))
			})
		}
	}
}

func TestTagFailureDisclosureAndChecks(t *testing.T) {
	s := tagFailureFixture(t)
	record := s.snapshot.TagHistory[0]
	s.saveTagWorkspaceCheckState(tagWorkspaceCheckState{OperationID: record.ID, TaskID: record.TaskID, Check: &application.TagWorkspaceCheck{Changes: []string{" M src/完整检查结果.go"}}})
	tester := tagLayoutTester(s, 1100, false)
	if !tester.HasText("工作区仍有未提交改动，请处理后重试。") || tester.HasText(" M src/完整检查结果.go") {
		t.Fatal("check summary/details were not kept with the record")
	}
	if err := tester.Click("Tag 失败详情 · conflict"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(220 * time.Millisecond)
	tester.Frame()
	if tester.HasText(record.Detail) || tester.HasText(record.Guidance) || tester.HasText("解决冲突后提交并推送") {
		t.Fatal("expanded details repeated the raw error or long recovery guidance")
	}
	prompt := record.ConflictSummary
	for _, text := range []string{prompt, record.ConflictFiles[1], " M src/完整检查结果.go"} {
		if !tester.HasText(text) {
			t.Fatalf("expanded native disclosure lost complete evidence: %q", text)
		}
	}
	captureTagFailure(t, tester, "failure-expanded-wide")
	// The native disclosure retains focus and responds to the keyboard.
	tester.Key(0, ui.KeySpace)
	time.Sleep(220 * time.Millisecond)
	tester.Frame()
	if tester.HasText(prompt) {
		t.Fatal("Space did not collapse focused disclosure")
	}
	if err := tester.Click("Tag 失败详情 · conflict"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(220 * time.Millisecond)
	tester.Frame()
	if err := tester.Click("复制处理提示 · conflict"); err != nil {
		t.Fatal(err)
	}
	if tester.Clipboard() != prompt || s.lastMessage != "已复制" || !tester.HasText(prompt) {
		t.Fatal("prompt copy differed from the visible text or collapsed its disclosure")
	}
	s.busy = true // Unrelated global operations must not prevent copying evidence.
	if err := tester.Click("复制冲突信息 · conflict"); err != nil {
		t.Fatal(err)
	}
	if tester.Clipboard() != tagConflictCopyText(record) {
		t.Fatal("conflict copy changed original Unicode/CRLF/file order")
	}
	for _, check := range []tagWorkspaceCheckState{
		{OperationID: "conflict", Loading: true},
		{OperationID: "conflict", Check: &application.TagWorkspaceCheck{Clean: true}},
		{OperationID: "conflict", Error: "fixture: permission denied"},
	} {
		s.saveTagWorkspaceCheckState(check)
		tester.Frame()
		want := "正在检查工作区…"
		if check.Check != nil {
			want = "工作区干净，可以重试。"
		} else if check.Error != "" {
			want = "工作区检查失败，请展开详情查看。"
		}
		if !tester.HasText(want) {
			t.Fatalf("record-scoped check state missing: %s", want)
		}
	}
}

func TestTagBatchProgressAndTruePhase(t *testing.T) {
	s := tagFailureFixture(t)
	scope := s.tagJobScope()
	view := s.tagJobView(scope)
	view.snapshot = application.TagJobSnapshot{Scope: scope, ID: "job", BatchID: "current-batch", Busy: true}
	for i, record := range s.snapshot.TagHistory {
		item := application.TagJobItem{ID: record.ID, OperationID: record.ID, ServiceID: record.ServiceID, ModuleID: record.ModuleID, Kind: "tag.build", Record: &record}
		if i == 0 {
			item.Phase, item.Busy = application.TagJobPhaseMerging, true
		} else {
			item.Phase, item.Done = application.TagJobPhaseFailed, true
		}
		view.snapshot.Items = append(view.snapshot.Items, item)
	}
	view.focusBatch = "current-batch"
	tester := tagLayoutTester(s, 1100, true)
	if !tester.HasText("本次操作 2 项 · 执行中 1 · 完成 0 · 失败 1") || !tester.HasText("合并到目标分支") || !tester.HasText("执行失败") || view.focusBatch != "" {
		t.Fatalf("batch summary, real phase, or one-time batch focus missing: progress=%+v focus=%q texts=%q", s.tagBatchProgress("current-batch"), view.focusBatch, tester.Texts())
	}
	activeRecord := s.snapshot.TagHistory[0]
	_, retryExplanation := tagHistoryRetryPresentation(activeRecord)
	if tester.HasText("上次操作："+tagFailureSummary(activeRecord)) || tester.HasText(tagFailureSummary(activeRecord)) ||
		retryExplanation != "" && tester.HasText(retryExplanation) {
		t.Fatal("an active record displayed its previous failure or recovery advice")
	}
	if tester.HasText("构建 未知") || tester.HasText("UAT 未知") || tester.HasText("构建 成功") {
		t.Fatal("unconfirmed local work displayed remote Genbu badges")
	}
	view.snapshot.Items[0].Phase = application.TagJobPhasePushingTag
	tester.Frame()
	if !tester.HasText("推送 Tag") || s.consumeTagBatchFocus("current-batch") {
		t.Fatal("phase did not update, or ordinary progress stole scroll focus again")
	}
	captureTagFailure(t, tester, "batch-progress-dark")
}

func TestTagBatchPlaceholderRecordsImmediatelyVisible(t *testing.T) {
	s := tagFailureFixture(t)
	s.snapshot.TagHistory = nil
	scope := s.tagJobScope()
	view := s.tagJobView(scope)
	view.snapshot = application.TagJobSnapshot{Scope: scope, ID: "new-job", BatchID: "selected-batch", Busy: true}
	for i := range 4 {
		record := application.TagRecord{ID: fmt.Sprintf("selected-%d", i), TaskID: "task-a", BatchID: "selected-batch", ServiceID: fmt.Sprintf("service-%d", i), ModuleID: "module", ServiceName: fmt.Sprintf("selected-example-service-%d", i), State: "CREATED", SourceBranch: "feature/example", TargetBranch: "release/test"}
		view.snapshot.Items = append(view.snapshot.Items, application.TagJobItem{Kind: "tag.build", OperationID: record.ID, ServiceID: record.ServiceID, ModuleID: record.ModuleID, Phase: application.TagJobPhaseQueued, Busy: true, Queued: true, Record: &record})
	}
	tester := tagLayoutTester(s, 1100, false)
	if !tester.HasText("本次操作 4 项 · 执行中 4 · 完成 0 · 失败 0") || len(s.snapshot.TagHistory) != 4 {
		t.Fatal("initial job snapshot did not reveal the complete selected batch")
	}
	for i := range 4 {
		if _, ok := tester.Find(fmt.Sprintf("selected-example-service-%d", i)); !ok {
			t.Fatalf("selected item %d was withheld until its operation completed", i)
		}
	}
	view.snapshot.Items[0].Phase = application.TagJobPhaseCompleted
	view.snapshot.Items[0].Busy, view.snapshot.Items[0].Queued, view.snapshot.Items[0].Done = false, false, true
	tester.Frame()
	if !tester.HasText("本次操作 4 项 · 执行中 3 · 完成 1 · 失败 0") {
		t.Fatal("batch completion count did not advance with its actual item")
	}
}

func TestTagFailureNarrowDetailsEnd(t *testing.T) {
	s := tagFailureFixture(t)
	s.snapshot.TagHistory = s.snapshot.TagHistory[:1]
	s.saveTagWorkspaceCheckState(tagWorkspaceCheckState{OperationID: "conflict", Check: &application.TagWorkspaceCheck{Changes: []string{" M src/末尾检查证据.go"}}})
	tester := tagLayoutTester(s, 360, true)
	if err := tester.Click("Tag 失败详情 · conflict"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(220 * time.Millisecond)
	tester.Frame()
	tester.Scroll(180, 400, 0, 100000)
	tester.Frame()
	end, ok := tester.Find(" M src/末尾检查证据.go")
	if !ok || end.W <= 0 || end.X < 0 || end.X+end.W > 332 || end.H <= 0 || end.Y+end.H > 780 {
		t.Fatalf("narrow expanded evidence cannot scroll to its complete end: %+v", end)
	}
	captureTagFailure(t, tester, "failure-expanded-narrow-end")
}

type tagFailureJobBackend struct{ actions chan application.Action }

func (b *tagFailureJobBackend) Snapshot(context.Context) (application.Snapshot, error) {
	return application.Snapshot{}, nil
}
func (b *tagFailureJobBackend) Dispatch(context.Context, application.Action) (application.ActionResult, error) {
	panic("Tag controls must use the independent job channel")
}
func (b *tagFailureJobBackend) StartTagJob(ctx context.Context, _ application.TagJobScope, action application.Action) (application.TagJobSnapshot, error) {
	b.actions <- action
	<-ctx.Done()
	return application.TagJobSnapshot{}, ctx.Err()
}
func (b *tagFailureJobBackend) AppendTagJob(ctx context.Context, scope application.TagJobScope, _ string, action application.Action) (application.TagJobSnapshot, error) {
	return b.StartTagJob(ctx, scope, action)
}
func (b *tagFailureJobBackend) TagJobSnapshot(context.Context, application.TagJobScope) (application.TagJobSnapshot, error) {
	return application.TagJobSnapshot{}, nil
}
func (b *tagFailureJobBackend) WatchTagJob(context.Context, application.TagJobScope, string, uint64, func(application.TagJobSnapshot)) error {
	return nil
}
func (b *tagFailureJobBackend) CancelTagJob(context.Context, application.TagJobScope, string) error {
	return nil
}

type tagFailureWindow struct{}

func (tagFailureWindow) IsDestroyed() bool { return false }
func (tagFailureWindow) Update(fn func())  { fn() }

func TestTagFailureActionsUseScopedIndependentQueue(t *testing.T) {
	for _, kind := range []string{"tag.retry", "tag.workspace-check"} {
		t.Run(kind, func(t *testing.T) {
			s := tagFailureFixture(t)
			backend := &tagFailureJobBackend{actions: make(chan application.Action, 4)}
			s.backend, s.busy = backend, true
			s.ensureTagDesktopJobs().window = tagFailureWindow{}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := s.closeTagDesktopJobs(ctx); err != nil {
					t.Error(err)
				}
			})
			// A job in one repository must leave the other record actionable.
			record := s.snapshot.TagHistory[0]
			scope := s.tagJobScope()
			view := s.tagJobView(scope)
			view.snapshot = application.TagJobSnapshot{Scope: scope, ID: "running-job", Busy: true, Items: []application.TagJobItem{{OperationID: record.ID, ServiceID: record.ServiceID, ModuleID: record.ModuleID, WorkspacePath: s.taskUnit.state.tasks[0].workspaces[0].Path, Busy: true}}}
			tester := tagLayoutTester(s, 1100, false)
			button := "重试 Tag · "
			if kind == "tag.workspace-check" {
				button = "检查工作区 · "
			}
			if _, visible := tester.Find(button + "conflict"); visible {
				t.Fatal("active record exposed a recovery action")
			}
			select {
			case action := <-backend.actions:
				t.Fatalf("busy repository dispatched an action: %+v", action)
			default:
			}
			otherPath := s.taskUnit.state.tasks[0].workspaces[1].Path
			s.taskUnit.state.tasks[0].workspaces[1].Path = s.taskUnit.state.tasks[0].workspaces[0].Path
			tester.Frame()
			if err := tester.Click(button + "dirty"); err != nil {
				t.Fatal(err)
			}
			if len(view.commands) != 0 {
				t.Fatal("another record sharing the busy workspace bypassed its gate")
			}
			s.taskUnit.state.tasks[0].workspaces[1].Path = otherPath
			tester.Frame()
			if err := tester.Click(button + "dirty"); err != nil {
				t.Fatal(err)
			}
			var action application.Action
			select {
			case action = <-backend.actions:
			case <-time.After(time.Second):
				t.Fatal("unrelated global busy/other repository blocked the record action")
			}
			if action.Kind != kind || action.TaskID != "task-a" || action.ServiceID != "service-b" || action.ModuleID != "module-b" || action.Path != s.taskUnit.state.tasks[0].workspaces[1].Path || payloadString(action.Payload, "operationId") != "dirty" || payloadString(action.Payload, "tagHistoryTaskPath") != s.taskUnit.state.tasks[0].path {
				t.Fatalf("record action lost its complete target identity: %+v", action)
			}
			if kind == "tag.workspace-check" {
				check := s.tagWorkspaceCheckState("dirty")
				if !check.Loading || check.RequestKey == "" || payloadString(action.Payload, "requestKey") != check.RequestKey || check.Path != action.Path {
					t.Fatal("check request lost its operation/path/generation identity")
				}
			}
			tester.Frame()
			if _, visible := tester.Find(button + "dirty"); visible {
				t.Fatal("queued record exposed a duplicate recovery action")
			}
			tester.Frame()
			if len(view.commands) != 1 || len(backend.actions) != 0 {
				t.Fatal("one active record action was queued more than once")
			}
		})
	}
}
