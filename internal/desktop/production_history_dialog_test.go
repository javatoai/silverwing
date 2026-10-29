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

func productionHistoryRecord(b production.Batch, id, branch string, created time.Time) production.Batch {
	b.ID, b.ReleaseBranch, b.CreatedAt = id, branch, created.UTC().Format(time.RFC3339Nano)
	b.Ended, b.Phase = true, "ENDED"
	b.Tags = []production.Tag{{Name: "2.3.42", PushedAt: b.CreatedAt, Status: domain.GenbuTagProbeStatus{Build: domain.GenbuSuccess}}}
	return b
}

func openProductionHistoryTest(t *testing.T, s *app, v *productionView, backend *productionTestBackend, window *refreshTestWindow) *productionHistoryDialog {
	t.Helper()
	s.openProductionHistory(v)
	call := productionTestCall(t, backend)
	if call.Kind != "production.list" || len(call.Payload) != 0 {
		t.Fatalf("history opened an external action: %+v", call)
	}
	backend.replies <- productionTestAnswer{result: application.ActionResult{Data: production.Report{Batches: append([]production.Batch(nil), s.releaseUnit.state.production.batches...)}}}
	productionTestUpdate(t, window)
	return s.releaseUnit.state.production.historyDialog
}

func TestProductionHistoryListLocalReadFailureRetryAndScope(t *testing.T) {
	s, v, b, backend, window := productionRequestFixture(t)
	older := productionHistoryRecord(b, "older", "release/older", time.Now().Add(-time.Hour))
	newer := productionHistoryRecord(b, "newer", "release/newer", time.Now())
	other := newer
	other.ID, other.Target.ServiceID = "other", "other-service"
	s.releaseUnit.state.production.batches = []production.Batch{older, b, other, newer}
	v.Error, v.Message, v.ActiveStep = "kept error", "kept result", 4
	v.Steps = map[int]productionStepView{4: {State: "complete", Message: "kept step"}}
	preview, branch, version := v.Preview, v.Branch, v.Version
	d := openProductionHistoryTest(t, s, v, backend, window)
	list := s.productionHistoryRecords(d)
	if len(list) != 2 || list[0].ID != newer.ID || list[1].ID != older.ID {
		t.Fatalf("list scope/order wrong: %+v", list)
	}
	if v.BatchID != b.ID || v.Branch != branch || v.Version != version || v.Preview != preview || v.Error != "kept error" || v.Message != "kept result" || v.ActiveStep != 4 || v.Steps[4].Message != "kept step" || v.Busy || len(backend.calls) != 0 {
		t.Fatal("opening history changed live operation state or triggered a follow-up")
	}
	s.readProductionHistory(d)
	productionTestCall(t, backend)
	backend.replies <- productionTestAnswer{err: errors.New("local history unreadable")}
	productionTestUpdate(t, window)
	if d.Error != "local history unreadable" || len(s.releaseUnit.state.production.batches) != 4 || v.Error != "kept error" {
		t.Fatal("list read failure destroyed the last valid snapshot or main feedback")
	}
	tester := productionIterationTester(s, 960, false)
	if !tester.HasText("历史批次读取失败：local history unreadable") {
		t.Fatal("missing read failure")
	}
	if err := tester.Click("重试"); err != nil {
		t.Fatal(err)
	}
	productionTestCall(t, backend)
	backend.replies <- productionTestAnswer{result: application.ActionResult{Data: production.Report{}}}
	productionTestUpdate(t, window)
	tester.Frame()
	if !tester.HasText("暂无历史批次") || v.Error != "kept error" || len(backend.calls) != 0 {
		t.Fatal("retry failed or ran an external follow-up")
	}
}

func TestProductionHistoryViewReturnPreservesInputsAndInvalidatesPreparation(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(fmt.Sprint(active), func(t *testing.T) {
			s, v, b := productionFixture(t)
			old := productionHistoryRecord(b, "old", "release/old", time.Now())
			other := productionHistoryRecord(b, "other", "release/other", time.Now().Add(-time.Hour))
			s.releaseUnit.state.production.batches = []production.Batch{old, other}
			if active {
				s.releaseUnit.state.production.batches = append(s.releaseUnit.state.production.batches, b)
			} else {
				v.BatchID = ""
			}
			v.Version, v.Branch, v.BranchQuery, v.SuggestedBranch, v.ManualTag = "release/manually-typed", "feature/typed", "query", "suggestion", "2.3.43"
			preview := v.Preview
			s.openProductionHistory(v)
			d := s.releaseUnit.state.production.historyDialog
			s.viewProductionHistory(d, old)
			if d.Open || v.BatchID != old.ID || v.Preview != nil || !v.PreparationInvalid {
				t.Fatal("view did not isolate history")
			}
			tester := productionIterationTester(s, 960, false)
			wantReturn := "返回发布操作"
			if active {
				wantReturn = "返回当前批次"
			}
			if !tester.HasText(wantReturn) || !tester.HasText("已合并：feature/synthetic") {
				t.Fatal("missing return or ended merge record")
			}
			s.openProductionHistory(v)
			d = s.releaseUnit.state.production.historyDialog
			if len(s.productionHistoryRecords(d)) != 2 {
				t.Fatal("current historical row was excluded")
			}
			tester.Frame()
			if !tester.HasText("当前查看") {
				t.Fatal("current row not marked")
			}
			s.viewProductionHistory(d, other)
			s.returnFromProductionHistory(v)
			wantID := ""
			if active {
				wantID = b.ID
			}
			if v.BatchID != wantID || v.Version != "release/manually-typed" || v.Branch != "feature/typed" || v.BranchQuery != "query" || v.SuggestedBranch != "suggestion" || v.ManualTag != "2.3.43" || v.Preview != preview || !v.PreparationInvalid || v.BuildChecked || v.historyReturn != nil {
				t.Fatal("return lost inputs or restored build authorization")
			}
		})
	}
}

func TestProductionHistoryRowDeletionKeepsDialogAndExactTarget(t *testing.T) {
	for _, scenario := range []string{"other", "current-with-active", "current-last"} {
		t.Run(scenario, func(t *testing.T) {
			s, v, b, backend, window := productionRequestFixture(t)
			old := productionHistoryRecord(b, "history-delete", "release/history", time.Now())
			s.releaseUnit.state.production.batches = append(s.releaseUnit.state.production.batches, old)
			if scenario == "current-last" {
				s.releaseUnit.state.production.batches, v.BatchID = []production.Batch{old}, ""
			}
			v.Version, v.Branch, v.BranchQuery = "release/manual", "feature/manual", "search"
			d := openProductionHistoryTest(t, s, v, backend, window)
			if scenario != "other" {
				s.viewProductionHistory(d, old)
				d = openProductionHistoryTest(t, s, v, backend, window)
			}
			ownerID := v.BatchID
			confirm := func() {
				s.win = &mygo.Window{}
				s.confirmProductionDeleteRecord(v, old, d)
				confirmation := s.confirmationDialog
				if confirmation == nil || !confirmation.Current() || confirmation.Buttons[0].label != "删除本地记录" {
					t.Fatal("missing protected confirmation")
				}
				s.win = nil
				s.finishConfirmationDialog(confirmation, 0)
			}
			confirm()
			call := productionTestCall(t, backend)
			token, _ := production.BatchRecordToken(old)
			if call.Kind != "production.delete" || call.Payload["batchId"] != old.ID || call.Payload["recordToken"] != token || v.BatchID != ownerID {
				t.Fatal("row deletion switched the page or targeted its current batch")
			}
			s.confirmProductionDeleteRecord(v, old, d) // Duplicate click while the request is busy.
			if s.confirmationDialog != nil {
				t.Fatal("duplicate deletion opened another confirmation")
			}
			backend.replies <- productionTestAnswer{err: errors.New("save denied")}
			productionTestUpdate(t, window)
			if !d.Open || d.RowErrors[old.ID] != "save denied" || s.releaseUnit.state.production.deletedBatches[old.ID] || v.BatchID != ownerID {
				t.Fatal("failed deletion changed record/page")
			}
			confirm()
			productionTestCall(t, backend)
			backend.replies <- productionTestAnswer{result: application.ActionResult{Message: "本地批次记录已删除"}}
			productionTestUpdate(t, window)
			wantID := b.ID
			if scenario == "current-last" {
				wantID = ""
			}
			if !d.Open || !s.releaseUnit.state.production.deletedBatches[old.ID] || len(s.productionHistoryRecords(d)) != 0 || v.BatchID != wantID || v.Version != "release/manual" || v.Branch != "feature/manual" || v.BranchQuery != "search" || len(backend.calls) != 0 || s.lastMessage == "" {
				t.Fatal("success failed to retain the dialog/inputs or removed the wrong record")
			}
		})
	}
}

func TestProductionHistoryDeleteNestedEscapeAndNoClickThrough(t *testing.T) {
	s, v, b := productionFixture(t)
	old := productionHistoryRecord(b, "old", "release/old", time.Now())
	s.releaseUnit.state.production.batches = append(s.releaseUnit.state.production.batches, old)
	s.win = &mygo.Window{}
	s.openProductionHistory(v)
	d := s.releaseUnit.state.production.historyDialog
	tester := productionIterationTester(s, 960, false)
	if err := tester.Click("删除"); err != nil {
		t.Fatal(err)
	}
	if s.confirmationDialog == nil || !tester.Focused("取消") {
		t.Fatal("delete did not focus cancel")
	}
	// A button in the lower dialog must not navigate underneath confirmation.
	if r, ok := tester.Find("查看"); ok {
		tester.ClickAt(r.X+r.W/2, r.Y+r.H/2)
	}
	if v.BatchID != b.ID || s.confirmationDialog == nil || !d.Open {
		t.Fatal("click passed through confirmation")
	}
	tester.Key(0, ui.KeyEscape)
	if s.confirmationDialog != nil || !d.Open || v.Busy || v.BatchID != b.ID {
		t.Fatal("Esc closed both layers or deleted a batch")
	}
	tester.Key(0, ui.KeyEscape)
	if d.Open {
		t.Fatal("second Esc did not close the list")
	}
}

func TestProductionHistoryDeleteConfirmationRejectsStaleRows(t *testing.T) {
	for _, change := range []string{"record", "active", "home", "config", "service", "closed", "busy", "batch"} {
		t.Run(change, func(t *testing.T) {
			s, v, b, backend, _ := productionRequestFixture(t)
			old := productionHistoryRecord(b, "old", "release/old", time.Now())
			s.releaseUnit.state.production.batches = append(s.releaseUnit.state.production.batches, old)
			// No I/O is needed to create the dialog snapshot in this test.
			s.backend = nil
			s.openProductionHistory(v)
			d := s.releaseUnit.state.production.historyDialog
			s.backend, s.win = backend, &mygo.Window{}
			s.confirmProductionDeleteRecord(v, old, d)
			confirmation := s.confirmationDialog
			if confirmation == nil {
				t.Fatal("missing confirmation")
			}
			switch change {
			case "record":
				s.releaseUnit.state.production.batches[1].Branches = nil // Same timestamps, different full record token.
			case "active":
				s.releaseUnit.state.production.batches[1].Ended = false
			case "home":
				s.snapshot.Home = "another-home"
			case "config":
				s.settingsUnit.state.configJSON = `{"tagEnabled":false}`
			case "service":
				s.releaseUnit.state.production.selected = "another-service"
			case "closed":
				d.Open = false
			case "busy":
				v.Busy = true
			case "batch":
				v.BatchID = "another-batch"
			}
			if confirmation.Current() {
				t.Fatal("stale row confirmation still current")
			}
			s.finishConfirmationDialog(confirmation, 0)
			if len(backend.calls) != 0 || s.releaseUnit.state.production.deletedBatches[old.ID] {
				t.Fatal("stale confirmation executed")
			}
		})
	}
}

func TestProductionHistoryLateReadAndDeleteDoNotCrossScopes(t *testing.T) {
	for _, kind := range []string{"production.list", "production.delete"} {
		for _, change := range []string{"service", "home", "config", "generation", "closed", "record"} {
			t.Run(kind+"/"+change, func(t *testing.T) {
				s, v, b, backend, window := productionRequestFixture(t)
				old := productionHistoryRecord(b, "old", "release/old", time.Now())
				s.releaseUnit.state.production.batches = append(s.releaseUnit.state.production.batches, old)
				d := openProductionHistoryTest(t, s, v, backend, window)
				action := application.Action{Kind: kind}
				if kind == "production.delete" {
					token, _ := production.BatchRecordToken(old)
					action.Payload = map[string]any{"batchId": old.ID, "recordToken": token, "confirmed": true}
				}
				s.productionRequestViewFromHistory(v, action, "", d)
				productionTestCall(t, backend)
				switch change {
				case "service":
					s.releaseUnit.state.production.selected = "new-service"
				case "home":
					s.snapshot.Home = "new-home"
				case "config":
					s.settingsUnit.state.configJSON = `{"tagEnabled":false}`
				case "generation":
					v.Generation++
				case "closed":
					d.Open = false
				case "record":
					s.releaseUnit.state.production.batches[1].Branches = nil
				}
				backend.replies <- productionTestAnswer{result: application.ActionResult{Data: production.Report{Batches: append([]production.Batch(nil), s.releaseUnit.state.production.batches...)}}}
				productionTestUpdate(t, window)
				if v.Busy || v.BatchID != b.ID || s.releaseUnit.state.production.deletedBatches[old.ID] || len(s.releaseUnit.state.production.batches) != 2 || len(backend.calls) != 0 {
					t.Fatal("late response modified a new target or started a successor")
				}
			})
		}
	}
}

func TestProductionHistoryDialogLayoutsAndMergedEmptyState(t *testing.T) {
	for _, width := range []int{960, 1440} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%t", width, dark), func(t *testing.T) {
				s, v, b := productionFixture(t)
				b.Branches = nil
				s.releaseUnit.state.production.batches[0] = b
				tester := productionIterationTester(s, width, dark)
				if !tester.HasText("暂无已合并分支") || tester.HasText("历史批次 ·") {
					t.Fatal("merged empty state missing or old disclosure retained")
				}
				for i := 0; i < 18; i++ {
					old := productionHistoryRecord(b, fmt.Sprint(i), fmt.Sprintf("release/历史分支-%02d-%s", i, strings.Repeat("long-name/", 6)), time.Now().Add(-time.Duration(i)*time.Hour))
					s.releaseUnit.state.production.batches = append(s.releaseUnit.state.production.batches, old)
				}
				s.openProductionHistory(v)
				tester.Frame()
				d := s.releaseUnit.state.production.historyDialog
				panel, ok := tester.Find("历史批次弹窗")
				close, closeOK := tester.Find("关闭")
				list, listOK := tester.Find("历史批次列表")
				if !ok || !closeOK || !listOK || panel.W > 721 || panel.Y < 0 || close.Y+close.H > 900 || list.Y+list.H > close.Y {
					t.Fatalf("dialog overflow or non-fixed footer: panel=%+v list=%+v close=%+v", panel, list, close)
				}
				captureProductionSummary(t, tester, fmt.Sprintf("history-list-%d-dark-%t", width, dark))
				tester.Scroll(list.X+list.W/2, list.Y+list.H/2, 0, 10000)
				closeAfter, _ := tester.Find("关闭")
				if closeAfter != close {
					t.Fatal("list scroll moved footer")
				}
				s.win = &mygo.Window{}
				s.confirmProductionDeleteRecord(v, s.productionHistoryRecords(d)[0], d)
				tester.Frame()
				if !tester.Focused("取消") {
					t.Fatal("nested confirmation did not focus cancel")
				}
				captureProductionSummary(t, tester, fmt.Sprintf("history-delete-%d-dark-%t", width, dark))
				tester.Key(0, ui.KeyEscape)
				if !d.Open {
					t.Fatal("nested Esc dismissed list")
				}
				d.Open = false
				v.selectProductionBatch(s.releaseUnit.state.production.batches[1].ID)
				tester.Frame()
				if !tester.HasText("暂无已合并分支") {
					t.Fatal("ended detail omitted merge empty state")
				}
				captureProductionSummary(t, tester, fmt.Sprintf("history-ended-%d-dark-%t", width, dark))
			})
		}
	}
}

func TestProductionHistoryNarrowRowsAndDisabledCurrentFocus(t *testing.T) {
	s, v, b := productionFixture(t)
	old := productionHistoryRecord(b, "old", "release/"+strings.Repeat("很长的名称/", 12), time.Now())
	s.releaseUnit.state.production.batches = append(s.releaseUnit.state.production.batches, old)
	v.BatchID = old.ID
	s.openProductionHistory(v)
	// Render just this overlay so duplicate branch/status text in the page
	// header cannot be mistaken for the row under test.
	tester := ui.NewTester(func(c *ui.Context) {
		s.renderProductionHistoryDialog(c, setTheme(c, false))
	}, 640, 900)
	name, found := tester.Find(old.ReleaseBranch)
	status, statusFound := tester.Find("已结束")
	button, buttonFound := tester.Find("当前查看")
	panel, panelFound := tester.Find("历史批次弹窗")
	if !found || !statusFound || !buttonFound || !panelFound || button.Y < name.Y+name.H || status.Y < button.Y || status.Y+status.H > button.Y+button.H || button.X+button.W > panel.X+panel.W {
		t.Fatalf("narrow row did not wrap its actions below the name: name=%+v status=%+v button=%+v panel=%+v", name, status, button, panel)
	}
	for i := 0; i < 12; i++ {
		tester.Key(0, ui.KeyTab)
		if tester.Focused("当前查看") {
			t.Fatal("disabled current row entered keyboard focus")
		}
	}
	captureProductionSummary(t, tester, "history-list-640-light")
}

func TestProductionHistoryDeleteLastRemovedServiceKeepsEmptyDialogUntilClose(t *testing.T) {
	s, v, b, backend, window := productionRequestFixture(t)
	b = productionHistoryRecord(b, b.ID, b.ReleaseBranch, time.Now())
	s.releaseUnit.state.production.batches = []production.Batch{b}
	s.releaseUnit.state.production.choices[0].Historical = true
	d := openProductionHistoryTest(t, s, v, backend, window)
	token, _ := production.BatchRecordToken(b)
	s.productionRequestViewFromHistory(v, application.Action{Kind: "production.delete", Payload: map[string]any{"batchId": b.ID, "recordToken": token, "confirmed": true}}, "", d)
	productionTestCall(t, backend)
	backend.replies <- productionTestAnswer{result: application.ActionResult{Message: "本地批次记录已删除"}}
	productionTestUpdate(t, window)
	if !s.productionHistoryCurrent(d) || len(s.productionHistoryRecords(d)) != 0 {
		t.Fatal("last deletion closed its empty list")
	}
	tester := productionIterationTester(s, 960, false)
	if !tester.HasText("暂无历史批次") {
		t.Fatal("empty dialog not shown")
	}
	if err := tester.Click("关闭"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if len(s.releaseUnit.state.production.choices) != 0 || s.releaseUnit.state.production.views[productionKey(v.Selection)] != nil {
		t.Fatal("removed service survived list dismissal")
	}
}
