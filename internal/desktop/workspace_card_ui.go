package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

type workspaceCardCopyTarget uint8

const (
	workspaceCardCopyProjectName workspaceCardCopyTarget = iota
	workspaceCardCopyPath
	workspaceCardCopyBranch
)

func workspaceCardClipboardValue(target workspaceCardCopyTarget, projectName, path, branch string) string {
	switch target {
	case workspaceCardCopyProjectName:
		return projectName
	case workspaceCardCopyPath:
		return path
	case workspaceCardCopyBranch:
		return branch
	default:
		return ""
	}
}

func workspaceCardCopyButton(c *ui.Context, key, name, tooltip string, disabled bool) ui.Element {
	return ui.ButtonBase(c.Key(key)).AlignSelf(ui.Start).MaxWidthPercent(100).MinWidth(0).Padding(2).Justify(ui.Start).
		Label(name).Tooltip(tooltip).FocusRing(true).Disabled(disabled)
}

func (s *app) workspaceCard(c *ui.Context, p palette, t task, w application.Workspace, index int) {
	width, _ := c.Size()
	stacked := width < 790
	name, moduleName := s.workspaceNames(t.groupID, w)
	title := name
	if moduleName != "" && moduleName != name {
		title += " · " + moduleName
	}
	branch, verifiedBranch := w.Branch, false
	if health, ok := s.workspaceHealth(w.Path); ok && health.ActualBranch != nil && strings.TrimSpace(*health.ActualBranch) != "" {
		branch, verifiedBranch = *health.ActualBranch, true
	}
	branchText := branch
	if !verifiedBranch {
		branchText += "（未验证）"
	}
	key := firstNonEmpty(w.ID, w.ServiceID+"-"+w.ModuleID)
	showAddModule := true
	for previous := 0; previous < index && previous < len(t.workspaces); previous++ {
		if t.workspaces[previous].ServiceID == w.ServiceID {
			showAddModule = false
			break
		}
	}
	ui.Column(c).FillWidth().Border(1, p.line).Radius(12).Background(p.surface).Padding(10, 14).Gap(8).Children(func() {
		ui.Row(c).Gap(12).AlignItems(ui.Start).Children(func() {
			ui.Box(c).Width(34).Height(34).Radius(8).Background(p.soft).Center().Children(func() { icon(c, "branch", 20, p.muted) })
			ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
				projectCopy := workspaceCardCopyButton(c, key+"-copy-project-name", "复制项目名", "点击复制项目名\n"+title, strings.TrimSpace(title) == "")
				projectColor := p.text
				if projectCopy.Hovered() {
					projectColor = p.blue
				}
				projectCopy.Children(func() { label(c, title, 14, projectColor).FontWeight(600).SingleLine() })
				if projectCopy.Clicked() {
					s.copyTextToClipboard(c.Services(), workspaceCardClipboardValue(workspaceCardCopyProjectName, title, w.Path, branch), "已复制项目名")
				}
				branchCopy := workspaceCardCopyButton(c, key+"-copy-branch", "复制分支名", "点击复制分支名\n"+branchText, strings.TrimSpace(branch) == "")
				branchColor := p.muted
				if branchCopy.Hovered() {
					branchColor = p.blue
				}
				branchCopy.Children(func() { label(c, branchText, 13, branchColor).SingleLine() })
				if branchCopy.Clicked() {
					s.copyTextToClipboard(c.Services(), workspaceCardClipboardValue(workspaceCardCopyBranch, title, w.Path, branch), "已复制分支名")
				}
				s.workspaceHealthLine(c, p, t, w, key)
				if w.State != "" && !strings.EqualFold(w.State, "READY") && len(w.Warnings) == 0 {
					state, color := "工作区有提醒", p.amber
					if strings.EqualFold(w.State, "FAILED") || strings.EqualFold(w.State, "MISSING") {
						state = "工作区创建失败"
					} else if strings.EqualFold(w.State, "CREATING") {
						state, color = "工作区创建中", p.muted
					}
					label(c, state, 11, color)
				}
			})
			if !stacked {
				s.workspaceActions(c, p, t, w, title, key, showAddModule)
			}
		})
		if stacked {
			ui.Divider(c)
			s.workspaceActions(c, p, t, w, title, key, showAddModule)
		}
		s.workspaceWarning(c, p, t, w, key)
		s.renderWorkspaceMergePanel(c, p, t, w)
	})
}

func (s *app) workspaceWarning(c *ui.Context, p palette, t task, w application.Workspace, key string) {
	if len(w.Warnings) == 0 {
		return
	}
	heading := "工作区提醒"
	if strings.EqualFold(w.State, "FAILED") || strings.EqualFold(w.State, "MISSING") {
		heading = "工作区创建失败"
	} else if strings.EqualFold(w.State, "CREATING") {
		heading = "工作区创建中"
	}
	detail := strings.Join(w.Warnings, "\n")
	ui.Row(c.Key(key+"-workspace-warning")).FillWidth().MinWidth(0).Padding(8, 10).Gap(8).
		AlignItems(ui.Start).Background(p.amberBg).Radius(8).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			label(c, heading, 12, p.amber).FontWeight(500).SingleLine()
			label(c, detail, 11, p.text).FillWidth().MinWidth(0).Wrap().Selectable().Tooltip(detail)
		})
		ui.Row(c).Width(72).Height(34).Shrink(0).Gap(4).AlignItems(ui.Center).Label("工作区提醒操作").Children(func() {
			if iconButtonDisabled(c, key+"-bootstrap-rerun", "refresh", "重新执行 Bootstrap", p, s.busy || s.settingsPickerBusy).Clicked() {
				s.confirmNative(
					"重新执行 Bootstrap",
					"将按当前服务配置对该工作区重新执行 Bootstrap 复制规则与命令；已有文件按规则覆盖，命令会重复执行一次。禁止覆盖的规则会因目标已存在而报警告。",
					w.Path,
					func() {
						s.act("workspace.bootstrap-rerun", t.id, w.ServiceID, w.ModuleID, w.Path, "", map[string]any{"confirmed": true, "selectionKeys": []string{w.Path}})
					},
				)
			}
			if iconButtonDisabled(c, key+"-bootstrap-clear", "close", "清除警告（确认已知晓）", p, s.busy).Clicked() {
				s.act("workspace.bootstrap-clear", t.id, w.ServiceID, w.ModuleID, w.Path, "", map[string]any{"selectionKeys": []string{w.Path}})
			}
		})
	})
}

func (s *app) workspaceNames(groupID string, w application.Workspace) (string, string) {
	name, moduleName := w.Record.ServiceName, w.Record.ModuleName
	for _, service := range s.snapshot.Services {
		if service.GroupID != groupID || service.ID != w.ServiceID {
			continue
		}
		name = firstNonEmpty(name, service.Name, w.Name, w.ServiceID)
		for _, module := range service.Modules {
			if module.ID == w.ModuleID {
				return name, firstNonEmpty(moduleName, module.Name, w.ModuleID)
			}
		}
		return name, firstNonEmpty(moduleName, w.ModuleID)
	}
	return firstNonEmpty(name, w.Name, w.ServiceID), firstNonEmpty(moduleName, w.ModuleID)
}

func (s *app) workspaceHealthLine(c *ui.Context, p palette, t task, w application.Workspace, key string) {
	health, ok := s.workspaceHealth(w.Path)
	if s.workspaceGitStatusBusy() {
		health, ok = domain.WorkspaceGitHealth{State: domain.WorkspaceGitChecking}, true
	}
	ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
		if ok {
			switch health.State {
			case domain.WorkspaceGitChecking:
				label(c, "检查中", 11, p.muted)
			case domain.WorkspaceGitReady:
				dirty, dirtyColor := "本地未提交 0", p.muted
				if health.DirtyFileCount > 0 {
					dirty = fmt.Sprintf("本地未提交 %d", health.DirtyFileCount)
					dirtyColor = p.amber
				}
				dirtyButton := ui.ButtonBase(c.Key(key+"-dirty-summary")).Padding(0, 2).Label(dirty).Tooltip("查看暂存、未暂存和未跟踪文件的本地改动").FocusRing(true).Disabled(s.busy)
				dirtyFocus := ui.Local(dirtyButton, key+"-dirty-review-focus", func() ui.Handle { return ui.Handle{} })
				dirtyButton.Bind(dirtyFocus)
				dirtyButton.Children(func() { label(c, dirty, 11, dirtyColor).SingleLine() })
				if dirtyButton.Clicked() {
					s.openWorkspaceDirtyFiles(t, w, health, dirtyFocus)
				}
				label(c, "·", 11, p.muted)
				push := "检查失败"
				switch health.PushState {
				case domain.LocalPushPushed:
					push = "已推送"
				case domain.LocalPushAhead:
					push = fmt.Sprintf("%d 个提交未推送", health.UnpushedCommitCount)
				case domain.LocalPushRemoteBranchMissing:
					push = "未发现远程分支"
				case domain.LocalPushNoUpstream:
					push = "未关联远程"
				case domain.LocalPushFailed:
					push = "检查失败"
				}
				label(c, push, 11, p.muted).SingleLine()
			case domain.WorkspaceGitMissing, domain.WorkspaceGitFailed:
				message := workspaceHealthIssueLabel(health.Issue)
				if health.Issue == domain.WorkspaceGitIssueBranchMismatch {
					actual, expected := "未知", "未提供"
					if health.ActualBranch != nil && strings.TrimSpace(*health.ActualBranch) != "" {
						actual = *health.ActualBranch
					}
					if health.ExpectedBranch != nil && strings.TrimSpace(*health.ExpectedBranch) != "" {
						expected = *health.ExpectedBranch
					}
					message += "：" + actual + " → " + expected
				}
				if health.Message != nil && strings.TrimSpace(*health.Message) != "" {
					message += " · " + strings.TrimSpace(*health.Message)
				}
				label(c, message, 11, p.amber).Wrap()
			}
		}
		if ok {
			label(c, "·", 11, p.muted)
		}
		diffButton := ui.ButtonBase(c.Key(key+"-overall-diff")).Padding(0, 2).Label("整体差异").Tooltip("查看当前分支相对主分支的全部已提交改动").FocusRing(true).Disabled(s.busy)
		diffFocus := ui.Local(diffButton, key+"-overall-diff-focus", func() ui.Handle { return ui.Handle{} })
		diffButton.Bind(diffFocus)
		diffButton.Children(func() { label(c, "整体差异", 11, p.blue).SingleLine() })
		if diffButton.Clicked() {
			s.openWorkspaceReview(t, w, workspaceReviewMainline, nil, diffFocus)
		}
		if ok && health.State == domain.WorkspaceGitReady {
			label(c, "·", 11, p.muted)
			historyButton := ui.ButtonBase(c.Key(key+"-history")).Padding(0, 2).Label("提交历史").Tooltip("查看当前分支相对主分支新增的提交").FocusRing(true).Disabled(s.busy)
			historyFocus := ui.Local(historyButton, key+"-history-focus", func() ui.Handle { return ui.Handle{} })
			historyButton.Bind(historyFocus)
			historyButton.Children(func() { label(c, "提交历史", 11, p.blue).SingleLine() })
			if historyButton.Clicked() {
				s.openWorkspaceHistory(t, w, historyFocus)
			}
		}
	})
}

func workspaceHealthIssueLabel(issue domain.WorkspaceGitIssue) string {
	switch issue {
	case domain.WorkspaceGitIssueMissing:
		return "工作区不存在"
	case domain.WorkspaceGitIssueNotGit:
		return "不是有效的 Git 工作区"
	case domain.WorkspaceGitIssueIdentityMismatch:
		return "Git 仓库身份不匹配"
	case domain.WorkspaceGitIssueBranchMismatch:
		return "当前分支与任务记录不一致"
	case domain.WorkspaceGitIssueDetachedHead:
		return "Detached HEAD"
	case domain.WorkspaceGitIssueOperationInProgress:
		return "存在进行中的 Git 操作"
	case domain.WorkspaceGitIssueInspectionFailed:
		return "Git 状态检查失败"
	default:
		return "工作区检查异常"
	}
}

func (s *app) workspaceActions(c *ui.Context, p palette, t task, w application.Workspace, name, key string, showAddModule bool) {
	ui.Row(c).Gap(8).Children(func() {
		if s.configBool("showWorkspacePathActionGroup", false) {
			iconGroup(c, p, func() {
				if iconButton(c, key+"-copy-path", "copy", "复制工作区路径", p).Clicked() {
					s.copyTextToClipboard(c.Services(), workspaceCardClipboardValue(workspaceCardCopyPath, name, w.Path, w.Branch), "已复制工作区路径")
				}
				if iconButton(c, key+"-terminal", "terminal", "打开终端", p).Clicked() {
					s.act("system.terminal", t.id, w.ServiceID, w.ModuleID, w.Path, "", nil)
				}
				if iconButton(c, key+"-folder", "folder", "打开工作区目录", p).Clicked() {
					s.act("system.open", t.id, w.ServiceID, w.ModuleID, w.Path, "", map[string]any{"desktopSystemOpenDirectory": true})
				}
			})
		}
		if s.configBool("showWorkspaceGitActionGroup", false) {
			writeBlocked := workspaceGitWritesBlocked(s, w)
			iconGroup(c, p, func() {
				if iconButtonDisabled(c, key+"-commit", "commit", "提交", p, s.busy || writeBlocked).Clicked() {
					s.openWorkspaceGitSingle(t, w, workspaceGitModeCommit)
				}
				if iconButtonDisabled(c, key+"-commit-push", "cloud", "提交并推送", p, s.busy || writeBlocked).Clicked() {
					s.openWorkspaceGitSingle(t, w, workspaceGitModeCommitAndPush)
				}
				if iconButtonDisabled(c, key+"-push", "push", "推送", p, s.busy || writeBlocked).Clicked() {
					s.act("workspace.push", t.id, w.ServiceID, w.ModuleID, w.Path, "", map[string]any{"selectionKeys": []string{w.Path}})
				}
				if iconButtonRunning(c, key+"-merge", "git", s.workspaceMainBranchMergeActionLabel(t, w), p, !s.workspaceMainBranchMergeEnabled(t, w), s.workspaceMainBranchMergeRunning(t, w)).Clicked() {
					s.beginWorkspaceMerge(t, w)
				}
			})
		}
		if s.workspaceCanBuildTag(t, w) {
			iconGroup(c, p, func() {
				if iconButtonDisabled(c, key+"-tag", "tag", workspaceTagActionLabel(w), p, s.tagWorkspaceBusy(t, w)).Clicked() {
					s.beginWorkspaceTagBuild(t, w)
				}
				if s.workspaceTagTargetEnabled(t, w) && iconButtonDisabled(c, key+"-tag-target", "branch", "设置测试目标分支", p, s.busy).Clicked() {
					s.beginTagTargetEdit(t, w)
				}
			})
		}
		iconGroup(c, p, func() {
			if showAddModule && iconButtonDisabled(c, key+"-add-module", "plus", "添加模块", p, s.busy || t.archived).Clicked() {
				s.openTaskAppendModules(t, w.ServiceID)
			}
			if iconButtonDisabled(c, key+"-delete-module", "delete", "删除当前模块", p, len(t.workspaces) <= 1 || s.busy).Clicked() {
				s.beginWorkspaceRemoval(t, w)
			}
		})
		if s.workspaceNeedsRepair(t, w) {
			iconGroup(c, p, func() {
				if iconButtonDisabled(c, key+"-repair", "build", "修复工作区", p, s.busy).Clicked() {
					s.beginWorkspaceRepair(t, w)
				}
			})
		}
	})
	if showAddModule && !t.archived && taskServiceFailed(t, w.ServiceID) {
		iconGroup(c, p, func() {
			if iconButtonDisabled(c, key+"-retry-service", "refresh", "重试此服务", p, s.busy).Clicked() {
				s.retryFailedTaskServices(t, []string{w.ServiceID})
			}
		})
	}
	iconGroup(c, p, func() {
		if iconButton(c, key+"-ide", "code", "使用 "+workspaceDevelopmentToolName(w.Record.DevelopmentTool)+" 打开", p).Clicked() {
			s.act("system.ide", t.id, w.ServiceID, w.ModuleID, w.Path, "", map[string]any{"tool": string(w.Record.DevelopmentTool)})
		}
	})
	s.renderWorkspaceCommandActions(c, p, t, w)
}

func (s *app) workspaceNeedsRepair(t task, w application.Workspace) bool {
	if t.id == "" || w.ServiceID == "" || w.Path == "" {
		return false
	}
	if strings.EqualFold(w.State, "FAILED") || strings.EqualFold(w.State, "MISSING") {
		return true
	}
	health, ok := s.workspaceHealth(w.Path)
	if !ok {
		return false
	}
	if health.State == domain.WorkspaceGitMissing || health.State == domain.WorkspaceGitFailed {
		return true
	}
	switch health.Issue {
	case domain.WorkspaceGitIssueMissing, domain.WorkspaceGitIssueNotGit, domain.WorkspaceGitIssueIdentityMismatch,
		domain.WorkspaceGitIssueBranchMismatch, domain.WorkspaceGitIssueDetachedHead,
		domain.WorkspaceGitIssueOperationInProgress, domain.WorkspaceGitIssueInspectionFailed:
		return true
	default:
		return false
	}
}

func workspaceDevelopmentToolName(tool domain.DevelopmentToolType) string {
	return map[domain.DevelopmentToolType]string{
		domain.DevelopmentToolIntelliJIdea: "IntelliJ IDEA", domain.DevelopmentToolWebStorm: "WebStorm",
		domain.DevelopmentToolPyCharm: "PyCharm", domain.DevelopmentToolVisualStudioCode: "Visual Studio Code",
		domain.DevelopmentToolAndroidStudio: "Android Studio", domain.DevelopmentToolDevEcoStudio: "DevEco Studio",
	}[tool]
}

func taskServiceFailed(t task, serviceID string) bool {
	for _, item := range t.workspaces {
		if item.ServiceID == serviceID && strings.EqualFold(item.State, "FAILED") {
			return true
		}
	}
	return false
}
