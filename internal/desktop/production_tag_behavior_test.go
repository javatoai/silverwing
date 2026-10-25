package desktop

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/production"

	"github.com/egoist/mygo/ui"
)

func TestProductionCanceledBuildReadCannotPublishQueuedSuccess(t *testing.T) {
	t.Run("prepare", func(t *testing.T) {
		s, v, b, backend, window := productionRequestFixture(t)
		p := *v.Preview
		p.MainInRelease = true
		s.productionInspect("build-preview")
		productionTestCall(t, backend)
		b.TagPlan = &production.TagPlan{Token: "canceled-plan", Tag: "2.3.44", Commit: b.ReleaseSHA, MainSHA: p.MainSHA, Preview: p}
		worker := v.request
		backend.replies <- productionTestAnswer{result: application.ActionResult{Data: production.Report{Batch: &b, Preview: &p}}}
		// Cancel after the backend succeeded but before its queued UI update.
		// Worker cleanup also cancels its context, so only explicit cancellation
		// may prevent a successful foreground build from continuing.
		select {
		case apply := <-window.updates:
			worker.cancel()
			apply()
		case <-time.After(time.Second):
			t.Fatal("missing queued build result")
		}
		if v.Busy || v.Error == "" || s.confirmationDialog != nil || len(backend.calls) != 0 || v.ActiveAction == "production.publish" {
			t.Fatal("canceled queued success continued the build")
		}
		s.productionInspect("build-preview")
		if call := productionTestCall(t, backend); call.Kind != "production.prepare-tag" {
			t.Fatal("a retry replayed the canceled preparation")
		}
		backend.replies <- productionTestAnswer{err: errors.New("finish retry")}
		productionTestUpdate(t, window)
	})
}

func TestProductionSavedTagPlanAndRefreshNeverPublishAutomatically(t *testing.T) {
	for _, action := range []string{"production.inspect", "production.prepare-tag", "production.sync", "production.refresh-build", "production.list"} {
		t.Run(action, func(t *testing.T) {
			s, v, b, backend, window := productionRequestFixture(t)
			v.Preview.MainInRelease = true
			b.TagPlan = &production.TagPlan{Token: "saved-plan", Tag: "2.3.44", Commit: b.ReleaseSHA, MainSHA: v.Preview.MainSHA, Preview: *v.Preview}
			b.Phase = "TAG_READY"
			s.production.batches[0] = b
			tester := productionIterationTester(s, 960, false)
			tester.Frame()
			if tester.HasText("等待构建确认") || !tester.HasText("准备就绪") {
				t.Fatal("restored preparation still asks for the removed confirmation")
			}
			if v.Busy || len(backend.calls) != 0 || s.confirmationDialog != nil {
				t.Fatal("displaying a restored plan started a build")
			}
			s.productionRequestFor(v, application.Action{Kind: action, Payload: map[string]any{"batchId": b.ID}}, "")
			if call := productionTestCall(t, backend); call.Kind != action {
				t.Fatal("refresh dispatched a write")
			}
			backend.replies <- productionTestAnswer{result: application.ActionResult{Data: production.Report{Batch: &b, Batches: []production.Batch{b}, Preview: v.Preview}}}
			productionTestUpdate(t, window)
			if v.Busy || len(backend.calls) != 0 || s.confirmationDialog != nil {
				t.Fatal("refresh continued into publishing or confirmation")
			}
		})
	}
}

func TestProductionBuildWithoutFeaturePreservesOperationGuards(t *testing.T) {
	for _, state := range []string{"ready", "unchanged", "not-created", "busy", "conflict", "aborting", "pending", "ended"} {
		t.Run(state, func(t *testing.T) {
			s, v, b := productionFixture(t)
			b.Branches, b.ReleaseSHA = nil, b.InitialSHA
			switch state {
			case "unchanged":
				b.Tags = []production.Tag{{Name: "2.3.44", Commit: b.ReleaseSHA}}
			case "not-created":
				b.InitialSHA = ""
			case "busy":
				v.Busy = true
			case "conflict", "aborting":
				b = productionConflictBatch(b, "feature")
				if state == "aborting" {
					b.Phase = "ABORTING"
				}
			case "pending":
				b.Intent = &production.Intent{Kind: "tag", Step: "tag-push"}
			case "ended":
				b.Ended = true
			}
			m := s.productionReleaseModel(v, &b)
			if m.CanBuild != (state == "ready" || state == "unchanged") {
				t.Fatalf("wrong build availability: %+v", m)
			}
		})
	}
}

func TestProductionUnchangedTagDiffUsesEmptyState(t *testing.T) {
	for _, width := range []int{960, 1440} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%t", width, dark), func(t *testing.T) {
				s, v, b := productionFixture(t)
				b.Branches, b.ReleaseSHA = nil, b.InitialSHA
				v.Preview.ReleaseSHA = b.ReleaseSHA
				b.TagPlan = &production.TagPlan{Token: "empty-diff", Tag: "2.3.44", Commit: b.ReleaseSHA, MainSHA: v.Preview.MainSHA, Preview: *v.Preview}
				s.production.batches[0] = b
				tester := productionIterationTester(s, width, dark)
				if !tester.HasText("无差异") || tester.HasText("请先合并至少一个有新增提交的分支。") || tester.HasText("没有新增提交，无需构建；可继续合并分支。") {
					t.Fatal("new Tag flow still displays the old prerequisite")
				}
				captureProductionSummary(t, tester, fmt.Sprintf("unchanged-tag-%d-dark-%t", width, dark))
				s.openProductionDiff(v, "本次构建差异", b.ReleaseBranch, "release 初始提交", b.ReleaseSHA, b.InitialSHA, false)
				d := s.production.diffDialog
				d.Review.TargetCommit, d.Review.BaseCommit = b.ReleaseSHA, b.InitialSHA
				tester.Frame()
				if !tester.HasText("无差异") || tester.HasText("选择文件查看变更") {
					t.Fatal("verified empty diff still asks to select a nonexistent file")
				}
				captureProductionSummary(t, tester, fmt.Sprintf("unchanged-tag-diff-%d-dark-%t", width, dark))
				modal := ui.NewTester(func(c *ui.Context) { s.renderProductionDiffDialog(c, setTheme(c, dark)) }, width, 900)
				if !modal.HasText("无差异") {
					t.Fatal("empty diff dialog did not explain its empty file list")
				}
				// A failed or unfinished read must not be presented as no differences.
				d.Review.Error = "读取失败"
				modal.Frame()
				if !modal.HasText("读取失败") || modal.HasText("无差异") {
					t.Fatal("empty state hid the read failure")
				}
				d.Review.Error, d.Review.FilesLoading = "", true
				modal.Frame()
				if modal.HasText("无差异") {
					t.Fatal("unfinished diff read displayed a successful empty result")
				}
			})
		}
	}
}
