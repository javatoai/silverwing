package desktop

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestTaskContentTabsOnActiveAndArchivedTasks(t *testing.T) {
	for _, page := range []string{"tasks", "archive"} {
		t.Run(page, func(t *testing.T) {
			s := newApp(nil, Options{Page: page, CapturePath: "task-content-tabs"})
			seedPlatformConnectionsForDesktopTest(s, true, false)
			s.taskUnit.state.tasks = []task{{id: "task-a", title: "示例任务", archived: page == "archive"}}
			tester := ui.NewTester(func(c *ui.Context) { s.taskWorkspaceDetail(c, setTheme(c, false)) }, 1100, 600)
			row, ok := tester.Find("任务内容页签")
			if !ok {
				t.Fatal("task tabs row is absent")
			}
			if _, found := tester.Find("任务说明"); found {
				t.Fatal("removed task notes tab is still visible")
			}
			lastX := row.X - 1
			for _, name := range []string{"任务分支", "任务资料", "需求详情", "Tag", "任务文件", "服务信息", "终端"} {
				bounds, found := tester.Find(name)
				if !found || bounds.X <= lastX || bounds.Y < row.Y || bounds.Y+bounds.H > row.Y+row.H {
					t.Fatalf("%q is missing or out of order in the task tabs row: %+v", name, bounds)
				}
				lastX = bounds.X
			}
		})
	}
}

func TestProjectInstructionsTabRequiresOnlyRepositoryConfiguration(t *testing.T) {
	s := newApp(nil, Options{Page: "tasks", CapturePath: "project-instructions-tabs"})
	if slices.Contains(s.visibleTaskContentTabs(), taskProjectInstructionsTab) {
		t.Fatal("project materials appeared without a repository")
	}
	s.settingsUnit.state.configJSON = `{"projectInstructionsGitUrl":"https://example.invalid/project.git"}`
	tabs := s.visibleTaskContentTabs()
	if index := slices.Index(tabs, taskProjectInstructionsTab); index < 1 || tabs[index-1] != taskFilesTab {
		t.Fatalf("project materials should follow task files without a selected task: %v", tabs)
	}
	if !s.selectTaskContentTab(taskProjectInstructionsTab) {
		t.Fatal("could not open project materials without a selected task")
	}
	tester := ui.NewTester(func(c *ui.Context) { s.taskWorkspaceDetail(c, setTheme(c, false)) }, 1100, 600)
	if _, ok := tester.Find("正在准备项目资料…"); !ok {
		t.Fatal("the project tab rendered the no-task placeholder")
	}
}

func TestTaskContentTabsReadingRoundTripAndLegacyIndices(t *testing.T) {
	for index, view := range []string{"DETAIL", "MATERIALS", "REQUIREMENT", "", "TAG", "FILES", "SERVICES", "TERMINAL"} {
		if view == "" {
			continue
		}
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

func TestRetiredTaskNotesTabFallsBackToAvailablePage(t *testing.T) {
	const retiredTab = 3
	if _, ok := readingTabForView("NOTES"); ok {
		t.Fatal("removed notes view still restores to a task tab")
	}
	if bounded := (ReadingSnapshot{Schema: 1, View: "NOTES"}).Bounded(); bounded.View != "DETAIL" {
		t.Fatalf("legacy notes snapshot selected %q, want DETAIL", bounded.View)
	}

	settings := newApp(nil, Options{Page: "settings"})
	path := taskTabsPreferencesFixture(t, settings)
	if !settings.setTaskContentTabEnabled(0, false) {
		t.Fatal(settings.lastError)
	}
	s := newApp(nil, Options{Page: "tasks", Tab: retiredTab, TabExplicit: true})
	s.restoreDesktopPreferences(filepath.Dir(path))
	if s.tab != 1 {
		t.Fatalf("legacy CLI notes tab selected %d, want first enabled tab 1", s.tab)
	}
	if s.selectTaskContentTab(retiredTab) || s.tab != 1 || slices.Contains(s.visibleTaskContentTabs(), retiredTab) {
		t.Fatal("removed notes tab is still selectable")
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
