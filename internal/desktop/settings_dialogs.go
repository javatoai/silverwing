package desktop

import (
	"encoding/json"
	"fmt"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"strings"
	"time"
)

func (s *app) openSettingsConfig() {
	s.settingsConfigOpen = false
	s.settingsConfigText = ""
	s.settingsConfigPath = ""
	s.act("config.view", "", "", "", "", "", nil)
}

func (s *app) exportConfig() {
	s.act("config.export", "", "", "", "", "", nil)
}

func (s *app) importConfig() {
	s.pickNativePath("选择要导入的配置", s.snapshot.Home, false, nil, func(path string) {
		s.importPath = path
		s.act("config.import-preview", "", "", "", path, "", nil)
	})
}

func (s *app) pickNativePath(title, start string, directory bool, filters []mygo.FileFilter, selected func(string)) {
	if s.settingsPickerBusy || s.win == nil {
		return
	}
	s.settingsPickerBusy = true
	win := s.win
	win.Invalidate()
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: win, Title: title, DefaultPath: start, Directory: directory, Filters: filters})
		win.Update(func() {
			s.settingsPickerBusy = false
			if err != nil {
				s.lastError = err.Error()
				return
			}
			if len(paths) > 0 && selected != nil {
				selected(paths[0])
			}
		})
	}()
}

func (s *app) chooseSavePath(title, start string, filters []mygo.FileFilter, selected func(string)) {
	if s.settingsPickerBusy || s.win == nil {
		return
	}
	s.settingsPickerBusy = true
	win := s.win
	win.Invalidate()
	go func() {
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: win, Title: title, DefaultPath: start, Filters: filters})
		win.Update(func() {
			s.settingsPickerBusy = false
			if err != nil {
				s.lastError = err.Error()
				return
			}
			if path != "" && selected != nil {
				selected(path)
			}
		})
	}()
}

func (s *app) confirmNative(title, message, detail string, confirmed func()) {
	s.showConfirmationDialog(title, message, detail, []confirmationDialogButton{
		{label: "继续", primary: true},
		{label: "取消"},
	}, 1, 1, func(button int) {
		if button == 0 && confirmed != nil {
			confirmed()
		}
	})
}

func (s *app) settingsConfigDialog(c *ui.Context, p palette) {
	ui.DialogBase(c.Key("settings-config-dialog"), &s.settingsConfigOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.34))
		w, h := c.Size()
		panel.Width(min(900, w-80)).Height(min(760, h-32)).Padding(20).Gap(12).Background(p.surface).Border(1, p.line).Radius(16)
		panel.Children(func() {
			ui.Row(c).Gap(8).Shrink(0).Children(func() {
				label(c, "应用配置", 17, p.text).FontWeight(600).Grow(1)
				if iconButton(c, "settings-config-copy", "copy", "复制配置", p).Clicked() {
					s.copyTextToClipboard(c.Services(), s.settingsConfigText, "已复制配置")
				}
				if action(c, "settings-config-close", "close", "关闭", p, false) {
					s.settingsConfigOpen = false
				}
			})
			if s.settingsConfigPath != "" {
				label(c, s.settingsConfigPath, 10, p.muted).SingleLine()
			}
			ui.Scroll(c).Grow(1).MinHeight(0).Padding(12).Background(p.soft).Border(1, p.line).Radius(8).Children(func() {
				label(c, s.settingsConfigText, 11, p.text).Font("Consolas").LineHeight(1.45).Selectable()
			})
		})
	})
}

func (s *app) settingsImportDialog(c *ui.Context, p palette) {
	if !s.importPreview {
		return
	}
	ui.DialogBase(c.Key("settings-import-preview"), &s.importPreview, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.34))
		w, h := c.Size()
		panel.Width(min(720, w-80)).Height(min(640, h-32)).Padding(20).Gap(12).Background(p.surface).Border(1, p.line).Radius(16)
		panel.Children(func() {
			label(c, "导入配置预览", 17, p.text).FontWeight(600).Shrink(0)
			label(c, s.importPath, 10, p.muted).SingleLine().Shrink(0)
			ui.Scroll(c).Grow(1).MinHeight(0).Padding(12).Background(p.soft).Radius(8).Children(func() {
				text := s.ensureSettingsConfigRecovery().previewText
				if text == "" {
					text = "后端尚未返回预览内容。"
				}
				label(c, text, 11, p.text).LineHeight(1.45).Wrap().Selectable()
			})
			ui.Row(c).FillWidth().Gap(8).Shrink(0).Wrap().Justify(ui.End).Children(func() {
				if action(c, "settings-import-cancel", "close", "取消", p, false) {
					s.importPreview = false
				}
				if action(c, "settings-import-apply", "arrow", "确认导入", p, true) {
					s.confirmSettingsConfigImport()
				}
			})
		})
	})
}

func (s *app) settingsSkillPreviewDialog(c *ui.Context, p palette) {
	if !s.settingsSkillPreviewOpen {
		return
	}
	state := s.settingsExtensionRequests
	markdownPreview := state != nil && state.previewMarkdown != nil
	ui.DialogBase(c.Key("settings-skill-preview"), &s.settingsSkillPreviewOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.34))
		w, h := c.Size()
		panel.Width(min(900, w-80)).Height(min(760, h-32)).Padding(20).Gap(12).Background(p.surface).Border(1, p.line).Radius(16)
		panel.Children(func() {
			ui.Row(c).Gap(8).Children(func() {
				label(c, firstNonEmpty(s.settingsSkillPreviewName, "Skill 预览"), 16, p.text).FontWeight(600).Grow(1)
				if markdownPreview {
					s.renderReadonlyMarkdownReaderActions(c, p, state.previewMarkdown, func(menu *overflowMenuBuilder) {
						if menu.Item("copy-preview", "复制预览内容").Icon("copy").Chosen() {
							s.copyTextToClipboard(c.Services(), s.settingsSkillPreviewText, "预览内容已复制")
						}
						if copyAction, ok := s.settingsExtensionPreviewFileAction(); ok {
							if menu.Item("copy-preview-path", "复制文件路径").Icon("file").Chosen() {
								s.copyTextToClipboard(c.Services(), state.previewPath, "文件路径已复制")
							}
							if menu.Item("copy-preview-file", "复制文件引用").Icon("copy").Chosen() {
								s.dispatch(copyAction)
							}
						}
					})
				} else {
					if iconButton(c, "settings-skill-preview-copy", "copy", "复制预览内容", p).Clicked() {
						s.copyTextToClipboard(c.Services(), s.settingsSkillPreviewText, "预览内容已复制")
					}
					if copyAction, ok := s.settingsExtensionPreviewFileAction(); ok {
						if iconButton(c, "settings-skill-preview-copy-path", "file", "复制文件路径", p).Clicked() {
							s.copyTextToClipboard(c.Services(), state.previewPath, "文件路径已复制")
						}
						if iconButton(c, "settings-skill-preview-copy-file", "copy", "复制文件引用", p).Clicked() {
							s.dispatch(copyAction)
						}
					}
				}
				if action(c, "settings-skill-preview-close", "close", "关闭", p, false) {
					s.settingsSkillPreviewOpen = false
				}
			})
			if state != nil && !state.previewLoading && state.previewError == "" && s.settingsExtensionActionRelevant(state.readActions["preview"]) {
				if len(state.previewPluginSkills) > 0 {
					ui.Scroll(c.Key("settings-plugin-skills-" + state.previewToken)).FillWidth().Height(float32(min(160, len(state.previewPluginSkills)*34))).Gap(4).Children(func() {
						for index, name := range state.previewPluginSkills {
							if action(c, fmt.Sprintf("settings-plugin-skill-%s-%d", state.previewToken, index), "file", "预览 Skill · "+name, p, false) {
								s.navigateSettingsPluginPreview(name)
							}
						}
					})
				} else if state.readActions["preview"].Kind == "codex.extensions/plugin/skill-preview" {
					if action(c, "settings-plugin-return-overview", "chevron-left", "返回概览", p, false) {
						s.navigateSettingsPluginPreview("")
					}
				}
			}
			ui.Column(c).Grow(1).MinHeight(260).Padding(14).Background(p.soft).Border(1, p.line).Radius(10).Children(func() {
				state := s.settingsExtensionRequests
				if state != nil && state.previewLoading {
					label(c, "正在读取预览…", 12, p.muted)
				} else if state != nil && state.previewError != "" {
					label(c, "预览读取失败："+state.previewError, 12, p.danger).Wrap().Selectable()
					if retry, ok := s.settingsExtensionPreviewRetryAction(); ok {
						if action(c, "settings-skill-preview-retry", "refresh", "重新读取", p, false) {
							s.dispatch(retry)
						}
					}
				} else if state != nil && state.previewMarkdown != nil {
					s.renderReadonlyMarkdownReaderContent(c, p, state.previewMarkdown)
				} else if s.settingsSkillPreviewText == "" {
					label(c, "没有可显示的预览内容。", 12, p.muted)
				} else {
					ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
						label(c, s.settingsSkillPreviewText, 12, p.text).LineHeight(1.55).Selectable()
					})
				}
			})
		})
	})
}

func pluginPreviewText(data any) string {
	if text := dataString(data, "text"); text != "" {
		return text
	}
	name := dataString(data, "name")
	if name == "" {
		return resultText(application.ActionResult{Data: data})
	}
	lines := []string{name}
	if version := dataString(data, "version"); version != "" {
		lines[0] += "  ·  " + version
	}
	if description := dataString(data, "description"); description != "" {
		lines = append(lines, "", description)
	}
	if author := dataString(data, "author"); author != "" {
		lines = append(lines, "", "作者："+author)
	}
	if category := dataString(data, "category"); category != "" {
		lines = append(lines, "分类："+category)
	}
	capabilities := mapsString(dataValue(data, "capabilities"))
	if len(capabilities) > 0 {
		lines = append(lines, "", "能力")
		for _, capability := range capabilities {
			lines = append(lines, "• "+capability)
		}
	}
	for _, skill := range mapsFromData(dataValue(data, "skills")) {
		if skillName := mapString(skill, "name"); skillName != "" {
			lines = append(lines, "", "Skill · "+skillName)
			if description := mapString(skill, "description"); description != "" {
				lines = append(lines, description)
			}
		}
	}
	if readme := dataString(data, "readme"); readme != "" {
		lines = append(lines, "", readme)
	}
	return strings.Join(lines, "\n")
}

func settingsOutputPanel(c *ui.Context, p palette, value string) {
	ui.Scroll(c).FillWidth().Height(110).Padding(11).Background(p.soft).Border(1, p.line).Radius(8).Children(func() {
		label(c, value, 10, p.text).Font("Consolas").LineHeight(1.45).Selectable()
	})
}

func settingsPathValue(c *ui.Context, p palette, title, value string) {
	ui.Row(c).Padding(9, 10).Gap(9).Background(p.soft).Radius(8).Children(func() {
		label(c, title, 10, p.muted).Width(120)
		label(c, value, 10, p.text).Grow(1).SingleLine().Selectable()
	})
}

func (s *app) settingsActionForm(form actionForm, payload map[string]any) bool {
	switch form.kind {
	case "settings.group.add":
		name := payloadString(payload, "name")
		if name == "" {
			s.lastError = "项目组名称不能为空。"
			return true
		}
		config, err := s.settingsConfigMap()
		if err != nil {
			s.lastError = err.Error()
			return true
		}
		groups, _ := config["groups"].([]any)
		groupID := fmt.Sprintf("group-%x", time.Now().UnixNano())
		groups = append(groups, map[string]any{"id": groupID, "name": name, "services": []any{}})
		config["groups"] = groups
		s.dialogOpen = false
		s.saveCreatedRepositoryGroup(config, groupID)
		return true
	case "settings.group.rename":
		name := payloadString(payload, "name")
		if name == "" {
			s.lastError = "项目组名称不能为空。"
			return true
		}
		if err := s.updateConfigGroup(form.serviceID, func(group map[string]any) { group["name"] = name }); err != nil {
			s.lastError = err.Error()
			return true
		}
		s.dialogOpen = false
		return true
	case "settings.meegle-project.add":
		s.lastError = "请从 Meegle 项目目录中选择项目空间后添加。"
		return true
	case "codex.extensions/marketplaces/add-source":
		name, repository := strings.TrimSpace(payloadString(payload, "name")), strings.TrimSpace(payloadString(payload, "repositoryUrl"))
		if name == "" || repository == "" {
			s.lastError = "Marketplace 名称和 Git 仓库地址不能为空。"
			return true
		}
		ref := strings.TrimSpace(payloadString(payload, "ref"))
		var refValue any
		if ref != "" {
			refValue = ref
		}
		source := map[string]any{"id": newSettingsSourceID(), "name": name, "repositoryUrl": repository, "marketplaceDirectory": ".", "ref": refValue}
		s.dialogOpen = false
		s.confirmNative("添加 Codex Marketplace", "将把此 Git 仓库注册为 Codex 插件来源并读取目录。", repository, func() {
			s.act("codex.extensions/marketplaces/add-source", "", "", "", "", "", map[string]any{"source": source, "confirmed": true})
		})
		return true
	case "skills.sources/save":
		name, repository := strings.TrimSpace(payloadString(payload, "name")), strings.TrimSpace(payloadString(payload, "repositoryUrl"))
		if name == "" || repository == "" {
			s.lastError = "Skill 来源名称和 Git 仓库地址不能为空。"
			return true
		}
		ref := strings.TrimSpace(payloadString(payload, "ref"))
		root := strings.TrimSpace(payloadString(payload, "skillRoot"))
		var refValue, rootValue any
		if ref != "" {
			refValue = ref
		}
		if root != "" {
			rootValue = root
		}
		source := map[string]any{"id": newSettingsSourceID(), "name": name, "repositoryUrl": repository, "ref": refValue, "skillRoot": rootValue}
		s.dialogOpen = false
		s.confirmNative("添加 Skill 来源", "将克隆此 Git 仓库并读取其中的 SKILL.md。", repository, func() {
			s.act("skills.sources/save", "", "", "", "", "", map[string]any{"source": source, "confirmed": true})
		})
		return true
	case "codex.extensions/mcp/add", "codex.extensions/mcp/edit":
		name := strings.TrimSpace(payloadString(payload, "name"))
		oldName := strings.TrimSpace(payloadString(payload, "oldName"))
		if !validMCPServerName(name) || form.kind == "codex.extensions/mcp/edit" && !validMCPServerName(oldName) {
			s.lastError = "MCP 名称只能包含英文、数字、点、下划线和短横线，长度为 1 到 100 个字符。"
			return true
		}
		original := map[string]any{}
		if form.kind == "codex.extensions/mcp/edit" {
			if err := json.Unmarshal([]byte(form.text), &original); err != nil || original == nil {
				s.lastError = "无法读取现有 MCP 配置。"
				return true
			}
		}
		config, err := mcpConfigFromForm(payload, original)
		if err != nil {
			s.lastError = err.Error()
			return true
		}
		extra := map[string]any{"name": name, "config": config, "confirmed": true}
		if form.kind == "codex.extensions/mcp/edit" {
			extra["oldName"], extra["file"] = oldName, form.path
		}
		payload = s.settingsMCPPayload(extra)
		if form.kind == "codex.extensions/mcp/add" {
			scope := payloadString(payload, "scope")
			delete(payload, "scope")
			if scope == "全局配置" {
				delete(payload, "cwd")
			} else if scope == "当前任务" {
				if taskPath := s.currentTask().path; taskPath != "" {
					payload["cwd"] = taskPath
				} else {
					s.lastError = "没有可用的当前任务目录。"
					return true
				}
			}
		}
		s.dialogOpen = false
		message := "将写入当前 Codex MCP 配置。保存后需新建或重启 Codex 会话。"
		if form.kind == "codex.extensions/mcp/edit" {
			message = "将更新 Codex MCP 配置。未知字段会保留；保存后需新建或重启 Codex 会话。"
		}
		s.confirmNative("保存 MCP 配置", message, name, func() {
			s.act(form.kind, "", "", "", "", "", payload)
		})
		return true
	}
	return false
}
