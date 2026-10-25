package desktop

import (
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

func productionEndedDeleteFixture(t *testing.T) (*app, *productionView, production.Batch) {
	t.Helper()
	s, v, b := productionFixture(t)
	b.Schema, b.Ended, b.Phase = 1, true, "ENDED"
	b.Intent = &production.Intent{Kind: "feature", Preview: production.Preview{ProductionStatus: domain.GenbuTagProbeStatus{Build: domain.GenbuUnknown, UAT: domain.GenbuUnknown, Production: domain.GenbuUnknown}}, Merge: &production.Merge{Worktree: "C:/synthetic/retained workspace", Conflicts: []string{"unfinished.go"}}}
	s.production.batches[0] = b
	return s, v, b
}

func TestProductionDeleteMenuOnlyAppearsForEndedBatches(t *testing.T) {
	for _, state := range []string{"active", "ended", "busy", "none"} {
		t.Run(state, func(t *testing.T) {
			s, v, b := productionEndedDeleteFixture(t)
			if state == "active" {
				b.Ended, b.Phase = false, "READY"
				s.production.batches[0] = b
			}
			if state == "none" {
				s.production.batches, v.BatchID = nil, ""
			}
			v.Busy = state == "busy"
			s.win = &mygo.Window{}
			tester := productionIterationTester(s, 960, false)
			if err := tester.Click("更多生产发布操作"); err != nil {
				t.Fatal(err)
			}
			found := tester.HasText("删除批次")
			if found != (state == "ended") {
				t.Fatalf("invalid deletion availability: %s text=%v", state, tester.Texts())
			}
			if state == "ended" {
				if err := tester.Click("删除批次"); err != nil {
					t.Fatal(err)
				}
				d := s.confirmationDialog
				if d == nil || d.Title != "删除批次" || d.DefaultIndex != d.CancelIndex || !d.FoldDetail || !strings.Contains(d.Notices[0], "保留工作区") {
					t.Fatal("deletion lost its explicit local-only confirmation or safe default")
				}
				tester.Key(0, ui.KeyEscape)
				if s.confirmationDialog != nil || v.Busy {
					t.Fatal("Esc executed or retained batch deletion")
				}
			}
		})
	}
}

func TestProductionDeleteConfirmationRejectsChangedContext(t *testing.T) {
	for _, change := range []string{"home", "config", "page", "batch", "selection", "record", "recreated", "active", "busy"} {
		t.Run(change, func(t *testing.T) {
			s, v, b := productionEndedDeleteFixture(t)
			s.win = &mygo.Window{}
			s.confirmProductionDelete(v, b)
			d := s.confirmationDialog
			if d == nil || !d.Current() {
				t.Fatal("missing valid local deletion confirmation")
			}
			switch change {
			case "home":
				s.snapshot.Home = "another-home"
			case "config":
				s.configJSON = `{"tagEnabled":false}`
			case "page":
				s.page = "tasks"
			case "batch":
				v.BatchID = "another-batch"
			case "selection":
				v.Selection.ServiceID = "another-service"
			case "record":
				s.production.batches[0].UpdatedAt = "newer record"
			case "recreated":
				s.production.batches[0].CreatedAt = "recreated"
			case "active":
				s.production.batches[0].Ended = false
			case "busy":
				v.Busy = true
			}
			if d.Current() {
				t.Fatal("old deletion confirmation remained current")
			}
		})
	}
}

func TestProductionDeleteConfirmationCapturesExactLocalRecord(t *testing.T) {
	s, v, b, backend, window := productionRequestFixture(t)
	b.Ended, b.Phase = true, "ENDED"
	s.production.batches[0] = b
	s.win = &mygo.Window{}
	s.confirmProductionDelete(v, b)
	d := s.confirmationDialog
	if d == nil {
		t.Fatal("missing confirmation")
	}
	wantToken, err := production.BatchRecordToken(b)
	if err != nil {
		t.Fatal(err)
	}
	s.win = nil
	s.finishConfirmationDialog(d, 0)
	call := productionTestCall(t, backend)
	if call.Kind != "production.delete" || call.TaskID != "" || call.Payload["batchId"] != b.ID || call.Payload["recordToken"] != wantToken || call.Payload["confirmed"] != true {
		t.Fatalf("wrong delete target: %+v", call)
	}
	backend.replies <- productionTestAnswer{err: errors.New("local file is read-only")}
	productionTestUpdate(t, window)
	if len(s.production.batches) != 1 || s.production.batches[0].ID != b.ID || s.production.deletedBatches[b.ID] || !strings.Contains(v.Error, "read-only") {
		t.Fatal("failed deletion removed the record or hid its cause")
	}
}

func TestProductionDeleteResultPreservesOtherStateAndRejectsOldReports(t *testing.T) {
	for _, scenario := range []string{"last", "active-remains", "history-remains", "page", "batch", "service", "home", "late-generation"} {
		t.Run(scenario, func(t *testing.T) {
			s, v, b, backend, window := productionRequestFixture(t)
			b.Ended, b.Phase = true, "ENDED"
			s.production.batches[0] = b
			other := b
			other.ID, other.Version, other.ReleaseBranch = strings.Repeat("d", 32), "2.3.45", "release/2.3.45"
			other.Ended, other.Phase = scenario != "active-remains", "READY"
			if scenario == "service" {
				other.Target.ServiceID = "other-service"
				key := productionKey(other.Target.Selection)
				s.production.views[key] = &productionView{Selection: other.Target.Selection, BatchID: other.ID}
				s.production.choices = append(s.production.choices, productionServiceChoice{Service: application.Service{ID: other.Target.ServiceID, GroupID: other.Target.GroupID, Name: "another service"}})
			}
			if scenario != "last" && scenario != "page" && scenario != "home" && scenario != "late-generation" {
				s.production.batches = append(s.production.batches, other)
			}
			oldList := append([]production.Batch(nil), s.production.batches...)
			s.tasks = []task{{id: "current-task", title: "kept"}}
			s.notes[0] = "unsaved draft"
			s.production.timers[b.ID] = time.AfterFunc(time.Hour, func() {})
			s.production.diffDialog = &productionDiffDialog{Open: true, BatchID: b.ID}
			token, _ := production.BatchRecordToken(b)
			s.productionRequestFor(v, application.Action{Kind: "production.delete", Payload: map[string]any{"batchId": b.ID, "recordToken": token, "confirmed": true}}, "")
			productionTestCall(t, backend)
			originalSelection := s.production.selected
			switch scenario {
			case "page":
				s.page = "tasks"
			case "batch":
				v.selectProductionBatch(other.ID)
			case "service":
				s.production.selected = productionKey(other.Target.Selection)
			case "home":
				s.snapshot.Home = "another-home"
			case "late-generation":
				v.Generation++
			}
			backend.replies <- productionTestAnswer{result: application.ActionResult{Message: "本地批次记录已删除"}}
			productionTestUpdate(t, window)
			if scenario == "home" {
				if s.production.deletedBatches[b.ID] || len(s.production.batches) != len(oldList) {
					t.Fatal("old-home deletion changed the current data scope")
				}
				return
			}
			if !s.production.deletedBatches[b.ID] || s.production.timers[b.ID] != nil || s.production.diffDialog != nil {
				t.Fatal("deletion left an old timer/diff or failed to mark the removed record")
			}
			if s.currentTask().id != "current-task" || s.notes[0] != "unsaved draft" || len(backend.calls) != 0 {
				t.Fatal("local deletion changed task state or triggered an external follow-up")
			}
			if scenario == "service" && s.production.selected != productionKey(other.Target.Selection) || scenario != "service" && s.production.selected != originalSelection {
				t.Fatal("deletion changed the selected service")
			}
			if scenario == "service" && s.productionView().BatchID != other.ID {
				t.Fatal("deletion changed another service's selected batch")
			}
			if scenario == "active-remains" || scenario == "batch" {
				if v.BatchID != other.ID {
					t.Fatal("remaining/current batch was not retained")
				}
			} else if v.BatchID != "" {
				t.Fatal("last deleted batch retained its selection")
			}
			s.production.mergeBatch(b)
			s.production.replaceBatches(oldList)
			for _, record := range s.production.batches {
				if record.ID == b.ID {
					t.Fatal("late list/status result restored a deleted batch")
				}
			}
		})
	}
}

func TestProductionDeleteConfirmationLayouts(t *testing.T) {
	for _, width := range []int{960, 1440} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%t", width, dark), func(t *testing.T) {
				s, v, b := productionEndedDeleteFixture(t)
				s.win = &mygo.Window{}
				s.confirmProductionDelete(v, b)
				tester := productionIterationTester(s, width, dark)
				if !tester.HasText("仅删除本地批次记录，保留工作区及其中的修改、远端分支和 Tag。") || !tester.HasText("取消") || !tester.Focused("取消") {
					t.Fatal("local-only scope or safe keyboard focus is missing")
				}
				captureProductionSummary(t, tester, fmt.Sprintf("delete-confirm-%d-dark-%t", width, dark))
				tester.Key(0, ui.KeyEscape)
				if err := tester.Click("更多生产发布操作"); err != nil {
					t.Fatal(err)
				}
				captureProductionSummary(t, tester, fmt.Sprintf("delete-menu-%d-dark-%t", width, dark))
			})
		}
	}
}

func TestProductionDeletePrunesOnlyOrphanHistoricalServices(t *testing.T) {
	for _, remaining := range []bool{false, true} {
		t.Run(fmt.Sprint(remaining), func(t *testing.T) {
			s, v, b := productionEndedDeleteFixture(t)
			s.production.choices[0].Historical = true
			originalKey := s.production.selected
			otherSelection := production.Selection{GroupID: "another-group", ServiceID: "another-service"}
			otherKey := productionKey(otherSelection)
			s.production.choices = append(s.production.choices, productionServiceChoice{Service: application.Service{ID: otherSelection.ServiceID, GroupID: otherSelection.GroupID, Name: "configured service"}})
			s.production.views[otherKey] = &productionView{Selection: otherSelection}
			if remaining {
				other := b
				other.ID = strings.Repeat("f", 32)
				s.production.batches = append(s.production.batches, other)
			}
			s.forgetProductionBatch(s.production, b.ID)
			if remaining {
				if len(s.production.choices) != 2 || s.production.selected != originalKey || s.production.views[originalKey] != v {
					t.Fatal("a historical service with another record was removed")
				}
			} else if len(s.production.choices) != 1 || s.production.selected != otherKey || s.production.views[originalKey] != nil {
				t.Fatal("orphan historical service was retained or a configured service was removed")
			}
		})
	}
}

func TestProductionDeleteTombstonesAreScopedToTheDataDirectory(t *testing.T) {
	s, _, b := productionEndedDeleteFixture(t)
	s.forgetProductionBatch(s.production, b.ID)
	s.snapshot.Home = t.TempDir()
	s.enterProductionPage() // No backend is attached; this only changes local state.
	if len(s.production.deletedBatches) != 0 {
		t.Fatal("old data-directory deletions leaked into a new scope")
	}
	s.production.mergeBatch(b)
	if len(s.production.batches) != 1 {
		t.Fatal("an independent directory's record was hidden by an old deletion")
	}
}
