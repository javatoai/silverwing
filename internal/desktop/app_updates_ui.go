package desktop

import (
	"time"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/domain"
)

func appUpdateDot(c *ui.Context, p palette, absolute bool) {
	dot := ui.Box(c).Width(7).Height(7).Shrink(0).Radius(4).Background(p.danger).Label("有新版本可更新").Tooltip("有新版本可更新")
	if absolute {
		dot.Absolute().Top(5).Right(5)
	}
}

func (s *app) renderAppUpdates(c *ui.Context, p palette) {
	state := s.ensureAppUpdateState()
	prefs := s.appUpdatePreferences()
	ready := s.desktopPreferences != nil && s.desktopPreferences.enabled && s.desktopPreferences.store != nil && !s.desktopPreferences.closed && s.desktopPreferences.home == s.snapshot.Home
	settingsSystemCard(c, p, func() {
		ui.Row(c).FillWidth().MinWidth(0).Gap(8).AlignItems(ui.Center).Children(func() {
			label(c, "获取新版", 19, p.text).FontWeight(600).Wrap()
			if s.hasAppUpdate() {
				appUpdateDot(c, p, false)
			}
		})
		ui.Divider(c)
		ui.Row(c).FillWidth().MinWidth(0).Wrap().Gap(20).AlignItems(ui.Center).Children(func() {
			ui.Column(c).MinWidth(0).Gap(6).Children(func() {
				label(c, "当前版本", 12, p.muted)
				label(c, domain.CurrentProductVersion, 14, p.text).FontWeight(500)
			})
			text, color := "尚未检查更新", p.muted
			switch {
			case state.checking:
				text = "正在检查…"
			case s.hasAppUpdate():
				text, color = "发现新版 "+state.result.Release.Version, p.blue
			case state.result.CheckedAt != "" && state.result.Release != nil:
				text = "已是最新版本"
			case state.result.CheckedAt != "":
				text = "暂无可用的稳定版本"
			}
			label(c, text, 13, color).Wrap()
		})
		ui.Row(c).FillWidth().MinWidth(0).Gap(12).AlignItems(ui.Center).Children(func() {
			label(c, "自动检查更新", 13, p.text).Grow(1).MinWidth(0).Wrap()
			enabled := !prefs.Disabled
			ui.Switch(c.Key("app-update-enabled"), &enabled).Label("自动检查更新").Disabled(!ready).OnChange(func() {
				s.setAppUpdateSchedule(enabled, prefs.CheckTime)
			})
		})
		ui.Row(c).FillWidth().MinWidth(0).Wrap().Gap(8).AlignItems(ui.Center).Children(func() {
			label(c, "每天", 12, p.muted)
			ui.TextInput(c.Key("app-update-time"), &state.timeDraft).Width(80).Height(34).Placeholder("10:00").Label("每日检查时间").Disabled(!ready).OnChange(func() {
				if clock, valid := normalizeAppUpdateTime(state.timeDraft); valid && clock != s.appUpdatePreferences().CheckTime {
					s.setAppUpdateSchedule(!s.appUpdatePreferences().Disabled, clock)
				}
			}).OnSubmit(func() { s.setAppUpdateSchedule(!s.appUpdatePreferences().Disabled, state.timeDraft) })
			label(c, "本地时间", 12, p.muted)
		})
		if checked, err := time.Parse(time.RFC3339, state.result.CheckedAt); err == nil {
			label(c, "上次检查："+checked.Local().Format("2006-01-02 15:04"), 11, p.muted).Wrap()
		}
		if state.failure != "" {
			label(c, state.failure, 12, p.danger).FillWidth().MinWidth(0).Wrap()
		}
		if install := state.install; install != nil {
			if install.preparing || install.exiting {
				text := "正在下载更新…"
				if install.status.Phase == "recovery" {
					text = "正在准备恢复安装包…"
				}
				if install.exiting {
					text = "正在保存并重启…"
				}
				label(c, text, 12, p.muted).Wrap()
				if install.preparing && install.status.Total > 0 {
					ui.Progress(c, float64(install.status.Downloaded)/float64(install.status.Total)).FillWidth().Label("更新下载进度")
				}
			}
			if install.errorText != "" {
				label(c, install.errorText, 12, p.danger).FillWidth().MinWidth(0).Wrap()
			}
		}
		ui.Column(c).FillWidth().MinWidth(0).Gap(8).Children(func() {
			settingsHelpLabel(c, p, "GitHub Releases", "应用运行时按本地时间检查；错过检查时间会在启动后补查。支持签名更新时可立即更新；其他版本下载后手动安装。", 13, 500)
			label(c, silverwingReleasesURL, 11, p.muted).FillWidth().MinWidth(0).Wrap().Selectable()
		})
		ui.Row(c).FillWidth().MinWidth(0).Wrap().Gap(8).Label("更新操作").Children(func() {
			checkText := "检查更新"
			if state.checking {
				checkText = "检查中…"
			}
			if settingsSystemAction(c, "updates-check", "refresh", checkText, p, settingsSystemActionOutline, !ready || s.backend == nil || s.appUpdatePoster() == nil || state.checking) {
				s.beginAppUpdateCheck(true)
			}
			if s.hasAppUpdate() && state.result.Release.Package != nil {
				install := state.install
				working := install != nil && (install.preparing || install.exiting)
				if state.result.CanAutoUpdate {
					if settingsSystemAction(c, "updates-install", "download", "立即更新", p, settingsSystemActionPrimary, !ready || working || s.backend == nil) {
						s.beginAppUpdateInstall()
					}
				}
				if install != nil && install.preparing {
					if settingsSystemAction(c, "updates-cancel", "close", "取消下载", p, settingsSystemActionOutline, false) {
						s.cancelAppUpdateInstall()
					}
				}
				downloadStyle := settingsSystemActionPrimary
				if state.result.CanAutoUpdate {
					downloadStyle = settingsSystemActionOutline
				}
				if settingsSystemAction(c, "updates-download", "download", "下载新版", p, downloadStyle, working) {
					s.openAppUpdateURL(c, state, state.result.Release.Package.URL)
				}
			}
			if settingsSystemAction(c, "updates-open", "launch", "打开更新页面", p, settingsSystemActionOutline, false) {
				s.openAppUpdateURL(c, state, silverwingReleasesURL)
			}
			if settingsSystemAction(c, "updates-copy", "copy", "复制链接", p, settingsSystemActionOutline, false) {
				s.copyTextToClipboard(c.Services(), silverwingReleasesURL, "更新链接已复制")
			}
		})
	})
}

func (s *app) openAppUpdateURL(c *ui.Context, state *appUpdateState, address string) {
	state.failure = ""
	c.OpenURLThen(address, func(err error) {
		if s.appUpdates != state || state.closed || state.home != s.snapshot.Home {
			return
		}
		if err != nil {
			state.failure = "无法打开链接，请复制发布页地址后重试：" + err.Error()
		}
	})
}
