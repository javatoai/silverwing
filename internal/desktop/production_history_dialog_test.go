package desktop

import (
	"errors"
	"fmt"
	"maps"
	"reflect"
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
	b.ReleaseCreatedAt = b.CreatedAt
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

func TestProductionHistoryClosingLoadingDrawerRetiresItsListResult(t *testing.T) {
	s, v, b, backend, window := productionRequestFixture(t)
	v.Message, v.BuildChecked = "kept result", true
	before := *v
	now := time.Now()
	retired := productionHistoryRecord(b, "retired-read", "release/retired-read", now)
	fresh := productionHistoryRecord(b, "current-read", "release/current-read", now)
	state := s.releaseUnit.state.production
	tester := productionIterationTester(s, 960, false)
	open := func() *productionHistoryDialog {
		t.Helper()
		if err := tester.Click("历史批次"); err != nil {
			t.Fatal(err)
		}
		if call := productionTestCall(t, backend); call.Kind != "production.list" {
			t.Fatalf("opening the drawer dispatched %s", call.Kind)
		}
		drawer := state.historyDialog
		if drawer == nil || !drawer.Loading || !v.Busy || v.request == nil {
			t.Fatal("drawer did not start a list read")
		}
		return drawer
	}
	oldDrawer := open()
	oldWorker := v.request
	// Simulate a completed read whose UI callback is queued behind the user's close.
	backend.replies <- productionTestAnswer{result: application.ActionResult{Data: production.Report{Batches: []production.Batch{b, retired}}}}
	var retiredUpdate func()
	select {
	case retiredUpdate = <-window.updates:
	case <-time.After(time.Second):
		t.Fatal("retired list result was not queued")
	}
	if err := tester.Click("关闭"); err != nil {
		t.Fatal(err)
	}
	if oldDrawer.Open || state.historyDialog != nil || v.Busy || v.request != nil || !oldWorker.canceled.Load() {
		t.Fatal("closing the loading drawer did not immediately release its request")
	}
	newDrawer := open()
	newWorker := v.request
	retiredUpdate()
	if newDrawer == oldDrawer || state.historyDialog != newDrawer || !newDrawer.Loading || !v.Busy || v.request != newWorker || len(s.productionHistoryRecords(newDrawer)) != 0 {
		t.Fatal("retired result modified the reopened drawer or its request")
	}
	backend.replies <- productionTestAnswer{result: application.ActionResult{Data: production.Report{Batches: []production.Batch{b, fresh}}}}
	productionTestUpdate(t, window)
	list := s.productionHistoryRecords(newDrawer)
	if !newDrawer.Open || newDrawer.Loading || newDrawer.Error != "" || len(list) != 1 || list[0].ID != fresh.ID || !reflect.DeepEqual(*v, before) {
		t.Fatal("the reopened drawer did not show its own result and preserve the release view")
	}
}

func TestProductionHistoryDrawerNavigationPreservesCurrentView(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(fmt.Sprint(active), func(t *testing.T) {
			s, v, b := productionFixture(t)
			old := productionHistoryRecord(b, "old", "release/old", time.Now())
			old.Branches = []production.MergedBranch{{Branch: "feature/only-in-history"}}
			other := productionHistoryRecord(b, "other", "release/other", time.Now().Add(-time.Hour))
			s.releaseUnit.state.production.batches = []production.Batch{old, other}
			if active {
				s.releaseUnit.state.production.batches = append(s.releaseUnit.state.production.batches, b)
			} else {
				v.BatchID = ""
			}
			v.Version, v.Branch, v.BranchQuery, v.SuggestedBranch = "release/manually-typed", "feature/typed", "query", "suggestion"
			v.BuildChecked, v.Generation, v.ActiveStep = active, 12, 4
			v.Error, v.Message = "kept error", "kept result"
			v.Steps = map[int]productionStepView{4: {State: "complete", Message: "kept step"}}
			before := *v
			before.Steps = maps.Clone(v.Steps)
			assertPreserved := func() {
				t.Helper()
				if s.productionView() != v || v.Preview != before.Preview || !reflect.DeepEqual(*v, before) {
					t.Fatalf("drawer navigation changed the release view: got %+v, want %+v", *v, before)
				}
			}
			tester := productionIterationTester(s, 960, false)
			if err := tester.Click("历史批次"); err != nil {
				t.Fatal(err)
			}
			d := s.releaseUnit.state.production.historyDialog
			if d == nil || !d.Open || d.SelectedBatchID != "" {
				t.Fatal("opening history did not show the list")
			}
			assertPreserved()
			if err := tester.Click(old.ReleaseBranch); err != nil {
				t.Fatal(err)
			}
			if !d.Open || d.SelectedBatchID != old.ID || !tester.HasText("已合并：feature/only-in-history") || !tester.HasText("示例服务：2.3.42") {
				t.Fatal("opening the row did not show the recorded merge and Tag inside the drawer")
			}
			assertPreserved()
			if err := tester.Click("返回历史批次"); err != nil {
				t.Fatal(err)
			}
			if !d.Open || d.SelectedBatchID != "" || !tester.HasText(old.ReleaseBranch) || !tester.HasText(other.ReleaseBranch) {
				t.Fatal("returning from detail did not restore the scoped list")
			}
			assertPreserved()
			if err := tester.Click(other.ReleaseBranch); err != nil {
				t.Fatal(err)
			}
			if !d.Open || d.SelectedBatchID != other.ID || tester.HasText("已合并：feature/only-in-history") {
				t.Fatal("second row did not replace the historical detail")
			}
			if err := tester.Click("关闭"); err != nil {
				t.Fatal(err)
			}
			if d.Open || tester.HasText(other.ReleaseBranch) {
				t.Fatal("closing from detail left historical data on the release page")
			}
			assertPreserved()
		})
	}
}

func TestProductionHistoryRowDeletionKeepsDialogAndExactTarget(t *testing.T) {
	for _, scenario := range []string{"multiple-history", "with-active", "history-only"} {
		t.Run(scenario, func(t *testing.T) {
			s, v, b, backend, window := productionRequestFixture(t)
			old := productionHistoryRecord(b, "history-delete", "release/history", time.Now())
			s.releaseUnit.state.production.batches = append(s.releaseUnit.state.production.batches, old)
			var wantRecords int
			switch scenario {
			case "multiple-history":
				other := productionHistoryRecord(b, "history-kept", "release/kept", time.Now().Add(-time.Hour))
				s.releaseUnit.state.production.batches = append(s.releaseUnit.state.production.batches, other)
				wantRecords = 1
			case "history-only":
				s.releaseUnit.state.production.batches, v.BatchID = []production.Batch{old}, ""
			}
			v.Version, v.Branch, v.BranchQuery = "release/manual", "feature/manual", "search"
			d := openProductionHistoryTest(t, s, v, backend, window)
			ownerID := v.BatchID
			tester := productionIterationTester(s, 960, false)
			confirm := func(action string) {
				t.Helper()
				s.win = &mygo.Window{}
				tester.Frame()
				if err := tester.Click("更多批次操作 · " + old.ReleaseBranch); err != nil {
					t.Fatal(err)
				}
				if err := tester.Click(action); err != nil {
					t.Fatal(err)
				}
				confirmation := s.confirmationDialog
				if confirmation == nil || !confirmation.Current() || confirmation.Buttons[0].label != "删除本地记录" {
					t.Fatal("missing protected confirmation")
				}
				s.win = nil
				s.finishConfirmationDialog(confirmation, 0)
			}
			confirm("删除")
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
			tester.Frame()
			if !tester.HasText("删除失败：save denied") {
				t.Fatal("failed deletion did not show the row error")
			}
			confirm("重试删除")
			productionTestCall(t, backend)
			backend.replies <- productionTestAnswer{result: application.ActionResult{Message: "本地批次记录已删除"}}
			productionTestUpdate(t, window)
			if !d.Open || !s.releaseUnit.state.production.deletedBatches[old.ID] || len(s.productionHistoryRecords(d)) != wantRecords || v.BatchID != ownerID || v.Version != "release/manual" || v.Branch != "feature/manual" || v.BranchQuery != "search" || len(backend.calls) != 0 || s.lastMessage == "" {
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
	row, found := tester.Find(old.ReleaseBranch)
	if !found {
		t.Fatal("missing historical row")
	}
	openDelete := func() {
		t.Helper()
		if err := tester.Click("更多批次操作 · " + old.ReleaseBranch); err != nil {
			t.Fatal(err)
		}
		if err := tester.Click("删除"); err != nil {
			t.Fatal(err)
		}
		if s.confirmationDialog == nil || !tester.Focused("取消") {
			t.Fatal("delete did not focus cancel")
		}
	}
	openDelete()
	// The row must not navigate underneath confirmation.
	tester.ClickAt(row.X+row.W-3, row.Y+row.H/2)
	if v.BatchID != b.ID || d.SelectedBatchID != "" || !d.Open {
		t.Fatal("click passed through confirmation")
	}
	if s.confirmationDialog == nil {
		openDelete()
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

func TestProductionHistoryDrawerLayoutsAndMergedEmptyState(t *testing.T) {
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
				panel, ok := tester.Find("历史批次抽屉")
				close, closeOK := tester.Find("关闭")
				list, listOK := tester.Find("历史批次列表")
				if !ok || !closeOK || !listOK || panel.X <= 0 || panel.X+panel.W != float32(width) || panel.Y != 0 || panel.H != 900 || close.Y+close.H > list.Y || list.Y+list.H > 900 {
					t.Fatalf("drawer is not right-aligned and full-height with a fixed header: panel=%+v list=%+v close=%+v", panel, list, close)
				}
				captureProductionSummary(t, tester, fmt.Sprintf("history-list-%d-dark-%t", width, dark))
				tester.Scroll(list.X+list.W/2, list.Y+list.H/2, 0, 10000)
				closeAfter, _ := tester.Find("关闭")
				if closeAfter != close {
					t.Fatal("list scroll moved the close button")
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
				s.viewProductionHistory(d, s.productionHistoryRecords(d)[0])
				tester.Frame()
				if !tester.HasText("返回历史批次") || !tester.HasText("暂无已合并分支") {
					t.Fatal("ended detail omitted merge empty state")
				}
				captureProductionSummary(t, tester, fmt.Sprintf("history-ended-%d-dark-%t", width, dark))
			})
		}
	}
}

func TestProductionHistoryNarrowRowsShowBuildAndAcceptKeyboard(t *testing.T) {
	s, v, b := productionFixture(t)
	created := time.Date(2026, 11, 15, 14, 20, 0, 0, time.Local)
	old := productionHistoryRecord(b, "old", "release/"+strings.Repeat("很长的名称/", 12), created)
	old.Tags = append(old.Tags, production.Tag{Name: "2.3.42.1", PushedAt: created.Add(time.Hour).UTC().Format(time.RFC3339), Status: domain.GenbuTagProbeStatus{Build: domain.GenbuFailed}})
	unbuilt := productionHistoryRecord(b, "unbuilt", "release/unbuilt", created.Add(-time.Hour))
	unbuilt.Tags = nil
	s.releaseUnit.state.production.batches = append(s.releaseUnit.state.production.batches, old, unbuilt)
	s.openProductionHistory(v)
	// Render just this overlay so duplicate branch/status text in the page
	// header cannot be mistaken for the row under test.
	tester := ui.NewTester(func(c *ui.Context) {
		s.renderProductionHistoryDialog(c, setTheme(c, false))
	}, 640, 900)
	row, found := tester.Find(old.ReleaseBranch)
	more, menuFound := tester.Find("更多批次操作 · " + old.ReleaseBranch)
	panel, panelFound := tester.Find("历史批次抽屉")
	if !found || !menuFound || !panelFound || row.X < panel.X || more.X < row.X+row.W || more.X+more.W > panel.X+panel.W {
		t.Fatalf("narrow row and more menu overlap or overflow: row=%+v more=%+v panel=%+v", row, more, panel)
	}
	if !tester.HasText(created.Format("2006-01-02")) || !tester.HasText("构建失败") || !tester.HasText("未构建") || tester.HasText("构建成功") {
		t.Fatalf("row date or latest build result is wrong: %v", tester.Texts())
	}
	captureProductionSummary(t, tester, "history-list-640-light")
	for i := 0; i < 8 && !tester.Focused(old.ReleaseBranch); i++ {
		tester.Key(0, ui.KeyTab)
	}
	if !tester.Focused(old.ReleaseBranch) {
		t.Fatal("historical row could not be focused")
	}
	tester.Key(0, ui.KeyEnter)
	d := s.releaseUnit.state.production.historyDialog
	if !d.Open || d.SelectedBatchID != old.ID || v.BatchID != b.ID {
		t.Fatal("keyboard activation did not select only the drawer row")
	}
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
