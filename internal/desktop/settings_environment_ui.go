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

func (s *app) renderGitEnvironment(c *ui.Context, p palette) {
	settingsToolStatusRow(c, p, "Git 版本", firstNonEmpty(mapString(s.settingsUnit.state.settingsGitEnvironment, "gitVersion"), "未读取到"), p.text)
	settingsToolStatusRow(c, p, "系统用户", firstNonEmpty(mapString(s.settingsUnit.state.settingsGitEnvironment, "systemUser"), "未读取到"), p.text)
	settingsToolStatusRow(c, p, "全局用户名", gitValueSummary(dataValue(s.settingsUnit.state.settingsGitEnvironment, "globalUserName")), p.text)
	settingsToolStatusRow(c, p, "全局邮箱", gitValueSummary(dataValue(s.settingsUnit.state.settingsGitEnvironment, "globalUserEmail")), p.text)
	credentialHelpers := gitConfigSummaries(dataValue(s.settingsUnit.state.settingsGitEnvironment, "globalCredentialHelpers"))
	settingsToolStatusRow(c, p, "凭据助手", firstNonEmpty(strings.Join(credentialHelpers, "\n"), "未配置"), p.text)
	keySettings := gitConfigSummaries(dataValue(s.settingsUnit.state.settingsGitEnvironment, "globalKeyConfig"))
	settingsToolStatusRow(c, p, "关键配置", firstNonEmpty(strings.Join(keySettings, "\n"), "未配置"), p.text)
	for _, errText := range mapsString(dataValue(s.settingsUnit.state.settingsGitEnvironment, "errors")) {
		settingsToolStatusRow(c, p, "环境读取错误", errText, p.amber)
	}
}

func gitValueSummary(value any) string {
	data := mapData(value)
	if len(data) == 0 {
		return "未配置"
	}
	return firstNonEmpty(mapString(data, "value"), "未配置") + gitOriginSuffix(data)
}

func gitConfigSummaries(value any) []string {
	entries := mapsFromData(value)
	result := make([]string, 0, len(entries))
	for _, entry := range entries {
		result = append(result, mapString(entry, "key")+"="+mapString(entry, "value")+gitOriginSuffix(entry))
	}
	return result
}

func gitOriginSuffix(entry map[string]any) string {
	origin := mapString(entry, "origin")
	if origin == "" {
		return ""
	}
	return "  [" + origin + "]"
}

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
			{"CODEX", "Codex CLI", "AI 命名与 Codex 扩展。"}, {"GIT", "Git", "仓库和远程分支操作。"},
			{"WORKSPACE_COMMAND", "服务快捷命令", "Maven、Gradle 等初始化命令。"}, {"MEEGLE", "Meegle CLI", "需求、项目与登录状态。"},
			{"GENBU", "Genbu CLI", "Tag 构建与发布状态。"},
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
				for _, target := range []string{"CODEX", "GIT", "WORKSPACE_COMMAND", "MEEGLE", "GENBU"} {
					*s.settingsBoolInput("proxy-target:"+target, false) = false
				}
			}
		})
		targetIDs := []string{"CODEX", "GIT", "WORKSPACE_COMMAND", "MEEGLE", "GENBU"}
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

func (s *app) settingsGit(c *ui.Context, p palette) {
	resolved := firstNonEmpty(mapString(s.settingsUnit.state.settingsGitEnvironment, "gitExecutable"), mapString(s.settingsUnit.state.settingsCLIStatus["git"], "command"), mapString(s.settingsUnit.state.settingsCLIStatus["git"], "commandPath"))
	settingsHelpCardWithActions(c, p, "Git 状态", "本机 Git 与全局身份配置。", func() {
		s.overflowMenu(c, p, "settings-git-more", "更多 Git 操作", false, func(menu *overflowMenuBuilder) {
			if menu.Item("refresh", "刷新 Git 状态").Icon("refresh").Disabled(s.busy).Chosen() {
				s.act("git.environment", "", "", "", "", "", nil)
			}
			menu.Separator()
			if menu.Item("copy-result", "复制结果").Icon("copy").Disabled(mapString(s.settingsUnit.state.settingsGitEnvironment, "copyText") == "").Chosen() {
				s.copyTextToClipboard(c.Services(), mapString(s.settingsUnit.state.settingsGitEnvironment, "copyText"), "")
			}
			s.settingsCLICommandMenuItems(c, menu, "git", resolved)
		})
	}, func() {
		gitStatus, gitColor := settingsToolAvailability(s.settingsUnit.state.settingsCLIStatus["git"], p)
		if _, checked := s.settingsUnit.state.settingsCLIStatus["git"]["installed"].(bool); !checked && strings.TrimSpace(mapString(s.settingsUnit.state.settingsGitEnvironment, "gitVersion")) != "" {
			gitStatus, gitColor = "可用", p.green
		}
		settingsToolStatusRow(c, p, "命令状态", gitStatus, gitColor)
		if strings.HasPrefix(s.settingsUnit.state.settingsOutput, "CLI 状态读取失败：") {
			settingsToolStatusRow(c, p, "检测错误", strings.TrimPrefix(s.settingsUnit.state.settingsOutput, "CLI 状态读取失败："), p.amber)
		}
		if source := mapString(s.settingsUnit.state.settingsCLIStatus["git"], "source"); source != "" {
			settingsToolStatusRow(c, p, "来源", cliStatusSourceLabel(source), p.text)
		}
		if len(s.settingsUnit.state.settingsGitEnvironment) == 0 {
			settingsToolStatusRow(c, p, "Git 环境", "未检查", p.muted)
		} else {
			s.renderGitEnvironment(c, p)
		}
	})
	settingsHelpCard(c, p, "Git 命令路径", "留空时使用 PATH 自动探测；更改后自动保存。", func() {
		s.settingsConfigPathRow(c, p, "gitExecutablePath", "Git 可执行文件路径", "留空时使用 PATH 自动探测", false)
		if s.toolsUnit.state.cliJobs == nil || s.toolsUnit.state.cliJobs.requests["status:git"] == nil {
			s.settingsCLIPathDiscoveryHint(c, p, "git")
		}
		s.settingsCLICommandActions(c, p, "git", resolved)
	})
	settingsHelpCard(c, p, "分支写保护", "匹配实际分支完整名称，忽略大小写。\n\n启用后会禁用受保护分支上的 Commit、Push、Commit & Push，以及需要写入分支的测试 Tag 流程。", func() {
		branches := stringArray(s.configArray("blockedGitWriteBranches"))
		for index, branch := range branches {
			current := branch
			ui.Row(c).Height(32).Padding(0, 10).Gap(8).Background(p.soft).Radius(8).Children(func() {
				label(c, current, 11, p.text).Grow(1)
				if iconButton(c, fmt.Sprintf("protected-branch-remove-%d", index), "close", "移除写保护分支", p).Clicked() {
					s.saveConfigField("blockedGitWriteBranches", removeString(branches, current))
				}
			})
		}
		value := s.input("setting:blocked-git-branch", "")
		ui.Row(c).Gap(8).Children(func() {
			ui.TextInput(c.Key("blocked-git-branch"), value).Grow(1).Height(36).Placeholder("例如 master")
			if action(c, "blocked-git-branch-add", "plus", "添加", p, false) {
				branch := strings.TrimSpace(*value)
				if branch != "" && !containsFold(branches, branch) {
					s.saveConfigField("blockedGitWriteBranches", append(branches, branch))
					*value = ""
				}
			}
		})
	})
}

func (s *app) settingsBranchNaming(c *ui.Context, p palette) {
	settingsHelpCard(c, p, "分支名设置", "{num} 表示需求编号，{ai} 表示 AI 英文描述；没有 {ai} 时不会追加描述。", func() {
		s.settingsTextRow(c, p, "defaultBranchPrefix", "默认分支名规则", "例如 feature/{num}_{ai}", false, func(value string) {
			value = strings.TrimSpace(value)
			if strings.ContainsAny(value, " \t\r\n") {
				s.lastError = "默认分支名前缀不能包含空白字符。"
				return
			}
			s.saveConfigField("defaultBranchPrefix", value)
		})
	})
}

func (s *app) settingsTag(c *ui.Context, p palette) {
	settingsHelpCard(c, p, "Tag 设置", "全局开关会影响 Tag 导航、构建入口与其他 Tag 配置。", func() {
		s.settingsSwitch(c, p, "tagEnabled", "全局 Tag 开关", "启用后显示 Tag 页面、构建入口与 Tag 配置。", false)
		s.settingsSwitch(c, p, "allowTaskTagTargetEditing", "允许在任务详情修改测试目标分支", "修改只影响当前任务对应模块。", false)
		maxGroups := s.input("setting:tagHistoryMaxGroups", fmt.Sprint(s.configInt("tagHistoryMaxGroups", 3)))
		ui.Column(c).Gap(4).Children(func() {
			settingsHelpLabel(c, p, "每任务最多保留 Tag 组", "按最近更新时间保留，超过后清理最旧的整组记录。范围：1–1000。", 11, 400)
			ui.TextInput(c.Key("tag-history-max-groups"), maxGroups).Width(180).Height(36).Placeholder("1 到 1000")
		})
		s.autosaveSettingsInput("settings:tagHistoryMaxGroups", maxGroups, func(value string) {
			count, err := parseBoundedInt(value, 1, 1000)
			if err != nil {
				s.lastError = "Tag 组保留数量必须在 1 到 1000 之间。"
				return
			}
			s.saveConfigField("tagHistoryMaxGroups", count)
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
				if menu.Item("copy-latest", "复制最新错误").Icon("copy").Disabled(len(s.settingsUnit.state.settingsLogEntries) == 0).Chosen() {
					s.copyTextToClipboard(c.Services(), formatLogEntry(s.settingsUnit.state.settingsLogEntries[0]), "最新错误已复制")
				}
				if menu.Item("copy-all", "复制全部错误").Icon("copy").Disabled(len(s.settingsUnit.state.settingsLogEntries) == 0).Chosen() {
					s.copyTextToClipboard(c.Services(), formatLogEntries(s.settingsUnit.state.settingsLogEntries), "错误日志已复制")
				}
				if menu.Item("export", "导出诊断信息").Icon("save").Chosen() {
					s.chooseSavePath("导出诊断信息", filepath.Join(s.snapshot.Home, "silverwing-diagnostics.zip"), []mygo.FileFilter{{Name: "压缩文件", Extensions: []string{"zip"}}}, func(path string) {
						s.act("diagnostics.export", "", "", "", path, "", nil)
					})
				}
			})
		})
		if len(s.settingsUnit.state.settingsLogEntries) == 0 {
			settingsMuted(c, p, "暂无错误记录。")
		} else {
			for index, entry := range s.settingsUnit.state.settingsLogEntries {
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
