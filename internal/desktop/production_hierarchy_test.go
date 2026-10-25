package desktop

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/production"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func productionRequestFixture(t *testing.T) (*app, *productionView, production.Batch, *productionTestBackend, *refreshTestWindow) {
	t.Helper()
	s, v, b := productionFixture(t)
	backend := &productionTestBackend{calls: make(chan application.Action, 8), replies: make(chan productionTestAnswer, 8)}
	window := &refreshTestWindow{updates: make(chan func(), 16)}
	s.backend, s.production.window = backend, window
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := s.closeProductionJobs(ctx); err != nil {
			t.Error(err)
		}
	})
	return s, v, b, backend, window
}

func productionTestCall(t *testing.T, backend *productionTestBackend) application.Action {
	t.Helper()
	select {
	case call := <-backend.calls:
		return call
	case <-time.After(time.Second):
		t.Fatal("production request not dispatched")
		return application.Action{}
	}
}

func productionTestUpdate(t *testing.T, window *refreshTestWindow) {
	t.Helper()
	select {
	case update := <-window.updates:
		update()
	case <-time.After(time.Second):
		t.Fatal("production result not delivered")
	}
}

func TestProductionHierarchyHasDirectBuildAndPermanentInitialization(t *testing.T) {
	for _, width := range []int{960, 1440, 1920} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-%t", width, dark), func(t *testing.T) {
				s, _, b := productionFixture(t)
				tester := productionIterationTester(s, width, dark)
				for _, label := range []string{"合并分支", "构建生产 Tag", b.ReleaseBranch} {
					if !tester.HasText(label) {
						t.Errorf("missing clear zone/field: %s", label)
					}
				}
				if productionExactText(tester, "release 分支") || tester.HasText("选择要合并的远端分支") {
					t.Error("redundant release label or branch picker instructions are still visible")
				}
				for _, result := range []string{"生产 Tag → 主分支", "创建 release 分支", "重新检查"} {
					if !tester.HasText(result) {
						t.Errorf("initialization result must remain visible: %s", result)
					}
				}
				if tester.HasText("展开初始化结果") || tester.HasText("收起初始化结果") || tester.HasText("结束批次") || tester.HasText("处理中…") || tester.HasText("构建前检查") || tester.HasText("检查构建条件") {
					t.Error("obsolete disclosure or generic controls still clutter the page")
				}
				field, found := tester.Find("待合并的单个远端分支")
				if !found || field.W > 560 || field.W < 200 || field.H < 36 {
					t.Errorf("branch field bounds: %+v", field)
				}
				content, found := tester.Find("生产发布操作区")
				if !found || content.W > 1080 {
					t.Errorf("unbounded production content: %+v", content)
				}
				for _, control := range []string{"合并到 release", "构建 Tag"} {
					r, ok := tester.Find(control)
					if !ok || r.Y < 0 || r.X+r.W > float32(width) {
						t.Errorf("action displaced: %s %+v", control, r)
					}
				}
				tester.SetSize(width, 1600)
				captureProductionSummary(t, tester, fmt.Sprintf("hierarchy-%d-dark-%t-default", width, dark))
			})
		}
	}
}

func TestProductionContainedFeatureSkipsConfirmationAndCanCheckNewCommit(t *testing.T) {
	s, v, b, backend, window := productionRequestFixture(t)
	for attempt := 0; attempt < 2; attempt++ {
		s.win = nil
		s.productionRequestFor(v, application.Action{Kind: "production.feature-preview", Payload: map[string]any{"batchId": b.ID, "branch": v.Branch}}, "feature-preview")
		if action := productionTestCall(t, backend); action.Kind != "production.feature-preview" {
			t.Fatalf("unexpected write while previewing: %+v", action)
		}
		p := production.Preview{Target: b.Target, ReleaseSHA: b.ReleaseSHA, BranchHeads: map[string]string{v.Branch: strings.Repeat("d", 40)}}
		if attempt == 1 {
			p.BranchHeads[v.Branch], p.Commits = strings.Repeat("e", 40), "eeeeeeee new feature commit"
		}
		backend.replies <- productionTestAnswer{result: application.ActionResult{Data: production.Report{Batch: &b, Preview: &p}}}
		s.win = &mygo.Window{}
		productionTestUpdate(t, window)
		if attempt == 0 {
			if s.confirmationDialog != nil {
				t.Fatal("contained feature opened a pointless merge confirmation")
			}
			if v.Steps[4].Message != "已包含，无需合并" {
				t.Fatalf("missing branch-local no-op feedback: %+v", v.Steps[4])
			}
		} else if s.confirmationDialog == nil || !strings.Contains(s.confirmationDialog.Detail, strings.Repeat("e", 40)) {
			t.Fatal("same branch with new commits could not be confirmed")
		}
		s.cancelConfirmationDialog()
	}
}

func TestProductionMergeCompletesWithoutFollowupRead(t *testing.T) {
	s, v, b, backend, window := productionRequestFixture(t)
	s.productionRequestFor(v, application.Action{Kind: "production.merge", Payload: map[string]any{"batchId": b.ID, "branch": v.Branch, "sourceSha": strings.Repeat("d", 40), "releaseSha": b.ReleaseSHA, "confirmed": true}}, "")
	productionTestCall(t, backend)
	b.ReleaseSHA, b.Message = strings.Repeat("f", 40), "合并完成"
	backend.replies <- productionTestAnswer{result: application.ActionResult{Message: b.Message, Data: production.Report{Batch: &b}}}
	productionTestUpdate(t, window)
	if v.Busy || len(backend.calls) != 0 || v.ActiveStep != 4 || v.Steps[4].State != "complete" {
		t.Fatalf("successful merge started another read or stayed busy: %+v", v.Steps[4])
	}
	if s.productionBatch(v).ReleaseSHA != b.ReleaseSHA || v.Preview.ReleaseSHA != b.ReleaseSHA || v.Preview.BranchHeads[b.ReleaseBranch] != b.ReleaseSHA {
		t.Fatal("pushed SHA was not applied to the batch and preview")
	}
}

func TestProductionIncludedBranchAndFollowupCheckShowOneLocalResult(t *testing.T) {
	for _, width := range []int{960, 1440} {
		for _, dark := range []bool{false, true} {
			s, v, b := productionFixture(t)
			const message = "已包含，无需合并"
			v.Message, b.Message = message, message
			b.Tags = []production.Tag{{Name: "2.3.44", Commit: b.ReleaseSHA, Status: domain.GenbuTagProbeStatus{Build: domain.GenbuBuilding}}}
			s.production.batches[0] = b
			v.Preview.MainInRelease = true
			v.Steps = map[int]productionStepView{4: {State: "skipped", Message: message}, 6: {State: "running"}}
			v.Busy, v.ActiveStep, v.ActiveAction = true, 6, "production.inspect"
			tester := productionIterationTester(s, width, dark)
			count := 0
			for _, text := range tester.Texts() {
				if text == message {
					count++
				}
			}
			if count != 0 || s.productionMergeStatus(v, &b).Text != "已完成" || tester.HasText("处理中…") || tester.HasText("正在检查线上版本及远端提交…") {
				t.Fatalf("duplicated or vague status: count=%d", count)
			}
			merge, _ := tester.Find("合并分支")
			check, _ := tester.Find("构建生产 Tag")
			result, _ := tester.Find("合并分支状态")
			if result.Y < merge.Y || result.Y > check.Y {
				t.Fatal("branch result escaped its own zone")
			}
			tester.SetSize(width, 1600)
			captureProductionSummary(t, tester, fmt.Sprintf("hierarchy-%d-dark-%t-contained-checking", width, dark))
		}
	}
}

func TestProductionBranchPickerKeyboardSearchAndEscClearOldFeedback(t *testing.T) {
	s, v, _ := productionFixture(t)
	v.Preview.Branches = append(v.Preview.Branches, "feature/second-branch", "feature/third")
	v.Steps = map[int]productionStepView{4: {State: "skipped", Message: "old branch-only feedback"}}
	tester := productionIterationTester(s, 960, false)
	if err := tester.Click("待合并的单个远端分支"); err != nil {
		t.Fatal(err)
	}
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Type("second")
	if tester.HasText("old branch-only feedback") || !tester.HasText("feature/second-branch") || tester.HasText("feature/third") {
		t.Fatal("typing did not filter options or clear old branch feedback")
	}
	tester.Key(0, ui.KeyDown)
	tester.Key(0, ui.KeyEnter)
	if v.Branch != "feature/second-branch" {
		t.Fatalf("keyboard selection did not bind branch: %s", v.Branch)
	}
	tester.Key(0, ui.KeyDown)
	tester.Key(0, ui.KeyEscape)
	if tester.HasText("feature/third") {
		t.Fatal("Esc did not close dropdown")
	}
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Type("feature/manually-entered")
	tester.Key(0, ui.KeyEscape)
	if v.Branch != "feature/manually-entered" {
		t.Fatal("manual remote-branch input was lost")
	}
}

func TestProductionResultIdentityAndHistorySwitchRejectStaleFeedback(t *testing.T) {
	for _, change := range []string{"none", "branch", "batch", "home", "config", "service", "generation", "source", "target"} {
		t.Run(change, func(t *testing.T) {
			s, v, b := productionFixture(t)
			identity := &productionResultIdentity{Selection: v.Selection, BatchID: b.ID, Branch: v.Branch, SourceSHA: strings.Repeat("d", 40), TargetSHA: b.ReleaseSHA, Home: s.snapshot.Home, ConfigScope: s.currentProductionScope(), Generation: v.Generation}
			v.Steps = map[int]productionStepView{4: {State: "skipped", Message: "bound result", Identity: identity}}
			switch change {
			case "branch":
				v.Branch = "feature/other"
			case "batch":
				v.selectProductionBatch("other")
			case "home":
				s.snapshot.Home = "other"
			case "config":
				s.configJSON = `{"tagEnabled":false}`
			case "service":
				v.Selection.ServiceID = "other"
			case "generation":
				identity.Generation++
			case "source", "target":
				p := *v.Preview
				p.BranchHeads = map[string]string{v.Branch: identity.SourceSHA}
				if change == "source" {
					p.BranchHeads[v.Branch] = strings.Repeat("e", 40)
				} else {
					p.ReleaseSHA = strings.Repeat("e", 40)
				}
				v.invalidateProductionBranchResult(&p)
			}
			shown := s.productionReleaseModel(v, &b).Merge.Message == "bound result"
			if shown != (change == "none") {
				t.Fatalf("stale local result accepted: %t", shown)
			}
			if completed := s.productionMergeStatus(v, &b).Text == "已完成"; completed != (change == "none") {
				t.Fatalf("completion survived an identity or commit change: %t", completed)
			}
		})
	}
}

func TestProductionHistoryMenuAndEndBatchUsesMoreConfirmation(t *testing.T) {
	s, v, b := productionFixture(t)
	old := b
	old.ID, old.Version, old.ReleaseBranch, old.Ended, old.Phase = "previous", "2.3.42", "release/2.3.42", true, "ENDED"
	s.production.batches = append(s.production.batches, old)
	tester := productionIterationTester(s, 960, false)
	if tester.HasText(old.ReleaseBranch) || tester.HasText("结束批次") {
		t.Fatal("historical content or end action is expanded by default")
	}
	s.win = &mygo.Window{}
	if err := tester.Click("更多生产发布操作"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("结束批次"); err != nil {
		t.Fatal(err)
	}
	if s.confirmationDialog == nil || !strings.Contains(s.confirmationDialog.Detail, b.Target.Repository) || !strings.Contains(s.confirmationDialog.Detail, b.ReleaseBranch) {
		t.Fatal("end batch lost its protected confirmation")
	}
	tester.Key(0, ui.KeyEscape)
	if s.confirmationDialog != nil || s.productionBatch(v).Ended {
		t.Fatal("canceling confirmation ended batch")
	}
	if err := tester.Click("更多生产发布操作"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("查看历史批次"); err != nil {
		t.Fatal(err)
	}
	tester.SetPreferences(ui.Preferences{ReduceMotion: true, TextScale: 1})
	if !tester.HasText(old.ReleaseBranch) {
		t.Fatal("past batch not accessible")
	}
	v.Steps = map[int]productionStepView{4: {State: "skipped", Message: "old selection result"}}
	// Render the injected feedback before locating the shifted history action.
	tester.Frame()
	productionRevealControl(t, tester, "查看")
	if err := tester.Click("查看"); err != nil {
		t.Fatal(err)
	}
	if v.BatchID != old.ID || v.Preview != nil || len(v.Steps) != 0 || tester.HasText("old selection result") {
		t.Fatalf("history switch reused another batch's preview/result: batch=%q preview=%t steps=%v text=%t", v.BatchID, v.Preview != nil, v.Steps, tester.HasText("old selection result"))
	}
}

func productionRevealControl(t *testing.T, tester *ui.Tester, text string) {
	t.Helper()
	r, ok := tester.Find(text)
	if !ok {
		t.Fatalf("missing control: %s", text)
	}
	if r.Y+r.H > 850 || r.Y < 60 {
		tester.Scroll(900, 750, 0, r.Y-650)
	}
}

func TestProductionLateFeaturePreviewCannotConfirmAnotherContext(t *testing.T) {
	for _, change := range []string{"branch", "batch", "home", "config", "generation", "page"} {
		t.Run(change, func(t *testing.T) {
			s, v, b, backend, window := productionRequestFixture(t)
			branch := v.Branch
			s.productionRequestFor(v, application.Action{Kind: "production.feature-preview", Payload: map[string]any{"batchId": b.ID, "branch": branch}}, "feature-preview")
			productionTestCall(t, backend)
			switch change {
			case "branch":
				v.Branch = "feature/other"
			case "batch":
				v.selectProductionBatch("other")
			case "home":
				s.snapshot.Home = "other"
			case "config":
				s.configJSON = `{"tagEnabled":false}`
			case "generation":
				v.Generation++
			case "page":
				s.page = "tasks"
			}
			p := production.Preview{Target: b.Target, ReleaseSHA: b.ReleaseSHA, BranchHeads: map[string]string{branch: strings.Repeat("d", 40)}, Commits: "dddd new incoming commit"}
			backend.replies <- productionTestAnswer{result: application.ActionResult{Data: production.Report{Batch: &b, Preview: &p}}}
			s.win = &mygo.Window{}
			productionTestUpdate(t, window)
			if s.confirmationDialog != nil {
				t.Fatal("late preflight opened confirmation for another context")
			}
		})
	}
}

func TestProductionDirectBuildChecksFreshDataAndPublishesWithoutConfirmation(t *testing.T) {
	for _, scenario := range []string{"ready", "first-tag", "needs-sync", "needs-sync-after-tag", "no-changes", "read-failure", "prepare-failure", "publish-failure", "page", "service", "config", "home", "batch", "generation", "manual", "branch", "ended", "pending", "replaced-plan"} {
		t.Run(scenario, func(t *testing.T) {
			s, v, b, backend, window := productionRequestFixture(t)
			s.notes[0] = "unsaved draft"
			if scenario == "first-tag" {
				b.Branches, b.ReleaseSHA = nil, b.InitialSHA
				v.Preview.ReleaseSHA = b.ReleaseSHA
			}
			if scenario == "needs-sync-after-tag" || scenario == "no-changes" {
				b.Tags = []production.Tag{{Name: "2.3.44", Commit: b.ReleaseSHA, Status: domain.GenbuTagProbeStatus{Build: domain.GenbuSuccess}}}
			}
			s.production.batches[0] = b
			tester := productionIterationTester(s, 960, false)
			productionRevealControl(t, tester, "构建 Tag")
			if err := tester.Click("构建 Tag"); err != nil {
				t.Fatal(err)
			}
			if call := productionTestCall(t, backend); call.Kind != "production.prepare-tag" || call.Payload["confirmed"] != nil {
				t.Fatalf("build must start with a fresh read: %+v", call)
			}
			generation := v.Generation
			if err := tester.Click("构建 Tag"); err != nil || v.Generation != generation || len(backend.calls) != 0 {
				t.Fatal("duplicate build click started another request")
			}
			p := *v.Preview
			p.MainInRelease = scenario != "needs-sync" && scenario != "needs-sync-after-tag"
			if scenario == "read-failure" {
				backend.replies <- productionTestAnswer{err: errors.New("synthetic read failed")}
				productionTestUpdate(t, window)
				if !v.PreparationInvalid || v.Busy || s.confirmationDialog != nil || len(backend.calls) != 0 {
					t.Fatal("failed read continued into preparation or publishing")
				}
				return
			}
			if v.ActiveStep != 6 || !v.Busy {
				t.Fatal("automatic check lost its build-region ownership")
			}
			b.TagPlan = &production.TagPlan{Token: "fresh-check", Tag: "2.3.44", Commit: b.ReleaseSHA, MainSHA: p.MainSHA, Preview: p}
			if scenario == "no-changes" {
				b.TagPlan.Tag = "2.3.44.1"
			}
			switch scenario {
			case "page":
				s.page = "tasks"
			case "service":
				s.production.selected = "another-service"
			case "config":
				s.configJSON = `{"tagEnabled":false}`
			case "home":
				s.snapshot.Home += "-other"
			case "batch":
				v.selectProductionBatch("another")
			case "generation":
				v.Generation++
			case "manual":
				v.ManualTag = "other"
			case "branch":
				v.Branch = "feature/other"
			case "ended":
				b.Ended = true
			case "pending":
				b.Intent = &production.Intent{Kind: "tag", Step: "tag-push"}
			case "replaced-plan":
				newer := b
				plan := *b.TagPlan
				plan.Token = "replacement-token"
				newer.TagPlan = &plan
				newer.UpdatedAt = time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)
				s.production.batches[0] = newer
			}
			if scenario == "needs-sync" || scenario == "needs-sync-after-tag" || scenario == "prepare-failure" {
				b.TagPlan = nil
				backend.replies <- productionTestAnswer{result: application.ActionResult{Data: production.Report{Batch: &b, Preview: &p}}, err: errors.New("preparation failed or branches require synchronization")}
			} else {
				backend.replies <- productionTestAnswer{result: application.ActionResult{Data: production.Report{Batch: &b, Preview: &p}}}
			}
			productionTestUpdate(t, window)
			if s.confirmationDialog != nil {
				t.Fatal("build opened a confirmation dialog")
			}
			ready := scenario == "ready" || scenario == "first-tag" || scenario == "no-changes" || scenario == "publish-failure"
			if !ready {
				if len(backend.calls) != 0 {
					t.Fatal("failed, stale or blocked preparation continued into a write")
				}
				if scenario == "needs-sync" || scenario == "needs-sync-after-tag" {
					tester.Frame()
					if !tester.HasText("合并主分支到 release") || tester.HasText("检查构建条件") || tester.HasText("构建前检查") {
						t.Fatal("synchronization must remain an explicit build-region action")
					}
				}
				return
			}
			call := productionTestCall(t, backend)
			if call.Kind != "production.publish" || call.Payload["batchId"] != b.ID || call.Payload["token"] != b.TagPlan.Token || call.Payload["confirmed"] != true {
				t.Fatalf("direct build lost its prepared token or batch identity: %+v", call)
			}
			if !v.Busy || v.ActiveStep != 6 {
				t.Fatal("publishing left the foreground build gate")
			}
			if scenario == "publish-failure" {
				backend.replies <- productionTestAnswer{result: application.ActionResult{Data: production.Report{Batch: &b}}, err: errors.New("synthetic push failed")}
			} else {
				b.Tags = append(append([]production.Tag(nil), b.Tags...), production.Tag{Name: b.TagPlan.Tag, Commit: b.TagPlan.Commit, PushedAt: time.Now().UTC().Format(time.RFC3339Nano), Status: domain.GenbuTagProbeStatus{Build: domain.GenbuSuccess}})
				b.TagPlan, b.Phase = nil, "READY"
				backend.replies <- productionTestAnswer{result: application.ActionResult{Data: production.Report{Batch: &b, Preview: &p}}}
			}
			productionTestUpdate(t, window)
			if v.Busy || len(backend.calls) != 0 || s.confirmationDialog != nil || s.notes[0] != "unsaved draft" {
				t.Fatal("build did not stop after one result or changed the task draft")
			}
			if scenario == "publish-failure" && (!v.PreparationInvalid || v.Error == "") {
				t.Fatal("push failure did not invalidate preparation or expose its error")
			}
			// A second click must obtain another fresh check, never replay the token.
			tester.Frame()
			productionRevealControl(t, tester, "构建 Tag")
			if err := tester.Click("构建 Tag"); err != nil {
				t.Fatal(err)
			}
			if call := productionTestCall(t, backend); call.Kind != "production.prepare-tag" {
				t.Fatalf("repeated build reused the previous preparation: %+v", call)
			}
			backend.replies <- productionTestAnswer{err: errors.New("finish repeated read")}
			productionTestUpdate(t, window)
		})
	}
}

func TestProductionAutomaticCheckProgressStaysInBuildRegion(t *testing.T) {
	s, v, b, base, window := productionRequestFixture(t)
	backend := &productionProgressBackend{productionTestBackend: base, contexts: make(chan context.Context, 1)}
	s.backend = backend
	s.productionRequestFor(v, application.Action{Kind: "production.sync", Payload: map[string]any{"batchId": b.ID}}, "build-publish")
	ctx := <-backend.contexts
	productionTestCall(t, base)
	production.EmitProgress(ctx, production.Progress{Selection: v.Selection, BatchID: b.ID, Step: 5, State: "running", Message: "checking latest baseline"})
	productionTestUpdate(t, window)
	if v.ActiveStep != 6 || v.Steps[6].Message != "checking latest baseline" || v.Steps[5].Message != "" {
		t.Fatal("automatic read exposed a separate check operation")
	}
	base.replies <- productionTestAnswer{err: errors.New("finish progress scenario")}
	productionTestUpdate(t, window)
}

func TestProductionBuildProbeWaitsUntilOtherConfirmationCloses(t *testing.T) {
	s, v, b, backend, window := productionRequestFixture(t)
	v.Preview.MainInRelease = true
	b.TagPlan = &production.TagPlan{Token: "prepared", Tag: "2.3.44.1", Commit: b.ReleaseSHA, MainSHA: v.Preview.MainSHA, Preview: *v.Preview}
	b.Tags = []production.Tag{{Name: "2.3.44", PushedAt: time.Now().UTC().Format(time.RFC3339Nano), Status: domain.GenbuTagProbeStatus{Build: domain.GenbuBuilding}}}
	s.production.batches[0] = b
	s.win = &mygo.Window{}
	s.confirmProductionEnd(v, b)
	if s.confirmationDialog == nil {
		t.Fatal("missing write confirmation")
	}
	s.scheduleProductionProbe(v, b, window)
	wake := func() {
		s.production.mu.Lock()
		s.production.timers[b.ID].Reset(time.Millisecond)
		s.production.mu.Unlock()
		productionTestUpdate(t, window)
	}
	wake()
	if v.Busy || len(backend.calls) != 0 || s.confirmationDialog == nil {
		t.Fatal("background probe stole the foreground confirmation's request gate")
	}
	s.cancelConfirmationDialog()
	s.win = nil
	wake()
	if call := productionTestCall(t, backend); call.Kind != "production.refresh-build" {
		t.Fatalf("build tracking did not resume: %+v", call)
	}
	backend.replies <- productionTestAnswer{result: application.ActionResult{Data: production.Report{Batch: &b}}}
	productionTestUpdate(t, window)
}

func TestProductionHierarchyEvidenceStates(t *testing.T) {
	for _, width := range []int{960, 1440} {
		for _, dark := range []bool{false, true} {
			for _, state := range []string{"dropdown", "merge-refresh-failed", "check-conflict", "sync-needed", "history", "unknown"} {
				t.Run(fmt.Sprintf("%d-%t-%s", width, dark, state), func(t *testing.T) {
					s, v, b := productionFixture(t)
					switch state {
					case "dropdown":
						v.Preview.Branches = append(v.Preview.Branches, "feature/requirement-7133064844-blacklist-rules-with-a-long-branch-name", "feature/payment-configuration", "release/test")
					case "merge-refresh-failed":
						v.Steps = map[int]productionStepView{4: {State: "warning", Message: "合并已完成，状态刷新失败：网络暂不可用，请重新检查。"}}
					case "check-conflict":
						b.Intent = &production.Intent{Kind: "release", Step: "release-sync", Merge: &production.Merge{TargetBranch: b.ReleaseBranch, SourceSHA: b.ReleaseSHA, Worktree: "C:/synthetic/conflict-worktree", Conflicts: []string{"src/payment.go"}}}
						b.Phase, b.Message = "CONFLICT", "主分支同步发生冲突，请解决后校验并继续。"
					case "sync-needed":
						v.BuildChecked = true
					case "history":
						b.Tags = []production.Tag{{Name: "2.3.44", Commit: strings.Repeat("a", 40), Status: domain.GenbuTagProbeStatus{Build: domain.GenbuSuccess}}, {Name: "2.3.44.1", Commit: strings.Repeat("b", 40), Status: domain.GenbuTagProbeStatus{Build: domain.GenbuFailed}}, {Name: "2.3.44.2", Commit: b.ReleaseSHA, Status: domain.GenbuTagProbeStatus{Build: domain.GenbuBuilding}}}
					case "unknown":
						v.Preview = nil
					}
					s.production.batches[0] = b
					tester := productionIterationTester(s, width, dark)
					tester.SetSize(width, 1600)
					if state == "dropdown" {
						if err := tester.Click("待合并的单个远端分支"); err != nil {
							t.Fatal(err)
						}
					} else if state == "history" {
						if !productionExactText(tester, "示例服务：2.3.44") || !productionExactText(tester, "示例服务：2.3.44.1") || !productionExactText(tester, "示例服务：2.3.44.2") || tester.HasText("历史 Tag") {
							t.Fatal("Tag history is not flat and fully visible")
						}
						tester.SetPreferences(ui.Preferences{ReduceMotion: true, TextScale: 1})
						for _, status := range []string{"构建成功", "构建失败", "构建中"} {
							if !tester.HasText(status) {
								t.Fatalf("independent Tag result missing: %s", status)
							}
						}
					}
					captureProductionSummary(t, tester, fmt.Sprintf("hierarchy-%d-dark-%t-%s", width, dark, state))
				})
			}
		}
	}
}
