package desktop

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

func (s *app) settingsCodexPlugins(c *ui.Context, p palette) {
	settingsCard(c, p, "", "", func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			if action(c, "codex-marketplace-add", "plus", "添加来源", p, false) {
				s.openForm("codex.extensions/marketplaces/add-source", "添加 Codex Marketplace", "", "", "", "", formField{key: "name", label: "显示名称"}, formField{key: "repositoryUrl", label: "Git 仓库地址"}, formField{key: "ref", label: "分支或标签（可留空）"})
			}
			ui.Spacer(c)
			s.overflowMenu(c, p, "codex-extensions-actions", "更多插件来源操作", false, func(m *overflowMenuBuilder) {
				if m.Item("reload", "重新读取").Icon("refresh").Chosen() {
					s.act("codex.extensions/snapshot", "", "", "", "", "", nil)
				}
			})
		})
		sources := s.settingsMarketplaces
		if len(sources) == 0 {
			settingsMuted(c, p, "还没有插件来源。仓库中需包含 Codex Marketplace 清单。")
		}
		for _, wrapper := range sources {
			source := mapData(wrapper["source"])
			status := mapData(wrapper["status"])
			sourceID := mapString(source, "id")
			name := firstNonEmpty(mapString(source, "name"), sourceID)
			plugins := mapsFromData(dataValue(status, "plugins"))
			ui.Column(c).FillWidth().Padding(12).Gap(8).Background(p.soft).Radius(10).Children(func() {
				ui.Row(c).Gap(8).Children(func() {
					ui.Column(c).Grow(1).Gap(3).Children(func() {
						label(c, name, 12, p.text).FontWeight(600)
						statusSummary, statusColor := "目录未读取", p.muted
						if mapString(status, "error") != "" {
							statusSummary, statusColor = "目录读取失败", p.amber
						} else if len(status) > 0 {
							statusSummary, statusColor = fmt.Sprintf("目录已读取 · %d 个插件", len(plugins)), p.green
							if marketplaceName := mapString(status, "marketplaceName"); marketplaceName != "" {
								statusSummary = marketplaceName + " · " + fmt.Sprintf("%d 个插件", len(plugins))
							}
						}
						label(c, statusSummary, 10, statusColor).Wrap()
						if errText := mapString(status, "error"); errText != "" {
							label(c, "读取失败："+errText, 10, p.amber).LineHeight(1.4).Wrap()
						}
						label(c, mapString(source, "repositoryUrl"), 10, p.muted).SingleLine()
					})
					s.overflowMenu(c, p, "codex-marketplace-more-"+sourceID, "更多来源操作 · "+name, false, func(m *overflowMenuBuilder) {
						if m.Item("refresh-source", "刷新目录").Icon("refresh").Chosen() {
							s.act("codex.extensions/marketplaces/refresh-source", "", "", "", "", "", map[string]any{"sourceId": sourceID})
						}
						m.Separator()
						if m.Item("remove-source", "移除来源").Icon("delete").Dangerous().Chosen() {
							s.confirmNative("移除插件来源", "将从 Codex 移除 Marketplace 并删除 SilverWing 本机缓存。已安装插件不会自动卸载。", name, func() {
								s.act("codex.extensions/marketplaces/remove-source", "", "", "", "", "", map[string]any{"sourceId": sourceID, "confirmed": true})
							})
						}
					})
				})
				if len(plugins) == 0 {
					settingsMuted(c, p, "刷新来源后显示插件目录。")
				}
				for pluginIndex, plugin := range plugins {
					pluginName := mapString(plugin, "name")
					installed, _ := plugin["installed"].(bool)
					ownership := mapString(plugin, "ownership")
					managed := ownership == "MANAGED"
					ui.Row(c).Padding(8, 10).Gap(8).Background(p.surface).Radius(8).Children(func() {
						ui.Column(c).Grow(1).Gap(3).Children(func() {
							label(c, pluginName, 11, p.text).FontWeight(500)
							label(c, extensionOwnershipLabel(ownership), 10, p.muted)
							label(c, mapString(plugin, "description"), 10, p.muted).LineHeight(1.35)
						})
						if action(c, fmt.Sprintf("codex-plugin-preview-%s-%d", sourceID, pluginIndex), "file", "预览", p, false) {
							s.act("codex.extensions/plugin/preview", "", "", "", "", "", map[string]any{"sourceId": sourceID, "name": pluginName})
						}
						if !installed || !managed {
							buttonLabel := "安装"
							if installed {
								buttonLabel = "接管"
							}
							if action(c, fmt.Sprintf("codex-plugin-install-%s-%d", sourceID, pluginIndex), "plus", buttonLabel, p, false) {
								takeOver := installed
								message := "将通过本机 Codex CLI 安装此插件。"
								if takeOver {
									message = "此插件已安装。确认接管后，SilverWing 才能管理其卸载操作。"
								}
								s.confirmNative(buttonLabel+" Codex 插件", message, pluginName, func() {
									s.act("codex.extensions/plugin/install", "", "", "", "", "", map[string]any{"sourceId": sourceID, "name": pluginName, "takeOver": takeOver, "confirmed": true})
								})
							}
						}
						bundledSkills := stringArray(mustArray(plugin["bundledSkills"]))
						if len(bundledSkills) > 0 || installed && managed {
							s.overflowMenu(c, p, "codex-plugin-more-"+sourceID+":"+pluginName, "更多插件操作 · "+pluginName, false, func(m *overflowMenuBuilder) {
								for _, skillName := range bundledSkills {
									if m.Item("preview-skill-"+skillName, "预览 Skill · "+skillName).Icon("file").Chosen() {
										s.act("codex.extensions/plugin/skill-preview", "", "", "", "", "", map[string]any{"sourceId": sourceID, "pluginName": pluginName, "skillName": skillName})
									}
								}
								if installed && managed {
									if len(bundledSkills) > 0 {
										m.Separator()
									}
									if m.Item("uninstall", "卸载").Icon("delete").Dangerous().Chosen() {
										s.confirmNative("卸载 Codex 插件", "将通过本机 Codex CLI 卸载此 SilverWing 管理的插件。", pluginName, func() {
											s.act("codex.extensions/plugin/uninstall", "", "", "", "", "", map[string]any{"sourceId": sourceID, "name": pluginName, "confirmed": true})
										})
									}
								}
							})
						}
					})
				}
			})
		}
	})
}

func (s *app) settingsCodexMCP(c *ui.Context, p palette) {
	pending := s.settingsMCPOperationPending()
	scopeKey := s.snapshot.Home + "\x00" + fmt.Sprint(s.settingsMCPTaskScope)
	if s.settingsMCPTaskScope {
		task := s.currentTask()
		scopeKey += "\x00" + strings.Join([]string{task.id, task.createdAt, task.path}, "\x00")
	}
	settingsCard(c, p, "", "", func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Disabled(pending).Children(func() {
			for _, scope := range []struct {
				task  bool
				title string
			}{{false, "全局配置"}, {true, "当前任务"}} {
				if scope.task && s.currentTask().path == "" {
					continue
				}
				s.settingsChoice(c, p, "mcp-scope-"+scope.title, scope.title, s.settingsMCPTaskScope == scope.task, func() {
					s.settingsMCPTaskScope = scope.task
					s.settingsMCPPath = ""
					s.settingsMCPServers = nil
					s.act("codex.extensions/mcp/status", "", "", "", "", "", s.settingsMCPPayload(nil))
				})
			}
			if action(c, "codex-mcp-add", "plus", "添加服务", p, true) {
				scope := "全局配置"
				if s.settingsMCPTaskScope {
					scope = "当前任务"
				}
				fields := s.mcpFormFields(nil, "", scope, s.currentTask().path != "")
				s.openForm("codex.extensions/mcp/add", "添加 MCP 服务", "", "", "", "", fields...)
			}
			ui.Spacer(c)
			s.overflowMenu(c, p, "codex-mcp-actions-"+scopeKey, "更多 MCP 管理操作", pending, func(m *overflowMenuBuilder) {
				if m.Item("read-status", "读取状态").Icon("refresh").Disabled(pending).Chosen() {
					s.act("codex.extensions/mcp/status", "", "", "", "", "", s.settingsMCPPayload(nil))
				}
				if m.Item("check-connection", "检查连接").Icon("check").Disabled(pending).Chosen() {
					s.act("codex.extensions/mcp/check", "", "", "", "", "", s.settingsMCPPayload(nil))
				}
			})
		})
		if s.settingsMCPPath != "" {
			settingsPathValue(c, p, "当前配置", s.settingsMCPPath)
		} else if s.settingsMCPTaskScope {
			settingsPathValue(c, p, "当前任务", s.currentTask().path)
		}
		for _, warning := range s.settingsMCPWarnings {
			label(c, warning, 10, p.amber).LineHeight(1.4)
		}
		if s.settingsMCPConnectionError != "" {
			label(c, s.settingsMCPConnectionError, 10, p.amber).LineHeight(1.4)
		}
		if len(s.settingsMCPServers) == 0 {
			settingsMuted(c, p, "尚未读取到 MCP 服务。")
		}
		for index, server := range s.settingsMCPServers {
			name := firstNonEmpty(mapString(server, "name"), mapString(server, "id"))
			raw := mapData(server["raw"])
			connection := mapData(s.settingsMCPConnections[name])
			connected, hasConnection := connection["success"].(bool)
			enabled, ok := raw["enabled"].(bool)
			if !ok {
				enabled = true
			}
			editable, toggle, authenticate := settingsMCPServerCapabilities(server)
			ui.Row(c).Padding(9, 10).Gap(8).Background(p.soft).Radius(8).Disabled(pending).Children(func() {
				ui.Column(c).Grow(1).Gap(2).Children(func() {
					label(c, name, 12, p.text).FontWeight(500)
					if hasConnection {
						status := firstNonEmpty(mapString(connection, "label"), map[bool]string{true: "已连接", false: "未连接"}[connected])
						tools := mapsFromData(dataValue(connection, "tools"))
						if len(tools) > 0 {
							status += fmt.Sprintf(" · %d 个工具", len(tools))
						}
						label(c, status, 9, map[bool]ui.Color{true: p.green, false: p.amber}[connected]).Wrap()
					} else {
						label(c, "连接状态未检查", 9, p.muted)
					}
					label(c, strings.Join(stringArray(mustArray(raw["args"])), " "), 10, p.muted).SingleLine()
					label(c, firstNonEmpty(mapString(server, "scope"), "用户配置")+" · "+mapString(server, "file"), 9, p.muted).SingleLine()
					if note := mapString(server, "note"); note != "" {
						label(c, note, 10, p.amber).LineHeight(1.35)
					}
				})
				if toggle {
					value := enabled
					ui.Switch(c.Key("codex-mcp-enabled-"+mapString(server, "file")+":"+name+":"+mapString(server, "pluginId")), &value).Label("启用").OnChange(func() {
						if s.settingsMCPOperationPending() {
							return
						}
						request := s.settingsMCPSwitchAction(server, value)
						if s.settingsMCPSwitchResultCurrent(request) {
							s.dispatch(request)
						}
					})
				} else {
					label(c, "受管理的配置", 9, p.muted)
				}
				if editable {
					if action(c, fmt.Sprintf("codex-mcp-edit-%d", index), "edit", "编辑", p, false) {
						encoded, _ := json.Marshal(raw)
						s.openForm("codex.extensions/mcp/edit", "编辑 MCP 服务", "", "", "", mapString(server, "file"), s.mcpFormFields(raw, name, "", false)...)
						s.form.text = string(encoded)
					}
				}
				if editable || authenticate {
					menuKey := "codex-mcp-more-" + scopeKey + "\x00" + strings.Join([]string{mapString(server, "file"), name, mapString(server, "pluginId")}, "\x00")
					s.overflowMenu(c, p, menuKey, "更多 MCP 服务操作 · "+name, pending, func(m *overflowMenuBuilder) {
						if authenticate {
							if m.Item("logout", "退出授权").Icon("close").Disabled(pending).Chosen() {
								payload := s.settingsMCPPayload(map[string]any{"name": name, "login": false})
								s.act("codex.ai/mcp/authenticate", "", "", "", "", "", payload)
							}
						}
						if editable {
							if authenticate {
								m.Separator()
							}
							if m.Item("remove", "移除 MCP 服务").Icon("delete").Dangerous().Disabled(pending).Chosen() {
								s.confirmNative("移除 MCP 服务", "将从 Codex 配置中移除此服务。", name, func() {
									payload := s.settingsMCPPayload(map[string]any{"name": name, "file": mapString(server, "file"), "confirmed": true})
									s.act("codex.extensions/mcp/remove", "", "", "", "", "", payload)
								})
							}
						}
					})
				}
			})
			if authenticate {
				ui.Row(c).Gap(6).Padding(0, 8).Disabled(pending).Children(func() {
					if action(c, fmt.Sprintf("codex-mcp-login-%d", index), "key", "登录 / 授权", p, false) {
						payload := s.settingsMCPPayload(map[string]any{"name": name, "login": true})
						s.act("codex.ai/mcp/authenticate", "", "", "", "", "", payload)
					}
				})
			}
		}
	})
}

func (s *app) settingsMCPPayload(extra map[string]any) map[string]any {
	payload := make(map[string]any, len(extra)+1)
	for key, value := range extra {
		payload[key] = value
	}
	if s.settingsMCPTaskScope {
		if taskPath := s.currentTask().path; taskPath != "" {
			payload["cwd"] = taskPath
		}
	}
	return payload
}

func (s *app) mcpFormFields(raw map[string]any, name, scope string, includeScope bool) []formField {
	transport, endpoint := "本地命令", mapString(raw, "command")
	if url := mapString(raw, "url"); url != "" {
		transport, endpoint = "HTTP 服务", url
	}
	args := strings.Join(stringArray(mustArray(raw["args"])), "\n")
	fields := []formField{
		{key: "name", label: "名称", value: name},
		{key: "transport", label: "传输方式", value: transport, options: []string{"本地命令", "HTTP 服务"}},
		{key: "endpoint", label: "启动命令或 HTTP/HTTPS 地址", value: endpoint},
		{key: "args", label: "命令参数 · 每行一个", value: args, multiline: true},
		{key: "env", label: "环境变量 JSON（敏感值默认隐藏）", value: mcpJSONText(raw["env"]), secret: true},
		{key: "httpHeaders", label: "HTTP 请求头 JSON（敏感值默认隐藏）", value: mcpJSONText(raw["http_headers"]), secret: true},
		{key: "bearerTokenEnvVar", label: "Bearer Token 环境变量名", value: mapString(raw, "bearer_token_env_var")},
		{key: "startupTimeoutSec", label: "启动超时（1–3600 秒）", value: mcpConfigString(raw, "startup_timeout_sec", "10")},
		{key: "toolTimeoutSec", label: "工具超时（1–3600 秒）", value: mcpConfigString(raw, "tool_timeout_sec", "60")},
	}
	if includeScope {
		fields = append(fields, formField{key: "scope", label: "添加到配置", value: scope, options: []string{"全局配置", "当前任务"}})
	}
	if name != "" {
		fields = append(fields, formField{key: "oldName", label: "原服务名称", value: name})
	}
	return fields
}

func mcpJSONText(value any) string {
	if value == nil {
		return "{}"
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

func mcpConfigString(raw map[string]any, key, fallback string) string {
	if value, ok := raw[key]; ok && value != nil {
		return fmt.Sprint(value)
	}
	return fallback
}

func mcpConfigFromForm(payload, original map[string]any) (map[string]any, error) {
	name := strings.TrimSpace(payloadString(payload, "name"))
	if !validMCPServerName(name) {
		return nil, fmt.Errorf("MCP 名称只能包含英文、数字、点、下划线和短横线，长度为 1 到 100 个字符")
	}
	endpoint := strings.TrimSpace(payloadString(payload, "endpoint"))
	if endpoint == "" {
		return nil, fmt.Errorf("请填写启动命令或 HTTP/HTTPS 服务地址")
	}
	startupTimeout, err := mcpFormTimeout(payloadString(payload, "startupTimeoutSec"), original, "startup_timeout_sec")
	if err != nil {
		return nil, fmt.Errorf("启动超时必须是 1 到 3600 之间的整数")
	}
	toolTimeout, err := mcpFormTimeout(payloadString(payload, "toolTimeoutSec"), original, "tool_timeout_sec")
	if err != nil {
		return nil, fmt.Errorf("工具超时必须是 1 到 3600 之间的整数")
	}
	config := make(map[string]any, len(original)+8)
	for key, value := range original {
		config[key] = value
	}
	for _, key := range []string{"command", "url", "args", "env", "http_headers", "bearer_token_env_var", "startup_timeout_sec", "tool_timeout_sec"} {
		delete(config, key)
	}
	config["startup_timeout_sec"], config["tool_timeout_sec"] = startupTimeout, toolTimeout
	if payloadString(payload, "transport") == "HTTP 服务" {
		config["url"] = endpoint
		headers, err := parseMCPEnv(payloadString(payload, "httpHeaders"))
		if err != nil {
			return nil, fmt.Errorf("HTTP 请求头必须是 JSON 对象：%w", err)
		}
		config["http_headers"] = headers
		if tokenVariable := strings.TrimSpace(payloadString(payload, "bearerTokenEnvVar")); tokenVariable != "" {
			config["bearer_token_env_var"] = tokenVariable
		}
		return config, nil
	}
	args, err := mcpFormArguments(payloadRawString(payload, "args"), original)
	if err != nil {
		return nil, fmt.Errorf("MCP 参数格式错误：%w", err)
	}
	env, err := parseMCPEnv(payloadString(payload, "env"))
	if err != nil {
		return nil, fmt.Errorf("环境变量必须是 JSON 对象：%w", err)
	}
	config["command"], config["args"], config["env"] = endpoint, args, env
	return config, nil
}

func validMCPServerName(name string) bool {
	if len(name) < 1 || len(name) > 100 {
		return false
	}
	for index := 0; index < len(name); index++ {
		ch := name[index]
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == '-' || ch == '.') {
			return false
		}
	}
	return true
}

func parseMCPArgs(value string) ([]any, error) {
	if value == "" {
		return []any{}, nil
	}
	values := []any{}
	value = strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n")
	for _, line := range strings.Split(value, "\n") {
		if line != "" {
			values = append(values, line)
		}
	}
	return values, nil
}

func parseMCPEnv(value string) (map[string]any, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return map[string]any{}, nil
	}
	var values map[string]any
	if err := json.Unmarshal([]byte(value), &values); err != nil {
		return nil, err
	}
	if values == nil {
		return nil, fmt.Errorf("需要 JSON 对象")
	}
	return values, nil
}
