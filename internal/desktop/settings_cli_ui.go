package desktop

import (
	"fmt"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"

	"strings"
	"time"
)

func (s *app) settingsCLI(c *ui.Context, p palette) {
	tool, title, pathKey := s.cliSettingsTarget()
	if tool == "" {
		return
	}
	settingsHelpCardWithActions(c, p, title+" 状态", "", func() {
		s.settingsCLIToolsMenu(c, p, tool, title)
	}, func() {
		s.settingsCLIStatusDetails(c, p, tool)
		if tool == "meegle" {
			s.settingsCLIAuth(c, p, tool)
		}
	})
	settingsHelpCard(c, p, title+" 命令路径", "留空时自动探测，也可以手动选择；更改后自动保存。", func() {
		s.settingsConfigPathRow(c, p, pathKey, title+" 可执行文件", "留空时自动探测", false)
		if s.toolsUnit.state.cliJobs == nil || s.toolsUnit.state.cliJobs.requests["status:"+tool] == nil {
			s.settingsCLIPathDiscoveryHint(c, p, tool)
		}
		status := s.settingsUnit.state.settingsCLIStatus[tool]
		command := firstNonEmpty(mapString(status, "command"), mapString(status, "commandPath"))
		s.settingsCLICommandActions(c, p, tool, command)
	})
	if tool == "meegle" {
		s.settingsMeegleProjects(c, p)
	}
}

func (s *app) settingsCLIStatusDetails(c *ui.Context, p palette, tool string) {
	status := s.settingsUnit.state.settingsCLIStatus[tool]
	state, color := settingsToolAvailability(status, p)
	settingsToolStatusRow(c, p, "命令状态", state, color)
	if version := strings.TrimSpace(mapString(status, "version")); version != "" && tool != "git" {
		settingsToolStatusRow(c, p, "版本", version, p.text)
	}
	if source := mapString(status, "source"); source != "" {
		settingsToolStatusRow(c, p, "来源", cliStatusSourceLabel(source), p.text)
	}
	if message := mapString(status, "error"); message != "" {
		settingsToolStatusRow(c, p, "检测错误", message, p.amber)
	} else if strings.HasPrefix(s.settingsUnit.state.settingsOutput, "CLI 状态读取失败：") {
		settingsToolStatusRow(c, p, "检测错误", strings.TrimPrefix(s.settingsUnit.state.settingsOutput, "CLI 状态读取失败："), p.amber)
	}
}

func cliAuthLinkRemaining(session map[string]any) string {
	expiry := dataInt64(session, "expiresAtEpochMillis")
	if expiry <= 0 {
		return ""
	}
	remaining := time.Until(time.UnixMilli(expiry)).Round(time.Second)
	if remaining < 0 {
		remaining = 0
	}
	return fmt.Sprintf("%d分%02d秒", int(remaining.Minutes()), int(remaining.Seconds())%60)
}

// settingsCLICommandActions only explains a resolved command that differs from
// the configured executable. Command actions live in the card's menu.
func (s *app) settingsCLICommandActions(c *ui.Context, p palette, tool, command string) {
	switch tool {
	case "git", "codex", "genbu", "meegle":
	default:
		return
	}
	if strings.TrimSpace(command) == "" || sameTaskRepositoryPath(s.cliConfiguredPath(tool), command) {
		return
	}
	label(c, "实际命令："+command, 10, p.muted).Wrap().Selectable()
}

func cliStatusSourceLabel(source string) string {
	if label := map[string]string{"CONFIGURED": "手动配置", "DESKTOP_APP": "Codex 桌面应用", "PROBED": "自动检测", "PATH_FALLBACK": "系统 PATH"}[source]; label != "" {
		return label
	}
	return "未确认"
}

func (s *app) settingsCLIAuth(c *ui.Context, p palette, tool string) {
	if s.toolsUnit.state.cliJobs != nil {
		if request := s.toolsUnit.state.cliJobs.requests["auth:"+tool]; request != nil && (request.action.Kind == "cli.auth.start" || request.action.Kind == "cli.auth.logout") {
			message := "正在生成授权链接…"
			if request.action.Kind == "cli.auth.logout" {
				message = "正在退出登录…"
			}
			label(c, message, 11, p.muted)
			if action(c, "cli-auth-cancel-init-"+tool, "close", "取消", p, false) {
				request.userCancelled = true
				request.cancel()
				s.settingsUnit.state.settingsOutput = "已请求取消登录操作，正在等待进程清理。"
			}
			return
		}
	}
	status := s.settingsUnit.state.settingsCLIStatus[tool]
	authState := mapString(status, "authenticationState")
	if authState == "" || authState == "NOT_CHECKED" {
		if dataBool(status, "authenticated") {
			authState = "AUTHENTICATED"
		} else if authState == "" {
			if _, checked := status["authenticated"].(bool); checked {
				if mapString(status, "authenticationError") != "" {
					authState = "CHECK_FAILED"
				} else {
					authState = "LOGIN_REQUIRED"
				}
			}
		}
	}
	statusLabel, statusColor := "未检查", p.muted
	switch authState {
	case "AUTHENTICATED":
		statusLabel, statusColor = "已登录"+cliIdentitySuffix(status), p.green
	case "LOGIN_REQUIRED":
		statusLabel, statusColor = "尚未登录", p.muted
	case "CHECK_FAILED":
		statusLabel, statusColor = "登录状态读取失败", p.amber
	case "NOT_INSTALLED":
		statusLabel, statusColor = "CLI 未安装", p.amber
	case "NOT_CHECKED":
		statusLabel, statusColor = "未检查", p.muted
	}
	settingsToolStatusRow(c, p, "登录状态", statusLabel, statusColor)
	if errorText := mapString(status, "authenticationError"); errorText != "" {
		settingsToolStatusRow(c, p, "登录检查", errorText, p.amber)
	}
	if authState != "AUTHENTICATED" && authState != "NOT_INSTALLED" {
		if action(c, "cli-auth-start-"+tool, "launch", "登录 Meegle CLI", p, true) {
			s.act("cli.auth.start", "", "", "", "", "", map[string]any{"tool": "meegle", "host": "project.feishu.cn"})
		}
	}
	if session := s.settingsUnit.state.settingsCLISessions[tool]; len(session) > 0 {
		state := mapString(session, "state")
		if tool == "meegle" {
			label(c, "设备码状态："+cliAuthStateLabel(state), 10, cliAuthStateColor(state, p))
		}
		if userCode := mapString(session, "userCode"); userCode != "" {
			label(c, "验证码："+userCode, 15, p.text).FontWeight(600).Selectable()
		}
		if url := mapString(session, "authorizationUrl"); url != "" {
			ui.Row(c).Gap(7).Children(func() {
				if action(c, "cli-auth-open-"+tool, "launch", "打开授权页", p, false) {
					mygo.Shell.OpenExternal(url)
				}
				if action(c, "cli-auth-copy-"+tool, "copy", "复制授权链接", p, false) {
					s.copyTextToClipboard(c.Services(), url, "授权链接已复制")
				}
				if userCode := mapString(session, "userCode"); userCode != "" && action(c, "cli-auth-copy-code-"+tool, "copy", "复制验证码", p, false) {
					s.copyTextToClipboard(c.Services(), userCode, "验证码已复制")
				}
			})
		}
		if tool == "meegle" {
			if remaining := cliAuthLinkRemaining(session); remaining != "" {
				label(c, "授权链接剩余 "+remaining, 10, p.muted)
			}
		}
		if errorText := mapString(session, "error"); errorText != "" {
			label(c, errorText, 10, p.amber).LineHeight(1.4).Selectable()
		}
		if state == "AUTHORIZATION_PENDING" || state == "CONFIRMING" {
			ui.Row(c).Gap(7).Children(func() {
				if action(c, "cli-auth-check-"+tool, "refresh", "检查授权状态", p, false) {
					s.act("cli.auth.session", "", "", "", "", "", map[string]any{"tool": tool, "sessionId": mapString(session, "sessionId")})
				}
				if action(c, "cli-auth-cancel-"+tool, "close", "取消登录", p, false) {
					s.settingsUnit.stopCLIAuthPoll(tool)
					s.act("cli.auth.cancel", "", "", "", "", "", map[string]any{"tool": tool, "sessionId": mapString(session, "sessionId")})
				}
			})
		}
	}
}

func (s *app) applyCLIAuthResult(action application.Action, result application.ActionResult) {
	data := mapData(result.Data)
	tool := strings.ToLower(firstNonEmpty(mapString(data, "tool"), payloadString(action.Payload, "tool")))
	if tool == "" {
		return
	}
	if s.settingsUnit.state.settingsCLISessions == nil {
		s.settingsUnit.state.settingsCLISessions = make(map[string]map[string]any)
	}
	s.settingsUnit.state.settingsCLISessions[tool] = data
	s.settingsUnit.state.settingsOutput = result.Message
	if action.Kind == "cli.auth.start" {
		if url := mapString(data, "authorizationUrl"); url != "" {
			mygo.Shell.OpenExternal(url)
		}
	}
	state := mapString(data, "state")
	if state == "AUTHORIZATION_PENDING" || state == "CONFIRMING" {
		s.scheduleCLIAuthPoll(tool, data)
	} else {
		s.settingsUnit.stopCLIAuthPoll(tool)
	}
	if state == "AUTHENTICATED" {
		if tool == "meegle" {
			s.clearMeegleSessionViews()
		}
		s.act("cli.status", "", "", "", "", "", map[string]any{"tool": tool})
	}
}

func (s *app) scheduleCLIAuthPoll(tool string, session map[string]any) {
	s.settingsUnit.stopCLIAuthPoll(tool)
	delay := time.Second
	if tool == "meegle" {
		seconds := dataInt64(session, "pollingIntervalSeconds")
		if seconds < 1 {
			seconds = 2
		}
		if seconds > 60 {
			seconds = 60
		}
		delay = time.Duration(seconds) * time.Second
	}
	sessionID := mapString(session, "sessionId")
	if s.settingsUnit.state.settingsAuthPollTimers == nil {
		s.settingsUnit.state.settingsAuthPollTimers = make(map[string]*time.Timer)
	}
	s.settingsUnit.state.settingsAuthPollTimers[tool] = time.AfterFunc(delay, func() {
		if s.win == nil || s.win.IsDestroyed() {
			return
		}
		s.win.Update(func() {
			current := s.settingsUnit.state.settingsCLISessions[tool]
			if mapString(current, "sessionId") != sessionID || (mapString(current, "state") != "AUTHORIZATION_PENDING" && mapString(current, "state") != "CONFIRMING") {
				return
			}
			s.act("cli.auth.poll", "", "", "", "", "", map[string]any{"tool": tool, "sessionId": sessionID})
		})
	})
}

func (s *settingsController) stopCLIAuthPoll(tool string) {
	if timer := s.state.settingsAuthPollTimers[tool]; timer != nil {
		timer.Stop()
		delete(s.state.settingsAuthPollTimers, tool)
	}
}

func cliAuthStateLabel(state string) string {
	return map[string]string{"AUTHORIZATION_PENDING": "等待浏览器授权", "CONFIRMING": "正在确认登录", "AUTHENTICATED": "已登录", "EXPIRED": "授权已过期", "FAILED": "授权失败", "CANCELLED": "已取消"}[state]
}

func cliAuthStateColor(state string, p palette) ui.Color {
	if state == "AUTHENTICATED" {
		return p.green
	}
	if state == "FAILED" || state == "EXPIRED" || state == "CANCELLED" {
		return p.amber
	}
	return p.blue
}

func cliIdentitySuffix(status map[string]any) string {
	return map[bool]string{true: " · " + firstNonEmpty(mapString(status, "brand"), mapString(status, "host")), false: ""}[firstNonEmpty(mapString(status, "brand"), mapString(status, "host")) != ""]
}

func (s *app) cliSettingsTarget() (tool, title, pathKey string) {
	switch s.settingsUnit.state.settingsPage {
	case "cli-meegle":
		return "meegle", "Meegle CLI", "meegleExecutablePath"
	case "cli-genbu":
		return "genbu", "Genbu CLI", "genbuExecutablePath"
	default:
		return "", "", ""
	}
}
