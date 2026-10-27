package desktop

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/domain"
)

func TestSettingsAboutLayoutAcrossSizesAndThemes(test *testing.T) {
	for _, width := range []int{960, 640, 360} {
		for _, dark := range []bool{false, true} {
			test.Run(fmt.Sprintf("%d-dark-%t", width, dark), func(test *testing.T) {
				state := &app{}
				state.snapshot.Home = filepath.Join("Silver Wing Home", "data", "silverwing")
				tester := settingsSystemPageTester(state, width, dark, func(context *ui.Context, theme palette) {
					state.settingsAbout(context, theme)
				})

				if !tester.HasText("silverwing-product") || !tester.HasText("MyGO 原生桌面") || !tester.HasText("当前版本") || !tester.HasText(domain.CurrentProductVersion) {
					test.Fatalf("brand, runtime description, or version is missing: %v", tester.Texts())
				}
				for _, text := range tester.Texts() {
					if text == "关于 SilverWing" || text == "应用数据目录" {
						test.Fatalf("About card contains a duplicated title or data directory: %q", text)
					}
				}
				if _, found := tester.Find("SilverWing 应用图标"); !found {
					test.Fatal("the embedded SilverWing brand icon is missing")
				}
				assertSettingsSystemCardBounds(test, tester, width)

				actions, actionsFound := tester.Find("GitHub 仓库操作")
				open, openFound := tester.Find("打开仓库")
				copy, copyFound := tester.Find("复制地址")
				release, releaseFound := tester.Find("版本发布")
				if !actionsFound || !openFound || !copyFound || !releaseFound {
					test.Fatalf("repository action group is incomplete: %v", tester.Texts())
				}
				assertSettingsSystemBounds(test, actions, width, 720, "repository action group")
				assertSettingsSystemBounds(test, open, width, 720, "open repository action")
				assertSettingsSystemBounds(test, copy, width, 720, "copy repository action")
				assertSettingsSystemBounds(test, release, width, 720, "release action")
				if width >= 640 && (open.Y != copy.Y || open.Y != release.Y || open.X >= copy.X || copy.X >= release.X) {
					test.Fatalf("wide repository actions must remain in the requested order: open=%+v copy=%+v release=%+v", open, copy, release)
				}
				if width == 360 && release.Y <= copy.Y {
					test.Fatalf("narrow repository actions must wrap without changing order: copy=%+v release=%+v", copy, release)
				}
				if !tester.HasText(silverwingRepositoryURL) || tester.HasText("GitLab") {
					test.Fatal("repository address is missing or an unrelated repository is shown")
				}
				captureDesktopTestFrame(test, tester, fmt.Sprintf("about-%d-dark-%t", width, dark))

				tester.Key(0, ui.KeyTab)
				if !tester.Focused("打开仓库") {
					test.Fatal("first repository tab stop must open the repository")
				}
				tester.Key(0, ui.KeyTab)
				if !tester.Focused("复制地址") {
					test.Fatal("second repository tab stop must copy the address")
				}
				tester.Key(0, ui.KeyTab)
				if !tester.Focused("版本发布") {
					test.Fatal("third repository tab stop must open the release page")
				}
				if err := tester.Click("复制地址"); err != nil {
					test.Fatal(err)
				}
				if tester.Clipboard() != silverwingRepositoryURL || state.lastMessage != "GitHub 仓库地址已复制" {
					test.Fatalf("repository copy bytes or feedback changed: clipboard=%q feedback=%q", tester.Clipboard(), state.lastMessage)
				}
			})
		}
	}
}

func TestSettingsUpdatesLayoutCopyAndKeyboardOrder(test *testing.T) {
	for _, width := range []int{960, 640, 360} {
		for _, dark := range []bool{false, true} {
			test.Run(fmt.Sprintf("%d-dark-%t", width, dark), func(test *testing.T) {
				state := &app{}
				tester := settingsSystemPageTester(state, width, dark, func(context *ui.Context, theme palette) {
					state.settingsUpdates(context, theme)
				})

				if !tester.HasText("获取新版") || !tester.HasText("当前版本") || !tester.HasText(domain.CurrentProductVersion) || !tester.HasText(silverwingReleasesURL) {
					test.Fatalf("update page is missing its title, current version, or release URL: %v", tester.Texts())
				}
				if tester.HasText("已是最新版本") || tester.HasText("检查更新") {
					test.Fatal("update page makes an unsupported release-status claim")
				}
				assertSettingsSystemCardBounds(test, tester, width)
				open, openFound := tester.Find("打开更新页面")
				copy, copyFound := tester.Find("复制链接")
				actions, actionsFound := tester.Find("更新操作")
				if !openFound || !copyFound || !actionsFound {
					test.Fatalf("update actions are missing: %v", tester.Texts())
				}
				assertSettingsSystemBounds(test, open, width, 720, "open updates action")
				assertSettingsSystemBounds(test, copy, width, 720, "copy updates action")
				assertSettingsSystemBounds(test, actions, width, 720, "update action group")
				rendered := tester.Image()
				openFill := rendered.RGBAAt(int(open.X+5), int(open.Y+open.H/2))
				copyFill := rendered.RGBAAt(int(copy.X+5), int(copy.Y+copy.H/2))
				if openFill != copyFill {
					test.Fatalf("update actions must share the same neutral background: open=%v copy=%v", openFill, copyFill)
				}
				if tester.HasText("手动安装") {
					test.Fatal("update instructions should stay in the help tooltip")
				}
				captureDesktopTestFrame(test, tester, fmt.Sprintf("updates-%d-dark-%t", width, dark))

				tester.Key(0, ui.KeyTab)
				if !tester.Focused("GitHub Releases说明") || !tester.HasText("手动安装") {
					test.Fatal("the update help must explain manual installation on keyboard focus")
				}
				tester.Key(0, ui.KeyEscape)
				if tester.HasText("手动安装") {
					test.Fatal("Escape did not dismiss the update help")
				}
				tester.Key(0, ui.KeyTab)
				if !tester.Focused("打开更新页面") {
					test.Fatal("the open updates action must follow the help in keyboard order")
				}
				tester.Key(0, ui.KeyTab)
				if !tester.Focused("复制链接") {
					test.Fatal("the update copy action must follow the open updates action")
				}
				if err := tester.Click("复制链接"); err != nil {
					test.Fatal(err)
				}
				if tester.Clipboard() != silverwingReleasesURL || state.lastMessage != "更新链接已复制" {
					test.Fatalf("update copy bytes or feedback changed: clipboard=%q feedback=%q", tester.Clipboard(), state.lastMessage)
				}
			})
		}
	}
}

func TestSettingsAppDataLayoutPathsCopyAndKeyboardOrder(test *testing.T) {
	paths := []struct {
		name string
		path string
	}{
		{"normal", filepath.Join("Silver Wing Home", "中文资料", "profile")},
		{"long-spaced", filepath.Join("Silver Wing Home", strings.Repeat("中文 空间 ", 24), "local profile")},
		{"long-unspaced", filepath.Join("Silver Wing Home", strings.Repeat("无空格目录", 24), "profile")},
	}
	for _, width := range []int{960, 640, 360} {
		for _, dark := range []bool{false, true} {
			for _, pathCase := range paths {
				test.Run(fmt.Sprintf("%d-dark-%t-%s", width, dark, pathCase.name), func(test *testing.T) {
					state := &app{}
					state.snapshot.Home = pathCase.path
					tester := settingsSystemPageTester(state, width, dark, func(context *ui.Context, theme palette) {
						state.settingsAppData(context, theme)
					})

					assertSettingsSystemCardBounds(test, tester, width)
					path, pathFound := tester.Find(pathCase.path)
					open, openFound := tester.Find("打开目录")
					copy, copyFound := tester.Find("复制路径")
					actions, actionsFound := tester.Find("应用数据目录操作")
					if !pathFound || !openFound || !copyFound || !actionsFound {
						test.Fatalf("application data directory content is incomplete: %v", tester.Texts())
					}
					assertSettingsSystemBounds(test, path, width, 720, "application data path")
					assertSettingsSystemBounds(test, open, width, 720, "open directory action")
					assertSettingsSystemBounds(test, copy, width, 720, "copy directory action")
					assertSettingsSystemBounds(test, actions, width, 720, "application data action group")
					if pathCase.name == "normal" || width == 360 {
						captureDesktopTestFrame(test, tester, fmt.Sprintf("app-data-%d-dark-%t-%s", width, dark, pathCase.name))
					}

					tester.Key(0, ui.KeyTab)
					if !tester.Focused("打开目录") {
						test.Fatal("open directory action must be the first keyboard stop")
					}
					tester.Key(0, ui.KeyTab)
					if !tester.Focused("复制路径") {
						test.Fatal("copy path action must follow open directory")
					}
					if err := tester.Click("复制路径"); err != nil {
						test.Fatal(err)
					}
					if tester.Clipboard() != pathCase.path || state.lastMessage != "应用数据目录路径已复制" {
						test.Fatalf("application data path copy bytes or feedback changed: clipboard=%q feedback=%q", tester.Clipboard(), state.lastMessage)
					}
				})
			}
		}
	}
}

func settingsSystemPageTester(state *app, width int, dark bool, render func(*ui.Context, palette)) *ui.Tester {
	return ui.NewTester(func(context *ui.Context) {
		theme := setTheme(context, dark)
		ui.Column(context).Fill().MinWidth(0).Padding(12).Background(theme.bg).Children(func() {
			render(context, theme)
		})
	}, width, 720)
}

func assertSettingsSystemCardBounds(test *testing.T, tester *ui.Tester, width int) {
	test.Helper()
	card, found := tester.Find("系统设置卡片")
	if !found || card.X != 12 || card.X+card.W != float32(width-12) {
		test.Fatalf("system settings card must fill the available width: bounds=%+v found=%t viewport=%d", card, found, width)
	}
}

func assertSettingsSystemBounds(test *testing.T, rect ui.Rect, width, height int, name string) {
	test.Helper()
	if rect.X < 0 || rect.Y < 0 || rect.W < 0 || rect.H < 0 || rect.X+rect.W > float32(width)-12 || rect.Y+rect.H > float32(height)-12 {
		test.Fatalf("%s exceeds the available viewport: rect=%+v viewport=%dx%d", name, rect, width, height)
	}
}
