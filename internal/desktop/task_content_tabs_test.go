package desktop

import (
	"slices"
	"testing"
)

func TestTaskFilesTabReadingRoundTripAndLegacyIndices(t *testing.T) {
	want := []string{"任务分支", "任务资料", "需求详情", "任务说明", "Tag", "任务文件", "服务信息", "终端"}
	if !slices.Equal(tabs, want) || taskTagTab != 4 || taskFilesTab != 5 {
		t.Fatalf("task files must be appended without moving existing tabs: %v", tabs)
	}
	for index, view := range []string{"DETAIL", "MATERIALS", "REQUIREMENT", "NOTES", "TAG", "FILES", "SERVICES", "TERMINAL"} {
		if readingViewForTab(index) != view {
			t.Fatalf("tab %d lost its reading view", index)
		}
		if restored, ok := readingTabForView(view); !ok || restored != index {
			t.Fatalf("view %s restored as %d, %v", view, restored, ok)
		}
		if bounded := (ReadingSnapshot{Schema: 1, View: view}).Bounded(); bounded.View != view {
			t.Fatalf("reading cache rejected view %s", view)
		}
	}
}

func TestTaskFilesTabEntryPreservesMaterialsAndDoesNotStartGit(t *testing.T) {
	s, backend, _ := taskSelectionGitFixture(t)
	s.tab = 0
	s.materialsUnit.state.documentPath, s.materialsUnit.state.documentContent = "existing.md", "existing material draft"
	s.materialsUnit.state.documentDirty = true
	s.materialsUnit.state.documentDrafts["existing.md"] = "existing material draft"
	if !s.selectTaskContentTab(taskFilesTab) || s.tab != taskFilesTab {
		t.Fatal("task files tab did not become active")
	}
	if s.materialsUnit.state.documentsRequested || s.workspaceUnit.state.workspaceGit != nil ||
		s.materialsUnit.state.documentContent != "existing material draft" || !s.materialsUnit.state.documentDirty ||
		s.materialsUnit.state.documentDrafts["existing.md"] != "existing material draft" {
		t.Fatal("entering task files started materials/Git or discarded the existing material draft")
	}
	select {
	case read := <-backend.reads:
		if read.action.Kind != "task.files.list" {
			t.Fatalf("entering task files invoked an unrelated operation: %s", read.action.Kind)
		}
	default:
	}
	if s.selectTaskContentTab(-1) || s.selectTaskContentTab(len(tabs)) || s.tab != taskFilesTab {
		t.Fatal("invalid tab selection changed the current file page")
	}
}

func TestTaskFilesSelectionKeepsTabWithoutLaunchingGit(t *testing.T) {
	s, backend, _ := taskSelectionGitFixture(t)
	s.tab = taskFilesTab
	if !s.selectTaskIndex(1) || s.taskUnit.currentTask().id != "task-b" || s.tab != taskFilesTab {
		t.Fatal("task selection did not retain the file page for the newly selected task")
	}
	s.refreshWorkspaceTaskSelection(true)
	if s.workspaceUnit.state.workspaceGit != nil {
		t.Fatal("read-only task-file selection launched a workspace Git query")
	}
	select {
	case read := <-backend.reads:
		t.Fatalf("task-file selection dispatched an unsolicited action: %s", read.action.Kind)
	default:
	}
}
