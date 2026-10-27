package desktop

import (
	"fmt"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"net"
	"net/url"
	"path/filepath"
	"strings"
)

func (s *app) settingsProxy(c *ui.Context, p palette) {
	settingsHelpCard(c, p, "命令网络代理", "只对选中的命令生效；例外地址按逗号分隔。", func() {
		proxy := proxyParts(s.configString("commandProxyUrl"))
		protocol := s.input("setting:proxy-scheme", proxy[0])
		host := s.input("setting:proxy-host", proxy[1])
		port := s.input("setting:proxy-port", proxy[2])
		noProxy := s.input("setting:proxy-no-proxy", s.configString("commandProxyNoProxy"))
		username := s.input("setting:proxy-username", s.configString("commandProxyUsername"))
		password := s.input("setting:proxy-password", s.configString("commandProxyPassword"))
		ui.Row(c).Gap(8).Children(func() {
			label(c, "协议", 11, p.muted).Width(48)
			for _, option := range []string{"http", "https", "socks5"} {
				choice := option
				s.settingsChoice(c, p, "proxy-scheme-"+choice, strings.ToUpper(choice), *protocol == choice, func() { *protocol = choice })
			}
			ui.TextInput(c.Key("proxy-host"), host).Grow(1).Height(36).Placeholder("代理主机")
			ui.TextInput(c.Key("proxy-port"), port).Width(110).Height(36).Placeholder("端口")
		})
		ui.Column(c).Gap(5).Children(func() {
			label(c, "不通过代理的地址", 11, p.muted)
			ui.TextInput(c.Key("proxy-no-proxy"), noProxy).FillWidth().Height(36).Placeholder("*.example.com, 192.168.*")
		})
		useAuth := s.configString("commandProxyUsername") != "" || s.configString("commandProxyPassword") != ""
		authentication := s.localSettingBool("proxy-auth-enabled", useAuth)
		passwordVisible := s.localSettingBool("proxy-password-visible", false)
		settingsSwitchRow(c, p, "proxy-auth-enabled", "代理认证", "需要账号密码时启用。", authentication)
		if *authentication {
			ui.Row(c).Gap(8).Children(func() {
				ui.TextInput(c.Key("proxy-username"), username).Grow(1).Height(36).Placeholder("账号")
				input := ui.TextInput(c.Key("proxy-password"), password).Grow(1).Height(36).Placeholder("密码").Label("代理密码")
				if !*passwordVisible {
					input.Password()
				}
				if action(c, "proxy-password-visibility", "eye", map[bool]string{true: "隐藏密码", false: "显示密码"}[*passwordVisible], p, false) {
					*passwordVisible = !*passwordVisible
				}
			})
		} else {
			*passwordVisible = false
		}
		label(c, "代理目标", 12, p.text).FontWeight(500)
		for _, target := range []struct{ id, title, description string }{
			{"MEEGLE", "Meegle CLI", "需求、项目与登录状态。"},
			{"CODEX", "Codex CLI", "AI 命名与 Codex 扩展。"}, {"GIT", "资料与 Skill 同步", "同步任务资料和 Skill 来源。"},
		} {
			selected := containsString(stringArray(s.configArray("commandProxyTargets")), target.id)
			value := s.settingsBoolInput("proxy-target:"+target.id, selected)
			settingsSwitchRow(c, p, "proxy-target-"+target.id, target.title, target.description, value)
		}
		ui.Row(c).Gap(8).Children(func() {
			if action(c, "proxy-test", "search", "测试代理", p, false) {
				s.act("proxy.test", "", "", "", "", "", map[string]any{"url": proxyURL(*host, *port, *protocol)})
			}
			if action(c, "proxy-clear", "close", "清除代理", p, false) {
				*protocol, *host, *port, *noProxy, *username, *password = "http", "", "80", "", "", ""
				*authentication = false
				*passwordVisible = false
				for _, target := range []string{"CODEX", "GIT", "MEEGLE"} {
					*s.settingsBoolInput("proxy-target:"+target, false) = false
				}
			}
		})
		targetIDs := []string{"CODEX", "GIT", "MEEGLE"}
		targetInputs := make([]*bool, 0, len(targetIDs))
		for _, target := range targetIDs {
			targetInputs = append(targetInputs, s.settingsBoolInput("proxy-target:"+target, containsString(stringArray(s.configArray("commandProxyTargets")), target)))
		}
		s.observeSettingsProxyAutosave(func() settingsProxyDraft {
			targets := []string{}
			for index, input := range targetInputs {
				if *input {
					targets = append(targets, targetIDs[index])
				}
			}
			return settingsProxyDraft{scheme: *protocol, host: *host, port: *port, noProxy: *noProxy,
				username: *username, password: *password, authentication: *authentication, targets: targets}
		})
	})
}

func (s *app) settingsLogs(c *ui.Context, p palette) {
	settingsHelpCard(c, p, "", "", func() {
		ui.Row(c).FillWidth().Wrap().Gap(8).AlignItems(ui.Center).Children(func() {
			if action(c, "logs-refresh", "refresh", "刷新日志", p, false) {
				s.act("logs.list", "", "", "", "", "", map[string]any{"filter": "recent"})
			}
			if action(c, "logs-open-folder", "folder", "打开日志目录", p, false) {
				s.act("system.logs", "", "", "", "", "", nil)
			}
			ui.Spacer(c)
			s.settingsLogsClearButton(c, p)
			s.overflowMenu(c, p, "logs-more", "更多日志操作", false, func(menu *overflowMenuBuilder) {
				if menu.Item("refresh-status", "刷新本地状态").Icon("refresh").Chosen() {
					s.act("task.refresh", "", "", "", "", "", nil)
				}
				if menu.Item("copy-latest", "复制最新错误").Icon("copy").Disabled(len(s.settingsLogEntries) == 0).Chosen() {
					s.copyTextToClipboard(c.Services(), formatLogEntry(s.settingsLogEntries[0]), "最新错误已复制")
				}
				if menu.Item("copy-all", "复制全部错误").Icon("copy").Disabled(len(s.settingsLogEntries) == 0).Chosen() {
					s.copyTextToClipboard(c.Services(), formatLogEntries(s.settingsLogEntries), "错误日志已复制")
				}
				if menu.Item("export", "导出诊断信息").Icon("save").Chosen() {
					s.chooseSavePath("导出诊断信息", filepath.Join(s.snapshot.Home, "silverwing-diagnostics.zip"), []mygo.FileFilter{{Name: "压缩文件", Extensions: []string{"zip"}}}, func(path string) {
						s.act("diagnostics.export", "", "", "", path, "", nil)
					})
				}
			})
		})
		if len(s.settingsLogEntries) == 0 {
			settingsMuted(c, p, "暂无错误记录。")
		} else {
			for index, entry := range s.settingsLogEntries {
				ui.Column(c.Key(fmt.Sprintf("log-entry-%d", index))).FillWidth().Padding(10, 12).Gap(4).Background(p.soft).Radius(8).Children(func() {
					label(c, firstNonEmpty(mapString(entry, "timestamp"), "未知时间")+" · "+mapString(entry, "event"), 10, p.muted).SingleLine()
					label(c, mapString(entry, "message"), 11, p.text).LineHeight(1.45).Selectable()
				})
			}
		}
	})
}

func (s *app) settingsDraftPath(key string) *string {
	return s.input("setting:"+key, s.configString(key))
}

func (s *app) settingsProxyDraftValue(key, value string) *string {
	return s.input("setting:"+key, value)
}

func proxyParts(raw string) [3]string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return [3]string{"http", "", "80"}
	}
	if endpoint, err := url.Parse(raw); err == nil && endpoint.Scheme != "" && endpoint.Host != "" {
		return [3]string{strings.ToLower(endpoint.Scheme), endpoint.Hostname(), endpoint.Port()}
	}
	return [3]string{"http", raw, "80"}
}

func proxyURL(host, port, scheme string) string {
	host, port = strings.TrimSpace(host), strings.TrimSpace(port)
	if host == "" {
		return ""
	}
	if scheme == "" {
		scheme = "http"
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	return strings.ToLower(scheme) + "://" + net.JoinHostPort(host, port)
}

func formatLogEntry(entry map[string]any) string {
	return strings.TrimSpace(strings.Join([]string{
		firstNonEmpty(mapString(entry, "timestamp"), "未知时间") + " · " + mapString(entry, "event"),
		mapString(entry, "message"),
	}, "\n"))
}

func formatLogEntries(entries []map[string]any) string {
	lines := make([]string, 0, len(entries)*2)
	for _, entry := range entries {
		if value := formatLogEntry(entry); value != "" {
			lines = append(lines, value)
		}
	}
	return strings.Join(lines, "\n\n")
}
