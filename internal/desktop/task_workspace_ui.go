package desktop

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"strings"
)

func workspaceGitRefreshButton(c *ui.Context, p palette, busy bool) ui.Element {
	tooltip := "刷新 Git 状态"
	if busy {
		tooltip = "正在刷新 Git 状态；再次点击会在当前读取完成后再刷新一次"
	}
	b := ui.ButtonBase(c.Key("task-git-refresh")).Width(34).Height(34).Radius(20).Center().Label("刷新 Git 状态").Tooltip(tooltip)
	if b.Hovered() {
		b.Background(p.soft)
	}
	b.Children(func() {
		if busy {
			ui.Spinner(c).Size(18, 18).TextColor(p.blue).Label("刷新中")
		} else {
			icon(c, "refresh", 18, p.text)
		}
	})
	return b
}

func (s *app) taskWorkspace(c *ui.Context, p palette) {
	if s.workspaceReviewPageVisible() {
		s.renderWorkspaceReviewPage(c, p)
		return
	}
	s.ensureTaskRequirementMetadata()
	s.requestWorkspaceGitStatus(false)
	w, _ := c.Size()
	// Match Source: keep both panes visible at every width. The 960dp
	// focused-pane helper is unused in Source; only split-pane width is clamped.
	ui.Column(c).Grow(1).MinHeight(0).Padding(8).Children(func() {
		s.taskListPaneSplit(c.Key("task-split"), w-16, p, func() { s.taskIndex(c, p) }, func() { s.taskWorkspaceDetail(c, p) })
	})
}

func (s *app) taskWorkspaceDetail(c *ui.Context, p palette) {
	ui.Column(c).Fill().MinWidth(0).Padding(0, 0, 0, 7).Children(func() {
		ui.Row(c).FillWidth().Height(51).Shrink(0).Padding(8).Gap(2).Label("任务内容页签").Children(func() {
			for i, name := range tabs {
				b := ui.ButtonBase(c.Key(fmt.Sprintf("tab-%d", i))).Height(35).Padding(0, 10).Radius(10).Label(name)
				fg := p.muted
				if s.tab == i {
					fg = p.blue
					b.Background(p.selected)
				} else if b.Hovered() {
					b.Background(p.soft)
				}
				b.Children(func() { label(c, name, 14, fg).NoWrap() })
				if b.Clicked() {
					s.markReadingInteraction()
					if s.tab == 3 && i != 3 {
						s.deactivateInstructionEditor()
					}
					s.tab = i
					if i == taskTagTab {
						s.requestTagHistoryRefresh()
					}
					if i == 1 {
						s.loadDocuments()
					}
				}
			}
			if s.tab == taskTagTab && s.currentTask().id != "" {
				ui.Spacer(c)
				s.renderTagHistoryPageMenu(c, p, s.currentTask())
			}
		})
		ui.Divider(c)
		if s.currentTask().id == "" {
			s.emptyTask(c, p)
			return
		}
		switch s.tab {
		case 1:
			s.materials(c, p)
		case 3:
			s.renderTaskNotes(c, p)
		case taskTagTab:
			s.renderTaskTagHistory(c, p, s.currentTask())
		case 0:
			ui.Column(c).Grow(1).MinHeight(0).Background(p.surface).Border(1, p.line).Radius(18).Children(func() {
				ui.Scroll(c).Grow(1).MinHeight(0).TrackScroll(s.taskWorkspaceDetailScroll(s.currentTask())).Padding(18).Children(func() { s.taskDetails(c, p) })
			})
		case 2:
			s.requirementDetails(c, p)
		}
	})
}

func (s *app) emptyTask(c *ui.Context, p palette) {
	archived := s.page == "archive"
	ui.Column(c).Grow(1).FillWidth().Center().Gap(12).Padding(24).Background(p.surface).Border(1, p.line).Radius(18).Children(func() {
		icon(c, map[bool]string{true: "archive", false: "tasks"}[archived], 28, p.muted)
		title := "还没有任务"
		if archived {
			title = "没有归档任务"
		}
		label(c, title, 18, p.text).FontWeight(600)
		message := "请在需求列表中创建任务。"
		if archived {
			message = "已归档的任务会保留在这里，可随时恢复。"
		}
		label(c, message, 13, p.muted)
	})
}

func (s *app) taskHeader(c *ui.Context, p palette) {
	t := s.currentTask()
	ui.Column(c).FillWidth().Gap(6).Padding(2).Children(func() {
		if t.requirementURL != "" {
			s.renderTaskRequirementMetadata(c, p, t)
			ui.Row(c).FillWidth().MinWidth(0).Gap(5).AlignItems(ui.Center).Children(func() {
				s.renderRequirementURLLink(c, p, t.id, t.requirementURL, 12)
				if s.configBool("showTaskAreaCopyIcons", true) && s.configBool("showTaskAreaRequirementCopyIcons", true) {
					if iconButton(c, "copy-task-detail-requirement-url", "copy", "复制需求链接", p).Shrink(0).Clicked() {
						s.copyTextToClipboard(c.Services(), t.requirementURL, "已复制需求链接")
					}
				}
			})
		}
	})
}

func (s *app) copyTaskWorkspacePath(services ui.Services) {
	s.copyTextToClipboard(services, s.currentTask().path, "已复制任务路径")
}

func (s *app) taskDetails(c *ui.Context, p palette) {
	t := s.currentTask()
	ui.Column(c).FillWidth().Gap(16).Children(func() {
		s.taskHeader(c, p)
		failedServiceIDs := s.taskFailedServiceIDs(t)
		ui.Row(c).Gap(8).Children(func() {
			if s.configBool("showTaskDetailPathActionGroup", false) {
				iconGroup(c, p, func() {
					if iconButton(c, "task-copy-path", "copy", "复制任务路径", p).Clicked() {
						s.copyTaskWorkspacePath(c.Services())
					}
					if iconButton(c, "task-terminal", "terminal", "在任务目录打开终端", p).Clicked() {
						s.act("system.terminal", t.id, "", "", t.path, "", nil)
					}
					if iconButton(c, "task-folder", "folder", "打开任务目录", p).Clicked() {
						s.act("system.open", t.id, "", "", t.path, "", map[string]any{"desktopSystemOpenDirectory": true})
					}
				})
			}
			if s.taskCanBuildAnyTag(t) {
				iconGroup(c, p, func() {
					if iconButton(c, "batch-tag", "tag", "批量构建 Tag", p).Clicked() {
						s.openTagBuildBatch(t)
					}
				})
			}
			if s.configBool("showTaskDetailGitActionGroup", false) && taskHasPhysicalWorkspace(t) {
				iconGroup(c, p, func() {
					for _, a := range []struct{ icon, name, mode string }{{"commit", "提交全部工作区", workspaceGitModeCommit}, {"push", "推送全部工作区", workspaceGitModePush}, {"cloud", "提交并推送全部工作区", workspaceGitModeCommitAndPush}} {
						if iconButtonDisabled(c, "task-git-"+a.icon, a.icon, a.name, p, s.busy).Clicked() {
							s.openWorkspaceGitBatch(t, a.mode, nil)
						}
					}
				})
			}
			iconGroup(c, p, func() {
				if len(s.taskAddableServices(t)) > 0 {
					if iconButton(c, "add-service", "plus", "为任务添加服务", p).Clicked() {
						s.openTaskAppendServices(t)
					}
				}
				if len(failedServiceIDs) > 0 && !t.archived {
					if iconButtonDisabled(c, "retry-failed-services", "refresh", "重试全部失败服务", p, s.busy || t.archived).Clicked() {
						s.retryFailedTaskServices(t, nil)
					}
				}
				if iconButton(c, "task-branches", "branch", "查看分支信息", p).Clicked() {
					s.openTaskBranchInfo(t)
				}
				if workspaceGitRefreshButton(c, p, s.workspaceGitStatusBusy()).Clicked() {
					s.requestWorkspaceGitStatus(true)
				}
			})
			iconGroup(c, p, func() {
				s.overflowMenuOwnedAt(c, p, "task-more", "task-more-"+t.id, "任务更多操作", false, ui.AnchorBottomLeft, ui.AnchorTopLeft, func(m *overflowMenuBuilder) {
					if m.Item("codex-open", "在 Codex 中打开").Icon("launch").Chosen() {
						s.act("codex.run", t.id, "", "", t.path, "", nil)
					}
					if m.Item("codex-config", "Codex 配置").Icon("settings").Chosen() {
						s.openCodexAI(t.path, "MCP")
					}
					if m.Item("codex-sessions", "Codex 会话关联").Icon("tasks").Chosen() {
						s.openCodexAI(t.path, "会话")
					}
					m.Separator()
					if m.Item("merge-main-batch", "批量合并主分支").Icon("branch").Disabled(s.busy || t.archived || !s.taskHasMainBranchMergeBatch(t)).Chosen() {
						s.openWorkspaceMergeBatch(t)
					}
					if t.materialsReady() {
						m.Separator()
						if m.Item("materials-open", "打开任务资料目录").Icon("folder").Chosen() {
							s.act("system.open", t.id, "", "", t.materialsPath, "", map[string]any{"desktopSystemOpenDirectory": true})
						}
						if m.Item("materials-copy-path", "复制任务资料路径").Icon("copy").Chosen() {
							s.copyTextToClipboard(c.Services(), t.materialsPath, "已复制任务资料路径")
						}
					} else if t.requirementURL != "" {
						configured := strings.TrimSpace(s.configString("requirementMaterialsRoot")) != "" && strings.TrimSpace(s.configString("requirementMaterialsSubdirectory")) != ""
						if !configured {
							m.Separator()
							if m.Item("materials-configure", "配置任务资料").Icon("settings").Chosen() {
								s.page, s.settingsTab, s.settingsPage, s.settingsLoaded = "settings", "存储", "paths", ""
							}
						} else {
							label := "关联任务资料"
							if t.materialsFailed() {
								label = "重试关联任务资料"
							}
							m.Separator()
							if m.Item("materials-retry", label).Icon("refresh").Chosen() {
								s.act("task.materials-retry", t.id, "", "", "", "", nil)
							}
						}
					}
					m.Separator()
					if t.archived {
						if m.Item("restore", "恢复任务").Icon("archive").Chosen() {
							s.act("task.restore", t.id, "", "", "", "", nil)
						}
					} else if m.Item("archive", "归档任务").Icon("archive").Chosen() {
						s.openTaskArchiveDialog(t)
					}
					m.Separator()
					if m.Item("delete", "删除任务").Icon("delete").Disabled(s.busy).Dangerous().Chosen() {
						s.openTaskDeleteDialog(t)
					}
				})
			})
		})
		for i, workspace := range t.workspaces {
			s.workspaceCard(c, p, t, workspace, i)
		}
		for _, launch := range t.workspaceToolLaunches {
			if launch.Status == domain.WorkspaceToolOpened {
				continue
			}
			ui.Row(c).FillWidth().Padding(11, 13).Gap(10).AlignItems(ui.Center).Background(p.surface).Border(1, p.line).Radius(10).Children(func() {
				icon(c, "launch", 17, map[bool]ui.Color{true: p.amber, false: p.muted}[launch.Status == domain.WorkspaceToolFailed])
				ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
					label(c, "工作区工具 · "+launch.ToolID, 12, p.text).FontWeight(600)
					status := "等待打开"
					color := p.muted
					if launch.Status == domain.WorkspaceToolFailed {
						status, color = "打开失败", p.amber
						if launch.Message != nil && strings.TrimSpace(*launch.Message) != "" {
							status += " · " + *launch.Message
						}
					}
					label(c, status, 11, color).Wrap()
				})
				if launch.Status == domain.WorkspaceToolFailed && action(c, "task-tool-retry-"+launch.ToolID, "refresh", "重试", p, true) {
					s.act("task.tools-retry", t.id, "", "", "", "", map[string]any{"toolId": launch.ToolID})
				}
			})
		}
	})
}

func taskHasPhysicalWorkspace(t task) bool {
	for _, workspace := range t.workspaces {
		if strings.TrimSpace(workspace.Path) != "" {
			return true
		}
	}
	return false
}

func (s *app) taskAddableServices(t task) []application.Service {
	if t.archived {
		return nil
	}
	used := taskUsedServiceIDs(t)
	services := make([]application.Service, 0)
	group, err := s.taskAppendGroup(t)
	if err == nil {
		for _, configured := range group.Services {
			if !configured.Enabled || used[configured.ID] {
				continue
			}
			service := application.Service{ID: configured.ID, Name: configured.DisplayName, RepositoryID: configured.RepositoryID, GroupID: group.ID}
			for _, module := range configured.Modules {
				service.Modules = append(service.Modules, application.Module{ID: module.ID, Name: module.Name, MasterBranch: configured.EffectiveMasterBranch(module), Strategy: string(module.Strategy)})
			}
			services = append(services, service)
		}
	}
	return services
}

func (s *app) taskCanBuildAnyTag(t task) bool {
	if t.archived {
		return false
	}
	for _, workspace := range t.workspaces {
		if s.workspaceCanBuildTag(t, workspace) {
			return true
		}
	}
	return false
}

func (s *app) workspaceCanBuildTag(t task, workspace application.Workspace) bool {
	return s.tagWorkspaceCanBuild(t, workspace)
}

func (s *app) hasGroup(groupID string) bool {
	for _, group := range s.snapshot.Groups {
		if group.ID == groupID {
			return true
		}
	}
	return false
}
