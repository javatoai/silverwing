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
	configJSON, groupID, requirementURL, requirementID     string
	materialsPath, materialsStatus, materialsFailureReason string
}

func (s *app) taskMenuScopeFor(t task) (taskMenuScope, bool) {
	base, valid := s.taskActionScopeFor(t)
	return taskMenuScope{taskActionScope: base, configJSON: s.settingsUnit.state.configJSON, groupID: t.groupID,
		requirementURL: t.requirementURL, requirementID: t.requirementID, materialsPath: t.materialsPath,
		materialsStatus: t.materialsStatus, materialsFailureReason: t.materialsFailureReason}, valid
}

func (s *app) taskMenuScopeCurrent(expected taskMenuScope) bool {
	actual, valid := s.taskMenuScopeFor(s.taskUnit.taskByID(expected.taskID))
	return valid && actual == expected
}

func (s *app) taskContextMenu(services ui.Services, t task, menu *ui.Menu) {
	scope, valid := s.taskMenuScopeFor(t)
	valid = valid && (s.page == "tasks" || s.page == "archive")
	pinned := s.taskIsPinned(t)
	pinLabel := "置顶任务"
	if pinned {
		pinLabel = "取消置顶"
	}
	chosen := ""
	for _, tool := range []struct{ id, name string }{{"codex", "Codex"}} {
		if !s.taskOpenToolEnabled(tool.id) {
			continue
		}
		if menu.Item("在 " + tool.name + " 中打开").Icon(icons["launch"]).Disabled(!valid || s.busy || s.backend == nil || t.path == "" || !s.taskOpenToolAvailable(tool.id)).Chosen() {
			chosen = tool.id + "-open"
		}
	}
	if t.materialsReady() {
		menu.Separator()
		if menu.Item("打开任务资料目录").Icon(icons["folder"]).Disabled(!valid || s.backend == nil).Chosen() {
			chosen = "materials-open"
		}
		if menu.Item("复制任务资料路径").Icon(icons["copy"]).Disabled(!valid).Chosen() {
			chosen = "materials-copy-path"
		}
	} else if t.requirementURL != "" {
		menu.Separator()
		configured := strings.TrimSpace(s.configString("requirementMaterialsRoot")) != "" && strings.TrimSpace(s.configString("requirementMaterialsSubdirectory")) != ""
		if !configured {
			if menu.Item("配置任务资料").Icon(icons["settings"]).Disabled(!valid).Chosen() {
				chosen = "materials-configure"
			}
		} else {
			label := "关联任务资料"
			if t.materialsFailed() {
				label = "重试关联任务资料"
			}
			if menu.Item(label).Icon(icons["refresh"]).Disabled(!valid || s.busy || s.backend == nil).Chosen() {
				chosen = "materials-retry"
			}
		}
	}
	menu.Separator()
	if menu.Item(pinLabel).Icon(icons["pin"]).Disabled(!valid).Chosen() {
		chosen = "pin"
	}
	menu.Separator()
	if t.archived {
		if menu.Item("恢复归档").Icon(icons["archive"]).Disabled(!valid || s.busy).Chosen() {
			chosen = "restore"
		}
	} else if menu.Item("归档任务").Icon(icons["archive"]).Disabled(!valid || s.busy).Chosen() {
		chosen = "archive"
	}
	menu.Separator()
	if menu.Item("删除任务").Icon(icons["delete"]).Dangerous().Disabled(!valid || s.busy).Chosen() {
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
	case "codex-open":
		s.dispatchTaskContextAction(application.Action{Kind: "codex.run", TaskID: t.id, Path: t.path}, opened)
	case "materials-open":
		s.dispatchScopedSystemOpen(application.Action{Kind: "system.open", TaskID: t.id, Path: t.materialsPath,
			Payload: map[string]any{"desktopSystemOpenDirectory": true}}, opened)
	case "materials-copy-path":
		s.copyTextToClipboard(services, t.materialsPath, "已复制任务资料路径")
	case "materials-configure":
		s.page, s.settingsUnit.state.settingsTab, s.settingsUnit.state.settingsPage, s.settingsUnit.state.settingsLoaded = "settings", "存储", "paths", ""
	case "materials-retry":
		s.dispatchTaskContextAction(application.Action{Kind: "task.materials-retry", TaskID: t.id}, opened)
	case "pin":
		s.setTaskPinned(t, !pinned)
	case "archive":
		s.openTaskArchiveDialog(t)
	case "restore":
		s.dispatchTaskContextAction(application.Action{Kind: "task.restore", TaskID: t.id, Path: t.path}, opened)
	case "delete":
		s.openTaskDeleteDialog(t)
	}
}
