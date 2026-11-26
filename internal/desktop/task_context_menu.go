package desktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
)

// A menu or confirmation belongs to a task incarnation and application context,
// independently of the task currently open in the detail pane.
type taskActionScope struct {
	taskID, taskPath, taskCreatedAt string
	home, taskRoot, page            string
	archived                        bool
}

func (s *app) taskActionScopeFor(t task) (taskActionScope, bool) {
	current := s.taskUnit.taskByID(t.id)
	if t.id == "" || current.id == "" || current.path != t.path ||
		current.createdAt != t.createdAt || current.archived != t.archived {
		return taskActionScope{}, false
	}
	return taskActionScope{
		taskID: t.id, taskPath: t.path, taskCreatedAt: t.createdAt, archived: t.archived,
		home: s.snapshot.Home, taskRoot: s.configString("taskRoot"), page: s.page,
	}, true
}

// Menu choices also depend on the material association and configuration shown
// when opening. Keep these transient details out of persisted task identity.
type taskMenuScope struct {
	taskActionScope
	configJSON, groupID, branch, requirementURL, requirementID string
	materialsPath, materialsStatus, materialsFailureReason     string
}

func (s *app) taskMenuScopeFor(t task) (taskMenuScope, bool) {
	base, valid := s.taskActionScopeFor(t)
	return taskMenuScope{taskActionScope: base, configJSON: s.settingsUnit.state.configJSON, groupID: t.groupID, branch: t.branch,
		requirementURL: t.requirementURL, requirementID: t.requirementID, materialsPath: t.materialsPath,
		materialsStatus: t.materialsStatus, materialsFailureReason: t.materialsFailureReason}, valid
}

func (s *app) taskMenuScopeCurrent(expected taskMenuScope) bool {
	actual, valid := s.taskMenuScopeFor(s.taskUnit.taskByID(expected.taskID))
	return valid && actual == expected
}

func (s *app) taskContextMenu(services ui.Services, t task, menu *ui.Menu) {
	menu.UseViewportHeight()
	scope, valid := s.taskMenuScopeFor(t)
	valid = valid && (s.page == "tasks" || s.page == "archive")
	pinned := s.taskIsPinned(t)
	pinLabel := "置顶任务"
	if pinned {
		pinLabel = "取消置顶"
	}
	chosen := ""
	if containsString(stringArray(s.configArray("defaultWorkspaceToolIds")), "codex") {
		if menu.Item("在 Codex 中打开").IconSVG(icons["launch"]).Disabled(!valid || s.busy || s.backend == nil || t.path == "").Chosen() {
			chosen = "codex-open"
		}
		menu.Separator()
	}
	if t.requirementURL == "" && menu.Item("关联需求").IconSVG(icons["file"]).Disabled(!valid || s.busy || s.backend == nil).Chosen() {
		chosen = "requirement-link"
	}
	if menu.Item("重试同步").IconSVG(icons["refresh"]).Disabled(!valid || !s.canRetryTaskRequirementCommentSync(t)).Chosen() {
		chosen = "retry-requirement-comment-sync"
	}
	if menu.Item("批量合并主分支").IconSVG(icons["branch"]).Disabled(!valid || s.busy || s.backend == nil || t.archived || !s.taskHasMainBranchMergeBatch(t)).Chosen() {
		chosen = "merge-main-batch"
	}
	menu.Separator()
	if menu.Item("打开任务目录").IconSVG(icons["folder"]).Disabled(!valid || s.backend == nil || t.path == "").Chosen() {
		chosen = "task-open-directory"
	}
	if menu.Item("复制任务目录").IconSVG(icons["copy"]).Disabled(!valid || t.path == "").Chosen() {
		chosen = "task-copy-directory"
	}
	if menu.Item(regenerateTaskInstructionsLabel).IconSVG(icons["file"]).Disabled(!valid || s.busy || s.backend == nil || t.path == "").Chosen() {
		chosen = "task-refresh-agents"
	}
	if t.materialsReady() {
		menu.Separator()
		if menu.Item("打开任务资料目录").IconSVG(icons["folder"]).Disabled(!valid || s.backend == nil).Chosen() {
			chosen = "materials-open"
		}
		if menu.Item("复制任务资料路径").IconSVG(icons["copy"]).Disabled(!valid).Chosen() {
			chosen = "materials-copy-path"
		}
	} else if t.requirementURL == "" {
		menu.Separator()
		if menu.Item("准备本地任务资料").IconSVG(icons["folder"]).Disabled(!valid || s.busy || s.backend == nil).Chosen() {
			chosen = "materials-retry"
		}
	} else {
		menu.Separator()
		configured := strings.TrimSpace(s.configString("requirementMaterialsRoot")) != "" && strings.TrimSpace(s.configString("requirementMaterialsSubdirectory")) != ""
		if !configured {
			if menu.Item("配置任务资料").IconSVG(icons["settings"]).Disabled(!valid).Chosen() {
				chosen = "materials-configure"
			}
		} else {
			label := "关联任务资料"
			if t.materialsFailed() {
				label = "重试关联任务资料"
			}
			if menu.Item(label).IconSVG(icons["refresh"]).Disabled(!valid || s.busy || s.backend == nil).Chosen() {
				chosen = "materials-retry"
			}
		}
	}
	menu.Separator()
	if menu.Item(pinLabel).IconSVG(icons["pin"]).Disabled(!valid).Chosen() {
		chosen = "pin"
	}
	menu.Separator()
	if t.archived {
		if menu.Item("恢复归档").IconSVG(icons["archive"]).Disabled(!valid || s.busy).Chosen() {
			chosen = "restore"
		}
	} else if menu.Item("归档任务").IconSVG(icons["archive"]).Disabled(!valid || s.busy).Chosen() {
		chosen = "archive"
	}
	if menu.Item("恢复已回收工作区").IconSVG(icons["refresh"]).Disabled(!valid || s.busy || s.backend == nil).Chosen() {
		chosen = "restore-recycled-workspaces"
	}
	menu.Separator()
	if menu.Item("删除任务").IconSVG(icons["delete"]).Danger().Disabled(!valid || s.busy).Chosen() {
		chosen = "delete"
	}
	if chosen == "" {
		// ContextMenu builds once when opening, then again for the chosen item.
		// Keep the opening scope so a late choice cannot act in another home.
		s.taskUnit.state.taskContextScope = nil
		if valid {
			s.taskUnit.state.taskContextScope = &scope
		}
		return
	}
	opened := s.taskUnit.state.taskContextScope
	s.taskUnit.state.taskContextScope = nil
	if !valid || opened == nil || *opened != scope {
		return
	}
	s.markReadingInteraction()
	switch chosen {
	case "requirement-link":
		s.openTaskRequirementForm(t)
	case "retry-requirement-comment-sync":
		if s.taskMenuScopeCurrent(*opened) {
			s.retryTaskRequirementCommentSync(t)
		}
	case "codex-open":
		s.dispatchTaskContextAction(application.Action{Kind: "codex.run", TaskID: t.id, Path: t.path}, opened)
	case "merge-main-batch":
		s.openWorkspaceMergeBatch(t)
	case "task-open-directory":
		s.dispatchScopedSystemOpen(application.Action{Kind: "system.open", TaskID: t.id, Path: t.path,
			Payload: map[string]any{"desktopSystemOpenDirectory": true}}, opened)
	case "task-copy-directory":
		s.copyTextToClipboard(services, t.path, "已复制任务目录")
	case "task-refresh-agents":
		s.regenerateTaskInstructions(*opened)
	case "materials-open":
		s.dispatchScopedSystemOpen(application.Action{Kind: "system.open", TaskID: t.id, Path: t.materialsPath,
			Payload: map[string]any{"desktopSystemOpenDirectory": true}}, opened)
	case "materials-copy-path":
		s.copyTextToClipboard(services, t.materialsPath, "已复制任务资料路径")
	case "materials-configure":
		s.page, s.settingsUnit.state.settingsTab, s.settingsUnit.state.settingsPage, s.settingsUnit.state.settingsLoaded = "settings", "资料与说明", "paths", ""
	case "materials-retry":
		s.dispatchTaskContextAction(application.Action{Kind: "task.materials-retry", TaskID: t.id}, opened)
	case "pin":
		s.setTaskPinned(t, !pinned)
	case "archive":
		s.openTaskArchiveDialog(t)
	case "restore":
		s.dispatchTaskContextAction(application.Action{Kind: "task.restore", TaskID: t.id, Path: t.path}, opened)
	case "restore-recycled-workspaces":
		s.dispatchTaskContextAction(application.Action{Kind: "task.restore-recycled-workspaces", TaskID: t.id, Path: t.path}, opened)
	case "delete":
		s.openTaskDeleteDialog(t)
	}
}
