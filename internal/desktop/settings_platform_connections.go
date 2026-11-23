package desktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
)

func legacyPlatformSettingsTarget(page string) string {
	switch page {
	case "cli-genbu":
		return "Genbu 连接"
	case "cli-dragon":
		return "Dragon 连接"
	}
	return ""
}

func (s *app) settingsPlatformConnections(c *ui.Context, p palette) {
	state := s.ensurePlatformConnections()
	s.requestPlatformConnections(false)
	s.observePlatformLoginTests(state)
	settingsReveal(c, ui.Column(c.Key("platform-account")).FillWidth(), p, "共用平台账号").Children(func() {
		settingsCard(c, p, "共用平台账号", "保存会更新 Genbu 和 Dragon 原工具的账号。各配置目录共用账号，分别保存连接开关。", func() {
			if state.readError != "" {
				label(c, state.readError, 11, p.danger).Wrap()
				if settingsCardAction(c, p, "platform-reload", "refresh", "重新读取") {
					s.requestPlatformConnections(true)
				}
			}
			if state.loaded {
				s.settingsPlatformAccount(c, p, state)
			} else if state.readError == "" {
				label(c, "正在读取连接设置…", 12, p.muted)
			}
		})
	})
	settingsCard(c, p, "", "", func() {
		s.settingsPlatformConnection(c, p, state, "genbu", "Genbu", "构建、发布与服务运行状态")
		ui.Divider(c)
		s.settingsPlatformConnection(c, p, state, "dragon", "Dragon", "青龙提测审批")
	})
}

func (s *app) settingsPlatformAccount(c *ui.Context, p palette, state *desktopPlatformConnections) {
	s.requestPlatformPassword(state)
	if state.value.AccountError != "" {
		label(c, state.value.AccountError, 11, p.danger).Wrap()
	}
	username := strings.TrimSpace(state.username)
	password := ""
	if state.passwordEdited {
		password = state.password
	}
	canSave := state.save == nil && username != "" && (password != "" || state.value.Configured && username == state.value.Username)
	ui.Column(c).FillWidth().Gap(9).Disabled(state.save != nil).Children(func() {
		label(c, "账号", 12, p.text).FontWeight(500)
		ui.TextInput(c.Key("platform-username"), &state.username).FillWidth().Height(36).Label("平台账号").OnChange(func() {
			if !state.passwordEdited && strings.TrimSpace(state.username) != state.value.Username {
				state.resetPlatformPassword()
			}
		})
		label(c, "密码", 12, p.text).FontWeight(500)
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			input := ui.TextInput(c.Key("platform-password"), &state.password).Grow(1).MinWidth(0).Height(36).Label("平台密码").OnChange(func() {
				state.passwordEdited = true
				state.passwordReadError = ""
			})
			if state.value.Configured && username == state.value.Username {
				input.Placeholder("留空保留已保存的密码")
			}
			if !state.passwordVisible {
				input.Password()
			}
			visibilityIcon, visibilityLabel := "eye", "显示密码"
			if state.passwordVisible {
				visibilityIcon, visibilityLabel = "eye-off", "隐藏密码"
			}
			if miniButton(c, p, "platform-password-visibility", visibilityIcon, visibilityLabel, false).FocusRing(true).Clicked() {
				state.passwordVisible = !state.passwordVisible
			}
		})
		if state.passwordReadError != "" {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
				label(c, state.passwordReadError, 11, p.danger)
				if settingsCardAction(c, p, "platform-reload-password", "refresh", "重新读取") {
					state.passwordRequested, state.passwordReadError = false, ""
					s.requestPlatformConnections(true)
				}
			})
		}
	})
	ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Wrap().Children(func() {
		ui.Box(c).Disabled(!canSave).Children(func() {
			if action(c, "platform-save-account", "", "保存账号", p, true) && canSave {
				s.savePlatformConnection(state, application.Action{Kind: "platform.credentials.save", Payload: map[string]any{
					"username": username, "password": password,
				}})
			}
		})
		message, color := state.saveMessage, p.muted
		if state.saveFailed {
			color = p.danger
		}
		if state.save != nil {
			message = "正在保存…"
		}
		if message != "" {
			label(c, message, 11, color).Wrap()
		}
	})
}

func (s *app) settingsPlatformConnection(c *ui.Context, p palette, state *desktopPlatformConnections, tool, name, description string) {
	title := name + " 连接"
	connection, enabled := state.connection(tool), state.enabled(tool)
	if state.save == nil {
		connection.draftEnabled = enabled
	}
	status := "已停用"
	switch {
	case !state.loaded:
		status = "未读取"
	case enabled && !state.value.Configured:
		status = "需要保存账号"
	case connection.test != nil:
		status = "正在测试登录…"
	case connection.testMessage != "":
		status = connection.testMessage
	case enabled:
		status = "已启用"
	}
	row := ui.Column(c.Key("platform-connection-" + tool)).FillWidth().MinWidth(0).Gap(6)
	settingsReveal(c, row, p, title).Children(func() {
		ui.Row(c).FillWidth().MinWidth(0).MinHeight(42).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
				label(c, title, 14, p.text).FontWeight(600).Wrap()
				label(c, description, 11, p.muted).Wrap()
			})
			ui.Box(c).Shrink(0).Role(ui.RoleStatus).Label(name + " " + status).Children(func() {
				compactState(c, status, p)
			})
			ui.Switch(c.Key("settings-switch-platform-"+tool), &connection.draftEnabled).Shrink(0).
				Label("启用 " + name).Disabled(!state.loaded || state.save != nil).OnChange(func() {
				s.savePlatformConnection(state, application.Action{Kind: "platform.connection.set", ServiceID: tool, Payload: map[string]any{"enabled": connection.draftEnabled}})
			})
		})
		if enabled && connection.testFailed {
			label(c, "请核对账号或网络后重新进入此页。", 11, p.muted).Wrap()
		}
	})
}
