package desktop

import (
	"strings"
	"sync"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/domain"
)

const (
	silverwingRepositoryURL = "https://github.com/javatoai/silverwing"
	silverwingReleasesURL   = "https://github.com/javatoai/silverwing/releases"
)

type settingsSystemActionStyle uint8

const (
	settingsSystemActionOutline settingsSystemActionStyle = iota
	settingsSystemActionPrimary
)

var settingsAboutLogoCache struct {
	once   sync.Once
	bitmap *ui.Bitmap
}

func cachedSettingsAboutLogo() *ui.Bitmap {
	settingsAboutLogoCache.once.Do(func() {
		bitmap, err := ui.DecodeBitmap(applicationIcon)
		if err == nil {
			settingsAboutLogoCache.bitmap = bitmap
		}
	})
	return settingsAboutLogoCache.bitmap
}

func settingsSystemCard(context *ui.Context, theme palette, children func()) {
	ui.Column(context).FillWidth().MinWidth(0).Padding(16).Gap(20).Background(theme.surface).Border(1, theme.line).Radius(12).Label("系统设置卡片").Children(children)
}

func settingsSystemAction(context *ui.Context, key, iconName, text string, theme palette, style settingsSystemActionStyle, disabled bool) bool {
	button := ui.ButtonBase(context.Key(key)).Row().Height(34).Padding(0, 12).Gap(7).Radius(8).AlignItems(ui.Center).Label(text).Disabled(disabled)
	background, foreground := theme.surface, theme.text
	switch style {
	case settingsSystemActionPrimary:
		background, foreground = ui.Hex("#356AE6"), ui.Hex("#FFFFFF")
	default:
		button.Border(1, theme.line)
	}
	if disabled {
		background, foreground = theme.soft, theme.muted
	} else if button.Hovered() {
		if style == settingsSystemActionPrimary {
			background = ui.Hex("#285CD3")
		} else {
			background = theme.soft
		}
	}
	button.Background(background).TextColor(foreground).Children(func() {
		icon(context, iconName, 17, foreground)
		label(context, text, 13, foreground).NoWrap()
	})
	return button.Clicked()
}

func (state *app) settingsAbout(context *ui.Context, theme palette) {
	settingsSystemCard(context, theme, func() {
		ui.Row(context).FillWidth().MinWidth(0).Wrap().Gap(12).AlignItems(ui.Center).Label("SilverWing 产品信息").Children(func() {
			if logo := cachedSettingsAboutLogo(); logo != nil {
				ui.Image(context.Key("about-silverwing-logo"), logo).Width(48).Height(48).Shrink(0).Fit(ui.Contain).Label("SilverWing 应用图标")
			}
			ui.Column(context).Grow(1).MinWidth(0).Gap(4).Children(func() {
				label(context, "SilverWing", 20, theme.text).FontWeight(600).Wrap()
				label(context, "MyGO 原生桌面", 12, theme.muted).Wrap()
			})
			ui.Box(context).Shrink(0).Padding(6, 10).Background(theme.soft).Radius(8).Label("当前版本 " + domain.CurrentProductVersion).Children(func() {
				label(context, "版本 "+domain.CurrentProductVersion, 10, theme.muted).NoWrap()
			})
		})
		ui.Divider(context)
		ui.Column(context).FillWidth().MinWidth(0).Gap(8).Children(func() {
			label(context, "GitHub", 13, theme.text).FontWeight(500)
			label(context, silverwingRepositoryURL, 11, theme.muted).FillWidth().MinWidth(0).Wrap().Selectable()
			ui.Row(context).FillWidth().MinWidth(0).Wrap().Gap(8).Label("GitHub 仓库操作").Children(func() {
				if settingsSystemAction(context, "about-open-GitHub", "launch", "打开仓库", theme, settingsSystemActionOutline, false) {
					mygo.Shell.OpenExternal(silverwingRepositoryURL)
				}
				if settingsSystemAction(context, "about-copy-GitHub", "copy", "复制地址", theme, settingsSystemActionOutline, false) {
					state.copyTextToClipboard(context.Services(), silverwingRepositoryURL, "GitHub 仓库地址已复制")
				}
				if settingsSystemAction(context, "about-releases", "launch", "版本发布", theme, settingsSystemActionOutline, false) {
					mygo.Shell.OpenExternal(silverwingReleasesURL)
				}
			})
		})
	})
}

func (state *app) settingsUpdates(context *ui.Context, theme palette) {
	settingsSystemCard(context, theme, func() {
		label(context, "获取新版", 19, theme.text).FontWeight(600).Wrap()
		ui.Divider(context)
		ui.Column(context).FillWidth().MinWidth(0).Gap(6).Children(func() {
			label(context, "当前版本", 12, theme.muted)
			label(context, domain.CurrentProductVersion, 14, theme.text).FontWeight(500)
		})
		ui.Column(context).FillWidth().MinWidth(0).Gap(8).Children(func() {
			label(context, "GitHub Releases", 13, theme.text).FontWeight(500)
			label(context, silverwingReleasesURL, 11, theme.muted).FillWidth().MinWidth(0).Wrap().Selectable()
			label(context, "下载适用于当前系统的安装包，并按安装包说明手动安装。", 11, theme.muted).FillWidth().MinWidth(0).Wrap()
		})
		ui.Row(context).FillWidth().MinWidth(0).Wrap().Gap(8).Label("更新操作").Children(func() {
			if settingsSystemAction(context, "updates-open", "launch", "打开更新页面", theme, settingsSystemActionOutline, false) {
				mygo.Shell.OpenExternal(silverwingReleasesURL)
			}
			if settingsSystemAction(context, "updates-copy", "copy", "复制链接", theme, settingsSystemActionOutline, false) {
				state.copyTextToClipboard(context.Services(), silverwingReleasesURL, "更新链接已复制")
			}
		})
	})
}

func (state *app) settingsAppData(context *ui.Context, theme palette) {
	settingsSystemCard(context, theme, func() {
		label(context, "应用数据目录", 19, theme.text).FontWeight(600).Wrap()
		home := state.snapshot.Home
		disabled := strings.TrimSpace(home) == ""
		if disabled {
			label(context, "应用数据目录尚未加载", 11, theme.muted).FillWidth().MinWidth(0).Wrap()
		} else {
			label(context, home, 11, theme.muted).FillWidth().MinWidth(0).Wrap().Selectable()
		}
		ui.Row(context).FillWidth().MinWidth(0).Wrap().Gap(8).Label("应用数据目录操作").Children(func() {
			if settingsSystemAction(context, "settings-app-data-open", "folder", "打开目录", theme, settingsSystemActionOutline, disabled) && !disabled {
				state.act("system.open", "", "", "", home, "", map[string]any{"desktopSystemOpenDirectory": true})
			}
			if settingsSystemAction(context, "settings-app-data-copy", "copy", "复制路径", theme, settingsSystemActionOutline, disabled) && !disabled {
				state.copyTextToClipboard(context.Services(), home, "应用数据目录路径已复制")
			}
		})
	})
}
