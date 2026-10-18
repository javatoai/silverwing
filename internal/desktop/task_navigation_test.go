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
	s.taskNavigationFor().value.Recent = []string{taskNavigationKey(s.tasks[2]), taskNavigationKey(s.tasks[3]), "removed-task", taskNavigationKey(s.tasks[0])}
	state.recentOnly = true
	s.taskIndexBrowsingFor(true).recentOnly = true
	if got := s.taskIndexRows(false); !slices.Equal(got, []int{2, 0}) {
		t.Fatalf("active recent order: %v", got)
	}
	if got := s.taskIndexRows(true); !slices.Equal(got, []int{3}) {
		t.Fatalf("archive recent order: %v", got)
	}
	state.recentOnly = false
	sections := s.taskIndexSections(false, s.taskIndexRows(false))
	var displayed []int
	for _, section := range sections {
		displayed = append(displayed, section.rows...)
	}
	if !slices.Equal(displayed, []int{2, 0, 1, 4}) || sections[0].title != "置顶" || sections[len(sections)-1].title != "其他任务" {
		t.Fatalf("pins must be unique and tasks without a configured group must remain visible: %+v", sections)
	}
}

func TestTaskNavigationControlsPreserveCurrentDraft(t *testing.T) {
	s := taskNavigationFixture()
	s.documentPath, s.documentContent, s.documentDirty = "draft.md", "尚未保存的修改", true
	s.documentDrafts["draft.md"], s.documentDirtyMap["draft.md"] = s.documentContent, true
	tester := taskNavigationTester(s, 240, 600)
	navigationClick(t, tester, "置顶任务：修复超时")
	if !s.taskIsPinned(s.tasks[2]) {
		t.Fatal("pin button did not pin the task")
	}
	navigationClick(t, tester, "搜索任务")
	tester.Type("不会匹配")
	if !tester.HasText("没有匹配的任务") {
		t.Fatal("search did not show the empty result state")
	}
	navigationClick(t, tester, "最近访问")
	navigationClick(t, tester, "清除任务搜索")
	if !tester.HasText("暂无最近访问的任务") {
		t.Fatal("pinning and filtering must not count as opening a task")
	}
	navigationClick(t, tester, "全部")
	navigationClick(t, tester, "搜索任务")
	tester.Key(0, ui.KeyDown)
	if !tester.Focused("修复超时") {
		t.Fatal("search Down did not focus the first visible pinned task")
	}
	if s.selected != 0 || s.documentPath != "draft.md" || s.documentContent != "尚未保存的修改" || !s.documentDirty || s.documentDrafts["draft.md"] != s.documentContent || !s.documentDirtyMap["draft.md"] || len(s.taskNavigationFor().value.Recent) != 0 {
		t.Fatal("navigation controls changed the current task, draft, or visit history")
	}
	tester.Key(0, ui.KeyEnter)
	if s.selected != 2 || !slices.Equal(s.taskNavigationFor().value.Recent, []string{taskNavigationKey(s.tasks[2])}) {
		t.Fatal("Enter did not explicitly open and record the focused task")
	}
	navigationClick(t, tester, "取消置顶：修复超时")
	if s.taskIsPinned(s.tasks[2]) || s.selected != 2 || len(s.taskNavigationFor().value.Recent) != 1 {
		t.Fatal("unpinning changed the current task or history")
	}
	navigationClick(t, tester, "搜索任务")
	tester.Type("REQ-42")
	tester.Key(0, ui.KeyEnter)
	if s.selected != 0 || !tester.Focused("支付 API 联调") || !slices.Equal(s.taskNavigationFor().value.Recent, []string{taskNavigationKey(s.tasks[0]), taskNavigationKey(s.tasks[2])}) {
		t.Fatal("Enter in search did not open and focus its first matching task")
	}
}

func TestTaskNavigationRecentKeyboardKeepsVisitOrderUntilActivation(t *testing.T) {
	s := taskNavigationFixture()
	for _, index := range []int{0, 1, 2} {
		s.openTaskIndex(index)
	}
	s.taskIndexBrowsingFor(false).recentOnly = true
	tester := taskNavigationTester(s, 240, 450)
	navigationClick(t, tester, "搜索任务")
	for _, title := range []string{"修复超时", "界面体验", "支付 API 联调"} {
		tester.Key(0, ui.KeyDown)
		if !tester.Focused(title) {
			t.Fatalf("Down should focus %q", title)
		}
		if s.selected != 2 || !slices.Equal(s.taskIndexRows(false), []int{2, 1, 0}) {
			t.Fatal("moving focus reordered recent tasks before activation")
		}
	}
	tester.Key(0, ui.KeySpace)
	if s.selected != 0 || !tester.Focused("支付 API 联调") || !slices.Equal(s.taskIndexRows(false), []int{0, 2, 1}) {
		t.Fatal("Space must open the task and retain focus after moving it to the top")
	}
	tester.Key(0, ui.KeyEnd)
	if !tester.Focused("界面体验") || s.selected != 0 {
		t.Fatal("End must focus the last displayed task without selecting it")
	}
	tester.Key(0, ui.KeyEnter)
	if s.selected != 1 || !slices.Equal(s.taskIndexRows(false), []int{1, 0, 2}) {
		t.Fatal("Enter did not update recent order")
	}
}

func TestTaskNavigationKeyboardSkipsCollapsedGroups(t *testing.T) {
	s := taskNavigationFixture()
	s.setTaskPinned(s.tasks[2], true)
	s.taskIndexBrowsingFor(false).collapsedGroups["backend"] = true
	tester := taskNavigationTester(s, 240, 450)
	navigationClick(t, tester, "搜索任务")
	for _, title := range []string{"修复超时", "界面体验", "历史项目任务"} {
		tester.Key(0, ui.KeyDown)
		if !tester.Focused(title) {
			t.Fatalf("keyboard order must match pinned, expanded groups and orphan rows: expected %q", title)
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
	navigationClick(t, tester, "最近访问")
	navigationClick(t, tester, "查看归档")
	active, archived := s.taskIndexBrowsingFor(false), s.taskIndexBrowsingFor(true)
	if s.page != "tasks" || active.search != "api" || active.recentOnly || archived.search != "归档" || !archived.recentOnly || len(s.taskNavigationFor().value.Recent) != 0 {
		t.Fatal("category switches mixed filters or created a fake visit")
	}
	navigationClick(t, tester, "搜索任务")
	tester.Key(0, ui.KeyEscape)
	if active.search != "" || !tester.Focused("搜索任务") || archived.search != "归档" {
		t.Fatal("Escape must clear only the current category and retain search focus")
	}
}

func TestTaskNavigationRejectedSelectionKeepsRouteAndDraft(t *testing.T) {
	for _, route := range []string{"row", "archive", "sidebar", "task.create", "task.restore"} {
		t.Run(route, func(t *testing.T) {
			s := taskNavigationFixture()
			if route == "sidebar" || route == "task.restore" {
				s.page, s.selected = "archive", 3
			}
			page, selected := s.page, s.selected
			active := s.taskIndexBrowsingFor(false)
			if route != "row" {
				active.search, active.recentOnly, active.collapsedGroups["client"] = "api", true, true
			}
			search, recent := active.search, active.recentOnly
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
			case "task.restore":
				s.applyTaskLifecycleSnapshotSelection(application.Action{Kind: route, TaskID: "task-ui"}, application.ActionResult{})
			}
			if s.page != page || s.selected != selected || active.search != search || active.recentOnly != recent || active.revealTaskKey != "" || !s.documentDirty || s.documentContent != "尚未提交的输入" || s.documentDrafts["draft.md"] != s.documentContent || s.markdown == nil || !s.markdown.doc.Composing() || s.lastError != editor.ErrComposing.Error() || len(s.taskNavigationFor().value.Recent) != 0 {
				t.Fatalf("refused navigation changed the page, selection, filter, draft or history: page=%s selected=%d search=%q recent=%t error=%q", s.page, s.selected, active.search, active.recentOnly, s.lastError)
			}
		})
	}
}

func TestTaskNavigationPreferencesFollowTaskIdentity(t *testing.T) {
	s := taskNavigationFixture()
	s.desktopPreferences.enabled = true
	s.snapshot.Home = t.TempDir()
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
	if !restarted.taskIsPinned(restarted.tasks[0]) || !reflect.DeepEqual(restarted.taskNavigationFor().value.Recent, s.taskNavigationFor().value.Recent) {
		t.Fatal("restart, archive or task-root relocation lost personal navigation")
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
	if s.selected != 2 || len(s.taskNavigationFor().value.Recent) != 1 || s.taskNavigationFor().error == "" || s.taskIsPinned(s.tasks[0]) {
		t.Fatal("preference failure blocked opening, lost the session visit or reported an unsaved pin as saved")
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

func TestTaskNavigationLifecycleRevealsTargetOnce(t *testing.T) {
	for _, kind := range []string{"task.create", "task.restore"} {
		t.Run(kind, func(t *testing.T) {
			s := taskNavigationFixture()
			for index := 0; index < 18; index++ {
				s.tasks = append(s.tasks, task{id: fmt.Sprintf("later-%d", index), title: fmt.Sprintf("后续任务 %d", index), createdAt: "2026-03-01", groupID: "client"})
			}
			target := s.tasks[len(s.tasks)-1]
			active, archived := s.taskIndexBrowsingFor(false), s.taskIndexBrowsingFor(true)
			active.search, active.recentOnly, active.collapsedGroups["client"] = "不存在", true, true
			archived.search, archived.recentOnly = "归档查询", true
			tester := taskNavigationTester(s, 240, 400)
			action := application.Action{Kind: kind, TaskID: target.id}
			if kind == "task.create" {
				s.applyTaskSnapshotSelection(action, application.ActionResult{Data: map[string]any{"taskDirectoryName": target.id}})
			} else {
				s.applyTaskLifecycleSnapshotSelection(action, application.ActionResult{})
			}
			tester.Frame()
			bounds, found := tester.Find(target.title)
			if !found || bounds.Y < 80 || bounds.Y+bounds.H > 352 || active.scroll.Y <= 0 || active.revealTaskKey != "" {
				t.Fatalf("new/restored task was not scrolled into view exactly once: bounds=%+v found=%t scroll=%+v reveal=%q", bounds, found, active.scroll, active.revealTaskKey)
			}
			if s.currentTask().id != target.id || active.search != "" || active.recentOnly || active.collapsedGroups["client"] || archived.search != "归档查询" || !archived.recentOnly || len(s.taskNavigationFor().value.Recent) != 0 {
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
				for _, label := range []string{"搜索任务", "全部", "最近访问", "取消置顶：" + s.tasks[0].title, "查看归档"} {
					bounds, found := tester.Find(label)
					if !found || bounds.X < 0 || bounds.X+bounds.W > float32(width) || bounds.W < 20 || bounds.H < 20 {
						t.Fatalf("control %q is clipped or too small: %+v found=%t", label, bounds, found)
					}
				}
				status, found := tester.Find(s.tasks[0].status)
				pin, pinFound := tester.Find("取消置顶：" + s.tasks[0].title)
				if !found || !pinFound || status.X+status.W > pin.X {
					t.Fatalf("long requirement status overlaps the pin control: status=%+v pin=%+v", status, pin)
				}
				captureReleaseNotice(t, tester, fmt.Sprintf("task-navigation-%d-dark-%t", width, dark))
			})
		}
	}
}
