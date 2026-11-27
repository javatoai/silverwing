package desktop

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/production"

	"github.com/egoist/mygo"
)

func TestProductionEndReturnsToIdleAndStartsWithFreshBaseline(t *testing.T) {
	for _, firstWrite := range []string{"production.create", "production.merge-main"} {
		t.Run(firstWrite, func(t *testing.T) {
			s, v, b, backend, window := productionRequestFixture(t)
			state := s.ensureProduction()
			v.ReleaseStarted, v.BuildChecked = true, true
			v.BranchQuery, v.SuggestedBranch = "synthetic", b.ReleaseBranch
			v.Steps = map[int]productionStepView{6: {State: "prepared"}}
			b.Tags = []production.Tag{{Name: "2.3.44", PushedAt: time.Now().UTC().Format(time.RFC3339Nano), Status: domain.GenbuTagProbeStatus{Build: domain.GenbuBuilding}}}
			b.RetainedWorktrees = []string{"C:/synthetic/retained-worktree"}
			state.batches[0] = b
			s.scheduleProductionProbe(v, b, window)

			s.win = &mygo.Window{}
			s.confirmProductionEnd(v, b)
			if s.confirmationDialog == nil {
				t.Fatal("missing end confirmation")
			}
			s.win = nil
			s.finishConfirmationDialog(s.confirmationDialog, 0)
			if call := productionTestCall(t, backend); call.Kind != "production.end" || call.Payload["batchId"] != b.ID || call.Payload["confirmed"] != true {
				t.Fatalf("unexpected end request: %+v", call)
			}
			ended := b
			ended.Ended, ended.Phase = true, "ENDED"
			backend.replies <- productionTestAnswer{result: application.ActionResult{Data: production.Report{Batch: &ended}}}
			productionTestUpdate(t, window)
			if v.BatchID != "" || v.Preview != nil || !s.productionWaitingToStart(v, s.productionBatch(v)) || v.Busy || v.ReleaseStarted || v.BuildChecked ||
				v.Version != "" || v.SuggestedBranch != "" || v.Branch != "" || v.BranchQuery != "" || len(v.Steps) != 0 || !v.PreparationInvalid {
				t.Fatalf("ended release still populates the main workspace: %+v", v)
			}
			if len(state.batches) != 1 || !state.batches[0].Ended || len(state.batches[0].Tags) != 1 || len(state.batches[0].RetainedWorktrees) != 1 || len(state.timers) != 0 {
				t.Fatalf("ended release was not retained solely as history: %+v", state.batches)
			}
			s.inspectProductionOnSelection(v)
			if call := productionTestCall(t, backend); call.Kind != "production.list" {
				t.Fatalf("entry must check local batches before baselines: %+v", call)
			}
			backend.replies <- productionTestAnswer{result: application.ActionResult{Data: production.Report{Batches: []production.Batch{ended}}}}
			productionTestUpdate(t, window)
			call := productionTestCall(t, backend)
			if call.Kind != "production.inspect" || call.ServiceID != v.Selection.ServiceID || len(call.Payload) != 0 || !v.Busy || s.productionWaitingToStart(v, nil) {
				t.Fatalf("start did not request an independent fresh baseline: %+v", call)
			}
			fresh := production.Preview{Target: b.Target, ProductionTag: "2.3.44", ProductionSHA: strings.Repeat("d", 40), MainSHA: strings.Repeat("e", 40),
				ProductionInMain: firstWrite == "production.create", SuggestedVersion: "2.3.45", Branches: []string{"master", b.ReleaseBranch}}
			backend.replies <- productionTestAnswer{result: application.ActionResult{Data: production.Report{Preview: &fresh}}}
			productionTestUpdate(t, window)
			if v.Busy || v.Preview == nil || v.Preview.ProductionTag != fresh.ProductionTag || v.Version != "release/2.3.45" || v.BatchID != "" || len(backend.calls) != 0 {
				t.Fatalf("fresh baseline did not reach confirmed initialization: %+v", v)
			}

			s.win = &mygo.Window{}
			if firstWrite == "production.create" {
				s.confirmProductionCreate(v)
			} else {
				s.confirmProductionMain(v)
			}
			if s.confirmationDialog == nil {
				t.Fatalf("missing confirmation for %s: %s", firstWrite, v.Error)
			}
			s.win = nil
			s.finishConfirmationDialog(s.confirmationDialog, 0)
			call = productionTestCall(t, backend)
			observed, _ := call.Payload["preview"].(production.Preview)
			if call.Kind != firstWrite || call.Payload["batchId"] != "" || call.Payload["confirmed"] != true || observed.ProductionSHA != fresh.ProductionSHA || observed.MainSHA != fresh.MainSHA {
				t.Fatalf("next release reused previous batch or baseline: %+v", call)
			}
			next := production.Batch{ID: strings.Repeat("f", 32), Target: b.Target, Phase: "MAIN_READY", MainSHA: fresh.MainSHA}
			if firstWrite == "production.create" {
				next.Phase, next.Version, next.ReleaseBranch, next.InitialSHA = "READY", "2.3.45", "release/2.3.45", fresh.MainSHA
			}
			backend.replies <- productionTestAnswer{result: application.ActionResult{Data: production.Report{Batch: &next}}}
			productionTestUpdate(t, window)
			if v.BatchID != next.ID || len(state.batches) != 2 || state.batches[0].ID != b.ID || !state.batches[0].Ended || s.productionWaitingToStart(v, s.productionBatch(v)) {
				t.Fatalf("next release failed to claim a fresh batch: %+v", state.batches)
			}
		})
	}
}

func TestProductionEndRequiresConfirmedEndedRecord(t *testing.T) {
	for _, name := range []string{"save-failed", "record-missing", "different-batch", "still-active"} {
		t.Run(name, func(t *testing.T) {
			s, v, b, backend, window := productionRequestFixture(t)
			s.productionRequest(application.Action{Kind: "production.end", Payload: map[string]any{"batchId": b.ID, "confirmed": true}}, "end-start")
			productionTestCall(t, backend)
			ended := b
			ended.Ended, ended.Phase = true, "ENDED"
			result := production.Report{Batch: &ended}
			var err error
			switch name {
			case "save-failed":
				err = errors.New("fixture record save failed")
			case "record-missing":
				result.Batch = nil
			case "different-batch":
				ended.ID = strings.Repeat("f", 32)
			case "still-active":
				result.Batch = &b
			}
			backend.replies <- productionTestAnswer{result: application.ActionResult{Data: result}, err: err}
			productionTestUpdate(t, window)
			batch := s.productionBatch(v)
			if v.Busy || v.Error == "" || v.BatchID != b.ID || batch == nil || batch.Ended || len(s.ensureProduction().batches) != 1 || s.productionWaitingToStart(v, batch) || len(backend.calls) != 0 {
				t.Fatalf("unconfirmed end changed the workspace or history: %+v / %+v", v, s.ensureProduction().batches)
			}
		})
	}
}

func TestProductionStartKeepsInitializationAfterBaselineFailure(t *testing.T) {
	s, v, _, backend, window := productionRequestFixture(t)
	s.ensureProduction().batches = nil
	v.resetProductionRelease()
	s.startProductionRelease(v)
	productionTestCall(t, backend)
	backend.replies <- productionTestAnswer{err: errors.New("fixture baseline read failed")}
	productionTestUpdate(t, window)
	if v.Busy || v.Preview != nil || v.BatchID != "" || v.ActiveStep != 1 || v.Error == "" || s.productionWaitingToStart(v, nil) {
		t.Fatalf("baseline failure replaced the started workflow or hid retry feedback: %+v", v)
	}
}

func TestProductionEntryReadsBatchesAndPreparesOnlySelectedConfiguredService(t *testing.T) {
	for _, name := range []string{"idle", "ended", "recovery", "active", "draft", "cached-preparation", "removed", "read-error"} {
		t.Run(name, func(t *testing.T) {
			s, v, b, backend, window := productionRequestFixture(t)
			if name != "cached-preparation" {
				v.Preview = nil
			}
			var records []production.Batch
			switch name {
			case "idle":
				v.BatchID = ""
			case "ended":
				b.Ended = true
				records = []production.Batch{b}
			case "recovery":
				b.InitialSHA = ""
				b.Intent = &production.Intent{Kind: "create", Step: "release-create"}
				records = []production.Batch{b}
			case "active":
				records = []production.Batch{b}
			case "draft", "cached-preparation":
				v.BatchID, v.ReleaseStarted = "", true
			case "removed":
				s.snapshot.Services = nil
				records = []production.Batch{b}
			case "read-error":
				records = []production.Batch{b}
			}
			s.ensureProduction().batches = records
			s.enterProductionPage()
			if call := productionTestCall(t, backend); call.Kind != "production.list" {
				t.Fatalf("wrong initial request: %+v", call)
			}
			if name == "read-error" {
				backend.replies <- productionTestAnswer{err: errors.New("fixture list read failed")}
			} else {
				backend.replies <- productionTestAnswer{result: application.ActionResult{Data: production.Report{Batches: records}}}
			}
			productionTestUpdate(t, window)
			if name != "removed" && name != "read-error" {
				call := productionTestCall(t, backend)
				kind := "production.inspect"
				var id any
				if name == "recovery" || name == "active" {
					id = b.ID
				}
				if name == "active" {
					kind = "production.release-branches"
				}
				if call.Kind != kind || call.Payload["batchId"] != id || call.ServiceID != b.Target.ServiceID {
					t.Fatalf("failed to resume %s inspection: %+v", name, call)
				}
				backend.replies <- productionTestAnswer{err: errors.New("fixture baseline read failed")}
				productionTestUpdate(t, window)
				return
			}
			if name == "read-error" && (s.ensureProduction().entryError == "" || s.productionBatch(v).ID != b.ID) {
				t.Fatal("failed list should retain the current batch and offer retry")
			}
			if v.Busy || len(backend.calls) != 0 {
				t.Fatalf("%s automatically inspected the baseline", name)
			}
		})
	}
}

func TestProductionActiveBranchRefreshKeepsCompletedBaselineAndShowsRemoteDrift(t *testing.T) {
	s, v, b, backend, window := productionRequestFixture(t)
	b.ProductionTag, b.ProductionSHA, b.MainSHA = v.Preview.ProductionTag, v.Preview.ProductionSHA, v.Preview.MainSHA
	s.releaseUnit.state.production.batches[0] = b
	v.Preview = nil
	s.inspectSelectedProductionRelease(v)
	if call := productionTestCall(t, backend); call.Kind != "production.release-branches" || call.Payload["batchId"] != b.ID {
		t.Fatalf("active release checked initialization again: %+v", call)
	}
	err := errors.New("fixture remote branch read failed")
	backend.replies <- productionTestAnswer{err: err}
	productionTestUpdate(t, window)
	tester := productionCardsTester(s, 960, false)
	if s.productionBaselineStatus(v, &b).Text != "已完成" || !tester.HasText(err.Error()) || tester.HasText("重新检查") {
		t.Fatal("branch refresh failure lost its retry path or reopened initialization")
	}
	if err := tester.Click("刷新分支"); err != nil {
		t.Fatal(err)
	}
	if call := productionTestCall(t, backend); call.Kind != "production.release-branches" || call.Payload["batchId"] != b.ID {
		t.Fatalf("branch refresh retry left its active release: %+v", call)
	}
	preview := production.Preview{Target: b.Target, ReleaseSHA: strings.Repeat("e", 40), Branches: []string{b.ReleaseBranch, "feature/refreshed"}}
	backend.replies <- productionTestAnswer{result: application.ActionResult{Data: production.Report{Batch: &b, Preview: &preview}}}
	productionTestUpdate(t, window)
	tester.Frame()
	if v.Error != "" || !tester.HasText("查看并采纳远端") || v.Preview == nil || !slices.Contains(v.Preview.Branches, "feature/refreshed") || len(backend.calls) != 0 {
		t.Fatal("remote branch refresh failed to restore choices and expose new release commits")
	}
}

func TestProductionDisabledGenbuProbeKeepsOnlyLocalHistory(t *testing.T) {
	s, v, b, backend, window := productionRequestFixture(t)
	state := s.ensureProduction()
	b.Target.GenbuService = "fixture-service"
	b.ReleaseCreatedAt = time.Now().Add(-13 * time.Hour).UTC().Format(time.RFC3339Nano)
	b.Tags = []production.Tag{{Name: "2.3.44", PushedAt: time.Now().UTC().Format(time.RFC3339Nano), Status: domain.GenbuTagProbeStatus{Build: domain.GenbuBuilding}}}
	ended := productionHistoryRecord(b, strings.Repeat("a", 32), "release/2.3.42", time.Now().Add(-24*time.Hour))
	state.batches = []production.Batch{b, ended}
	s.scheduleProductionProbe(v, b, window)
	if state.timers[b.ID] == nil {
		t.Fatal("enabled service did not schedule an active build probe")
	}

	var cfg domain.AppConfig
	if err := json.Unmarshal([]byte(s.settingsUnit.state.configJSON), &cfg); err != nil {
		t.Fatal(err)
	}
	cfg.Services[0].GenbuProbeEnabled = false
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	s.settingsUnit.state.configJSON = string(data)
	s.enterProductionPage()
	if call := productionTestCall(t, backend); call.Kind != "production.list" {
		t.Fatalf("disabled service requested a remote operation: %+v", call)
	}
	backend.replies <- productionTestAnswer{result: application.ActionResult{Data: production.Report{Batches: []production.Batch{b, ended}}}}
	productionTestUpdate(t, window)
	if len(state.choices) != 1 || !state.choices[0].Historical || v.Busy || s.confirmationDialog != nil || len(state.timers) != 0 {
		t.Fatal("disabled service retained active publishing, an old-release prompt, or a build probe")
	}
	s.productionInspect("")
	s.scheduleProductionProbe(v, b, window)
	if v.Busy || len(backend.calls) != 0 || len(state.timers) != 0 {
		t.Fatal("disabled service accepted an inspection or scheduled another probe")
	}

	tester := productionIterationTester(s, 960, false)
	if !tester.HasText("示例服务（历史）") || tester.HasText("开始新发布") || tester.HasText("构建生产 Tag") || tester.HasText("合并分支") {
		t.Fatal("the main page did not switch to history-only access")
	}
	if _, found := tester.Find("结束批次"); found {
		t.Fatal("active production actions remained accessible")
	}

	d := openProductionHistoryTest(t, s, v, backend, window)
	if len(s.productionHistoryRecords(d)) != 2 {
		t.Fatal("read-only history should retain both active and ended batches")
	}
	tester.Frame()
	if _, found := tester.Find("更多批次操作 · " + ended.ReleaseBranch); found {
		t.Fatal("read-only history still exposes changes")
	}
	s.viewProductionHistory(d, ended)
	tester.Frame()
	if !tester.HasText("生产 Tag 记录") || !tester.HasText(ended.Tags[0].Name) ||
		tester.HasText("构建成功") || tester.HasText("构建日志") || tester.HasText("刷新构建状态") {
		t.Fatal("local Tag details or Genbu visibility were incorrect")
	}
	s.requestProductionHistoryDetail(d, ended, "production.refresh-build")
	if v.Busy || len(backend.calls) != 0 {
		t.Fatal("read-only history accepted a remote refresh")
	}
}

func TestProductionStaleActiveReadsDoNotReopenEndedBatch(t *testing.T) {
	s, v, b, _, _ := productionRequestFixture(t)
	state := s.ensureProduction()
	ended := b
	ended.Ended, ended.Phase = true, "ENDED"
	state.mergeBatch(ended)
	state.detachEndedProductionBatches()
	state.replaceBatches([]production.Batch{b})
	state.mergeBatch(b)
	if batch := s.productionBatch(v); batch != nil || !s.productionWaitingToStart(v, batch) || len(state.batches) != 1 || !state.batches[0].Ended {
		t.Fatalf("stale active snapshot reopened an ended batch: %+v", state.batches)
	}
}

func TestProductionEntryChoosesLatestReleaseForSelectionWhenListArrives(t *testing.T) {
	s, first, b, backend, window := productionRequestFixture(t)
	state := s.ensureProduction()
	other := &productionView{Selection: production.Selection{ServiceID: "other"}}
	state.views[productionKey(other.Selection)] = other
	state.choices = append(state.choices, productionServiceChoice{Service: application.Service{ID: "other"}})
	latest := b
	latest.ID, latest.Target.Selection = strings.Repeat("e", 32), other.Selection
	latest.InitialSHA, latest.ReleaseCreatedAt = "ready", time.Now().UTC().Format(time.RFC3339Nano)
	older := latest
	older.ID, older.ReleaseCreatedAt = strings.Repeat("d", 32), time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	preparing := latest
	preparing.ID, preparing.InitialSHA, preparing.ReleaseCreatedAt = strings.Repeat("f", 32), "", ""
	preparing.CreatedAt = time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
	other.BatchID = older.ID
	s.inspectProductionOnSelection(first)
	if call := productionTestCall(t, backend); call.Kind != "production.list" {
		t.Fatalf("expected local read: %+v", call)
	}
	state.selected = productionKey(other.Selection)
	s.inspectProductionOnSelection(other)
	backend.replies <- productionTestAnswer{result: application.ActionResult{Data: production.Report{Batches: []production.Batch{older, preparing, b, latest}}}}
	productionTestUpdate(t, window)
	if call := productionTestCall(t, backend); call.Kind != "production.release-branches" || call.ServiceID != "other" || call.Payload["batchId"] != latest.ID {
		t.Fatalf("entry did not choose the latest release for the visible service: %+v", call)
	}
	if first.Busy || first.BatchID != b.ID || other.BatchID != latest.ID {
		t.Fatalf("entry changed the wrong service: first=%+v other=%+v", first, other)
	}
}

func TestProductionOldEndStartsNextReleaseOnlyInItsOwnView(t *testing.T) {
	for _, name := range []string{"new-release", "another-active", "switched"} {
		t.Run(name, func(t *testing.T) {
			s, v, b, backend, window := productionRequestFixture(t)
			state := s.ensureProduction()
			s.productionRequest(application.Action{Kind: "production.end", Payload: map[string]any{"batchId": b.ID, "confirmed": true}}, "end-start")
			productionTestCall(t, backend)
			if name == "switched" {
				state.selected = "another-service"
			}
			b.Ended = true
			backend.replies <- productionTestAnswer{result: application.ActionResult{Data: production.Report{Batch: &b}}}
			productionTestUpdate(t, window)
			if !state.batches[0].Ended {
				t.Fatal("the confirmed end was not retained in history")
			}
			if name == "switched" {
				if len(backend.calls) != 0 || v.Busy {
					t.Fatal("a background end should update only the old batch")
				}
				return
			}
			if call := productionTestCall(t, backend); call.Kind != "production.list" {
				t.Fatalf("expected a fresh read of local batches: %+v", call)
			}
			records := []production.Batch{b}
			var expected any
			kind := "production.inspect"
			if name == "another-active" {
				next := b
				next.ID, next.Ended = strings.Repeat("e", 32), false
				records, expected = append(records, next), next.ID
				kind = "production.release-branches"
			}
			backend.replies <- productionTestAnswer{result: application.ActionResult{Data: production.Report{Batches: records}}}
			productionTestUpdate(t, window)
			if call := productionTestCall(t, backend); call.Kind != kind || call.Payload["batchId"] != expected {
				t.Fatalf("end did not route to the expected release read: %+v", call)
			}
		})
	}
}
