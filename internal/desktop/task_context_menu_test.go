package desktop

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"

	"silverwing-go/internal/editor"
)

func navigationChooseTaskMenu(t *testing.T, tester *ui.Tester, taskTitle, item string) {
	t.Helper()
	if err := tester.RightClick(taskTitle); err != nil {
		t.Fatal(err)
	}
	if err := tester.ChooseMenuItem(item); err != nil {
		t.Fatal(err)
	}
}

func TestTaskContextMenuKeepsSelectionAndDraft(t *testing.T) {
	s := taskNavigationFixture()
	s.documentPath, s.documentContent, s.documentDirty = "draft.md", "正在输入的草稿", true
	s.documentDrafts["draft.md"], s.documentDirtyMap["draft.md"] = s.documentContent, true
	s.markdown = &markdownEditorState{path: "draft.md", doc: editor.NewDocument(s.documentContent, "menu-composition")}
	s.markdown.doc.SetComposing(true)
	tester := taskNavigationTester(s, 240, 500)
	for _, hidden := range []string{"服务端", "客户端", "其他任务", "置顶", "置顶任务：界面体验"} {
		if tester.HasText(hidden) {
			t.Fatalf("flat task index still displays %q", hidden)
		}
	}
	if err := tester.RightClick("界面体验"); err != nil {
		t.Fatal(err)
	}
	if got := tester.Menu(); !slices.Equal(got, []string{"在 Codex 中打开", "-", "配置任务资料", "-", "置顶任务", "-", "归档任务", "-", "删除任务"}) {
		t.Fatalf("active menu: %v", got)
	}
	if err := tester.ChooseMenuItem("置顶任务"); err != nil {
		t.Fatal(err)
	}
	if !s.taskIsPinned(s.tasks[1]) || s.selected != 0 || s.documentContent != "正在输入的草稿" || !s.documentDirty ||
		s.documentDrafts["draft.md"] != s.documentContent || !s.documentDirtyMap["draft.md"] ||
		!s.markdown.doc.Composing() || len(s.taskNavigationFor().value.Recent) != 0 {
		t.Fatal("context menu changed the open task, composition, draft or visit history")
	}
	navigationChooseTaskMenu(t, tester, "界面体验", "取消置顶")
	if s.taskIsPinned(s.tasks[1]) || s.selected != 0 {
		t.Fatal("unpin did not stay on the open task")
	}
	// Keyboard menu and the row's far-right area have the same target.
	navigationClick(t, tester, "搜索任务")
	tester.Key(0, ui.KeyDown)
	tester.Key(0, ui.KeyDown)
	tester.Key(ui.Shift, ui.KeyF10)
	if err := tester.ChooseMenuItem("置顶任务"); err != nil {
		t.Fatal(err)
	}
	if !s.taskIsPinned(s.tasks[1]) || s.selected != 0 {
		t.Fatal("keyboard menu did not use the focused task")
	}
	row, found := tester.Find("界面体验")
	if !found {
		t.Fatal("task row missing")
	}
	tester.RightClickAt(row.X+row.W-2, row.Y+row.H/2)
	if err := tester.ChooseMenuItem("取消置顶"); err != nil {
		t.Fatal(err)
	}
}

func TestTaskContextMenuRejectsChangedHome(t *testing.T) {
	s := taskNavigationFixture()
	tester := taskNavigationTester(s, 240, 500)
	if err := tester.RightClick("界面体验"); err != nil {
		t.Fatal(err)
	}
	s.snapshot.Home = t.TempDir()
	if err := tester.ChooseMenuItem("置顶任务"); err != nil {
		t.Fatal(err)
	}
	if s.taskIsPinned(s.tasks[1]) {
		t.Fatal("a late menu choice acted in a different application home")
	}
}

func TestTaskContextMenuRejectsChangedTargetAndBusyMutations(t *testing.T) {
	for _, change := range []string{"created", "path", "removed", "root", "page", "busy"} {
		t.Run(change, func(t *testing.T) {
			s := taskNavigationFixture()
			s.win = &mygo.Window{}
			tester := taskNavigationTester(s, 240, 500)
			if err := tester.RightClick("界面体验"); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "created":
				s.tasks[1].createdAt = "replacement"
			case "path":
				s.tasks[1].path = "moved"
			case "removed":
				s.tasks = append(s.tasks[:1], s.tasks[2:]...)
			case "root":
				s.configJSON = `{"taskRoot":"moved"}`
			case "page":
				s.page = "archive"
			case "busy":
				s.busy = true
			}
			// A disabled or vanished item may be rejected by the framework itself.
			_ = tester.ChooseMenuItem("归档任务")
			if s.confirmationDialog != nil || s.selected != 0 || len(s.taskNavigationFor().value.Recent) != 0 {
				t.Fatal("stale or busy context action was accepted")
			}
		})
	}
}

func TestTaskArchiveConfirmationUsesCapturedTarget(t *testing.T) {
	for _, change := range []string{"none", "recreated", "path", "home", "root", "page", "archived", "removed", "busy"} {
		t.Run(change, func(t *testing.T) {
			s := taskNavigationFixture()
			target := s.tasks[1]
			scope, ok := s.taskArchiveScope(target)
			if !ok {
				t.Fatal("non-current archive target was rejected")
			}
			switch change {
			case "recreated":
				s.tasks[1].createdAt = "replacement"
			case "path":
				s.tasks[1].path = "moved"
			case "home":
				s.snapshot.Home = "another-home"
			case "root":
				s.configJSON = `{"taskRoot":"another-root"}`
			case "page":
				s.page = "archive"
			case "archived":
				s.tasks[1].archived = true
			case "removed":
				s.tasks = append(s.tasks[:1], s.tasks[2:]...)
			case "busy":
				s.busy = true
			}
			action, accepted := s.taskArchiveConfirmationAction(scope)
			if accepted != (change == "none") || accepted && (action.TaskID != target.id || action.Path != target.path) {
				t.Fatalf("confirmation for %s: accepted=%t action=%+v", change, accepted, action)
			}
		})
	}
}

func TestTaskRestorationKeepsArchiveBrowsingState(t *testing.T) {
	s := taskNavigationFixture()
	s.page, s.selected = "archive", 3
	state := s.taskIndexBrowsingFor(true)
	state.search, state.scroll.Y = "归档", 120
	s.taskNavigationFor().value.Recent = []string{taskNavigationKey(s.tasks[3])}
	legacyRecent := slices.Clone(s.taskNavigationFor().value.Recent)
	s.tasks[3].archived = false
	s.selected = -1
	s.applyTaskLifecycleSnapshotSelection(application.Action{Kind: "task.restore", TaskID: s.tasks[3].id}, application.ActionResult{})
	if s.page != "archive" || s.selected != -1 || state.search != "归档" || state.scroll.Y != 120 ||
		state.revealTaskKey != "" || !slices.Equal(s.taskNavigationFor().value.Recent, legacyRecent) {
		t.Fatal("restoration opened the restored task or reset archive browsing")
	}
}

func TestSettleMaterialsBeforeRootActionUsesHeadlessSchedulerSeam(t *testing.T) {
	if _, err := (&app{}).settleMaterialsBeforeRootAction(context.Background(), application.Action{Kind: "task.delete"}, &materialsFileJobBarrier{}, nil); err == nil {
		t.Fatal("root action proceeded without a live window or injected scheduler")
	}

	s := &app{taskContextUpdate: func(update func()) { update() }}
	barrier := &materialsFileJobBarrier{owner: s.ensureMaterialsFileJobs()}
	if _, err := s.settleMaterialsBeforeRootAction(context.Background(), application.Action{Kind: "task.delete"}, barrier, nil); err != nil {
		t.Fatalf("headless scheduler did not settle the materials barrier: %v", err)
	}
}

func TestTaskLifecycleSnapshotPreservesUnrelatedDraftAndArchiveRoute(t *testing.T) {
	s := taskNavigationFixture()
	s.page, s.selected = "archive", 3
	s.tasks = append(s.tasks, task{id: "archive-next", title: "已归档的其他任务", createdAt: "next", archived: true})
	state := s.taskIndexBrowsingFor(true)
	state.search, state.scroll.Y = "已归档", 50
	s.documentPath, s.documentContent, s.documentDirty = "draft.md", "保留草稿", true
	s.documentDrafts["draft.md"] = s.documentContent
	snapshot := application.Snapshot{ConfigJSON: `{}`, Groups: s.snapshot.Groups}
	for index, item := range s.tasks {
		snapshot.Tasks = append(snapshot.Tasks, application.Task{ID: item.id, Title: item.title, Path: item.path,
			CreatedAt: item.createdAt, Archived: item.archived && index != len(s.tasks)-1, GroupID: item.groupID})
	}
	// Restore another archived task. The current archived task and draft survive.
	s.applySnapshot(snapshot)
	s.applyTaskLifecycleSnapshotSelection(application.Action{Kind: "task.restore", TaskID: "archive-next"}, application.ActionResult{})
	if s.currentTask().id != "task-archived" || s.page != "archive" || s.documentContent != "保留草稿" ||
		!s.documentDirty || s.documentDrafts["draft.md"] != s.documentContent || state.search != "已归档" || state.scroll.Y != 50 {
		t.Fatal("restoring a different task changed archive selection, browsing or the current draft")
	}
	// Restore the last matching archive: remain in the category with empty detail.
	snapshot.Tasks[3].Archived = false
	s.applySnapshot(snapshot)
	s.applyTaskLifecycleSnapshotSelection(application.Action{Kind: "task.restore", TaskID: "task-archived"}, application.ActionResult{})
	if s.page != "archive" || s.selected != -1 || state.search != "已归档" || state.revealTaskKey != "" {
		t.Fatal("restoring the last archive navigated away or selected a hidden task")
	}
}

func TestTaskLifecycleSelectionUsesFilteredNeighbors(t *testing.T) {
	for _, mode := range []string{"next", "previous", "empty", "non-current", "pinned"} {
		t.Run(mode, func(t *testing.T) {
			s := taskNavigationFixture()
			s.selected = 1
			next := slices.Clone(s.tasks)
			next[1].archived = true
			want := 2
			switch mode {
			case "previous":
				s.selected = 4
				next = next[:4]
				want = 2
			case "empty":
				s.taskIndexBrowsingFor(false).search = "UI-7"
				want = -1
			case "non-current":
				s.selected = 0
				want = 0
			case "pinned":
				s.setTaskPinned(s.tasks[4], true)
				s.setTaskPinned(s.tasks[1], true)
				want = 4
			}
			if got := s.taskLifecycleSnapshotIndex(next); got != want {
				t.Fatalf("%s selection = %d, want %d", mode, got, want)
			}
		})
	}
}

func TestTaskContextMenuThemedLayout(t *testing.T) {
	for _, width := range []int{160, 240, 340} {
		for _, dark := range []bool{false, true} {
			for _, archived := range []bool{false, true} {
				t.Run(fmt.Sprintf("%d-dark-%t-archive-%t", width, dark, archived), func(t *testing.T) {
					s := taskNavigationFixture()
					s.dark = dark
					title := "界面体验"
					lifecycleLabel := "归档任务"
					if archived {
						s.page, s.selected = "archive", 3
						title, lifecycleLabel = "已归档任务", "恢复归档"
					}
					tester := ui.NewTester(func(c *ui.Context) {
						p := setTheme(c, dark)
						ui.MenuScope(c, "task-context-layout")
						ui.Column(c).Fill().Children(func() { s.taskIndex(c, p) })
						ui.ThemedMenus(c, "task-context-layout")
					}, width, 520)
					if err := tester.RightClick(title); err != nil {
						t.Fatal(err)
					}
					tester.Frame()
					for _, label := range []string{"置顶任务", lifecycleLabel, "删除任务"} {
						bounds, found := tester.Find(label)
						if !found || bounds.X < 0 || bounds.Y < 0 || bounds.X+bounds.W > float32(width) || bounds.Y+bounds.H > 520 {
							t.Fatalf("context item %q is clipped: %+v texts=%q native=%q", label, bounds, tester.Texts(), tester.Menu())
						}
					}
					captureDesktopTestFrame(t, tester, fmt.Sprintf("task-context-%d-dark-%t-archive-%t", width, dark, archived))
					navigationClick(t, tester, "置顶任务")
					tester.Frame()
					index := 1
					if archived {
						index = 3
					}
					if !s.taskIsPinned(s.tasks[index]) {
						t.Fatalf("the themed popup did not activate its context target: scope=%+v texts=%q", s.taskContextScope, tester.Texts())
					}
				})
			}
		}
	}
}
