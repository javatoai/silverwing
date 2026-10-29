package desktop

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

type taskRootChangeRequest struct {
	home, config, target, stage string
}

func (s *app) taskRootChangeCurrent(request *taskRootChangeRequest) bool {
	return request != nil && s.settingsUnit.state.taskRootChange == request && request.home == s.snapshot.Home &&
		request.config == s.settingsUnit.state.configJSON && workspaceHealthPathKey(request.target) == workspaceHealthPathKey(*s.input("setting:taskRootTarget", ""))
}

func (s *app) requestTaskRootChange(target string) {
	if s.loading || s.busy || s.settingsUnit.state.settingsPickerBusy || s.settingsUnit.state.taskRootChange != nil || s.win == nil {
		return
	}
	target = strings.TrimSpace(target)
	if target == "" {
		return
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		s.lastError = "无法解析任务存放目录：" + err.Error()
		return
	}
	*s.input("setting:taskRootTarget", "") = abs
	if workspaceHealthPathKey(abs) == workspaceHealthPathKey(s.configString("taskRoot")) {
		return
	}
	request := &taskRootChangeRequest{home: s.snapshot.Home, config: s.settingsUnit.state.configJSON, target: abs, stage: "choice"}
	s.settingsUnit.state.taskRootChange = request
	s.settingsUnit.state.migrationPlan, s.settingsUnit.state.migrationReady, s.settingsUnit.state.migrationPreview = nil, false, ""
	detail := s.taskRootChangeDescription(request)
	if !s.showConfirmationDialog("更改任务存放目录", "如何处理旧任务数据？", detail, []confirmationDialogButton{
		{label: "迁移旧任务数据", primary: true},
		{label: "直接切换"},
		{label: "取消"},
	}, 2, 2, func(button int) {
		if s.settingsUnit.state.taskRootChange != request {
			return
		}
		s.applyTaskRootChangeChoice(request, button)
	}) {
		s.settingsUnit.state.taskRootChange = nil
	}
}

func (s *app) taskRootChangeDescription(request *taskRootChangeRequest) string {
	var config domain.AppConfig
	_ = json.Unmarshal([]byte(request.config), &config)
	current := ""
	if config.TaskRoot != nil {
		current = *config.TaskRoot
	}
	return current + " → " + request.target + "\n迁移会搬动旧任务并校验工作区；直接切换会保留原目录的数据。"
}

// Separate choice handling lets the existing focused UI proofs exercise all
// three decisions without replacing native dialog or backend business results.
func (s *app) applyTaskRootChangeChoice(request *taskRootChangeRequest, button int) {
	if !s.taskRootChangeCurrent(request) || s.busy {
		if s.settingsUnit.state.taskRootChange == request {
			s.settingsUnit.state.taskRootChange = nil
			s.lastError = "目录或配置已变化，请重新选择任务存放目录。"
		}
		return
	}
	switch button {
	case 0:
		if s.materialsUnit.state.pendingTaskRootMigration != nil || s.snapshot.TaskRootMigrationPending == nil || *s.snapshot.TaskRootMigrationPending {
			request.stage = "recovering"
			s.act("migration.recover", "", "", "", "", "", map[string]any{"taskRootChangeTarget": request.target})
			if !s.busy {
				s.settingsUnit.state.taskRootChange = nil
			}
			return
		}
		s.beginTaskRootChangeMigration(request)
	case 1:
		if s.materialsUnit.state.pendingTaskRootMigration != nil || s.snapshot.TaskRootMigrationPending == nil || *s.snapshot.TaskRootMigrationPending {
			s.settingsUnit.state.taskRootChange = nil
			s.lastError = "任务目录恢复尚未确认完成，不能直接切换；请选择迁移以先完成恢复。"
			return
		}
		request.stage = "switching"
		s.act("config.task-root.save", "", "", "", request.target, "", map[string]any{"confirmed": true, "expectedTaskRoot": s.configString("taskRoot")})
		if !s.busy {
			s.settingsUnit.state.taskRootChange = nil
		}
	default:
		s.settingsUnit.state.taskRootChange = nil
		s.taskUnit.state.taskRootTargetFocused = false
		*s.input("setting:taskRootTarget", "") = s.configString("taskRoot")
	}
}

func (s *app) beginTaskRootChangeMigration(request *taskRootChangeRequest) {
	if s.settingsUnit.state.taskRootChange != request || request.home != s.snapshot.Home {
		return
	}
	if workspaceHealthPathKey(request.target) == workspaceHealthPathKey(s.configString("taskRoot")) {
		s.settingsUnit.state.taskRootChange = nil
		*s.input("setting:taskRootTarget", "") = s.configString("taskRoot")
		return
	}
	request.config, request.stage = s.settingsUnit.state.configJSON, "preview"
	*s.input("setting:taskRootTarget", "") = request.target
	s.previewTaskRootMigrationTarget(request.target)
	if !s.busy {
		s.settingsUnit.state.taskRootChange = nil
	}
}

func (s *app) continueTaskRootChangePreview(action application.Action, preview workspace.TaskRootMigrationPreview) {
	request := s.settingsUnit.state.taskRootChange
	if request == nil || request.stage != "preview" || workspaceHealthPathKey(action.Path) != workspaceHealthPathKey(request.target) {
		return
	}
	if !s.taskRootChangeCurrent(request) || !preview.CanMigrate || len(preview.Blockers) != 0 {
		s.settingsUnit.state.taskRootChange = nil
		s.lastError = firstNonEmpty(s.settingsUnit.state.migrationPreview, "当前目录不能迁移，请检查任务目录和工作区。")
		return
	}
	request.stage = "migrating"
	s.applyConfirmedTaskRootMigration(preview)
	if !s.busy {
		s.settingsUnit.state.taskRootChange = nil
	}
}

// Root calls this after the action's result and refreshed Snapshot have been
// applied. err must include refresh failure, so no follow-up can use old state.
func (s *app) finishTaskRootChangeAction(action application.Action, _ application.ActionResult, err error) {
	request := s.settingsUnit.state.taskRootChange
	if request == nil {
		return
	}
	switch action.Kind {
	case "migration.recover":
		if request.stage != "recovering" || payloadString(action.Payload, "taskRootChangeTarget") != request.target {
			return
		}
		if err != nil || request.home != s.snapshot.Home || s.snapshot.TaskRootMigrationPending == nil || *s.snapshot.TaskRootMigrationPending || s.materialsUnit.state.pendingTaskRootMigration != nil {
			s.settingsUnit.state.taskRootChange = nil
			if err == nil {
				s.lastError = "任务目录恢复尚未完成，请处理恢复错误后再更改目录。"
			}
			return
		}
		s.beginTaskRootChangeMigration(request)
	case "migration.preview":
		if request.stage == "preview" && workspaceHealthPathKey(action.Path) == workspaceHealthPathKey(request.target) {
			if err != nil {
				s.settingsUnit.state.taskRootChange = nil
				return
			}
			preview, previewErr := s.taskRootMigrationPlan()
			if previewErr != nil {
				s.settingsUnit.state.taskRootChange = nil
				s.lastError = firstNonEmpty(s.settingsUnit.state.migrationPreview, previewErr.Error())
				return
			}
			s.continueTaskRootChangePreview(action, preview)
		}
	case "migration.apply", "config.task-root.save":
		if workspaceHealthPathKey(action.Path) == workspaceHealthPathKey(request.target) {
			s.settingsUnit.state.taskRootChange = nil
		}
	}
}
