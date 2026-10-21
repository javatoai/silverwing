package desktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

func (s *app) settingsCLIToolsMenu(c *ui.Context, p palette, tool, title string) {
	s.overflowMenu(c, p, "cli-tools-menu-"+tool, title+"更多操作", false, func(menu *overflowMenuBuilder) {
		if menu.Item("refresh", "刷新状态").Icon("refresh").Chosen() {
			s.requestSettingsCLIStatus(tool, true)
		}
		status := s.settingsCLIStatus[tool]
		command := firstNonEmpty(mapString(status, "command"), mapString(status, "commandPath"))
		s.settingsCLICommandMenuItems(c, menu, tool, command)
		if (tool == "meegle" || tool == "lark") && (dataBool(status, "authenticated") || mapString(status, "authenticationState") == "AUTHENTICATED") {
			pending := false
			if s.cliJobs != nil {
				request := s.cliJobs.requests["auth:"+tool]
				pending = request != nil && (request.action.Kind == "cli.auth.start" || request.action.Kind == "cli.auth.logout")
			}
			menu.Separator()
			menu.Separator()
			if menu.Item("logout", "退出登录").Icon("user").Disabled(pending).Dangerous().Chosen() {
				s.confirmNative("退出 "+strings.Title(tool)+" 登录", "将执行本机 CLI 的退出登录命令。", "已保存的项目空间配置会保留。", func() {
					s.act("cli.auth.logout", "", "", "", "", "", map[string]any{"tool": tool})
				})
			}
		}
	})
}

func (s *app) settingsCLICommandMenuItems(c *ui.Context, menu *overflowMenuBuilder, tool, command string) {
	disabled := s.busy || strings.TrimSpace(command) == ""
	menu.Separator()
	if menu.Item("copy-path", "复制命令路径").Icon("copy").Disabled(disabled).Chosen() {
		s.copyTextToClipboard(c.Services(), command, "命令路径已复制")
	}
	if menu.Item("terminal", "在终端中运行").Icon("terminal").Disabled(disabled).Chosen() {
		s.act("system.cli-terminal", "", "", "", command, "", map[string]any{"tool": tool})
	}
}
