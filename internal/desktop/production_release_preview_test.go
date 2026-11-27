package desktop

import (
	"strings"
	"testing"

	"github.com/egoist/mygo"
	"silverwing-go/internal/application"
	"silverwing-go/internal/production"
)

func TestProductionCustomReleaseBranchChecksThenConfirms(t *testing.T) {
	for _, mode := range []string{"reuse-new-batch", "reuse-current-batch", "cancel", "create"} {
		t.Run(mode, func(t *testing.T) {
			s, v, b, backend, window := productionRequestFixture(t)
			const branch, version = "release/2.3.45", "2.3.45"
			v.Version, v.SuggestedBranch = branch, "release/2.3.44"
			b.InitialSHA, b.ReleaseSHA, b.ReleaseBranch, b.Version, b.Phase = "", "", "", "", "MAIN_READY"
			b.Branches = nil
			s.releaseUnit.state.production.batches[0] = b
			if mode == "reuse-new-batch" {
				s.releaseUnit.state.production.batches = nil
				v.BatchID = ""
			}
			baseline, batchID := v.Preview, v.BatchID
			baseline.ReleaseSHA = ""
			tester := productionIterationTester(s, 960, false)
			if err := tester.Click("创建 release"); err != nil {
				t.Fatal(err)
			}
			read := productionTestCall(t, backend)
			if read.Kind != "production.release-preview" || read.Payload["version"] != version || read.Payload["batchId"] != batchID ||
				read.ServiceID != b.Target.ServiceID || read.Payload["allowReuse"] != true || read.Payload["confirmed"] != nil || s.confirmationDialog != nil {
				t.Fatalf("custom branch must read first without confirmation: %+v", read)
			}
			observed := *baseline
			observed.SuggestedVersion = "2.3.55"
			if mode != "create" {
				observed.ReleaseSHA = strings.Repeat("c", 40)
				observed.BranchHeads = map[string]string{branch: observed.ReleaseSHA}
				observed.MainInRelease = mode == "reuse-current-batch"
			}
			plan := &production.ReleaseBranchPlan{Version: version, Preview: observed}
			backend.replies <- productionTestAnswer{result: application.ActionResult{Data: production.Report{ReleasePlan: plan}}}
			s.win = &mygo.Window{}
			productionTestUpdate(t, window)
			if v.Version != branch || v.SuggestedBranch != "release/2.3.44" || v.Preview != baseline || v.Busy || s.confirmationDialog == nil {
				t.Fatalf("read replaced the draft or did not offer confirmation: %+v", v)
			}
			tester.Frame()
			confirm, kind := "复用分支", "production.reuse-release"
			if mode == "create" {
				confirm, kind = "创建并推送", "production.create"
			} else if !tester.HasText("远端分支已存在") || !tester.HasText("origin/"+branch) ||
				!tester.HasText("当前批次将从远端提交 "+shortProductionSHA(observed.ReleaseSHA)+" 继续。") {
				t.Fatal("reuse prompt omitted the branch or its observed commit")
			}
			s.win = nil
			if mode == "cancel" {
				if err := tester.Click("取消"); err != nil {
					t.Fatal(err)
				}
				current := s.productionBatch(v)
				if s.confirmationDialog != nil || v.request != nil || v.Version != branch || current == nil || current.ID != batchID || current.InitialSHA != "" {
					t.Fatal("cancel advanced the workflow or replaced the draft")
				}
				return
			}
			if err := tester.Click(confirm); err != nil {
				t.Fatal(err)
			}
			write := productionTestCall(t, backend)
			confirmed, ok := write.Payload["preview"].(production.Preview)
			if write.Kind != kind || write.Payload["version"] != version || write.Payload["batchId"] != batchID || write.Payload["confirmed"] != true ||
				!ok || confirmed.ReleaseSHA != observed.ReleaseSHA || confirmed.Target != observed.Target || confirmed.MainSHA != observed.MainSHA {
				t.Fatalf("confirmation lost the selected operation or commit: %+v", write)
			}
			if mode == "create" {
				return
			}
			b.Version, b.ReleaseBranch, b.InitialSHA, b.ReleaseSHA = version, branch, observed.ReleaseSHA, observed.ReleaseSHA
			b.ProductionTag, b.ProductionSHA, b.MainSHA = observed.ProductionTag, observed.ProductionSHA, observed.MainSHA
			b.ReleaseReused, b.Phase, b.Message = true, "READY", "已复用远端 release 分支"
			backend.replies <- productionTestAnswer{result: application.ActionResult{Data: production.Report{Batch: &b, Preview: &observed}}}
			productionTestUpdate(t, window)
			if current := s.productionBatch(v); current == nil || v.BatchID != b.ID || current.ReleaseBranch != branch ||
				v.Busy || v.Preview.ReleaseSHA != observed.ReleaseSHA || v.Preview.MainInRelease != observed.MainInRelease ||
				v.Steps[3].State != "complete" || v.request != nil {
				t.Fatalf("reuse result did not unlock the normal release steps: %+v", v)
			}
		})
	}
}

func TestProductionCustomReleaseBranchDropsStaleReadAndConfirmation(t *testing.T) {
	for _, when := range []string{"input-edited-before-read", "input-edited-before-confirmation", "batch-changed-before-confirmation", "service-changed-before-confirmation"} {
		t.Run(when, func(t *testing.T) {
			s, v, b, backend, window := productionRequestFixture(t)
			s.releaseUnit.state.production.batches, v.BatchID = nil, ""
			v.Version = "release/2.3.45"
			s.checkProductionReleaseBranch(v)
			productionTestCall(t, backend)
			observed := *v.Preview
			observed.ReleaseSHA = strings.Repeat("c", 40)
			if when == "input-edited-before-read" {
				v.Version = "release/2.3.46"
			}
			s.win = &mygo.Window{}
			backend.replies <- productionTestAnswer{result: application.ActionResult{Data: production.Report{
				ReleasePlan: &production.ReleaseBranchPlan{Version: "2.3.45", Preview: observed},
			}}}
			productionTestUpdate(t, window)
			d := s.confirmationDialog
			if when == "input-edited-before-read" {
				if d != nil || v.Busy || v.request != nil {
					t.Fatal("late response offered the previously typed branch")
				}
				return
			}
			if d == nil {
				t.Fatal("valid read did not offer the current branch before the context changed")
			}
			switch when {
			case "input-edited-before-confirmation":
				v.Version = "release/2.3.46"
			case "batch-changed-before-confirmation":
				v.BatchID = b.ID
			case "service-changed-before-confirmation":
				s.releaseUnit.state.production.selected = "service:another"
			}
			s.win = nil
			s.finishConfirmationDialog(d, 0)
			if s.confirmationDialog != nil || v.request != nil || v.Busy {
				t.Fatal("stale dialog dispatched a reuse")
			}
		})
	}
}
