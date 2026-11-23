package desktop

import (
	"context"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/production"

	"github.com/egoist/mygo/ui"
)

func TestProductionHistoryTagRecordsCopyAndReadSelectedBatch(t *testing.T) {
	s, view, active := productionFixture(t)
	historical := active
	historical.ID, historical.Version, historical.ReleaseBranch = "historical-batch", "2.3.42", "release/2.3.42"
	historical.Ended, historical.Phase = true, "ENDED"
	historical.Target.GenbuService = "historical-build-service"
	tag := production.Tag{Name: "2.3.42", Commit: historical.ReleaseSHA, Object: "historical-tag-object",
		PushedAt: "2026-11-20T00:01:00Z", Status: domain.GenbuTagProbeStatus{Build: domain.GenbuSuccess}}
	historical.Tags = []production.Tag{tag}
	state := s.releaseUnit.state.production
	state.batches = append(state.batches, historical)
	preview, branch, version := view.Preview, view.Branch, view.Version
	s.openProductionHistory(view)
	drawer := state.historyDialog
	if drawer == nil {
		t.Fatal("history drawer did not open")
	}
	s.viewProductionHistory(drawer, historical)
	calls := genbuPendingReads(t, s)
	tester := ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, false)
		s.renderProductionHistoryDialog(c, p)
		s.renderGenbuReadDialogs(c, p)
	}, 1100, 850)
	text := productionTagRecordText(historical, tag)
	if !tester.HasText(text) || !tester.HasText("构建成功") {
		t.Fatalf("history detail did not present its Tag record: %v", tester.Texts())
	}
	if err := tester.Click("复制 Tag 记录 " + tag.Name); err != nil {
		t.Fatal(err)
	}
	if tester.Clipboard() != text || len(calls) != 0 {
		t.Fatal("copy did not use the selected history Tag or dispatched a read")
	}
	read := func() (genbuReadTestCall, *genbuBuildLogDialogState) {
		t.Helper()
		if err := tester.Click("构建日志"); err != nil {
			t.Fatal(err)
		}
		call, log := genbuAwaitRead(t, calls), s.genbuBuildLog
		if log == nil || log.tag != tag.Name || log.genbuService != historical.Target.GenbuService || log.environment != "PROD" ||
			call.action.Kind != "production.build-log" || call.action.Payload["batchId"] != historical.ID ||
			call.action.Payload["tag"] != tag.Name || call.action.Payload["tailLines"] != 1000 {
			t.Fatalf("history log lost the selected production record: %+v", call.action)
		}
		if view.BatchID != active.ID || view.Preview != preview || view.Version != version || view.Branch != branch || view.Busy {
			t.Fatal("history Tag action changed the active production workflow")
		}
		return call, log
	}
	call, _ := read()
	tester.Key(0, ui.KeyEscape)
	if call.ctx.Err() != context.Canceled || s.genbuBuildLog != nil || !drawer.Open || drawer.SelectedBatchID != historical.ID {
		t.Fatal("Escape did not close only the selected history build log")
	}
	call, log := read()
	log.text = "outdated log output"
	drawer.SelectedBatchID = ""
	tester.Frame()
	if call.ctx.Err() != context.Canceled || s.genbuBuildLog != nil || log.text != "" || !drawer.Open {
		t.Fatal("leaving the history detail retained its log request or output")
	}
}
