package desktop

import (
	"context"
	"errors"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/production"

	"github.com/egoist/mygo/ui"
)

func productionIterationTester(s *app, width int, dark bool) *ui.Tester {
	return ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, dark)
		ui.MenuScope(c, s.popupMenuScope())
		ui.Row(c).Fill().Background(p.bg).AlignItems(ui.Stretch).Children(func() { s.renderProductionPage(c, p) })
		s.renderProductionBaselineDialog(c, p)
		s.renderProductionDiffDialog(c, p)
		s.renderProductionWorktreesDialog(c, p)
		s.renderProductionHistoryDialog(c, p)
		s.renderConfirmationDialog(c, p)
		ui.ThemedMenus(c, s.popupMenuScope())
	}, width, 900)
}

func TestProductionCompactServiceLabelsDoNotChangeWithSearch(t *testing.T) {
	s, _, _ := productionFixture(t)
	tester := productionIterationTester(s, 960, false)
	if !tester.HasText("示例服务") || tester.HasText("国内组 · 示例服务") || tester.HasText("origin/master · C:/synthetic/repo-one") {
		t.Fatal("single-group row was not distilled")
	}
	other := productionServiceChoice{Service: application.Service{ID: "other", GroupID: "other", Name: "国外服务"}, GroupName: "海外组"}
	s.production.choices = append(s.production.choices, other)
	*s.input("production-service-query", "") = "国内组"
	tester = productionIterationTester(s, 960, false)
	if !tester.HasText("国内组 · 示例服务") || tester.HasText("海外组 · 国外服务") {
		t.Fatal("filter changed group-count presentation or did not filter")
	}
}

func TestProductionBaselineMoreAndDialogCancelLeaveInputUnchanged(t *testing.T) {
	s, v, _ := productionFixture(t)
	tester := productionIterationTester(s, 960, false)
	if tester.HasText("手动确认生产 Tag") || tester.HasText("生产基线 Tag") {
		t.Fatal("manual baseline still permanently visible")
	}
	if err := tester.Click("更多生产发布操作"); err != nil {
		t.Fatal(err)
	}
	if !tester.HasText("手动确认生产 Tag") {
		t.Fatal("more menu did not expose manual baseline")
	}
	if err := tester.Click("手动确认生产 Tag"); err != nil {
		t.Fatal(err)
	}
	d := s.production.baselineDialog
	if d == nil || !tester.HasText("应用并重新检查") {
		t.Fatal("no native baseline dialog")
	}
	d.Tag = "typed-but-canceled"
	tester.Key(0, ui.KeyEscape)
	if d.Open || v.ManualTag != "" || v.Preview == nil {
		t.Fatal("Esc applied manual baseline")
	}
	s.openProductionBaseline(v)
	d = s.production.baselineDialog
	d.Tag = "another"
	s.page = "tasks"
	s.applyProductionBaseline(d, false)
	if v.ManualTag != "" {
		t.Fatal("stale dialog applied after page change")
	}
}

func TestProductionBaselineApplyAndAutomaticUseInspectWithoutWriting(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		t.Run(fmt.Sprint(automatic), func(t *testing.T) {
			s, v, _ := productionFixture(t)
			backend := &productionTestBackend{calls: make(chan application.Action, 1), replies: make(chan productionTestAnswer, 1)}
			window := &refreshTestWindow{updates: make(chan func(), 8)}
			s.backend, s.production.window = backend, window
			v.ManualTag = "old"
			s.openProductionBaseline(v)
			d := s.production.baselineDialog
			d.Tag = "  2.3.43  "
			s.applyProductionBaseline(d, automatic)
			select {
			case action := <-backend.calls:
				expected := "2.3.43"
				if automatic {
					expected = ""
				}
				if action.Kind != "production.inspect" || action.Payload["productionTag"] != expected || v.ManualTag != expected {
					t.Fatalf("wrong baseline action: %+v", action)
				}
			case <-time.After(time.Second):
				t.Fatal("missing read request")
			}
			if d.Open || !v.PreparationInvalid {
				t.Fatal("dialog did not close/invalidate old preparation")
			}
			backend.replies <- productionTestAnswer{result: application.ActionResult{Data: production.Report{}}}
			(<-window.updates)()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := s.closeProductionJobs(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestProductionRepeatedControlsRemainAfterTagsAndFailedBuild(t *testing.T) {
	s, v, b, backend, window := productionRequestFixture(t)
	b.Tags = []production.Tag{{Name: "2.3.44", Commit: b.ReleaseSHA, Status: domain.GenbuTagProbeStatus{Build: domain.GenbuBuilding}}}
	s.production.batches[0] = b
	for i := 0; i < 3; i++ {
		tester := productionIterationTester(s, 960, false)
		for _, control := range []string{"合并到 release", "构建 Tag"} {
			if !tester.HasText(control) {
				t.Fatalf("cycle %d hid %s", i, control)
			}
		}
		v.Preview.ReleaseSHA = strings.Repeat("f", 40)
		b.ReleaseSHA = v.Preview.ReleaseSHA
		b.TagPlan = &production.TagPlan{Token: fmt.Sprint(i), Tag: fmt.Sprintf("2.3.44.%d", i+1), Commit: b.ReleaseSHA, MainSHA: v.Preview.MainSHA, Preview: *v.Preview}
		s.production.batches[0] = b
		v.PreparationInvalid = true
		s.publishProductionTag(v, b)
		if s.confirmationDialog != nil || len(backend.calls) != 0 || v.Busy {
			t.Fatal("stale preparation opened a dialog or started publishing")
		}
		v.PreparationInvalid = false
		s.publishProductionTag(v, b)
		if call := productionTestCall(t, backend); call.Kind != "production.publish" || call.Payload["token"] != b.TagPlan.Token || s.confirmationDialog != nil {
			t.Fatalf("valid fresh preparation did not publish directly: %+v", call)
		}
		backend.replies <- productionTestAnswer{err: errors.New("synthetic push failure")}
		productionTestUpdate(t, window)
		if s.productionBatch(v).Ended || !v.PreparationInvalid || v.Busy {
			t.Fatal("failure ended the batch or retained a valid preparation")
		}
		b.TagPlan = nil
		v.PreparationInvalid = false
		s.production.batches[0] = b
	}
}

type productionProgressBackend struct {
	*productionTestBackend
	contexts chan context.Context
}

func (b *productionProgressBackend) Dispatch(ctx context.Context, action application.Action) (application.ActionResult, error) {
	b.contexts <- ctx
	return b.productionTestBackend.Dispatch(ctx, action)
}

func TestProductionProgressBoundToRequestBatchAndApplicationContext(t *testing.T) {
	for _, change := range []string{"none", "batch", "config", "home", "generation", "input", "wrong-target", "wrong-batch", "page", "state-reset"} {
		t.Run(change, func(t *testing.T) {
			s, v, b := productionFixture(t)
			backend := &productionProgressBackend{productionTestBackend: &productionTestBackend{calls: make(chan application.Action, 1), replies: make(chan productionTestAnswer, 1)}, contexts: make(chan context.Context, 1)}
			window := &refreshTestWindow{updates: make(chan func(), 8)}
			s.backend, s.production.window = backend, window
			s.productionInspect("")
			ctx := <-backend.contexts
			<-backend.calls
			event := production.Progress{Selection: v.Selection, BatchID: b.ID, Step: 1, State: "running", Message: "new progress"}
			if change == "wrong-target" {
				event.Selection.ServiceID = "another"
			}
			if change == "wrong-batch" {
				event.BatchID = "another"
			}
			production.EmitProgress(ctx, event)
			switch change {
			case "batch":
				v.BatchID = "another"
			case "config":
				s.configJSON = `{"tagEnabled":false}`
			case "home":
				s.snapshot.Home = "changed"
			case "generation":
				v.Generation++
			case "input":
				v.ManualTag = "changed"
			case "page":
				s.page = "tasks"
			case "state-reset":
				v.Steps = nil
			}
			(<-window.updates)()
			accepted := v.Steps[1].Message == "new progress"
			if accepted != (change == "none" || change == "page") {
				t.Fatalf("unexpected stale progress acceptance: %t", accepted)
			}
			backend.replies <- productionTestAnswer{result: application.ActionResult{Data: production.Report{}}}
			(<-window.updates)()
			closing, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := s.closeProductionJobs(closing); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestProductionIterationEvidenceStates(t *testing.T) {
	for _, width := range []int{960, 1440} {
		for _, dark := range []bool{false, true} {
			for _, state := range []string{"prepared", "building", "manual"} {
				t.Run(fmt.Sprintf("%d-%t-%s", width, dark, state), func(t *testing.T) {
					s, v, b := productionFixture(t)
					if state == "prepared" {
						b.TagPlan = &production.TagPlan{Tag: "2.3.44.1", Commit: b.ReleaseSHA, MainSHA: v.Preview.MainSHA, Preview: *v.Preview}
					}
					if state == "building" {
						b.Tags = []production.Tag{{Name: "2.3.44", Commit: b.ReleaseSHA, Status: domain.GenbuTagProbeStatus{Build: domain.GenbuBuilding}}}
					}
					s.production.batches[0] = b
					if state == "manual" {
						s.openProductionBaseline(v)
					}
					tester := productionIterationTester(s, width, dark)
					for _, control := range []string{"合并到 release", "构建 Tag"} {
						if !tester.HasText(control) {
							t.Fatalf("missing repeat action %s", control)
						}
					}
					if dir := os.Getenv("SILVERWING_PRODUCTION_EVIDENCE_DIR"); dir != "" {
						if err := os.MkdirAll(dir, 0700); err != nil {
							t.Fatal(err)
						}
						file, err := os.Create(filepath.Join(dir, fmt.Sprintf("iteration-%d-dark-%t-%s.png", width, dark, state)))
						if err != nil {
							t.Fatal(err)
						}
						err = png.Encode(file, tester.Image())
						closeErr := file.Close()
						if err != nil || closeErr != nil {
							t.Fatalf("capture: %v %v", err, closeErr)
						}
					}
				})
			}
		}
	}
}

func TestProductionManyMergesRemainFlatAndActionsReachable(t *testing.T) {
	for _, width := range []int{960, 1440} {
		for _, dark := range []bool{false, true} {
			s, v, b := productionFixture(t)
			b.TagPlan = &production.TagPlan{Tag: "2.3.44.1", Commit: b.ReleaseSHA, MainSHA: v.Preview.MainSHA, Preview: *v.Preview}
			s.production.batches[0] = b
			b.Branches = nil
			for i := 0; i < 120; i++ {
				b.Branches = append(b.Branches, production.MergedBranch{Branch: fmt.Sprintf("feature/iteration-%03d", i), SourceSHA: strings.Repeat("d", 40)})
			}
			s.production.batches[0] = b
			tester := productionIterationTester(s, width, dark)
			tester.SetSize(width, 5000)
			if !tester.HasText("已合并：feature/iteration-119") || !tester.HasText("已合并：feature/iteration-000") || tester.HasText("合并记录 ·") {
				t.Fatal("successful merge history is still collapsed or truncated")
			}
			for _, control := range []string{"合并到 release", "构建 Tag"} {
				rect, ok := tester.Find(control)
				if !ok || rect.X+rect.W > float32(width) {
					t.Fatalf("history displaced %s: %+v", control, rect)
				}
			}
			tester.SetSize(width, 900)
			tester.Scroll(float32(width-140), 500, 0, 6000)
			for _, control := range []string{"合并到 release", "构建 Tag"} {
				rect, ok := tester.Find(control)
				if !ok || rect.Y < 0 || rect.Y+rect.H > 900 {
					t.Fatalf("history made an action unreachable by scrolling: %s %+v", control, rect)
				}
			}
			if directory := os.Getenv("SILVERWING_PRODUCTION_EVIDENCE_DIR"); directory != "" {
				if err := os.MkdirAll(directory, 0700); err != nil {
					t.Fatal(err)
				}
				file, err := os.Create(filepath.Join(directory, fmt.Sprintf("iteration-%d-dark-%t-many-merges.png", width, dark)))
				if err != nil {
					t.Fatal(err)
				}
				err = png.Encode(file, tester.Image())
				closeErr := file.Close()
				if err != nil || closeErr != nil {
					t.Fatalf("capture: %v %v", err, closeErr)
				}
			}
		}
	}
}
