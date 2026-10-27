package desktop

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/domain"
)

func TestSidebarVisibilityCombinationsAndBottomGrouping(t *testing.T) {
	for mask := 0; mask < 16; mask++ {
		for _, tag := range []bool{false, true} {
			for _, tools := range []bool{false, true} {
				for _, compact := range []bool{false, true} {
					config := domain.DefaultAppConfig()
					config.TagEnabled = tag
					config.ToolVisibility = domain.ToolVisibilitySettings{JSON: tools}
					config.SidebarVisibility = domain.SidebarVisibilitySettings{Requirements: mask&1 != 0, Tools: mask&2 != 0, Production: mask&4 != 0, Services: mask&8 != 0}
					encoded, _ := json.Marshal(config)
					s := newApp(nil, Options{Page: "requirements", CapturePath: "isolated-sidebar"})
					s.loading, s.configJSON = false, string(encoded)
					want := []string{"tasks"}
					if mask&1 != 0 {
						want = append(want, "requirements")
					}
					if mask&2 != 0 && tools {
						want = append(want, "tools")
					}
					if mask&4 != 0 && tag {
						want = append(want, "production")
					}
					if mask&8 != 0 {
						want = append(want, "services")
					}
					want = append(want, "settings")
					items := s.sidebarNavigationItems()
					got := []string{}
					for _, item := range items {
						got = append(got, item.page)
					}
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("mask=%d tag=%t tools=%t: %v != %v", mask, tag, tools, got, want)
					}
					s.reconcileSidebarNavigation()
					if s.page != "requirements" {
						t.Fatal("hiding a navigation entry disabled an existing view")
					}
					tester := ui.NewTester(func(c *ui.Context) {
						ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() { s.sidebar(c, setTheme(c, false), compact) })
					}, 220, 760)
					previousBottom := ui.Rect{}
					for _, item := range items {
						if item.page == "tasks" || item.page == "requirements" {
							continue
						}
						bounds, ok := tester.Find(item.name)
						if !ok || bounds.Y < 450 || bounds.Y+bounds.H > 750 {
							t.Fatalf("bottom grouping lost for %q mask=%d: %+v", item.name, mask, bounds)
						}
						if previousBottom.H > 0 && math.Abs(float64(bounds.Y-previousBottom.Y-previousBottom.H-4)) > 0.1 {
							t.Fatalf("extra gap before %q: %+v %+v", item.name, previousBottom, bounds)
						}
						previousBottom = bounds
					}
				}
			}
		}
	}
}

func TestSidebarSettingsLayoutAndSaveFailure(t *testing.T) {
	for _, width := range []int{960, 1440} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%t", width, dark), func(t *testing.T) {
				s := newApp(nil, Options{Page: "settings", CapturePath: "isolated-sidebar-settings"})
				s.loading, s.dark = false, dark
				encoded, _ := json.Marshal(domain.DefaultAppConfig())
				s.configJSON = string(encoded)
				tester := ui.NewTester(func(c *ui.Context) {
					ui.Column(c).Fill().Padding(24).Gap(16).Children(func() { s.settingsBasic(c, setTheme(c, dark)) })
				}, width, 900)
				for _, name := range []string{"侧边栏展示", "需求", "研发工具", "生产发布", "服务"} {
					assertSettingsVisibleBounds(t, tester, name, width, 900)
				}
				if tester.HasText("任务和设置始终显示") {
					t.Fatal("the sidebar explanation should be in its help tooltip")
				}
				for i := 0; i < 16 && !tester.Focused("侧边栏展示说明"); i++ {
					tester.Key(0, ui.KeyTab)
				}
				if !tester.Focused("侧边栏展示说明") || !tester.HasText("任务和设置始终显示") {
					t.Fatal("fixed entries are not explained by the keyboard-accessible help")
				}
				tester.Key(0, ui.KeyEscape)
				var x float32 = -1
				for _, name := range []string{"需求", "研发工具", "生产发布", "服务"} {
					r, _ := tester.Find(name)
					if x >= 0 && x != r.X {
						t.Fatal("navigation settings labels are not aligned")
					}
					x = r.X
				}
				captureDesktopTestFrame(t, tester, fmt.Sprintf("sidebar-settings-%d-dark-%t", width, dark))
				for _, compact := range []bool{false, true} {
					shell := ui.NewTester(func(c *ui.Context) {
						p := setTheme(c, dark)
						ui.Row(c).Fill().AlignItems(ui.Stretch).Background(p.bg).Children(func() {
							s.sidebar(c, p, compact)
							ui.Column(c).Grow(1).MinWidth(0).Children(func() { s.settingsScreen(c, p) })
						})
					}, width, 900)
					assertSettingsVisibleBounds(t, shell, "侧边栏展示", width, 900)
					captureDesktopTestFrame(t, shell, fmt.Sprintf("sidebar-shell-%d-dark-%t-compact-%t", width, dark, compact))
				}
				for i := 0; i < 16 && !tester.Focused("需求"); i++ {
					tester.Key(0, ui.KeyTab)
				}
				if !tester.Focused("需求") {
					t.Fatal("navigation switch is not keyboard reachable")
				}
				tester.Key(0, ui.KeySpace)
				// No backend models a failed save. Do not hide navigation before
				// a successfully applied configuration snapshot.
				if s.lastError == "" || !s.configNestedBool("sidebarVisibility", "requirements", false) {
					t.Fatal("failed save changed applied visibility")
				}
				tester.Frame()
				if !*s.settingsBoolInputs["sidebar-visibility-requirements"] {
					t.Fatal("failed switch did not restore its applied value")
				}
				before := s.configJSON
				config := domain.DefaultAppConfig()
				config.SidebarVisibility.Requirements = false
				next, _ := json.Marshal(config)
				s.syncSettingsConfigInputs(before, string(next))
				s.configJSON = string(next)
				tester.Frame()
				if *s.settingsBoolInputs["sidebar-visibility-requirements"] {
					t.Fatal("successful configuration update did not refresh switch")
				}
			})
		}
	}
}
