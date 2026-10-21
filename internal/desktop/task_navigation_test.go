package desktop

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/editor"
)

func taskNavigationFixture() *app {
	s := newApp(nil, Options{CapturePath: "isolated-task-navigation"})
	s.loading, s.page, s.selected = false, "tasks", 0
	s.snapshot.Groups = []application.Group{{ID: "backend", Name: "服务端"}, {ID: "client", Name: "客户端"}}
	s.tasks = []task{
		{id: "task-api", title: "支付 API 联调", createdAt: "2026-01-01", groupID: "backend", requirementID: "REQ-42", branch: "feature/payment", services: []string{"billing"}, status: "进行中"},
		{id: "task-ui", title: "界面体验", createdAt: "2026-01-02", groupID: "client", requirementID: "UI-7", requirementURL: "https://example.invalid/UI-7", status: "待处理"},
		{id: "task-timeout", title: "修复超时", createdAt: "2026-01-03", groupID: "backend", requirementID: "BUG-9", status: "进行中"},
		{id: "task-archived", title: "已归档任务", createdAt: "2026-01-04", groupID: "client", archived: true, status: "已完成"},
		{id: "task-orphan", title: "历史项目任务", createdAt: "2026-01-05", groupID: "retired", status: "待处理"},
	}
	metadataTitle := "结算 页面优化"
	s.taskRequirementMetadata = map[string]taskRequirementMetadataState{
		"task-ui": {URL: s.tasks[1].requirementURL, Status: requirementMetadataLoaded, Metadata: requirementMetadata{ID: "META-17", Title: &metadataTitle}},
	}
	return s
}

func taskNavigationTester(s *app, width, height int) *ui.Tester {
	return ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() { s.taskIndex(c, setTheme(c, s.dark)) })
	}, width, height)
}

func navigationClick(t *testing.T, tester *ui.Tester, label string) {
	t.Helper()
	if err := tester.Click(label); err != nil {
		t.Fatal(err)
	}
}

func TestTaskNavigationSearchAndOrdering(t *testing.T) {
	s := taskNavigationFixture()
	s.setTaskPinned(s.tasks[0], true)
	s.setTaskPinned(s.tasks[2], true)
	state := s.taskIndexBrowsingFor(false)
	for _, test := range []struct {
		query string
		want  []int
	}{
		{"", []int{2, 0, 1, 4}},
		{"  aPi  reQ-42 ", []int{0}},
		{"服务端", []int{2, 0}},
		{"billing payment", []int{0}},
		{"结算 页面", []int{1}},
		{"meta-17", []int{1}},
		{"已归档", []int{}},
		{"API 客户端", []int{}},
	} {
		t.Run(test.query, func(t *testing.T) {
			state.search = test.query
			if got := s.taskIndexRows(false); !slices.Equal(got, test.want) {
				t.Fatalf("query %q: rows %v, want %v", test.query, got, test.want)
			}
			if s.selected != 0 || len(s.taskNavigationFor().value.Recent) != 0 {
				t.Fatal("search or pin changed selection/visit history")
			}
		})
	}
	state.search = ""
	s.taskNavigationFor().value.Recent = []string{taskNavigationKey(s.tasks[1]), taskNavigationKey(s.tasks[2]), taskNavigationKey(s.tasks[0])}
	if displayed := s.taskIndexRows(false); !slices.Equal(displayed, []int{2, 0, 1, 4}) {
		t.Fatalf("legacy recent data must not change pin-first order or hide tasks: %v", displayed)
	}
	if displayed := s.taskIndexRows(true); !slices.Equal(displayed, []int{3}) {
		t.Fatalf("archive list must show all matching tasks: %v", displayed)
	}
}

func TestTaskNavigationControlsPreserveCurrentDraft(t *testing.T) {
	s := taskNavigationFixture()
	s.documentPath, s.documentContent, s.documentDirty = "draft.md", "尚未保存的修改", true
	s.documentDrafts["draft.md"], s.documentDirtyMap["draft.md"] = s.documentContent, true
	tester := taskNavigationTester(s, 240, 600)
	navigationChooseTaskMenu(t, tester, "修复超时", "置顶任务")
	if !s.taskIsPinned(s.tasks[2]) {
		t.Fatal("context menu did not pin the task")
	}
	navigationClick(t, tester, "搜索任务")
	tester.Type("不会匹配")
	if !tester.HasText("没有匹配的任务") {
		t.Fatal("search did not show the empty result state")
	}
	navigationClick(t, tester, "清除任务搜索")
	navigationClick(t, tester, "搜索任务")
	tester.Key(0, ui.KeyDown)
	if !tester.Focused("修复超时") {
		t.Fatal("search Down did not focus the first visible pinned task")
	}
	legacyRecent := []string{taskNavigationKey(s.tasks[1])}
	s.taskNavigationFor().value.Recent = slices.Clone(legacyRecent)
	if s.selected != 0 || s.documentPath != "draft.md" || s.documentContent != "尚未保存的修改" || !s.documentDirty || s.documentDrafts["draft.md"] != s.documentContent || !s.documentDirtyMap["draft.md"] || !slices.Equal(s.taskNavigationFor().value.Recent, legacyRecent) {
		t.Fatal("navigation controls changed the current task, draft, or legacy navigation data")
	}
	tester.Key(0, ui.KeyEnter)
	if s.selected != 2 || !slices.Equal(s.taskNavigationFor().value.Recent, legacyRecent) {
		t.Fatal("Enter did not open the focused task or changed legacy recent data")
	}
	row, found := tester.Find("修复超时")
	if !found {
		t.Fatal("selected pinned task is missing")
	}
	tester.Move(row.X+row.W/2, row.Y+row.H/2)
	navigationChooseTaskMenu(t, tester, "修复超时", "取消置顶")
	if s.taskIsPinned(s.tasks[2]) || s.selected != 2 || !slices.Equal(s.taskNavigationFor().value.Recent, legacyRecent) {
		t.Fatal("unpinning changed the current task or legacy navigation data")
	}
	navigationClick(t, tester, "搜索任务")
	tester.Type("REQ-42")
	tester.Key(0, ui.KeyEnter)
	if s.selected != 0 || !tester.Focused("支付 API 联调") || !slices.Equal(s.taskNavigationFor().value.Recent, legacyRecent) {
		t.Fatal("Enter in search did not open and focus its first matching task without changing legacy data")
	}
}

func TestTaskNavigationKeyboardUsesPinFirstOrderUntilActivation(t *testing.T) {
	s := taskNavigationFixture()
	s.setTaskPinned(s.tasks[0], true)
	s.setTaskPinned(s.tasks[2], true)
	legacyRecent := []string{taskNavigationKey(s.tasks[1]), taskNavigationKey(s.tasks[0]), taskNavigationKey(s.tasks[2])}
	s.taskNavigationFor().value.Recent = slices.Clone(legacyRecent)
	tester := taskNavigationTester(s, 240, 450)
	navigationClick(t, tester, "搜索任务")
	for _, title := range []string{"修复超时", "支付 API 联调", "界面体验"} {
		tester.Key(0, ui.KeyDown)
		if !tester.Focused(title) {
			t.Fatalf("Down should focus %q", title)
		}
		if s.selected != 0 || !slices.Equal(s.taskIndexRows(false), []int{2, 0, 1, 4}) || !slices.Equal(s.taskNavigationFor().value.Recent, legacyRecent) {
			t.Fatal("moving focus changed selection, pin order, or legacy recent data")
		}
	}
	tester.Key(0, ui.KeySpace)
	if s.selected != 1 || !tester.Focused("界面体验") || !slices.Equal(s.taskIndexRows(false), []int{2, 0, 1, 4}) || !slices.Equal(s.taskNavigationFor().value.Recent, legacyRecent) {
		t.Fatal("Space must open the task, retain focus, and preserve the stable pin-first order")
	}
	tester.Key(0, ui.KeyEnd)
	if !tester.Focused("历史项目任务") || s.selected != 1 {
		t.Fatal("End must focus the last displayed task without selecting it")
	}
	tester.Key(0, ui.KeyEnter)
	if s.selected != 4 || !slices.Equal(s.taskIndexRows(false), []int{2, 0, 1, 4}) || !slices.Equal(s.taskNavigationFor().value.Recent, legacyRecent) {
		t.Fatal("Enter changed the pin-first order or legacy recent data")
	}
}

func TestTaskNavigationKeyboardUsesFlatOrder(t *testing.T) {
	s := taskNavigationFixture()
	s.setTaskPinned(s.tasks[2], true)
	tester := taskNavigationTester(s, 240, 450)
	navigationClick(t, tester, "搜索任务")
	for _, title := range []string{"修复超时", "支付 API 联调", "界面体验", "历史项目任务"} {
		tester.Key(0, ui.KeyDown)
		if !tester.Focused(title) {
			t.Fatalf("keyboard order must match the flat list with pins first: expected %q", title)
		}
	}
	tester.Key(0, ui.KeyHome)
	tester.Key(0, ui.KeyUp)
	if !tester.Focused("搜索任务") || s.selected != 0 || len(s.taskNavigationFor().value.Recent) != 0 {
		t.Fatal("Up from the first row must return to search without selecting a task")
	}
}

func TestTaskNavigationCategoryFiltersAreIndependent(t *testing.T) {
	s := taskNavigationFixture()
	tester := taskNavigationTester(s, 240, 450)
	navigationClick(t, tester, "搜索任务")
	tester.Type("api")
	navigationClick(t, tester, "查看归档")
	if s.page != "archive" || s.taskIndexBrowsingFor(true).search != "" {
		t.Fatal("archive inherited the active task query")
	}
	navigationClick(t, tester, "搜索任务")
	tester.Type("归档")
	navigationClick(t, tester, "查看归档")
	active, archived := s.taskIndexBrowsingFor(false), s.taskIndexBrowsingFor(true)
	if s.page != "tasks" || active.search != "api" || archived.search != "归档" || len(s.taskNavigationFor().value.Recent) != 0 {
		t.Fatal("category switches mixed filters or changed legacy navigation data")
	}
	navigationClick(t, tester, "搜索任务")
	tester.Key(0, ui.KeyEscape)
	if active.search != "" || !tester.Focused("搜索任务") || archived.search != "归档" {
		t.Fatal("Escape must clear only the current category and retain search focus")
	}
}

func TestTaskNavigationCategorySwitchPreservesScroll(t *testing.T) {
	s := taskNavigationFixture()
	for index := 0; index < 20; index++ {
		for _, archived := range []bool{false, true} {
			s.tasks = append(s.tasks, task{
				id: fmt.Sprintf("scroll-%t-%d", archived, index), title: fmt.Sprintf("Scroll task %t %d", archived, index),
				createdAt: "2026-03-01", groupID: "backend", archived: archived,
			})
		}
	}
	tester := taskNavigationTester(s, 240, 400)
	tester.Scroll(100, 220, 0, 600)
	active := s.taskIndexBrowsingFor(false)
	activeY := active.scroll.Y
	if activeY <= 0 {
		t.Fatal("active fixture did not scroll")
	}
	navigationClick(t, tester, "查看归档")
	tester.Scroll(100, 220, 0, 350)
	archived := s.taskIndexBrowsingFor(true)
	archivedY := archived.scroll.Y
	if archivedY <= 0 {
		t.Fatal("archive fixture did not scroll")
	}
	navigationClick(t, tester, "查看归档")
	if active.scroll.Y != activeY {
		t.Fatalf("returning to active tasks moved the saved scroll: got %v, want %v", active.scroll.Y, activeY)
	}
	navigationClick(t, tester, "查看归档")
	if archived.scroll.Y != archivedY {
		t.Fatalf("returning to archived tasks moved the saved scroll: got %v, want %v", archived.scroll.Y, archivedY)
	}
}

func TestTaskNavigationRejectedSelectionKeepsRouteAndDraft(t *testing.T) {
	for _, route := range []string{"row", "archive", "sidebar", "task.create"} {
		t.Run(route, func(t *testing.T) {
			s := taskNavigationFixture()
			if route == "sidebar" {
				s.page, s.selected = "archive", 3
			}
			page, selected := s.page, s.selected
			active := s.taskIndexBrowsingFor(false)
			if route != "row" {
				active.search = "api"
			}
			search := active.search
			s.documentPath, s.documentContent, s.documentDirty = "draft.md", "尚未提交的输入", true
			s.documentDrafts["draft.md"], s.documentDirtyMap["draft.md"] = s.documentContent, true
			s.markdown = &markdownEditorState{path: "draft.md", doc: editor.NewDocument(s.documentContent, "navigation-composition")}
			s.markdown.doc.SetComposing(true)
			tester := taskNavigationTester(s, 240, 600)
			switch route {
			case "row":
				navigationClick(t, tester, "界面体验")
			case "archive":
				navigationClick(t, tester, "查看归档")
			case "sidebar":
				sidebar := ui.NewTester(func(c *ui.Context) {
					s.sidebar(c, setTheme(c, false), false)
				}, 180, 600)
				for _, item := range s.sidebarNavigationItems() {
					if item.page == "tasks" {
						navigationClick(t, sidebar, item.name)
					}
				}
			case "task.create":
				s.applyTaskSnapshotSelection(application.Action{Kind: route}, application.ActionResult{Data: map[string]any{"taskDirectoryName": "task-ui"}})
			}
			if s.page != page || s.selected != selected || active.search != search || active.revealTaskKey != "" || !s.documentDirty || s.documentContent != "尚未提交的输入" || s.documentDrafts["draft.md"] != s.documentContent || s.markdown == nil || !s.markdown.doc.Composing() || s.lastError != editor.ErrComposing.Error() || len(s.taskNavigationFor().value.Recent) != 0 {
				t.Fatalf("refused navigation changed the page, selection, filter, draft or legacy history: page=%s selected=%d search=%q error=%q", s.page, s.selected, active.search, s.lastError)
			}
		})
	}
}

func TestTaskNavigationPreferencesFollowTaskIdentity(t *testing.T) {
	s := taskNavigationFixture()
	s.desktopPreferences.enabled = true
	s.snapshot.Home = t.TempDir()
	if _, err := s.taskNavigationFor().store.Visit(taskNavigationKey(s.tasks[4])); err != nil {
		t.Fatal(err)
	}
	s.setTaskPinned(s.tasks[0], true)
	s.openTaskIndex(1)
	if s.taskNavigationFor().error != "" {
		t.Fatal(s.taskNavigationFor().error)
	}
	restarted := taskNavigationFixture()
	restarted.desktopPreferences.enabled = true
	restarted.snapshot.Home = s.snapshot.Home
	restarted.tasks[0].path = filepath.Join(t.TempDir(), "relocated-task")
	restarted.tasks[0].archived = true
	restarted.tasks[0].groupID = "new-group"
	if !restarted.taskIsPinned(restarted.tasks[0]) || !reflect.DeepEqual(restarted.taskNavigationFor().value.Recent, []string{taskNavigationKey(s.tasks[4])}) {
		t.Fatal("restart, archive, relocation or opening a task changed pins or legacy recent data")
	}
	restarted.page = "archive"
	if !taskNavigationTester(restarted, 240, 520).HasText("已置顶：" + restarted.tasks[0].title) {
		t.Fatal("restored archived pin has no visible status icon")
	}
	restarted.tasks[0].createdAt = "2026-02-01"
	if restarted.taskIsPinned(restarted.tasks[0]) {
		t.Fatal("a replacement task inherited a deleted task's pin")
	}
	restarted.snapshot.Home = t.TempDir()
	if len(restarted.taskNavigationFor().value.Recent) != 0 || len(restarted.taskNavigationFor().value.Pinned) != 0 {
		t.Fatal("navigation preferences leaked into another Silverwing home")
	}
}

func TestTaskNavigationPreferenceFailureDoesNotBlockOpening(t *testing.T) {
	s := taskNavigationFixture()
	s.desktopPreferences.enabled = true
	s.snapshot.Home = t.TempDir()
	path := filepath.Join(s.snapshot.Home, "task-navigation.json")
	content := []byte(`{"version":999,"pinned":[],"recent":[]}`)
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	s.openTaskIndex(2)
	s.setTaskPinned(s.tasks[0], true)
	if s.selected != 2 || len(s.taskNavigationFor().value.Recent) != 0 || s.taskNavigationFor().error == "" || s.taskIsPinned(s.tasks[0]) {
		t.Fatal("preference failure blocked opening or reported an unsaved pin as saved")
	}
	stored, err := os.ReadFile(path)
	if err != nil || string(stored) != string(content) {
		t.Fatal("navigation overwrote an unsupported preference file")
	}
	tester := taskNavigationTester(s, 240, 450)
	if !tester.HasText("任务导航偏好未保存") {
		t.Fatal("navigation persistence failure was not visible")
	}
}

func TestTaskNavigationCreationRevealsTargetOnce(t *testing.T) {
	for _, kind := range []string{"task.create"} {
		t.Run(kind, func(t *testing.T) {
			s := taskNavigationFixture()
			for index := 0; index < 18; index++ {
				s.tasks = append(s.tasks, task{id: fmt.Sprintf("later-%d", index), title: fmt.Sprintf("后续任务 %d", index), createdAt: "2026-03-01", groupID: "client"})
			}
			target := s.tasks[len(s.tasks)-1]
			active, archived := s.taskIndexBrowsingFor(false), s.taskIndexBrowsingFor(true)
			active.search = "不存在"
			archived.search = "归档查询"
			tester := taskNavigationTester(s, 240, 400)
			action := application.Action{Kind: kind, TaskID: target.id}
			s.applyTaskSnapshotSelection(action, application.ActionResult{Data: map[string]any{"taskDirectoryName": target.id}})
			tester.Frame()
			bounds, found := tester.Find(target.title)
			if !found || bounds.Y < 80 || bounds.Y+bounds.H > 352 || active.scroll.Y <= 0 || active.revealTaskKey != "" {
				t.Fatalf("new task was not scrolled into view exactly once: bounds=%+v found=%t scroll=%+v reveal=%q", bounds, found, active.scroll, active.revealTaskKey)
			}
			if s.currentTask().id != target.id || active.search != "" || archived.search != "归档查询" || len(s.taskNavigationFor().value.Recent) != 0 {
				t.Fatal("lifecycle reveal failed to isolate filters or created a fake visit")
			}
			tester.Scroll(100, 220, 0, 2000)
			position := active.scroll.Y
			tester.Frame()
			if position != active.scroll.Y {
				t.Fatal("a later frame scrolled back to the selected task after the user moved away")
			}
		})
	}
}

func TestTaskNavigationNarrowLayout(t *testing.T) {
	for _, width := range []int{160, 240, 340} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%t", width, dark), func(t *testing.T) {
				s := taskNavigationFixture()
				s.dark = dark
				s.tasks[0].title = "支付平台跨项目联调与异常场景修复的较长任务名称"
				s.tasks[0].status = "等待研发负责人和业务测试人员联合确认"
				s.setTaskPinned(s.tasks[0], true)
				tester := taskNavigationTester(s, width, 520)
				for _, label := range []string{"搜索任务", s.tasks[0].title, "查看归档"} {
					bounds, found := tester.Find(label)
					if !found || bounds.X < 0 || bounds.X+bounds.W > float32(width) || bounds.W < 20 || bounds.H < 20 {
						t.Fatalf("control %q is clipped or too small: %+v found=%t", label, bounds, found)
					}
				}
				if tester.HasText("全部") || tester.HasText("最近访问") {
					t.Fatal("removed task list mode controls are still visible")
				}
				status, found := tester.Find(s.tasks[0].status)
				if !found || status.X < 0 || status.X+status.W > float32(width)-8 {
					t.Fatalf("long requirement status exceeds the task row: status=%+v", status)
				}
				row, _ := tester.Find(s.tasks[0].title)
				centerOffset := status.Y + status.H/2 - (row.Y + row.H/2)
				if centerOffset < -1 || centerOffset > 1 {
					t.Fatalf("task name and status must share a line: row=%+v status=%+v", row, status)
				}
				captureDesktopTestFrame(t, tester, fmt.Sprintf("task-navigation-%d-dark-%t", width, dark))
			})
		}
	}
}
