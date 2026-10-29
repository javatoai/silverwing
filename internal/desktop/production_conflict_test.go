package desktop

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/production"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func productionConflictBatch(b production.Batch, stage string) production.Batch {
	b.Phase, b.Message = "CONFLICT", "合并发生冲突，请在 IDE 中处理并提交"
	b.Intent = &production.Intent{Kind: "feature", Step: "feature", SourceBranch: "feature/confirmed-branch", SourceSHA: strings.Repeat("d", 40),
		Merge: &production.Merge{ID: strings.Repeat("e", 32), TargetBranch: b.ReleaseBranch, SourceSHA: strings.Repeat("d", 40), BeforeSHA: b.ReleaseSHA,
			Branch: "sw-production/" + b.ID + "/" + strings.Repeat("e", 32), Worktree: "C:/synthetic/app home/production-worktrees/" + b.ID + "/" + strings.Repeat("e", 32),
			Conflicts: []string{"src/main/支付服务.java", "src/test/支付服务Test.java"}}}
	b.Intent.Preview.ProductionStatus = domain.GenbuTagProbeStatus{Build: domain.GenbuUnknown, UAT: domain.GenbuUnknown, Production: domain.GenbuUnknown}
	if stage == "main" {
		b.Intent.Kind, b.Intent.Step, b.Intent.SourceBranch = "main", "main", ""
		b.Intent.Preview.ProductionTag = "2.3.43"
		b.Intent.Merge.TargetBranch = b.Target.MainBranch
		b.InitialSHA, b.ReleaseSHA, b.ReleaseBranch, b.Version, b.Branches = "", "", "", "", nil
	} else if stage == "release-sync" {
		b.Intent.Kind, b.Intent.Step, b.Intent.SourceBranch = "release", "release-sync", ""
	}
	return b
}

func TestProductionConflictPromptCopyAndRecoveryLayout(t *testing.T) {
	for _, stage := range []string{"feature", "main", "release-sync"} {
		for _, width := range []int{960, 1440} {
			for _, dark := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s-%d-dark-%t", stage, width, dark), func(t *testing.T) {
					s, v, b := productionFixture(t)
					b = productionConflictBatch(b, stage)
					s.releaseUnit.state.production.batches[0] = b
					v.ActiveStep = productionIntentStep(&b)
					v.Branch, v.Error, v.Message = "feature/not-the-conflict", b.Message, b.Message
					s.win = &mygo.Window{}
					tester := ui.NewTester(func(c *ui.Context) {
						p := setTheme(c, dark)
						ui.Row(c).Fill().AlignItems(ui.Stretch).Background(p.bg).Children(func() { s.sidebar(c, p, true); s.renderProductionPage(c, p) })
						s.renderConfirmationDialog(c, p)
					}, width, 1300)
					prompt := productionConflictSummary(b)
					count := 0
					for _, text := range tester.Texts() {
						if text == "合并冲突" {
							count++
						}
					}
					if count != 1 || tester.HasText(b.Message) {
						t.Fatalf("conflict repeated or missing: %v", tester.Texts())
					}
					source, target := productionConflictRefs(b)
					if !tester.HasText(source) || !tester.HasText(target) || tester.HasText("待合并的单个远端分支") ||
						tester.HasText(b.Intent.Merge.Worktree) || tester.HasText(b.Intent.Merge.Conflicts[0]) || tester.HasText(b.Intent.Merge.SourceSHA) {
						t.Fatal("conflict summary lost its target or exposed inactive controls/details")
					}
					for _, name := range []string{"复制冲突信息", "打开 IDE", "校验并继续", "核对远端变化", "撤回合并"} {
						r, ok := tester.Find(name)
						if !ok || r.W <= 0 || r.X < 0 || r.X+r.W > float32(width) {
							t.Fatalf("recovery control missing or overflowed: %s %+v", name, r)
						}
					}
					captureProductionSummary(t, tester, fmt.Sprintf("conflict-%s-%d-dark-%t", stage, width, dark))
					if err := tester.Click("复制冲突信息"); err != nil {
						t.Fatal(err)
					}
					copy := tester.Clipboard()
					for _, value := range []string{prompt, b.Target.ServiceName, b.Intent.Merge.Worktree, b.Intent.Merge.Conflicts[0], b.Intent.Merge.Conflicts[1], "不切换分支或推送"} {
						if !strings.Contains(copy, value) {
							t.Fatalf("copy lost %q", value)
						}
					}
					if strings.Contains(copy, v.Branch) || strings.Contains(prompt, b.Intent.Merge.SourceSHA) || s.lastMessage != "冲突信息已复制" {
						t.Fatal("copy used editable input or missed shared feedback")
					}
					if err := tester.Click("撤回合并"); err != nil {
						t.Fatal(err)
					}
					d := s.confirmationDialog
					if d == nil || d.DefaultIndex != d.CancelIndex || !d.FoldDetail || d.DetailOpen || !d.Current() {
						t.Fatal("abort confirmation is not scoped or cancellation-safe")
					}
					captureProductionSummary(t, tester, fmt.Sprintf("abort-confirm-%s-%d-dark-%t", stage, width, dark))
					tester.Key(0, ui.KeyEscape)
					if s.confirmationDialog != nil || v.Busy || s.productionBatch(v).Intent == nil {
						t.Fatal("Esc changed the merge")
					}
					v.Busy = true
					tester.Frame()
					if err := tester.Click("撤回合并"); err != nil {
						t.Fatal(err)
					}
					if s.confirmationDialog != nil {
						t.Fatal("busy abort opened a confirmation")
					}
					if err := tester.Click("复制冲突信息"); err != nil {
						t.Fatal(err)
					}
					if tester.Clipboard() != copy {
						t.Fatal("busy state blocked copying")
					}
				})
			}
		}
	}
}

func TestProductionAbortConfirmationRejectsChangedOperation(t *testing.T) {
	for _, change := range []string{"merge", "source", "before", "workspace", "target", "config", "page", "batch", "home", "busy", "ended"} {
		t.Run(change, func(t *testing.T) {
			s, v, b := productionFixture(t)
			b = productionConflictBatch(b, "feature")
			s.releaseUnit.state.production.batches[0] = b
			s.win = &mygo.Window{}
			s.confirmProductionAbort(v, b)
			d := s.confirmationDialog
			if d == nil || !d.Current() {
				t.Fatal("missing confirmation")
			}
			switch change {
			case "merge":
				b.Intent.Merge.ID = "another-merge"
			case "source":
				b.Intent.Merge.SourceSHA = strings.Repeat("f", 40)
			case "before":
				b.Intent.Merge.BeforeSHA = strings.Repeat("f", 40)
			case "workspace":
				b.Intent.Merge.Worktree = "another-workspace"
			case "target":
				s.releaseUnit.state.production.batches[0].Target.Remote = "other"
			case "config":
				s.settingsUnit.state.configJSON = `{"tagEnabled":false}`
			case "page":
				s.page = "tasks"
			case "batch":
				v.BatchID = "another-batch"
			case "home":
				s.snapshot.Home = "another-home"
			case "busy":
				v.Busy = true
			case "ended":
				s.releaseUnit.state.production.batches[0].Ended = true
			}
			if d.Current() {
				t.Fatal("stale operation can still be confirmed")
			}
		})
	}
}

func TestProductionAbortUsesConfirmedPayloadAndStopsAfterSuccess(t *testing.T) {
	s, v, b, backend, window := productionRequestFixture(t)
	b = productionConflictBatch(b, "feature")
	s.releaseUnit.state.production.batches[0] = b
	s.win = &mygo.Window{}
	s.taskUnit.state.notes[0] = "unsaved draft"
	v.Steps = map[int]productionStepView{4: {State: "failed", Message: b.Message}, 5: {State: "complete"}, 6: {State: "prepared"}}
	s.confirmProductionAbort(v, b)
	tester := productionIterationTester(s, 960, false)
	tester.Key(0, ui.KeyEnter)
	if s.confirmationDialog != nil || len(backend.calls) != 0 {
		t.Fatal("default Enter confirmed destructive operation")
	}
	s.confirmProductionAbort(v, b)
	tester.Frame()
	s.win = nil
	if err := tester.Click("放弃修改并撤回"); err != nil {
		t.Fatal(err)
	}
	action := productionTestCall(t, backend)
	m, ok := action.Payload["merge"].(production.Merge)
	if !ok || action.Kind != "production.abort-merge" || action.Payload["target"] != b.Target || m.ID != b.Intent.Merge.ID ||
		m.SourceSHA != b.Intent.Merge.SourceSHA || m.BeforeSHA != b.Intent.Merge.BeforeSHA || m.Worktree != b.Intent.Merge.Worktree {
		t.Fatalf("abort lost immutable identity: %+v", action)
	}
	branch := v.Branch
	b.Intent, b.Phase, b.Message = nil, "READY", "本次合并已撤回"
	backend.replies <- productionTestAnswer{result: application.ActionResult{Message: b.Message, Data: production.Report{Batch: &b}}}
	productionTestUpdate(t, window)
	if v.Busy || s.productionBatch(v).Intent != nil || len(v.Steps) != 0 || !v.PreparationInvalid || v.BuildChecked || v.Branch != branch || len(backend.calls) != 0 {
		t.Fatalf("abort left stale feedback or continued another operation: %+v", v)
	}
	if s.taskUnit.state.notes[0] != "unsaved draft" || s.lastMessage != b.Message {
		t.Fatal("abort lost draft or completion toast")
	}
	tester.Frame()
	if !tester.HasText("待合并的单个远端分支") || tester.HasText("合并冲突") || v.Branch != branch {
		t.Fatalf("completed abort did not restore the preserved branch picker: branch=%q texts=%v", v.Branch, tester.Texts())
	}
}

func TestProductionAbortRecoveryDoesNotOfferResume(t *testing.T) {
	s, v, b := productionFixture(t)
	b = productionConflictBatch(b, "feature")
	b.Phase, b.Message = "ABORTING", "合并已撤回，保存结果失败；请重试撤回以完成核对"
	s.releaseUnit.state.production.batches[0] = b
	s.win = &mygo.Window{}
	tester := productionIterationTester(s, 960, false)
	if !tester.HasText(b.Message) || !tester.HasText("撤回合并") {
		t.Fatal("missing abort recovery")
	}
	for _, label := range []string{"校验并继续", "核对远端变化"} {
		if err := tester.Click(label); err != nil {
			t.Fatal(err)
		}
		if s.confirmationDialog != nil || v.Busy {
			t.Fatal("aborting record resumed the old operation")
		}
	}
	if err := tester.Click("撤回合并"); err != nil {
		t.Fatal(err)
	}
	if s.confirmationDialog == nil {
		t.Fatal("abort cannot be retried after restart")
	}
}

func TestProductionAbortLateResultCannotClearANewerConflict(t *testing.T) {
	for _, change := range []string{"generation", "batch", "selection", "home", "config", "new-merge", "late-error"} {
		t.Run(change, func(t *testing.T) {
			s, v, b, backend, window := productionRequestFixture(t)
			b = productionConflictBatch(b, "feature")
			s.releaseUnit.state.production.batches[0] = b
			merge := *b.Intent.Merge
			s.productionRequestFor(v, application.Action{Kind: "production.abort-merge", Payload: map[string]any{"batchId": b.ID, "target": b.Target, "merge": merge, "confirmed": true}}, "")
			productionTestCall(t, backend)
			switch change {
			case "generation":
				v.Generation++
			case "batch":
				v.BatchID = "different-batch"
			case "selection":
				v.Selection.ServiceID = "different-service"
			case "home":
				s.snapshot.Home = "different-home"
			case "config":
				s.settingsUnit.state.configJSON = `{"tagEnabled":false}`
			case "new-merge", "late-error":
				freshIntent, freshMerge := *b.Intent, merge
				freshMerge.ID = "newer-merge"
				freshIntent.Merge = &freshMerge
				s.releaseUnit.state.production.batches[0].Intent = &freshIntent
			}
			completed := b
			completed.Intent, completed.Phase, completed.Message = nil, "READY", "本次合并已撤回"
			if change == "late-error" {
				backend.replies <- productionTestAnswer{err: errors.New("obsolete abort failure")}
			} else {
				backend.replies <- productionTestAnswer{result: application.ActionResult{Message: completed.Message, Data: production.Report{Batch: &completed}}}
			}
			productionTestUpdate(t, window)
			if s.releaseUnit.state.production.batches[0].Intent == nil || s.lastMessage == completed.Message || v.Error == "obsolete abort failure" || len(backend.calls) != 0 {
				t.Fatal("late abort rewrote current state or continued work")
			}
		})
	}
}
