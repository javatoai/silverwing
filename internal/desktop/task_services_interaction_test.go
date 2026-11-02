package desktop

import (
	"fmt"
	"strings"
	"testing"

	"silverwing-go/internal/production"

	"github.com/egoist/mygo/ui"
)

func TestTaskServicesMouseClicksKeepCorrectEnvironmentExpanded(t *testing.T) {
	for _, env := range []string{"uat", "prod"} {
		for _, which := range []string{"row", "count", "config"} {
			t.Run(env+"-"+which, func(t *testing.T) {
				s := taskServicesDesktopFixture(t)
				v, key := s.taskServicesProjection()
				v.active = true
				v.environment = env
				s.taskServicesUnit.current = v
				s.taskServicesUnit.views = map[string]*taskServicesView{key: v}
				row := v.rows[0]
				if env == "prod" {
					row = v.rows[1]
				}
				row.loaded = true
				row.pods = []production.RuntimePod{{Name: "p1", Namespace: "fp", Version: strings.Repeat("long", 20), Ready: true}, {Name: "p2", Namespace: "fp", Version: "v2"}}
				tester := ui.NewTester(func(c *ui.Context) { s.renderTaskServices(c, setTheme(c, false)) }, 1100, 650)
				target := row.name + " " + strings.ToUpper(env)
				if which == "count" {
					target = "查看 " + row.name + " " + env + " Pod 明细"
				}
				if which == "config" {
					target = "查看 " + row.name + " " + env + " 配置"
				}
				tester.Click(target)
				if v.expanded != row.key() {
					t.Fatalf("%s click did not preserve target environment: expanded=%q want=%q", which, v.expanded, row.key())
				}
				if which == "config" && v.detail != "config" {
					t.Fatal("configuration click did not select config panel")
				}
			})
		}
	}
}

func TestTaskServicesLongVersionKeepsCountVisible(t *testing.T) {
	s := taskServicesDesktopFixture(t)
	v, key := s.taskServicesProjection()
	v.active = true
	s.taskServicesUnit.current = v
	s.taskServicesUnit.views = map[string]*taskServicesView{key: v}
	row := v.rows[0]
	row.loaded = true
	row.pods = []production.RuntimePod{{Name: "p1", Version: strings.Repeat("very-long-version-", 20)}}
	tester := ui.NewTester(func(c *ui.Context) { s.renderTaskServices(c, setTheme(c, false)) }, 1100, 650)
	count, ok := tester.Find("× 1")
	if !ok || count.W <= 0 || count.X+count.W > 1100 {
		t.Fatal("long version clipped its independent count", count, ok)
	}
}

func TestTaskServicesEnvironmentTabsAndMovedActions(t *testing.T) {
	s := taskServicesDesktopFixture(t)
	v, key := s.taskServicesProjection()
	v.active = true
	s.taskServicesUnit.current = v
	s.taskServicesUnit.views = map[string]*taskServicesView{key: v}
	for _, row := range v.rows {
		row.loaded = true
		row.pods = []production.RuntimePod{{Name: row.environment + "-one", Namespace: "fp", Ready: true, CreatedTime: "2026-11-02 12:34:56"}, {Name: row.environment + "-two", Namespace: "fp", CreatedTime: "2026-11-01 10:11:12"}}
		row.fetchedAt = "test-fetched-time"
	}
	tester := ui.NewTester(func(c *ui.Context) { s.renderTaskServices(c, setTheme(c, false)) }, 1200, 650)
	if _, ok := tester.Find("Second service PROD"); ok {
		t.Fatal("hidden PROD row rendered under UAT")
	}
	for _, removed := range []string{"2 个就绪", "已查询", "2026-11-01 10:11:12"} {
		if tester.HasText(removed) {
			t.Fatal("redundant summary remains", removed)
		}
	}
	tester.Click("PROD 环境")
	if v.environment != "prod" {
		t.Fatal("PROD tab did not switch environment")
	}
	if _, ok := tester.Find("Second service UAT"); ok {
		t.Fatal("hidden UAT row rendered under PROD")
	}
	tester.Click("展开 Second service prod Pod 明细")
	if v.expanded != "s2:prod" || v.detail != "pods" || !tester.HasText("2026-11-01 10:11:12") {
		t.Fatal("moved detail action lost full Pod times")
	}
	tester.Click("查看 Second service prod 配置")
	if v.expanded != "s2:prod" || v.detail != "config" {
		t.Fatal("row action did not switch same detail to config")
	}
	tester.Click("查看 Second service prod 配置")
	if v.expanded != "" {
		t.Fatal("cannot close inline detail")
	}
	tester.Click("UAT 环境")
	if v.environment != "uat" {
		t.Fatal("cannot return to UAT")
	}
}

func TestTaskServicesResponsiveColumnsAndCompactRows(t *testing.T) {
	for _, width := range []int{700, 940, 1400} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%t", width, dark), func(t *testing.T) {
				s := taskServicesDesktopFixture(t)
				v, key := s.taskServicesProjection()
				v.active = true
				s.taskServicesUnit.current = v
				s.taskServicesUnit.views = map[string]*taskServicesView{key: v}
				row := v.rows[0]
				row.loaded = true
				row.pods = []production.RuntimePod{{Version: "v1"}}
				tester := ui.NewTester(func(c *ui.Context) { s.renderTaskServices(c, setTheme(c, dark)) }, width, 600)
				bounds, ok := tester.Find("Second service UAT")
				if !ok || bounds.H != 48 || bounds.W != float32(width) || v.horizontal.MaxX != max(taskServiceTableMinWidth-float32(width), 0) {
					t.Fatal("table must fill viewport with correct minimum-width scroll extent", bounds, ok, v.horizontal.MaxX)
				}
				columns := taskServiceColumnWidths(max(float32(width), taskServiceTableMinWidth))
				if columns[1] != 80 || columns[3] != 168 || columns[4] != 180 || columns[0]-220 != columns[2]-220 {
					t.Fatal("fixed or balanced columns changed", columns)
				}
			})
		}
	}
}

func TestTaskServicesResizeSettlesColumnsBeforeMouseInput(t *testing.T) {
	s := taskServicesDesktopFixture(t)
	v, key := s.taskServicesProjection()
	v.active = true
	s.taskServicesUnit.current = v
	s.taskServicesUnit.views = map[string]*taskServicesView{key: v}
	row := v.rows[0]
	row.loaded = true
	row.pods = []production.RuntimePod{{Name: "pod", Namespace: "fp"}}
	tester := ui.NewTester(func(c *ui.Context) { s.renderTaskServices(c, setTheme(c, false)) }, 1000, 650)
	for _, width := range []int{1400, 940, 1200} {
		tester.SetSize(width, 650)
		bounds, ok := tester.Find("Second service UAT")
		if !ok || bounds.W != float32(width) {
			t.Fatal("resized table did not settle", bounds, width)
		}
		tester.Click("查看 Second service uat Pod 明细")
		if v.expanded != row.key() {
			t.Fatal("resized count moved during pointer gesture", width)
		}
		tester.Click("展开 Second service uat Pod 明细")
	}
}

func TestTaskServicesCompactControlsToggleAndRefreshScope(t *testing.T) {
	for _, env := range []string{"uat", "prod"} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s-dark-%t", env, dark), func(t *testing.T) {
				s := taskServicesDesktopFixture(t)
				v, key := s.taskServicesProjection()
				v.active = true
				v.environment = env
				s.taskServicesUnit.current = v
				s.taskServicesUnit.views = map[string]*taskServicesView{key: v}
				for _, row := range v.rows {
					row.loaded = true
					row.error = "untouched"
					row.pods = []production.RuntimePod{{Name: row.serviceID + "-pod", Namespace: "fp", Version: "v1", CreatedTime: "2026-11-02 12:34:56"}}
				}
				row := v.rows[0]
				if env == "prod" {
					row = v.rows[1]
				}
				tester := ui.NewTester(func(c *ui.Context) { s.renderTaskServices(c, setTheme(c, dark)) }, 940, 650)
				if tester.HasText("2 个服务") {
					t.Fatal("redundant service count remains")
				}
				podsLabel := "展开 Second service " + env + " Pod 明细"
				configLabel := "查看 Second service " + env + " 配置"
				refreshLabel := "刷新 Second service " + env
				podButton, ok := tester.Find(podsLabel)
				if !ok {
					t.Fatal("Pod action missing")
				}
				configButton, ok := tester.Find(configLabel)
				if !ok {
					t.Fatal("config action missing")
				}
				refreshButton, ok := tester.Find(refreshLabel)
				if !ok || podButton.H != 30 || configButton.H != 30 || refreshButton.H != 30 || podButton.Y != configButton.Y || configButton.Y != refreshButton.Y || podButton.X+podButton.W > configButton.X || configButton.X+configButton.W > refreshButton.X || refreshButton.X+refreshButton.W > 940 {
					t.Fatal("text actions overlap or use inconsistent layout", podButton, configButton, refreshButton)
				}
				tester.Click(podsLabel)
				if v.expanded != row.key() || v.detail != "pods" {
					t.Fatal("Pod action did not open")
				}
				if _, ok := tester.Find("收起服务详情"); ok {
					t.Fatal("removed close action remains")
				}
				if tester.HasText("Second service · ") {
					t.Fatal("redundant expanded title remains")
				}
				tester.Click(podsLabel)
				if v.expanded != "" {
					t.Fatal("repeating Pod action did not collapse")
				}
				tester.Click(configLabel)
				if v.expanded != row.key() || v.detail != "config" {
					t.Fatal("configuration did not open")
				}
				row.configError = "no-new-config-query"
				tester.Click(configLabel)
				if v.expanded != "" || row.configError != "no-new-config-query" {
					t.Fatal("collapse queried configuration again")
				}
				tester.Click(podsLabel)
				tester.Click(configLabel)
				if v.expanded != row.key() || v.detail != "config" {
					t.Fatal("cannot switch inline content")
				}
				row.configError = "keep-config"
				tester.Click(refreshLabel)
				if v.expanded != row.key() || v.detail != "config" || row.configError != "keep-config" || row.error == "untouched" {
					t.Fatal("refresh lost detail or did not update target row")
				}
				for _, other := range v.rows {
					if other != row && other.error != "untouched" {
						t.Fatal("row refresh affected another service or environment", other)
					}
				}
				tester.Click("展开 A service " + env + " Pod 明细")
				if v.expanded != "s1:"+env || v.detail != "pods" {
					t.Fatal("another service did not replace open detail")
				}
			})
		}
	}
}
