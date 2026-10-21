package desktop

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"silverwing-go/internal/application"
	"silverwing-go/internal/storage"
)

type settingsBackupDeletePlan struct {
	scope settingsExtensionReadScope
	ids   []string
	names []string
}

func (s *app) reconcileSettingsBackupSelection() {
	state := s.ensureSettingsConfigRecovery()
	selected := map[string]bool{}
	for _, backup := range s.settingsBackups {
		id := mapString(backup, "id")
		if id != "" && state.backupSelected[id] {
			selected[id] = true
		}
	}
	state.backupSelected = selected
}

func (s *app) settingsSelectedBackupPlan() (settingsBackupDeletePlan, bool) {
	state := s.ensureSettingsConfigRecovery()
	plan := settingsBackupDeletePlan{scope: s.settingsExtensionCurrentScope()}
	if !s.settingsConfigRecoveryVisible() || state.backupDeleteLoading || state.backupsLoading || s.busy || state.backupsScope != plan.scope {
		return plan, false
	}
	for _, backup := range s.settingsBackups {
		if id := mapString(backup, "id"); id != "" && state.backupSelected[id] {
			plan.ids = append(plan.ids, id)
			plan.names = append(plan.names, filepath.Base(mapString(backup, "path")))
		}
	}
	return plan, len(plan.ids) > 0
}

func (s *app) beginSettingsBackupDelete() {
	plan, valid := s.settingsSelectedBackupPlan()
	if !valid {
		return
	}
	s.showConfirmationDialog("删除配置备份", fmt.Sprintf("将永久删除所选的 %d 个备份文件，此操作不可撤销。", len(plan.ids)),
		strings.Join(plan.names, "\n"), []confirmationDialogButton{{label: "删除所选"}, {label: "取消"}}, 1, 1,
		func(index int) { s.finishSettingsBackupDeleteConfirmation(plan, index) })
}

func (s *app) finishSettingsBackupDeleteConfirmation(plan settingsBackupDeletePlan, index int) {
	if index == 0 {
		s.confirmSettingsBackupDelete(plan)
	}
}

func (s *app) confirmSettingsBackupDelete(plan settingsBackupDeletePlan) {
	state := s.ensureSettingsConfigRecovery()
	if !s.settingsConfigRecoveryVisible() || plan.scope != s.settingsExtensionCurrentScope() || state.backupDeleteLoading || state.backupsLoading || s.busy {
		return
	}
	// The confirmation owns IDs captured before opening. Never recompute its
	// selection from the current row order or from a later Select all operation.
	known := map[string]bool{}
	for _, backup := range s.settingsBackups {
		known[mapString(backup, "id")] = true
	}
	for _, id := range plan.ids {
		if !known[id] {
			state.backupDeleteError = "备份列表已变化，请重新选择。"
			return
		}
	}
	if len(plan.ids) == 0 {
		return
	}
	s.dispatch(application.Action{Kind: "config.delete-backups", Payload: map[string]any{"confirmed": true, "ids": append([]string(nil), plan.ids...)}})
}

func (s *app) applySettingsBackupDelete(result application.ActionResult, actionErr error) {
	state := s.ensureSettingsConfigRecovery()
	var deleted storage.BackupDeleteResult
	encoded, err := json.Marshal(result.Data)
	if err == nil {
		err = json.Unmarshal(encoded, &deleted)
	}
	if result.Data == nil || err != nil {
		if actionErr == nil {
			actionErr = errors.New("删除结果无效，请重新读取备份。")
		}
		state.backupDeleteError = actionErr.Error()
		return
	}
	removed := map[string]bool{}
	for _, id := range deleted.DeletedIDs {
		removed[id] = true
		delete(state.backupSelected, id)
	}
	failedRows := []map[string]any{}
	state.backupFailures = map[string]string{}
	for _, failure := range deleted.Failures {
		state.backupFailures[failure.ID] = failure.Reason
	}
	for _, backup := range s.settingsBackups {
		id := mapString(backup, "id")
		if !removed[id] && state.backupFailures[id] != "" {
			failedRows = append(failedRows, backup)
		}
	}
	if actionErr == nil {
		s.settingsBackups = nil
		failedPaths := map[string]string{}
		for _, backup := range failedRows {
			failedPaths[mapString(backup, "path")] = mapString(backup, "id")
		}
		for _, backup := range mapsFromData(deleted.Backups) {
			if failedID := failedPaths[mapString(backup, "path")]; failedID == "" || failedID == mapString(backup, "id") {
				s.settingsBackups = append(s.settingsBackups, backup)
			}
		}
		listed := map[string]bool{}
		for _, backup := range s.settingsBackups {
			listed[mapString(backup, "id")] = true
		}
		for _, backup := range failedRows {
			if !listed[mapString(backup, "id")] {
				s.settingsBackups = append(s.settingsBackups, backup)
			}
		}
	} else {
		rows := s.settingsBackups[:0]
		for _, backup := range s.settingsBackups {
			if !removed[mapString(backup, "id")] {
				rows = append(rows, backup)
			}
		}
		s.settingsBackups = rows
	}
	s.reconcileSettingsBackupSelection()
	for id := range state.backupFailures {
		state.backupSelected[id] = true
	}
	state.backupsLoaded, state.backupsDirty, state.backupsScope = true, false, s.settingsExtensionCurrentScope()
	state.backupDeleteError = ""
	if actionErr != nil {
		state.backupDeleteError = actionErr.Error()
	} else if len(deleted.Failures) > 0 {
		state.backupDeleteError = fmt.Sprintf("已删除 %d 个，%d 个未删除；可重试或重新读取。", len(deleted.DeletedIDs), len(deleted.Failures))
	} else {
		s.settingsOutput = fmt.Sprintf("已删除 %d 个配置备份。", len(deleted.DeletedIDs))
		state.backupFocusReload = len(s.settingsBackups) == 0
	}
}
