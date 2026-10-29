package desktop

import (
	"fmt"
	"github.com/egoist/mygo/ui"
)

func (s *app) actionDialog(c *ui.Context, p palette) {
	if s.renderTaskAppendDialog(c, p) {
		return
	}
	if s.renderTaskCreationDialog(c, p) {
		return
	}
	if s.workspaceUnit.state.serviceEditor != nil {
		s.renderServiceEditorDialog(c, p)
		return
	}
	ui.DialogBase(c.Key("action-dialog"), &s.dialogOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.32))
		w, h := c.Size()
		panel.Width(min(680, w-48)).MaxHeight(min(720, h-32)).Padding(22).Gap(18).Background(p.surface).Border(1, p.line).Radius(18)
		panel.Children(func() {
			label(c, s.dialogTitle, 20, p.text).FontWeight(600).Shrink(0)
			ui.Divider(c).Shrink(0)
			ui.Scroll(c.Key("action-dialog-body")).FillWidth().Grow(1).MinHeight(0).Gap(18).Children(func() {
				if s.form.kind == "task.requirement-link" {
					label(c, s.form.text, 12, p.text).FillWidth().Wrap()
				}
				for index := range s.form.fields {
					field := &s.form.fields[index]
					if s.inputs["form:"+field.key] == nil {
						s.input("form:"+field.key, field.value)
					}
					if (s.form.mode == "service-add" && field.key == "groupId") ||
						((s.form.mode == "service-add" || s.form.mode == "service-edit") && field.key == "repositoryId") {
						continue
					}
					ui.Column(c).Gap(6).Children(func() {
						ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
							label(c, field.label, 12, p.muted).Grow(1)
							if field.secret {
								visible := s.settingsBoolInput("form-secret:"+s.form.kind+":"+field.key, false)
								if action(c, "form-secret-toggle-"+field.key, "", map[bool]string{true: "隐藏", false: "显示"}[*visible], p, false) {
									*visible = !*visible
								}
							}
						})
						if field.multiline {
							ui.TextArea(c.Key("action-form-"+field.key), s.input("form:"+field.key, field.value)).FillWidth().Height(160).Padding(12).Background(p.surface).Border(1, p.line).Radius(8)
						} else if len(field.options) > 0 {
							value := s.input("form:"+field.key, field.value)
							ui.Row(c).Gap(6).Wrap().Children(func() {
								for _, option := range field.options {
									choice := option
									s.settingsChoice(c, p, "form-"+field.key+"-"+choice, choice, *value == choice, func() { *value = choice })
								}
							})
						} else if s.form.kind == "repository.add" && field.key == "path" {
							ui.Row(c).Gap(7).Children(func() {
								ui.TextInput(c.Key("action-form-"+field.key), s.input("form:"+field.key, field.value)).Grow(1).Height(40).Placeholder(field.label)
								if action(c, "repository-add-browse", "folder", "浏览仓库目录", p, false) {
									s.pickNativePath("选择本地 Git 仓库", *s.input("form:"+field.key, field.value), true, nil, func(path string) {
										*s.input("form:"+field.key, field.value) = path
									})
								}
							})
						} else {
							input := ui.TextInput(c.Key("action-form-"+field.key), s.input("form:"+field.key, field.value)).FillWidth().Height(40).Placeholder(firstNonEmpty(field.placeholder, field.label))
							if field.secret && !*s.settingsBoolInput("form-secret:"+s.form.kind+":"+field.key, false) {
								input.Password()
							}
						}
						if field.error != "" {
							label(c, field.error, 11, p.danger).Wrap()
						}
					})
					if (s.form.kind == "skills.sources/save" || s.form.kind == "codex.extensions/marketplaces/add-source") && field.key == "repositoryUrl" {
						requests := s.ensureSettingsExtensionRequests()
						if requests.branchLoading {
							label(c, "正在读取远程分支…", 11, p.muted)
						}
						if requests.branchError != "" {
							label(c, requests.branchError, 11, p.danger).Wrap().Selectable()
						}
						if action(c, "source-read-branches", "branch", "读取远程分支", p, false) {
							kind := "skills.sources/branches/list"
							if s.form.kind == "codex.extensions/marketplaces/add-source" {
								kind = "codex.extensions/branches/list"
							}
							s.act(kind, "", "", "", "", "", map[string]any{"repositoryUrl": *s.input("form:repositoryUrl", "")})
						}
					}
					if field.key == "ref" && len(s.settingsUnit.state.settingsBranchChoices) > 0 {
						ui.Scroll(c).Height(72).FillWidth().Padding(6).Gap(4).Background(p.soft).Radius(8).Children(func() {
							for index, branch := range s.settingsUnit.state.settingsBranchChoices {
								value := s.input("form:ref", "")
								if action(c, fmt.Sprintf("source-branch-%d", index), "branch", branch, p, *value == branch) {
									*value = branch
								}
							}
						})
					}
				}
				if s.form.mode == "service-add" || s.form.mode == "service-edit" {
					s.serviceConfigurationSelection(c, p)
				}
				if s.form.mode == "task-create" {
					s.taskCreateSelection(c, p)
				}
				if s.form.mode == "workspace-add" {
					s.workspaceAddSelection(c, p)
				}
			})
			ui.Row(c).FillWidth().Gap(8).Shrink(0).Wrap().Justify(ui.End).Children(func() {
				if action(c, "action-cancel", "close", "取消", p, false) {
					s.dialogOpen = false
				}
				submitLabel := "继续"
				if s.form.kind == "task.requirement-link" {
					submitLabel = "关联需求"
				}
				if action(c, "action-submit", "arrow", submitLabel, p, true) {
					s.submitForm()
				}
			})
		})
	})
}

func (s *app) taskCreateSelection(c *ui.Context, p palette) {
	ui.Column(c).Gap(9).Children(func() {
		label(c, "选择项目与服务模块", 12, p.muted)
		ui.Row(c).Gap(6).Children(func() {
			if len(s.snapshot.Groups) == 0 {
				label(c, "当前配置没有项目组。", 12, p.muted)
				return
			}
			for _, group := range s.snapshot.Groups {
				name := group.Name
				if name == "" {
					name = group.ID
				}
				button := ui.ButtonBase(c.Key("task-create-group-"+group.ID)).Height(32).Padding(0, 12).Radius(8).Label(name)
				if s.taskUnit.state.taskGroupID == group.ID {
					button.Background(p.selected)
				}
				button.Children(func() { label(c, name, 12, p.text) })
				if button.Clicked() && s.taskUnit.state.taskGroupID != group.ID {
					s.taskUnit.state.taskGroupID = group.ID
					s.taskUnit.state.taskServices = make(map[string]bool)
					s.taskUnit.state.taskModules = make(map[string]map[string]bool)
				}
			}
		})
		ui.Scroll(c).Height(220).FillWidth().Padding(8).Gap(6).Background(p.soft).Radius(10).Children(func() {
			group := s.taskGroup(s.taskUnit.state.taskGroupID)
			if group.ID == "" || len(group.ServiceIDs) == 0 {
				emptyPanel(c, p, "此项目组没有服务", "先在“仓库与服务”中配置服务及模块。")
				return
			}
			for _, id := range group.ServiceIDs {
				service, ok := s.serviceByID(group.ID, id)
				if !ok {
					continue
				}
				selected := s.taskUnit.state.taskServices[id]
				ui.Column(c).FillWidth().Padding(8, 10).Gap(5).Background(p.surface).Border(1, p.line).Radius(8).Children(func() {
					button := ui.ButtonBase(c.Key("task-create-service-" + id)).FillWidth().Height(30).Label(service.Name)
					button.Children(func() {
						ui.Row(c).FillWidth().Gap(8).Children(func() {
							icon(c, map[bool]string{true: "check", false: "plus"}[selected], 14, p.blue)
							label(c, service.Name, 12, p.text).Grow(1).SingleLine()
							label(c, id, 10, p.muted).SingleLine()
						})
					})
					if button.Clicked() {
						s.toggleTaskService(service)
					}
					if selected {
						for _, module := range service.Modules {
							moduleSelected := s.taskUnit.state.taskModules[id][module.ID]
							moduleID := module.ID
							moduleName := module.Name
							moduleButton := ui.ButtonBase(c.Key("task-create-module-"+id+"-"+moduleID)).Height(28).FillWidth().Padding(0, 26).Label(moduleName)
							moduleButton.Children(func() {
								ui.Row(c).Gap(7).Children(func() {
									icon(c, map[bool]string{true: "check", false: ""}[moduleSelected], 12, p.blue)
									label(c, moduleName, 11, p.text).Grow(1)
									if module.MasterBranch != "" {
										label(c, module.MasterBranch, 10, p.muted)
									}
								})
							})
							if moduleButton.Clicked() {
								s.toggleTaskModule(id, moduleID, len(service.Modules))
							}
						}
						if len(service.Modules) == 0 {
							label(c, "此服务没有可选择的模块。", 11, p.muted).Padding(20, 0)
						}
					}
				})
			}
		})
	})
}

func (s *app) serviceConfigurationSelection(c *ui.Context, p palette) {
	if s.form.mode == "service-add" {
		groupID := s.input("form:groupId", "")
		ui.Column(c).Gap(6).Children(func() {
			label(c, "项目组", 12, p.muted)
			ui.Row(c).Gap(6).Children(func() {
				for _, group := range s.snapshot.Groups {
					name := firstNonEmpty(group.Name, group.ID)
					s.settingsChoice(c, p, "service-group-"+group.ID, name, *groupID == group.ID, func() { *groupID = group.ID })
				}
			})
		})
	}
	repositoryID := s.input("form:repositoryId", "")
	ui.Column(c).Gap(6).Children(func() {
		label(c, "仓库", 12, p.muted)
		ui.Row(c).Gap(6).Children(func() {
			for _, repository := range s.snapshot.Repositories {
				name := firstNonEmpty(repository.Name, repository.ID)
				s.settingsChoice(c, p, "service-repository-"+repository.ID, name, *repositoryID == repository.ID, func() { *repositoryID = repository.ID })
			}
		})
	})
}

func (s *app) workspaceAddSelection(c *ui.Context, p palette) {
	t := s.taskUnit.taskByID(s.form.taskID)
	ui.Column(c).Gap(8).Children(func() {
		label(c, "选择要添加的服务模块", 12, p.muted)
		ui.Scroll(c).Height(210).FillWidth().Padding(8).Gap(7).Background(p.soft).Radius(10).Children(func() {
			found := false
			for _, group := range s.snapshot.Groups {
				if t.groupID != "" && group.ID != t.groupID {
					continue
				}
				serviceIDs := group.ServiceIDs
				if len(serviceIDs) == 0 && t.groupID == "" {
					for _, service := range s.snapshot.Services {
						serviceIDs = append(serviceIDs, service.ID)
					}
				}
				for _, serviceID := range serviceIDs {
					service, ok := s.serviceByID(group.ID, serviceID)
					if !ok {
						continue
					}
					available := s.availableModules(t, service)
					if len(available) == 0 {
						continue
					}
					found = true
					selected := serviceID == s.workspaceUnit.state.workspaceAddServiceID
					ui.Column(c).FillWidth().Padding(8, 10).Gap(5).Background(p.surface).Border(1, p.line).Radius(8).Children(func() {
						button := ui.ButtonBase(c.Key("workspace-add-service-" + serviceID)).FillWidth().Height(30).Label(service.Name)
						button.Children(func() {
							ui.Row(c).Gap(8).Children(func() {
								icon(c, map[bool]string{true: "check", false: "plus"}[selected], 14, p.blue)
								label(c, service.Name, 12, p.text).Grow(1).SingleLine()
								label(c, group.Name, 10, p.muted)
							})
						})
						if button.Clicked() {
							if selected {
								s.setWorkspaceAddSelection("", "")
							} else {
								s.setWorkspaceAddSelection(serviceID, available[0].ID)
							}
						}
						if selected {
							for _, module := range available {
								moduleSelected := module.ID == s.workspaceUnit.state.workspaceAddModuleID
								moduleID, moduleName := module.ID, module.Name
								moduleButton := ui.ButtonBase(c.Key("workspace-add-module-"+serviceID+"-"+moduleID)).FillWidth().Height(28).Padding(0, 26).Label(moduleName)
								if moduleSelected {
									moduleButton.Background(p.selected)
								}
								moduleButton.Children(func() {
									ui.Row(c).Gap(7).Children(func() {
										icon(c, map[bool]string{true: "check", false: ""}[moduleSelected], 12, p.blue)
										label(c, moduleName, 11, p.text).Grow(1)
										if module.MasterBranch != "" {
											label(c, module.MasterBranch, 10, p.muted)
										}
									})
								})
								if moduleButton.Clicked() {
									s.setWorkspaceAddSelection(serviceID, moduleID)
								}
							}
						}
					})
				}
			}
			if !found {
				emptyPanel(c, p, "没有可添加的模块", "已添加的模块会从候选项中隐藏。")
			}
		})
	})
}
