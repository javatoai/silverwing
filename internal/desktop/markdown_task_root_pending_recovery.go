package desktop

import (
	"errors"

	"silverwing-go/internal/application"
)

const markdownTaskRootRecoveryPause = "任务目录恢复尚未完成，Markdown 自动保存已暂停；原文已保留，请先完成目录恢复"

// Configuration Before alone does not prove rollback: the original task may
// still exist only at target behind a retained PREPARED checkpoint. Evidence
// comes from a fresh backend snapshot, never from UI filesystem inspection.
func markdownTaskRootOriginalState(plan *markdownTaskRootMigrationPlan, snapshot application.Snapshot, snapshotErr error) bool {
	if plan == nil || snapshotErr != nil || snapshot.ConfigJSON == "" || normalizeIdentityPath(snapshot.Home) != normalizeIdentityPath(plan.home) || snapshot.TaskRootMigrationPending == nil || *snapshot.TaskRootMigrationPending {
		return false
	}
	before, err := plan.matchesConfigJSON(snapshot.ConfigJSON, false)
	if err != nil || !before || len(plan.tasks) != len(plan.preview.PathMappings) {
		return false
	}
	for _, mapping := range plan.preview.PathMappings {
		captured := false
		for _, owner := range plan.tasks {
			if normalizeIdentityPath(owner.path) == normalizeIdentityPath(mapping.SourceDirectory) {
				captured = true
				break
			}
		}
		if !captured {
			return false
		}
	}
	for _, captured := range plan.tasks {
		matches := 0
		for _, current := range snapshot.Tasks {
			if current.ID == captured.id && normalizeIdentityPath(current.Path) == normalizeIdentityPath(captured.path) && normalizeIdentityPath(current.MaterialsPath) == normalizeIdentityPath(captured.materialsPath) && current.CreatedAt == captured.createdAt && current.Archived == captured.archived {
				matches++
			}
		}
		if captured.createdAt == "" || matches != 1 {
			return false
		}
	}
	return true
}

// UI-thread ownership keeps Quiesce until recovery is proven. Pause supplies a
// visible error without the permanent FailClosed flag; Retry still refuses the
// held keys. Do not replace an existing error or conflict with our status text.
func (l *markdownTaskRootMigrationLease) retainForTaskRootRecovery() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return errors.New("Markdown 任务迁移保存租约已释放")
	}
	s := l.app
	if s == nil || l.plan == nil || s.materialsUnit.state.pendingTaskRootMigration != nil && s.materialsUnit.state.pendingTaskRootMigration != l {
		l.closed = true
		if l.engine != nil {
			l.engine.FailClosed(markdownTaskRootRecoveryPause)
		}
		return errors.New("无法保留 Markdown 任务目录恢复租约")
	}
	if s.materialsUnit.state.pendingTaskRootMigration == l {
		return nil
	}
	if l.plan.autosave != nil {
		for _, entry := range l.plan.entries {
			state := l.plan.autosave.Status(entry.oldKey)
			if _, exists := l.plan.autosave.Content(entry.oldKey); exists && state.Error == "" && !state.Conflict {
				l.plan.autosave.Pause(entry.oldKey, markdownTaskRootRecoveryPause)
				l.pendingPauseKeys = append(l.pendingPauseKeys, entry.oldKey)
			}
		}
	}
	s.materialsUnit.state.pendingTaskRootMigration = l
	return nil
}

// Call on the UI thread after applying snapshot tasks/config. Recovery restores
// filesystem/manifests in the backend; this only releases the retained save
// lease after the original complete identity and checkpoint absence are proven.
func (s *app) observePendingTaskRootMigration(snapshot application.Snapshot) {
	l := s.materialsUnit.state.pendingTaskRootMigration
	if l == nil || !markdownTaskRootOriginalState(l.plan, snapshot, nil) {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		s.materialsUnit.state.pendingTaskRootMigration = nil
		return
	}
	s.materialsUnit.state.documentSaveMu.Lock()
	for _, captured := range l.plan.entries {
		current, exists := s.materialsUnit.state.documentSaveRefs[captured.oldKey]
		if !exists || !sameMarkdownMigrationRefOwner(current, captured.before) {
			s.materialsUnit.state.documentSaveMu.Unlock()
			return
		}
	}
	s.materialsUnit.state.documentSaveMu.Unlock()
	l.closed = true
	s.materialsUnit.state.pendingTaskRootMigration = nil
	if l.engine != nil {
		// Keep our pause error through Release so recovery itself does not
		// implicitly save restored dirty content before the next edit/retry.
		l.engine.Release()
	}
	if l.plan.autosave != nil {
		for _, key := range l.pendingPauseKeys {
			state := l.plan.autosave.Status(key)
			if state.Error == markdownTaskRootRecoveryPause && !state.Conflict {
				// Pause with no message clears only our own error and does not
				// schedule a save. Real subsequent edits/retries use normal rules.
				l.plan.autosave.Pause(key, "")
			}
		}
	}
}

// OnClosed uses only the engine-owned lease. It never resumes an unconfirmed
// source identity or changes editor source/draft/reading maps during shutdown.
func (s *materialsController) abandonPendingTaskRootMigration() {
	l := s.state.pendingTaskRootMigration
	s.state.pendingTaskRootMigration = nil
	if l != nil {
		l.Abandon(true)
	}
}
