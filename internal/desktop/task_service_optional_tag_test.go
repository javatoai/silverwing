package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
)

func TestServiceOptionalKeepsSNBTagHistoryWithoutBuildTargets(t *testing.T) {
	s, _ := taskMenuFixture(t)
	s.tasks[0].workspaces = nil
	task := s.tasks[0]
	if s.taskCanBuildAnyTag(task) {
		t.Fatal("no-workspace task can build a Tag")
	}
	s.snapshot.TagHistory = []application.TagRecord{{ID: "retained", TaskID: task.id, FolderName: task.title, ServiceID: "removed-service", ModuleID: "module", ServiceName: "retained-service", Tag: "1.0.4", State: "SUCCESS", BatchID: "batch", CreatedAt: "2026-10-26T01:00:00Z"}}
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() { s.renderTaskTagHistory(c, setTheme(c, false), task) })
	}, 960, 800)
	if len(s.taskTagHistoryRecords(task.id)) != 1 || !tester.HasText("retained-service") || !tester.HasText("1.0.4") {
		t.Fatal("removing workspaces hid saved Tag history")
	}
}
