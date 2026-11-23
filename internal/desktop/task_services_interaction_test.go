package desktop

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/production"

	"github.com/egoist/mygo/ui"
	code "github.com/egoist/mygo/ui/editor"
)

func activateTaskServicesFixture(t *testing.T, s *app, page, environment string) (*taskServicesView, *taskServiceRow) {
	t.Helper()
	s.page = page
	if page == "archive" {
		s.taskUnit.state.tasks[0].archived = true
	}
	v, key := s.taskServicesProjection()
	if v == nil {
		t.Fatal("task services projection is unavailable")
	}
	v.active = true
	v.environment = environment
	s.taskServicesUnit.current = v
	s.taskServicesUnit.views = map[string]*taskServicesView{key: v}
	for _, row := range v.rows {
		if row.environment == environment {
			row.loaded = true
			row.pods = []production.RuntimePod{{Name: row.serviceID + "-pod", Namespace: "fp", Version: "v1", Ready: true}}
		}
	}
	for _, row := range v.visibleRows() {
		return v, row
	}
	t.Fatal("task services projection has no visible row")
	return nil, nil
}

func renderTaskServicesWithDialog(c *ui.Context, s *app, p palette) {
	s.renderTaskServices(c, p)
	s.renderTaskServiceDialog(c, p)
}

func newTaskServicesTester(s *app, width, height int) *ui.Tester {
	return ui.NewTester(func(c *ui.Context) {
		renderTaskServicesWithDialog(c, s, setTheme(c, false))
	}, width, height)
}

func taskServiceOpenTarget(row *taskServiceRow, source string) string {
	switch source {
	case "count":
		return "查看 " + row.name + " " + row.environment + " Pod 明细"
	case "detail":
		return row.name + " " + row.environment + " Pod 明细"
	default:
		return row.name + " " + strings.ToUpper(row.environment)
	}
}

func TestTaskServicesPodDialogOpensFromRowsAndActions(t *testing.T) {
	for _, page := range []string{"tasks", "archive"} {
		for _, environment := range []string{"uat", "prod"} {
			for _, source := range []string{"row", "count", "detail"} {
				t.Run(fmt.Sprintf("%s-%s-%s", page, environment, source), func(t *testing.T) {
					s := taskServicesDesktopFixture(t)
					v, row := activateTaskServicesFixture(t, s, page, environment)
					tester := newTaskServicesTester(s, 1100, 650)
					if err := tester.Click(taskServiceOpenTarget(row, source)); err != nil {
						t.Fatal(err)
					}
					dialog := v.dialog
					if dialog == nil || !dialog.open || dialog.kind != taskServicePodsDetail || dialog.row != row {
						t.Fatalf("%s did not open the selected environment's Pod dialog", source)
					}
					if !tester.HasText(row.serviceID+"-pod") || !tester.HasText("Pod / 分组") {
						t.Fatal("Pod dialog omitted the cached Pod rows")
					}
					if len(s.taskServicesUnit.jobs) != 0 {
						t.Fatal("opening cached Pod details started an extra live query")
					}
				})
			}
		}
	}
}

func TestTaskServicesPodDialogOpensFromKeyboard(t *testing.T) {
	for _, environment := range []string{"uat", "prod"} {
		t.Run(environment, func(t *testing.T) {
			s := taskServicesDesktopFixture(t)
			v, row := activateTaskServicesFixture(t, s, "tasks", environment)
			tester := newTaskServicesTester(s, 1100, 650)
			if err := tester.Click(taskServiceOpenTarget(row, "row")); err != nil {
				t.Fatal(err)
			}
			tester.Key(0, ui.KeyEscape)
			if v.dialog != nil {
				t.Fatal("Escape did not close the first Pod dialog")
			}
			tester.Key(0, ui.KeyEnter)
			if v.dialog == nil || !v.dialog.open || v.dialog.row != row || v.dialog.kind != taskServicePodsDetail {
				t.Fatal("Enter on the restored service-row focus did not reopen Pod details")
			}
		})
	}
}

func TestTaskServicesLongVersionKeepsCountVisible(t *testing.T) {
	s := taskServicesDesktopFixture(t)
	v, _ := activateTaskServicesFixture(t, s, "tasks", "uat")
	row := v.rows[0]
	row.pods = []production.RuntimePod{{Name: "p1", Version: strings.Repeat("very-long-version-", 20)}}
	tester := newTaskServicesTester(s, 1100, 650)
	count, ok := tester.Find("× 1")
	if !ok || count.W <= 0 || count.X+count.W > 1100 {
		t.Fatal("long version clipped its independent count", count, ok)
	}
}

func TestTaskServicesEnvironmentTabsAndDialogActions(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprintf("dark-%t", dark), func(t *testing.T) {
			s := taskServicesDesktopFixture(t)
			v, _ := activateTaskServicesFixture(t, s, "tasks", "uat")
			for _, row := range v.rows {
				row.loaded = true
				row.pods = []production.RuntimePod{
					{Name: row.environment + "-one", Namespace: "fp", Ready: true, CreatedTime: "2026-11-02 12:34:56"},
					{Name: row.environment + "-two", Namespace: "fp", CreatedTime: "2026-11-01 10:11:12"},
				}
			}
			tester := ui.NewTester(func(c *ui.Context) {
				renderTaskServicesWithDialog(c, s, setTheme(c, dark))
			}, 1200, 650)
			if _, ok := tester.Find("Second service PROD"); ok {
				t.Fatal("hidden PROD row rendered under UAT")
			}
			for _, removed := range []string{"2 个就绪", "已查询", "2026-11-01 10:11:12"} {
				if tester.HasText(removed) {
					t.Fatal("redundant summary remains", removed)
				}
			}
			if err := tester.Click("PROD 环境"); err != nil {
				t.Fatal(err)
			}
			if v.environment != "prod" {
				t.Fatal("PROD tab did not switch environment")
			}
			if _, ok := tester.Find("Second service UAT"); ok {
				t.Fatal("hidden UAT row rendered under PROD")
			}
			row := v.rows[1]
			if err := tester.Click(taskServiceOpenTarget(row, "detail")); err != nil {
				t.Fatal(err)
			}
			if v.dialog == nil || v.dialog.row != row || v.dialog.kind != taskServicePodsDetail || !tester.HasText("2026-11-01 10:11:12") {
				t.Fatal("Pod action did not open full Pod details")
			}
			if err := tester.Click("关闭"); err != nil {
				t.Fatal(err)
			}
			if v.dialog != nil {
				t.Fatal("close action left Pod dialog open")
			}
			if err := tester.Click("查看 Second service prod 配置"); err != nil {
				t.Fatal(err)
			}
			if v.dialog == nil || v.dialog.row != row || v.dialog.kind != taskServiceConfigDetail {
				t.Fatal("configuration action did not open the selected environment")
			}
		})
	}
}

func TestTaskServicesRowsFitContentPane(t *testing.T) {
	const sidebar, inset, height = 260, 16, 680
	const paneLabel = "Service content pane"
	const created = "2026-11-02 12:34:56"
	longVersion := strings.Repeat("very-long-version-", 20)
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprintf("dark-%t", dark), func(t *testing.T) {
			s := taskServicesDesktopFixture(t)
			v, row := activateTaskServicesFixture(t, s, "tasks", "uat")
			row.pods = []production.RuntimePod{
				{Name: "first-pod", Version: longVersion, CreatedTime: created},
				{Name: "second-pod", Version: longVersion},
				{Name: "third-pod", Version: "v2.3.4"},
			}
			var createdTextWidth, versionCountWidth float32
			tester := ui.NewTester(func(c *ui.Context) {
				p := setTheme(c, dark)
				createdTextWidth, _ = c.MeasureText(0, ui.Span{Text: created, Size: 12})
				versionCountWidth, _ = c.MeasureText(0, ui.Span{Text: "× 2", Size: 12})
				ui.Row(c).Fill().Padding(inset).Children(func() {
					ui.Box(c).Width(sidebar).Shrink(0)
					ui.Column(c).Grow(1).FillHeight().MinWidth(0).Label(paneLabel).Children(func() { s.renderTaskServices(c, p) })
				})
				s.renderTaskServiceDialog(c, p)
			}, sidebar+2*inset+1260, height)
			tester.SetLocale("en-US")
			fullSize := map[string]ui.Rect{}
			for _, layout := range []struct {
				name    string
				width   int
				compact bool
			}{
				{"wide", 1260, false}, {"screenshot", 820, false}, {"narrow", 480, true}, {"small", 350, true}, {"restored", 820, false},
			} {
				t.Run(layout.name, func(t *testing.T) {
					tester.SetSize(sidebar+2*inset+layout.width, height)
					pane, ok := tester.Find(paneLabel)
					wantPane := ui.Rect{X: sidebar + inset, Y: inset, W: float32(layout.width), H: height - 2*inset}
					if !ok || pane != wantPane {
						t.Fatalf("service content pane = %+v, want %+v", pane, wantPane)
					}
					for _, control := range []string{"UAT 环境", "PROD 环境", "刷新当前环境全部服务"} {
						taskServicesBoundsWithin(t, tester, pane, control)
					}
					for _, service := range v.visibleRows() {
						rowBounds := taskServicesBoundsWithin(t, tester, pane, taskServiceOpenTarget(service, "row"))
						scope := service.name + " " + service.environment
						actions := []string{taskServiceOpenTarget(service, "detail"), "查看 " + scope + " 配置", "修改 " + scope + " 配置", "刷新 " + scope}
						more := "更多 · " + service.name
						moreBounds := taskServicesBoundsWithin(t, tester, rowBounds, more)
						baseline, seen := fullSize[more]
						if !seen {
							fullSize[more] = moreBounds
						} else if moreBounds.W < baseline.W-1 || moreBounds.H < baseline.H-1 {
							t.Errorf("overflow button was clipped from %+v to %+v", baseline, moreBounds)
						}
						for _, action := range actions {
							if layout.compact {
								if _, found := tester.Find(action); found {
									t.Errorf("compact table still renders %q inline", action)
								}
								continue
							}
							bounds := taskServicesBoundsWithin(t, tester, rowBounds, action)
							if baseline, seen := fullSize[action]; seen && (bounds.W < baseline.W-1 || bounds.H < baseline.H-1) {
								t.Errorf("inline action %q was clipped from %+v to %+v", action, baseline, bounds)
							} else if !seen {
								fullSize[action] = bounds
							}
							if bounds.X+bounds.W > moreBounds.X+0.5 || bounds.Y >= moreBounds.Y+moreBounds.H || moreBounds.Y >= bounds.Y+bounds.H {
								t.Errorf("inline action %q at %+v must precede the overflow button on the same line at %+v", action, bounds, moreBounds)
							}
						}
					}
					rowBounds := taskServicesBoundsWithin(t, tester, pane, taskServiceOpenTarget(row, "row"))
					var previousHeader, previousCell ui.Rect
					for _, column := range []struct{ heading, value string }{
						{"服务", row.name}, {"Pod 数量", "3 个"}, {"运行版本", longVersion}, {"Pod 创建时间", created}, {"操作", "更多 · " + row.name},
					} {
						heading := taskServicesBoundsWithin(t, tester, pane, column.heading)
						cell := taskServicesBoundsWithin(t, tester, rowBounds, column.value)
						if heading.Y+heading.H > rowBounds.Y || heading.X < previousHeader.X+previousHeader.W-0.5 || cell.X < previousCell.X+previousCell.W-0.5 {
							t.Errorf("column %q at %+v (cell %+v) does not align in the table", column.heading, heading, cell)
						}
						previousHeader, previousCell = heading, cell
					}
					taskServicesBoundsWithin(t, tester, rowBounds, "v2.3.4")
					version := taskServicesBoundsWithin(t, tester, rowBounds, longVersion)
					count := taskServicesBoundsWithin(t, tester, rowBounds, "× 2")
					stamp := taskServicesBoundsWithin(t, tester, rowBounds, created)
					if count.W < versionCountWidth-1 || version.X+version.W > count.X+0.5 || count.X+count.W > stamp.X+0.5 || !layout.compact && stamp.W < createdTextWidth-1 {
						t.Errorf("version count or creation time lacks space: version=%+v count=%+v creation=%+v", version, count, stamp)
					}
					detailAction := taskServiceOpenTarget(row, "detail")
					if layout.compact {
						if err := tester.Click("更多 · " + row.name); err != nil {
							t.Fatal(err)
						}
						for _, action := range []string{"Pod 明细", "查看配置", "修改配置", "刷新", "查看配置审批"} {
							if _, found := tester.Find(action); !found {
								t.Errorf("compact menu omitted %q", action)
							}
						}
						detailAction = "Pod 明细"
					}
					if err := tester.Click(detailAction); err != nil {
						t.Fatal(err)
					}
					if v.dialog == nil || v.dialog.row != row || v.dialog.kind != taskServicePodsDetail {
						t.Fatal("Pod details click did not open the selected service")
					}
					tester.Key(0, ui.KeyEscape)
				})
			}
		})
	}
}

func taskServicesBoundsWithin(t *testing.T, tester *ui.Tester, parent ui.Rect, label string) ui.Rect {
	t.Helper()
	bounds, ok := tester.Find(label)
	if !ok || bounds.W <= 0 || bounds.H <= 0 || bounds.X < parent.X || bounds.Y < parent.Y || bounds.X+bounds.W > parent.X+parent.W+0.5 || bounds.Y+bounds.H > parent.Y+parent.H+0.5 {
		t.Fatalf("%q bounds %+v (found %t) are outside %+v", label, bounds, ok, parent)
	}
	return bounds
}

func TestTaskServicesGenbuActionsFollowTaskScope(t *testing.T) {
	for _, page := range []string{"tasks", "archive"} {
		t.Run(page, func(t *testing.T) {
			s := taskServicesDesktopFixture(t)
			v, row := activateTaskServicesFixture(t, s, page, "uat")
			tester := newTaskServicesTester(s, 1100, 650)
			edit := "修改 " + row.name + " uat 配置"
			if _, found := tester.Find(edit); found != (page == "tasks") {
				t.Fatalf("edit access did not follow task scope: found=%t", found)
			}
			if page == "tasks" {
				if err := tester.Click(edit); err != nil {
					t.Fatal(err)
				}
				if v.dialog == nil || v.dialog.kind != taskServiceConfigEditDetail || v.dialog.row != row {
					t.Fatal("edit action did not open this service's dialog")
				}
				if err := tester.Click("关闭"); err != nil {
					t.Fatal(err)
				}
			}
			if err := tester.Click("更多 · " + row.name); err != nil {
				t.Fatal(err)
			}
			if tester.HasText("构建日志") || !tester.HasText("查看配置审批") {
				t.Fatal("service menu must offer approvals with no build log entry")
			}
			if err := tester.Click("查看配置审批"); err != nil {
				t.Fatal(err)
			}
			if s.genbuApprovals == nil || s.genbuApprovals.source.row != row || v.dialog != nil {
				t.Fatal("approval action did not open independently for the selected service")
			}
			s.selectTaskServicesEnvironment(v, "prod")
			if s.genbuApprovals != nil {
				t.Fatal("previous environment's approval dialog survived the environment switch")
			}
			tester.SetSize(480, 650)
			if err := tester.Click("更多 · " + row.name); err != nil {
				t.Fatal(err)
			}
			if _, found := tester.Find("修改配置"); found != (page == "tasks") {
				t.Fatalf("compact menu edit access did not follow task scope: found=%t", found)
			}
			if _, found := tester.Find("查看配置审批"); !found {
				t.Fatal("compact menu omitted configuration approvals")
			}
		})
	}
}

func TestTaskServicesResizeSettlesColumnsBeforeMouseInput(t *testing.T) {
	s := taskServicesDesktopFixture(t)
	v, row := activateTaskServicesFixture(t, s, "tasks", "uat")
	tester := newTaskServicesTester(s, 1000, 650)
	for _, width := range []int{1400, 940, 1200} {
		tester.SetSize(width, 650)
		bounds, ok := tester.Find("Second service UAT")
		if !ok || bounds.W != float32(width) {
			t.Fatal("resized table did not settle", bounds, width)
		}
		if err := tester.Click("查看 Second service uat Pod 明细"); err != nil {
			t.Fatal(err)
		}
		if v.dialog == nil || v.dialog.row != row {
			t.Fatal("resized count moved during pointer gesture", width)
		}
		if err := tester.Click("关闭"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTaskServicesRowRefreshScopeAndDialogReplacement(t *testing.T) {
	for _, environment := range []string{"uat", "prod"} {
		t.Run(environment, func(t *testing.T) {
			s := taskServicesDesktopFixture(t)
			v, row := activateTaskServicesFixture(t, s, "tasks", environment)
			for _, other := range v.rows {
				other.error = "untouched"
			}
			tester := newTaskServicesTester(s, 1100, 650)
			if err := tester.Click("刷新 " + row.name + " " + environment); err != nil {
				t.Fatal(err)
			}
			if row.error == "untouched" {
				t.Fatal("row refresh did not update its target service")
			}
			for _, other := range v.rows {
				if other != row && other.error != "untouched" {
					t.Fatal("row refresh affected another service or environment", other)
				}
			}
			if err := tester.Click(taskServiceOpenTarget(row, "detail")); err != nil {
				t.Fatal(err)
			}
			firstDialog := v.dialog
			other := v.visibleRows()[0]
			if other == row {
				other = v.visibleRows()[1]
			}
			if err := tester.Click("关闭"); err != nil {
				t.Fatal(err)
			}
			if err := tester.Click(taskServiceOpenTarget(other, "detail")); err != nil {
				t.Fatal(err)
			}
			if v.dialog == nil || v.dialog == firstDialog || v.dialog.row != other {
				t.Fatal("opening another service did not replace the dialog owner")
			}
		})
	}
}

func TestTaskServicesDialogDismissesByCloseBackdropAndEscape(t *testing.T) {
	for _, method := range []string{"close", "backdrop", "escape"} {
		t.Run(method, func(t *testing.T) {
			s := taskServicesDesktopFixture(t)
			v, row := activateTaskServicesFixture(t, s, "tasks", "uat")
			tester := newTaskServicesTester(s, 1100, 650)
			if err := tester.Click(taskServiceOpenTarget(row, "count")); err != nil {
				t.Fatal(err)
			}
			if v.dialog == nil {
				t.Fatal("count did not open the Pod dialog")
			}
			switch method {
			case "close":
				if err := tester.Click("关闭"); err != nil {
					t.Fatal(err)
				}
			case "backdrop":
				tester.ClickAt(5, 5)
			case "escape":
				tester.Key(0, ui.KeyEscape)
			}
			if v.dialog != nil {
				t.Fatalf("%s left the dialog open", method)
			}
		})
	}
}

func addTaskServiceRows(t *testing.T, s *app, count int) {
	t.Helper()
	var config domain.AppConfig
	if err := json.Unmarshal([]byte(s.snapshot.ConfigJSON), &config); err != nil {
		t.Fatal(err)
	}
	task := &s.taskUnit.state.tasks[0]
	serviceIDs := []string{"s2", "s1"}
	for index := 0; index < count; index++ {
		serviceID := fmt.Sprintf("extra-%02d", index)
		name := fmt.Sprintf("Extra %02d", index)
		service := config.Services[0]
		service.ID = serviceID
		service.DisplayName = name
		service.GenbuServiceName = "alias-" + serviceID
		service.RepositoryID = "repo-" + serviceID
		config.Services = append(config.Services, service)
		repository := config.Repositories[0]
		repository.ID = service.RepositoryID
		repository.Name = serviceID
		repository.RootPath += "/" + serviceID
		repository.GitCommonDirectory += "/" + serviceID
		config.Repositories = append(config.Repositories, repository)
		serviceIDs = append(serviceIDs, serviceID)
	}
	task.workspaces = nil
	task.serviceIDs = serviceIDs
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	s.snapshot.ConfigJSON = string(data)
}

func TestTaskServicesDialogScrollingIsIsolatedFromServiceList(t *testing.T) {
	s := taskServicesDesktopFixture(t)
	addTaskServiceRows(t, s, 14)
	v, _ := activateTaskServicesFixture(t, s, "tasks", "uat")
	var pageBounds ui.Rect
	tester := ui.NewTester(func(c *ui.Context) {
		renderTaskServicesWithDialog(c, s, setTheme(c, false))
		pageBounds = v.canvas.Bounds(c)
	}, 800, 640)
	initialBounds := pageBounds
	tester.Scroll(420, 500, 0, 150)
	if v.vertical.Y <= 0 {
		t.Fatal("service rows did not scroll before opening the dialog")
	}
	pageScroll := v.vertical
	row := v.rows[8]
	row.pods = make([]production.RuntimePod, 80)
	for index := range row.pods {
		row.pods[index] = production.RuntimePod{Name: fmt.Sprintf("pod-%02d", index), Namespace: "fp", Version: "v1", Phase: "Running", Ready: true}
	}
	if err := tester.Click(taskServiceOpenTarget(row, "count")); err != nil {
		t.Fatal(err)
	}
	detail := v.dialog
	if detail == nil || detail.row != row {
		t.Fatal("scrolled service count did not open its Pod dialog")
	}
	if pageBounds != initialBounds || v.vertical != pageScroll {
		t.Fatalf("opening the modal changed the underlying page geometry or scroll: bounds %+v -> %+v, scroll %+v -> %+v", initialBounds, pageBounds, pageScroll, v.vertical)
	}
	firstPod, ok := tester.Find("pod-00")
	if !ok {
		t.Fatal("modal did not render its first Pod")
	}
	podHeader, ok := tester.Find("Pod / 分组")
	if !ok {
		t.Fatal("modal did not render its Pod table header")
	}
	tester.Scroll(podHeader.X+podHeader.W/2, podHeader.Y+podHeader.H/2, 260, 0)
	tester.Scroll(firstPod.X+firstPod.W/2, firstPod.Y+firstPod.H/2, 0, 420)
	if detail.vertical.Y <= 0 {
		t.Fatal("Pod rows did not scroll inside the modal")
	}
	if detail.horizontal.X <= 0 {
		t.Fatal("wide Pod columns did not scroll inside the modal")
	}
	if v.vertical != pageScroll || pageBounds != initialBounds {
		t.Fatal("modal scrolling changed the service list's scroll or bounds")
	}
}

func TestTaskServicesPodDialogFollowsWindowSize(t *testing.T) {
	s := taskServicesDesktopFixture(t)
	v, row := activateTaskServicesFixture(t, s, "archive", "prod")
	const created = "2026-01-02 03:04:05"
	row.pods = append(row.pods, production.RuntimePod{Name: "second-pod", CreatedTime: created})
	tester := newTaskServicesTester(s, 960, 760)
	if err := tester.Click(taskServiceOpenTarget(row, "count")); err != nil {
		t.Fatal(err)
	}
	detail := v.dialog
	initial, ok := tester.Find("关闭")
	if detail == nil || !ok {
		t.Fatal("Pod dialog did not open")
	}
	tester.SetSize(1920, 1080)
	large, ok := tester.Find("关闭")
	// The close button is anchored to the centered panel's top-right corner.
	wantX := initial.X + 960*(1+0.85)/2
	wantY := initial.Y + 320*(1-0.88)/2
	if !ok || large.X < wantX-1 || large.X > wantX+1 || large.Y < wantY-1 || large.Y > wantY+1 {
		t.Fatalf("after resize: close button at %+v, want %.1f, %.1f", large, wantX, wantY)
	}
	timestamp, ok := tester.Find(created)
	if !ok || timestamp.W <= 0 || timestamp.X+timestamp.W > large.X+large.W || detail.horizontal.MaxX > 1 {
		t.Fatalf("wide Pod dialog clipped the last column: %+v", timestamp)
	}
	tester.SetSize(960, 760)
	if small, ok := tester.Find("关闭"); !ok || small != initial || v.dialog != detail || detail.horizontal.MaxX <= 0 {
		t.Fatalf("after shrinking: Pod dialog did not return to its original layout: %+v", small)
	}
}

func TestTaskServicesConfigDialogNamespacePopupAndEscapePrecedence(t *testing.T) {
	s := taskServicesDesktopFixture(t)
	v, row := activateTaskServicesFixture(t, s, "archive", "prod")
	row.pods = []production.RuntimePod{{Name: "one", Namespace: "fp"}, {Name: "two", Namespace: "secondary"}}
	calls := recordTaskServiceConfigReadActions(t, s)
	tester := newTaskServicesTester(s, 1100, 650)
	if err := tester.Click("查看 " + row.name + " prod 配置"); err != nil {
		t.Fatal(err)
	}
	detail := v.dialog
	if detail == nil || detail.kind != taskServiceConfigDetail || detail.row != row {
		t.Fatal("config action did not open a config dialog")
	}
	awaitTaskServicesConfigDialogRead(t, calls, row, "")
	if tester.HasText("选择 namespace") || tester.HasText("读取配置") || !tester.HasText("正在读取最新配置…") {
		t.Fatal("the first request offered a namespace control or needed manual input")
	}
	s.finishTaskServiceConfig(v, detail, "", application.ActionResult{}, &production.ServiceConfigNamespaceError{Candidates: []string{"config-a", "config-b"}})
	tester.Frame()
	if err := tester.Click("选择 namespace"); err != nil {
		t.Fatal(err)
	}
	tester.Key(0, ui.KeyEscape)
	if v.dialog != detail || !detail.open {
		t.Fatal("Escape closed the config dialog while the namespace popup was open")
	}
	if err := tester.Click("选择 namespace"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("config-b"); err != nil {
		t.Fatal(err)
	}
	awaitTaskServicesConfigDialogRead(t, calls, row, "config-b")
	if detail.config.namespace != "config-b" || !detail.config.loading || v.dialog != detail || !detail.open {
		t.Fatal("namespace choice did not start a read in the open config dialog")
	}
	s.finishTaskServiceConfig(v, detail, "config-b", taskServicesConfigDialogResult(row, "config-b", "secret=<redacted>\n"), nil)
	tester.Frame()
	if !detail.config.loaded || detail.config.preview.view == nil {
		t.Fatal("selected namespace did not display its redacted configuration")
	}
	if err := tester.Click("选择 namespace"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("config-a"); err != nil {
		t.Fatal(err)
	}
	awaitTaskServicesConfigDialogRead(t, calls, row, "config-a")
	s.finishTaskServiceConfig(v, detail, "config-b", taskServicesConfigDialogResult(row, "config-b", "old text"), nil)
	if detail.config.loaded || detail.config.text != "" || detail.config.preview.view != nil || !detail.config.loading {
		t.Fatal("switching namespace retained or accepted stale configuration")
	}
	tester.Key(0, ui.KeyEscape)
	if v.dialog != nil {
		t.Fatal("Escape did not close the config dialog")
	}
	if err := tester.Click("查看 " + row.name + " prod 配置"); err != nil {
		t.Fatal(err)
	}
	awaitTaskServicesConfigDialogRead(t, calls, row, "")
	if v.dialog == detail || v.dialog.config.namespace != "" || len(v.dialog.config.namespaceOptions) != 0 {
		t.Fatal("a reopened configuration dialog inherited prior namespace choices")
	}
}

func awaitTaskServicesConfigDialogRead(t *testing.T, calls <-chan application.Action, row *taskServiceRow, namespace string) {
	t.Helper()
	select {
	case action := <-calls:
		selected, hasNamespace := action.Payload["namespace"]
		if action.Kind != "task.services.config" || action.ServiceID != row.serviceID || action.Payload["environment"] != row.environment ||
			hasNamespace != (namespace != "") || hasNamespace && selected != namespace {
			t.Fatalf("wrong configuration request: %+v", action)
		}
	case <-time.After(time.Second):
		t.Fatal("configuration request was not dispatched")
	}
}

func taskServicesConfigDialogResult(row *taskServiceRow, namespace, text string) application.ActionResult {
	return application.ActionResult{Data: application.TaskServiceConfigResult{
		ServiceID: row.serviceID, GenbuServiceName: row.genbuName, Environment: row.environment, Namespace: namespace, Text: text,
	}}
}

func TestTaskServicesConfigDialogRetriesOrdinaryErrors(t *testing.T) {
	s := taskServicesDesktopFixture(t)
	v, row := activateTaskServicesFixture(t, s, "tasks", "uat")
	calls := recordTaskServiceConfigReadActions(t, s)
	tester := newTaskServicesTester(s, 1100, 650)
	if err := tester.Click("查看 " + row.name + " uat 配置"); err != nil {
		t.Fatal(err)
	}
	awaitTaskServicesConfigDialogRead(t, calls, row, "")
	detail := v.dialog
	s.finishTaskServiceConfig(v, detail, "", application.ActionResult{}, errors.New("读取配置超时"))
	tester.Frame()
	if tester.HasText("选择 namespace") || !tester.HasText("读取配置超时") {
		t.Fatal("ordinary error did not render its diagnostic")
	}
	if err := tester.Click("重试"); err != nil {
		t.Fatal(err)
	}
	awaitTaskServicesConfigDialogRead(t, calls, row, "")
	s.finishTaskServiceConfig(v, detail, "", taskServicesConfigDialogResult(row, "", "secret=<redacted>\n"), nil)
	tester.Frame()
	if !detail.config.loaded || detail.config.preview.view == nil || tester.HasText("选择 namespace") || tester.HasText("重试") {
		t.Fatal("successful retry did not display config directly")
	}
}

func TestTaskServicesConfigDialogFollowsWindowSize(t *testing.T) {
	s := taskServicesDesktopFixture(t)
	v, row := activateTaskServicesFixture(t, s, "tasks", "uat")
	row.pods = append(row.pods, production.RuntimePod{Name: "secondary-pod", Namespace: "secondary"})
	tester := newTaskServicesTester(s, 1100, 820)
	if err := tester.Click("查看 " + row.name + " uat 配置"); err != nil {
		t.Fatal(err)
	}
	detail := v.dialog
	if detail == nil {
		t.Fatal("config dialog did not open")
	}
	detail.config.loaded = true
	detail.config.error = ""
	detail.config.text = "server.port=8080\n"
	tester.Frame()
	preview := detail.config.preview.view
	if preview == nil {
		t.Fatal("config preview did not open")
	}
	sizes := [][2]int{{1100, 820}, {1920, 1080}, {960, 760}}
	var initialPreview ui.Rect
	for i, size := range sizes {
		tester.SetSize(size[0], size[1])
		if v.dialog != detail || detail.config.preview.view != preview {
			t.Fatalf("window %v: resize replaced the dialog or config editor", size)
		}
		bounds, ok := tester.Find(preview.Label)
		if !ok || bounds.W <= 0 || bounds.H <= 0 {
			t.Fatalf("window %v: config preview has no layout bounds", size)
		}
		if i == 0 {
			initialPreview = bounds
		}
		wantWidth := initialPreview.W + float32(size[0]-sizes[0][0])*0.85
		wantHeight := initialPreview.H + float32(size[1]-sizes[0][1])*0.88
		if bounds.W < wantWidth-1 || bounds.W > wantWidth+1 || bounds.H < wantHeight-1 || bounds.H > wantHeight+1 {
			t.Fatalf("window %v: config preview = %+v, want %.1f×%.1f", size, bounds, wantWidth, wantHeight)
		}
		width, height := float32(size[0]), float32(size[1])
		panel := ui.Rect{X: width * 0.075, Y: height * 0.06, W: width * 0.85, H: height * 0.88}
		for _, control := range []string{"关闭", "刷新配置", "查找", "复制"} {
			bounds, ok := tester.Find(control)
			if !ok || bounds.W <= 0 || bounds.H <= 0 || bounds.X < panel.X || bounds.X+bounds.W > panel.X+panel.W ||
				bounds.Y < panel.Y || bounds.Y+bounds.H > panel.Y+panel.H {
				t.Fatalf("window %v: %q bounds %+v are outside the panel %+v", size, control, bounds, panel)
			}
		}
	}
}

func TestTaskServicesConfigFindEscapeKeepsDialogOpen(t *testing.T) {
	s := taskServicesDesktopFixture(t)
	v, row := activateTaskServicesFixture(t, s, "tasks", "uat")
	tester := newTaskServicesTester(s, 1100, 650)
	if err := tester.Click("查看 " + row.name + " uat 配置"); err != nil {
		t.Fatal(err)
	}
	detail := v.dialog
	if detail == nil || detail.kind != taskServiceConfigDetail {
		t.Fatal("config action did not open a config dialog")
	}
	detail.config.loaded = true
	detail.config.error = ""
	detail.config.text = "# 本地测试配置\nserver.port=8080\ncredential=***REDACTED***\n"
	tester.Frame()
	tester.Frame()
	preview := detail.config.preview.view
	if preview == nil || !preview.ReadOnly {
		t.Fatal("config preview did not open as a read-only editor")
	}
	language := preview.Buffer.Language()
	if language == nil || language.Name != "Properties" || language.Tokenizer == nil {
		t.Fatalf("config preview language = %v, want Properties with highlighting", language)
	}
	for _, kind := range []code.TokenKind{code.TokenProperty, code.TokenString, code.TokenComment} {
		if preview.Theme.Tokens[kind].Color == (ui.Color{}) {
			t.Fatalf("config preview theme is missing the color for token kind %v", kind)
		}
	}
	for _, control := range []string{"刷新配置", "查找", "复制"} {
		if !tester.HasText(control) {
			t.Fatalf("config dialog omitted %q", control)
		}
	}
	if err := tester.Click("复制"); err != nil {
		t.Fatal(err)
	}
	if got := tester.Clipboard(); got != detail.config.text {
		t.Fatalf("copied config = %q, want seeded redacted content %q", got, detail.config.text)
	}
	if err := tester.Click("查找"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !tester.HasText("Find") {
		t.Fatal("config editor find bar did not open")
	}
	tester.Key(0, ui.KeyEscape)
	if v.dialog != detail || !detail.open || tester.HasText("Find") {
		t.Fatal("first Escape did not close only the editor find bar")
	}
	tester.Key(0, ui.KeyEscape)
	if v.dialog != nil {
		t.Fatal("second Escape did not close the config dialog")
	}
}
