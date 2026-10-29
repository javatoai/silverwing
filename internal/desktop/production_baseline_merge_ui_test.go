package desktop

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/production"
	"silverwing-go/internal/workspace"
)

func TestProductionNoDiffButtonIsDisabled(t *testing.T) {
	for _, kind := range []string{"same-commit", "included", "commit-only", "release-report-only"} {
		t.Run(kind, func(t *testing.T) {
			s, v, _ := productionFixture(t)
			v.Branch = ""
			if kind != "same-commit" {
				v.Preview.ProductionSHA = strings.Repeat("e", 40)
			}
			v.Preview.ProductionInMain = kind == "same-commit" || kind == "included"
			if kind == "release-report-only" {
				v.Preview.Diff = "\n\n远端 release 相对本地批次的文件变化：\n changed.go | 1 +"
			}
			tester := productionIterationTester(s, 960, false)
			if err := tester.Click("无差异"); err != nil {
				t.Fatal(err)
			}
			if s.releaseUnit.state.production.diffDialog != nil {
				t.Fatal("no-file-change button opened an empty dialog")
			}
			for i := 0; i < 24; i++ {
				tester.Key(0, ui.KeyTab)
				if tester.Focused("无差异") {
					t.Fatal("disabled no-diff button is still a keyboard stop")
				}
			}
			if !v.Preview.ProductionInMain {
				if _, ok := tester.Find("合并生产 Tag 到 master"); !ok {
					t.Fatal("empty file diff incorrectly removed required commit merge")
				}
			}
		})
	}
}

func TestProductionUnifiedBaselineStatusUsesCurrentEvidence(t *testing.T) {
	for _, tc := range []struct{ name, text, state string }{
		{"unknown", "待检查", "pending"},
		{"needs-merge", "待合并", "pending"},
		{"included", "已完成", "skipped"},
		{"inspect-busy", "检查中…", "running"},
		{"merge-busy", "进行中…", "running"},
		{"inspect-failed", "需要处理", "failed"},
		{"inspect-failed-with-conflict", "需要处理", "failed"},
		{"conflict", "等待解决冲突", "warning"},
		{"abort-pending", "待核对撤回", "warning"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, v, b := productionFixture(t)
			v.Branch = ""
			v.Steps = map[int]productionStepView{1: {State: "complete"}, 2: {State: "complete"}}
			switch tc.name {
			case "unknown":
				v.Preview = nil
			case "needs-merge":
				v.Preview.ProductionSHA, v.Preview.ProductionInMain = strings.Repeat("e", 40), false
			case "inspect-busy":
				v.Busy, v.ActiveStep, v.ActiveAction = true, 1, "production.inspect"
			case "merge-busy":
				v.Busy, v.ActiveStep, v.ActiveAction = true, 2, "production.merge-main"
			case "inspect-failed":
				v.ActiveStep, v.Error = 1, "fixture inspect failed"
			case "inspect-failed-with-conflict":
				b = productionConflictBatch(b, "main")
				v.ActiveStep, v.Error = 1, "fixture inspect failed"
			case "conflict", "abort-pending":
				b = productionConflictBatch(b, "main")
				if tc.name == "abort-pending" {
					b.Phase = "ABORTING"
				}
			}
			s.releaseUnit.state.production.batches[0] = b
			status := s.productionBaselineStatus(v, &b)
			if status.Text != tc.text || status.State != tc.state {
				t.Fatalf("status=%+v, want %s / %s", status, tc.text, tc.state)
			}
			if tc.name == "included" && !strings.Contains(status.Tooltip, v.Preview.ProductionTag) {
				t.Fatal("inclusion relationship missing from tooltip")
			}
			tester := productionCardsTester(s, 960, false)
			for _, old := range []string{"对比生产 Tag 与主分支差异卡片", "合并生产 Tag 到主分支卡片"} {
				if tester.HasText(old) {
					t.Fatal("old baseline cards remain")
				}
			}
			if _, ok := tester.Find("生产 Tag → 主分支卡片"); !ok {
				t.Fatal("merged baseline card missing")
			}
			if strings.HasPrefix(tc.name, "inspect-failed") {
				if !tester.HasText(v.Error) || tester.HasText("无差异") {
					t.Fatal("failed read was disguised as an empty diff")
				}
				if err := tester.Click("查看差异"); err != nil || s.releaseUnit.state.production.diffDialog != nil {
					t.Fatal("failed comparison remained actionable")
				}
			}
		})
	}
}

func TestProductionMergeHistoryWaitsForSuccessfulSavedReport(t *testing.T) {
	s, v, b, backend, window := productionRequestFixture(t)
	failed := b
	failed.Branches = append(append([]production.MergedBranch{}, b.Branches...), production.MergedBranch{Branch: "feature/unsaved", Commit: strings.Repeat("e", 40)})
	s.productionRequest(application.Action{Kind: "production.merge", Payload: map[string]any{"batchId": b.ID}}, "")
	productionTestCall(t, backend)
	backend.replies <- productionTestAnswer{result: application.ActionResult{Data: production.Report{Batch: &failed}}, err: errors.New("fixture record save failed")}
	productionTestUpdate(t, window)
	tester := productionCardsTester(s, 960, false)
	if tester.HasText("已合并：feature/unsaved") || !tester.HasText("已合并：feature/synthetic") || v.Error == "" {
		t.Fatal("unconfirmed failed result became successful merge history")
	}
	// A later successful disk snapshot is authoritative even if the original
	// command's acknowledgement was lost. No new merge or push is initiated.
	s.productionRequest(application.Action{Kind: "production.list"}, "")
	if call := productionTestCall(t, backend); call.Kind != "production.list" {
		t.Fatal("recovery issued a write")
	}
	backend.replies <- productionTestAnswer{result: application.ActionResult{Data: production.Report{Batches: []production.Batch{failed}}}}
	productionTestUpdate(t, window)
	tester.Frame()
	if !tester.HasText("已合并：feature/unsaved") || len(s.releaseUnit.state.production.batches[0].Branches) != 2 {
		t.Fatal("persisted snapshot did not restore confirmed full history")
	}
}

func TestProductionMergedBranchesAreFlatUniqueAndScoped(t *testing.T) {
	for _, width := range []int{960, 1440} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%t", width, dark), func(t *testing.T) {
				s, v, b := productionFixture(t)
				long := "feature/" + strings.Repeat("long-branch-name-", 12)
				b.Branches = []production.MergedBranch{{Branch: "feature/one"}, {Branch: long}, {Branch: "feature/two"}, {Branch: "feature/one"}}
				s.releaseUnit.state.production.batches[0] = b
				tester := productionCardsTester(s, width, dark)
				previous := float32(-1)
				for _, name := range []string{"feature/one", "feature/two", long} {
					text := "已合并：" + name
					rect, ok := tester.Find(text)
					if !ok || rect.Y <= previous || rect.X+rect.W > float32(width) || rect.H <= 0 {
						t.Fatalf("flat record missing or incorrectly ordered: %q %+v", text, rect)
					}
					previous = rect.Y
					count := 0
					for _, label := range tester.Texts() {
						if label == text {
							count++
						}
					}
					if count != 1 {
						t.Fatalf("branch repeated %d times: %q", count, name)
					}
				}
				if tester.HasText("合入到") || tester.HasText("合并记录 ·") {
					t.Fatal("removed target row or collapsed history remains")
				}
				captureProductionSummary(t, tester, fmt.Sprintf("flat-merged-branches-%d-dark-%t", width, dark))
				// Changing the pending source, or its result, never rewrites the
				// persisted successful history of the batch.
				v.Branch, v.ActiveStep, v.Error = "feature/not-merged", 4, "fixture merge failed"
				tester.Frame()
				if tester.HasText("已合并：feature/not-merged") || !tester.HasText("已合并：feature/one") {
					t.Fatal("failed selection became a success record")
				}
				other := b
				other.ID, other.ReleaseBranch, other.Branches = "other-batch", "release/other", []production.MergedBranch{{Branch: "feature/other"}}
				s.releaseUnit.state.production.batches = append(s.releaseUnit.state.production.batches, other)
				v.selectProductionBatch(other.ID)
				tester.Frame()
				if tester.HasText("已合并：feature/one") || !tester.HasText("已合并：feature/other") {
					t.Fatal("previous batch records leaked after switching")
				}
				s.releaseUnit.state.production.selected = "missing-service"
				tester.Frame()
				if tester.HasText("已合并：") {
					t.Fatal("previous service records leaked after switching")
				}
			})
		}
	}
}

func TestProductionBaselineDiffFullPageLoadingErrorAndRetry(t *testing.T) {
	s, v, _, backend, window := productionRequestFixture(t)
	v.Preview.ProductionSHA, v.Preview.ProductionInMain = strings.Repeat("f", 40), false
	v.Preview.Diff = " changed.go | 1 +\n 1 file changed, 1 insertion(+)"
	v.Branch = ""
	s.taskUnit.state.notes[0] = "unsaved draft"
	// Exercise the application view's modal placement, not only the dialog.
	tester := ui.NewTester(func(c *ui.Context) { s.view(c) }, 1440, 1000)
	if err := tester.Click("查看差异"); err != nil {
		t.Fatal(err)
	}
	d := s.releaseUnit.state.production.diffDialog
	if d == nil || !d.Review.FilesLoading || !tester.HasText("源：") || !tester.HasText("目标：") {
		t.Fatal("click gave no immediate visible loading dialog")
	}
	action := productionTestCall(t, backend)
	if action.Kind != "production.diff-files" || v.Busy {
		t.Fatal("file read entered write flow")
	}
	backend.replies <- productionTestAnswer{err: errors.New("fixture diff read failed")}
	productionTestUpdate(t, window)
	tester.Frame()
	if !tester.HasText("fixture diff read failed") {
		t.Fatal("diff failure was silent")
	}
	if err := tester.Click("重试"); err != nil {
		t.Fatal(err)
	}
	action = productionTestCall(t, backend)
	if action.Kind != "production.diff-files" || action.Payload["sourceSha"] != d.SourceSHA || action.Payload["targetSha"] != d.TargetSHA {
		t.Fatal("retry changed fixed commits")
	}
	backend.replies <- productionTestAnswer{result: productionDiffEmptyAnswer(d.SourceSHA, d.TargetSHA)}
	productionTestUpdate(t, window)
	tester.Frame()
	if !tester.HasText("无差异") || d.Review.Error != "" {
		t.Fatal("successful empty read did not settle")
	}
	tester.Key(0, ui.KeyEscape)
	if s.releaseUnit.state.production.diffDialog != nil || s.taskUnit.state.notes[0] != "unsaved draft" {
		t.Fatal("closing diff changed the draft or left a modal")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.releaseUnit.closeProductionJobs(ctx); err != nil {
		t.Fatal(err)
	}
}

func productionDiffEmptyAnswer(source, target string) application.ActionResult {
	return application.ActionResult{Data: production.DiffResult{Files: workspace.CompareFilesResult{BaseCommit: target, TargetCommit: source}}}
}
