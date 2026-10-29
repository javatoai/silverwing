package desktop

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestSettingsSystemScreensIsolatedLayout(test *testing.T) {
	for _, size := range [][2]int{{960, 760}, {1440, 940}} {
		for _, dark := range []bool{false, true} {
			test.Run(fmt.Sprintf("%dx%d-dark-%t", size[0], size[1], dark), func(test *testing.T) {
				root := test.TempDir()
				state := newApp(nil, Options{Page: "settings", Dark: dark, CapturePath: "isolated-settings-ui"})
				state.loading = false
				state.settingsUnit.state.settingsTab, state.settingsUnit.state.settingsPage = "系统", "about"
				state.snapshot.Home = filepath.Join(root, "SilverWing 应用数据")
				tester := ui.NewTester(func(context *ui.Context) {
					palette := setTheme(context, dark)
					ui.Row(context).Fill().AlignItems(ui.Stretch).Background(palette.bg).Children(func() {
						state.sidebar(context, palette, size[0] < 1180)
						ui.Column(context).Grow(1).MinWidth(0).Children(func() {
							state.settingsScreen(context, palette)
						})
					})
				}, size[0], size[1])
				assertSettingsVisibleBounds(test, tester, "快捷工具", size[0], size[1])
				for _, tab := range []string{"关于", "更新", "应用数据目录"} {
					assertSettingsVisibleBounds(test, tester, tab, size[0], size[1])
				}
				assertSettingsSystemCardAlignment(test, tester, size[0])
				assertSettingsVisibleBounds(test, tester, "打开仓库", size[0], size[1])
				assertSettingsVisibleBounds(test, tester, "版本发布", size[0], size[1])
				if tester.HasText(state.snapshot.Home) {
					test.Fatal("application data path still appears on the about page")
				}
				captureDesktopTestFrame(test, tester, fmt.Sprintf("settings-system-about-%dx%d-dark-%t", size[0], size[1], dark))
				if err := tester.Click("更新"); err != nil {
					test.Fatal(err)
				}
				if state.settingsUnit.state.settingsPage != "updates" || !tester.HasText("获取新版") || tester.HasText("已是最新版本") {
					test.Fatal("update tab did not retain the manual update flow")
				}
				assertSettingsVisibleBounds(test, tester, "打开更新页面", size[0], size[1])
				assertSettingsVisibleBounds(test, tester, "复制链接", size[0], size[1])
				assertSettingsSystemCardAlignment(test, tester, size[0])
				captureDesktopTestFrame(test, tester, fmt.Sprintf("settings-system-updates-%dx%d-dark-%t", size[0], size[1], dark))
				if err := tester.Click("应用数据目录"); err != nil {
					test.Fatal(err)
				}
				if state.settingsUnit.state.settingsPage != "app-data" || !tester.HasText(state.snapshot.Home) {
					test.Fatal("application data tab did not show the current Home")
				}
				assertSettingsVisibleBounds(test, tester, "打开目录", size[0], size[1])
				assertSettingsVisibleBounds(test, tester, "复制路径", size[0], size[1])
				assertSettingsSystemCardAlignment(test, tester, size[0])
				captureDesktopTestFrame(test, tester, fmt.Sprintf("settings-system-app-data-%dx%d-dark-%t", size[0], size[1], dark))
				if err := tester.Click("复制路径"); err != nil {
					test.Fatal(err)
				}
				if tester.Clipboard() != state.snapshot.Home {
					test.Fatalf("data tab copied %q, want %q", tester.Clipboard(), state.snapshot.Home)
				}
				if err := tester.Click("关于"); err != nil {
					test.Fatal(err)
				}
				if state.settingsUnit.state.settingsPage != "about" || !tester.HasText("SilverWing") || tester.HasText(state.snapshot.Home) {
					test.Fatal("about tab was not restored after viewing the application data page")
				}
				if len(tester.OpenedURLs()) != 0 {
					test.Fatal("layout verification opened an external URL")
				}
				files, err := os.ReadDir(root)
				if err != nil || len(files) != 0 {
					test.Fatalf("rendering system settings wrote to the synthetic Home: %v, %v", files, err)
				}
			})
		}
	}
}

func assertSettingsSystemCardAlignment(test *testing.T, tester *ui.Tester, width int) {
	test.Helper()
	card, cardFound := tester.Find("系统设置卡片")
	tab, tabFound := tester.Find("关于")
	if !cardFound || !tabFound || card.X != tab.X || card.X+card.W != float32(width)-28 {
		test.Fatalf("system card does not fill the content area: card=%+v firstTab=%+v viewport=%d", card, tab, width)
	}
}

func assertSettingsVisibleBounds(test *testing.T, tester *ui.Tester, name string, width, height int) {
	test.Helper()
	bounds, found := tester.Find(name)
	if !found || bounds.W <= 0 || bounds.H <= 0 || bounds.X < 0 || bounds.Y < 0 || bounds.X+bounds.W > float32(width) || bounds.Y+bounds.H > float32(height) {
		test.Fatalf("%q is not visible within %dx%d: %+v", name, width, height, bounds)
	}
}
