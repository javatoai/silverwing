package desktop

import (
	"testing"
)

func TestArchiveBatchSelectionRetainsSearchOutsideAndUsesArchiveOrder(t *testing.T) {
	s := taskNavigationFixture()
	s.page = "archive"
	s.taskUnit.state.tasks[3].path = `D:\tasks\alpha`
	s.taskUnit.state.tasks = append(s.taskUnit.state.tasks,
		task{id: "archive-beta", title: "Beta", createdAt: "2026-02-01", path: `D:\tasks\beta`, archived: true},
		task{id: "archive-gamma", title: "Gamma", createdAt: "2026-02-02", path: `D:\tasks\gamma`, archived: true},
	)
	state := s.taskIndexBrowsingFor(true)
	state.archiveBatchSelecting = true
	s.setArchiveBatchTaskSelected(s.taskUnit.state.tasks[3], true)

	state.search = "beta"
	state.visibleRows = s.taskIndexRows(true)
	if len(state.visibleRows) != 1 || s.archiveBatchSelectedOutsideSearch(state) != 1 {
		t.Fatalf("search should retain one selected task outside its result: rows=%v outside=%d", state.visibleRows, s.archiveBatchSelectedOutsideSearch(state))
	}
	s.setArchiveBatchSearchResultsSelected(state.visibleRows, true)
	if len(state.archiveBatchSelected) != 2 || s.archiveBatchSelectedOutsideSearch(state) != 1 {
		t.Fatalf("selecting search results discarded existing choices: %v", state.archiveBatchSelected)
	}

	// Batch requests follow the archive list order, including its pinned-first order.
	s.setTaskPinned(s.taskUnit.state.tasks[3], true)
	selected := s.orderedArchiveBatchSelection(state)
	if len(selected) != 2 || selected[0].id != "task-archived" || selected[1].id != "archive-beta" {
		t.Fatalf("selected tasks should follow archive list order: %+v", selected)
	}
}

func TestArchiveBatchSelectionPrunesChangedIncarnationsAndClearsOnLeave(t *testing.T) {
	s := taskNavigationFixture()
	s.page = "archive"
	archived := &s.taskUnit.state.tasks[3]
	archived.path = `D:\tasks\archived`
	state := s.taskIndexBrowsingFor(true)
	state.archiveBatchSelecting = true
	s.setArchiveBatchTaskSelected(*archived, true)
	if len(state.archiveBatchSelected) != 1 {
		t.Fatal("archived task was not selected")
	}

	replacement := *archived
	replacement.path += "-replacement"
	s.taskUnit.state.tasks[3] = replacement
	s.reconcileArchiveBatchSelection()
	if len(state.archiveBatchSelected) != 0 || state.archiveBatchSelecting {
		t.Fatalf("replacement task inherited an old selection: mode=%v selected=%v", state.archiveBatchSelecting, state.archiveBatchSelected)
	}

	state.archiveBatchSelecting = true
	s.setArchiveBatchTaskSelected(replacement, true)
	s.page = "tasks"
	s.reconcileArchiveBatchSelection()
	if state.archiveBatchSelecting || state.archiveBatchSelected != nil {
		t.Fatalf("leaving archive retained batch selection: mode=%v selected=%v", state.archiveBatchSelecting, state.archiveBatchSelected)
	}
}

func TestArchiveBatchSelectionControlsCheckboxesSearchResultsAndCancel(t *testing.T) {
	s := taskNavigationFixture()
	s.page = "archive"
	s.taskUnit.state.tasks[3].path = `D:\tasks\archived`
	s.taskUnit.state.tasks = append(s.taskUnit.state.tasks,
		task{id: "archive-beta", title: "Beta", createdAt: "2026-02-01", path: `D:\tasks\beta`, archived: true},
	)
	tester := taskNavigationTester(s, 320, 560)
	if err := tester.Click("批量选择"); err != nil {
		t.Fatal(err)
	}
	if !s.taskIndexBrowsingFor(true).archiveBatchSelecting {
		t.Fatal("archive selection mode did not open")
	}
	if err := tester.Click("选择已归档任务：已归档任务"); err != nil {
		t.Fatal(err)
	}
	state := s.taskIndexBrowsingFor(true)
	if len(state.archiveBatchSelected) != 1 || !tester.HasText("已选 1 项") {
		t.Fatalf("row checkbox selection was not reflected: %v", state.archiveBatchSelected)
	}
	if err := tester.Click("全选搜索结果"); err != nil {
		t.Fatal(err)
	}
	if len(state.archiveBatchSelected) != 2 {
		t.Fatalf("select search results should preserve selected rows and add visible matches: %v", state.archiveBatchSelected)
	}
	if _, found := tester.Find("删除所选"); !found {
		t.Fatal("delete selected action missing")
	}
	if err := tester.Click("取消"); err != nil {
		t.Fatal(err)
	}
	if state.archiveBatchSelecting || state.archiveBatchSelected != nil {
		t.Fatalf("cancel did not clear archive selection: mode=%v selected=%v", state.archiveBatchSelecting, state.archiveBatchSelected)
	}
}

func TestArchiveBatchTaskKeyCapturesWholeIncarnation(t *testing.T) {
	base := task{id: "task", path: `D:\tasks\task`, createdAt: "created", archived: true}
	keys := []string{
		archiveBatchTaskKey(base),
		archiveBatchTaskKey(task{id: base.id, path: base.path + "-other", createdAt: base.createdAt, archived: true}),
		archiveBatchTaskKey(task{id: base.id, path: base.path, createdAt: base.createdAt + "-other", archived: true}),
		archiveBatchTaskKey(task{id: base.id, path: base.path, createdAt: base.createdAt, archived: false}),
	}
	if keys[0] == "" || keys[0] == keys[1] || keys[0] == keys[2] || keys[0] == keys[3] {
		t.Fatalf("batch key failed to distinguish task incarnation: %v", keys)
	}
	if got := archiveBatchTaskKey(task{id: "task", path: base.path, createdAt: base.createdAt}); got != "" {
		t.Fatalf("active task received a batch key: %q", got)
	}
}
