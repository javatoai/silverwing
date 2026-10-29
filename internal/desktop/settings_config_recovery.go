package desktop

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"silverwing-go/internal/application"
)

// Reuse the existing job container and executor, but own a separate registry,
// identity checks and presentation state for configuration recovery operations.
type settingsConfigRecoveryState struct {
	jobs                *settingsExtensionJobs
	preview             *application.ConfigImportPreview
	previewScope        settingsExtensionReadScope
	previewText         string
	previewLoading      bool
	previewError        string
	backupsLoading      bool
	backupsLoaded       bool
	backupsScope        settingsExtensionReadScope
	backupsError        string
	backupsDirty        bool
	backupSelected      map[string]bool
	backupFailures      map[string]string
	backupDeleteLoading bool
	backupDeleteError   string
	backupFocusReload   bool
}

func (s *app) ensureSettingsConfigRecovery() *settingsConfigRecoveryState {
	if s.settingsUnit.state.settingsConfigRecovery == nil {
		s.settingsUnit.state.settingsConfigRecovery = &settingsConfigRecoveryState{jobs: &settingsExtensionJobs{
			current: make(map[string]*settingsExtensionReadJob), running: make(map[*settingsExtensionReadJob]struct{}),
		}}
	}
	return s.settingsUnit.state.settingsConfigRecovery
}

func settingsConfigRead(action application.Action) bool {
	return action.Kind == "config.import-preview" || action.Kind == "config.backups" || action.Kind == "config.delete-backups"
}

func (s *app) settingsConfigRecoveryVisible() bool {
	return s.page == "settings" && s.settingsUnit.state.settingsPage == "config-backup"
}

func (s *app) cancelSettingsConfigRead(key string) {
	state := s.settingsUnit.state.settingsConfigRecovery
	if state == nil {
		return
	}
	if request := state.jobs.current[key]; request != nil {
		delete(state.jobs.current, key)
		request.cancel()
	}
	if key == "config.delete-backups" {
		state.backupDeleteLoading = false
	} else if key == "config.backups" {
		state.backupsLoading = false
	} else {
		state.previewLoading = false
	}
}

func (s *app) beginSettingsConfigRead(action application.Action) (*settingsExtensionReadJob, error) {
	if !settingsConfigRead(action) || !s.settingsConfigRecoveryVisible() {
		return nil, errors.New("配置读取页面已变化，请重新读取。")
	}
	state := s.ensureSettingsConfigRecovery()
	if state.backupDeleteLoading && (action.Kind == "config.delete-backups" || action.Kind == "config.backups") {
		return nil, errors.New("正在删除备份，请等待完成。")
	}
	jobs := state.jobs
	jobs.mu.Lock()
	closed := jobs.closed
	jobs.mu.Unlock()
	if closed {
		return nil, errors.New("配置读取已关闭。")
	}
	s.cancelSettingsConfigRead(action.Kind)
	// Keep the confirmation's immutable selection outside mutable UI state.
	payload := make(map[string]any, len(action.Payload))
	for key, value := range action.Payload {
		payload[key] = value
	}
	action.Payload = payload
	if ids, ok := payload["ids"].([]string); ok {
		payload["ids"] = append([]string(nil), ids...)
	}
	ctx, cancel := context.WithCancel(context.Background())
	request := &settingsExtensionReadJob{action: action, key: action.Kind,
		scope: s.settingsExtensionCurrentScope(), owner: jobs, ctx: ctx, cancel: cancel, done: make(chan struct{})}
	jobs.mu.Lock()
	jobs.running[request] = struct{}{}
	jobs.mu.Unlock()
	jobs.current[request.key] = request
	if action.Kind == "config.delete-backups" {
		s.cancelSettingsConfigRead("config.backups")
		state.backupDeleteLoading, state.backupDeleteError = true, ""
		state.backupFailures = map[string]string{}
	} else if action.Kind == "config.backups" {
		state.backupsLoading, state.backupsLoaded, state.backupsDirty = true, false, false
		state.backupsError = ""
		s.settingsUnit.state.settingsBackups = nil
	} else {
		state.previewLoading, state.previewError = true, ""
		state.preview, state.previewText = nil, ""
		s.materialsUnit.state.importPreview, s.materialsUnit.state.importPath = false, ""
	}
	return request, nil
}

func (s *app) settingsConfigReadRelevant(request *settingsExtensionReadJob) bool {
	state := s.settingsUnit.state.settingsConfigRecovery
	if request == nil || state == nil || state.jobs != request.owner ||
		request.owner.current[request.key] != request || request.ctx.Err() != nil ||
		request.scope != s.settingsExtensionCurrentScope() || !s.settingsConfigRecoveryVisible() {
		return false
	}
	request.owner.mu.Lock()
	closed := request.owner.closed
	request.owner.mu.Unlock()
	return !closed
}

func (s *app) dispatchIndependentSettingsConfigRead(action application.Action) bool {
	if !settingsConfigRead(action) {
		return false
	}
	request, err := s.beginSettingsConfigRead(action)
	if err != nil {
		state := s.ensureSettingsConfigRecovery()
		if action.Kind == "config.backups" {
			state.backupsError = err.Error()
		} else if action.Kind == "config.delete-backups" {
			state.backupDeleteError = err.Error()
		} else {
			state.previewError = err.Error()
		}
		return true
	}
	if s.backend == nil || s.win == nil || s.win.IsDestroyed() {
		s.finishSettingsConfigRead(request, application.ActionResult{}, errors.New("配置读取后端或窗口不可用，请重试。"))
		s.completeSettingsExtensionRead(request)
		return true
	}
	backend, win := s.backend, s.win
	win.Invalidate()
	go func() {
		defer s.completeSettingsExtensionRead(request)
		result, err := executeSettingsExtensionRead(backend, request)
		if win.IsDestroyed() {
			request.cancel()
			return
		}
		updated := make(chan struct{})
		win.Update(func() {
			defer close(updated)
			s.finishSettingsConfigRead(request, result, err)
		})
		select {
		case <-updated:
		case <-request.ctx.Done():
		}
	}()
	return true
}

func (s *app) finishSettingsConfigRead(request *settingsExtensionReadJob, result application.ActionResult, err error) {
	if request == nil {
		return
	}
	defer request.cancel()
	if !s.settingsConfigReadRelevant(request) {
		if request.owner.current[request.key] == request {
			s.cancelSettingsConfigRead(request.key)
			if request.key == "config.backups" {
				s.ensureSettingsConfigRecovery().backupsDirty = true
			}
		}
		return
	}
	delete(request.owner.current, request.key)
	state := s.ensureSettingsConfigRecovery()
	if request.key == "config.delete-backups" {
		state.backupDeleteLoading = false
		s.applySettingsBackupDelete(result, err)
		return
	}
	if request.key == "config.backups" {
		state.backupsLoading = false
		if err != nil {
			state.backupsError = err.Error()
			return
		}
	} else {
		state.previewLoading = false
		if err == nil {
			preview, decodeErr := settingsConfigPreview(result.Data)
			err = decodeErr
			if err == nil {
				selected, selectedErr := filepath.Abs(request.action.Path)
				returned, returnedErr := filepath.Abs(preview.Path)
				if selectedErr != nil || returnedErr != nil || selected != returned {
					err = errors.New("配置预览来源与本次选择不一致，请重新读取。")
				}
			}
		}
		if err != nil {
			state.previewError = err.Error()
			return
		}
	}
	s.applySettingsActionResult(request.action, result)
}

func settingsConfigPreview(data any) (application.ConfigImportPreview, error) {
	preview, ok := data.(application.ConfigImportPreview)
	if pointer, pointerOK := data.(*application.ConfigImportPreview); pointerOK && pointer != nil {
		preview, ok = *pointer, true
	}
	if !ok || preview.Path == "" {
		return application.ConfigImportPreview{}, errors.New("后端未返回有效配置预览，请重新读取。")
	}
	preview.ChangedFields = append([]string(nil), preview.ChangedFields...)
	preview.Changes = append([]string(nil), preview.Changes...)
	return preview, nil
}

func (s *app) applySettingsConfigPreview(data any) {
	state := s.ensureSettingsConfigRecovery()
	preview, err := settingsConfigPreview(data)
	if err != nil {
		state.previewError = err.Error()
		return
	}
	state.preview, state.previewScope = &preview, s.settingsExtensionCurrentScope()
	state.previewText, state.previewError = configPreviewText(preview), ""
	s.materialsUnit.state.importPreview, s.materialsUnit.state.importPath = true, preview.Path
	// Keep the existing settings summary and formatter proof. The confirmation
	// itself reads the dedicated immutable preview text below.
	s.settingsUnit.state.settingsOutput = state.previewText
}

func (s *app) applySettingsConfigBackups(data any) {
	state := s.ensureSettingsConfigRecovery()
	s.settingsUnit.state.settingsBackups = mapsFromData(data)
	s.reconcileSettingsBackupSelection()
	state.backupFailures, state.backupDeleteError = nil, ""
	state.backupsLoading, state.backupsLoaded, state.backupsDirty = false, true, false
	state.backupsScope, state.backupsError = s.settingsExtensionCurrentScope(), ""
}

func (s *app) observeSettingsConfigRecovery() {
	state := s.settingsUnit.state.settingsConfigRecovery
	if state == nil {
		return
	}
	for key, request := range state.jobs.current {
		if !s.settingsConfigReadRelevant(request) {
			s.cancelSettingsConfigRead(key)
			if key == "config.backups" {
				state.backupsDirty = true
			}
		}
	}
	if state.preview != nil && (!s.materialsUnit.state.importPreview || !s.settingsConfigRecoveryVisible() || state.previewScope != s.settingsExtensionCurrentScope()) {
		state.preview, state.previewText = nil, ""
		s.materialsUnit.state.importPreview = false
	}
	if state.backupsLoaded && state.backupsScope != s.settingsExtensionCurrentScope() {
		state.backupsLoaded, state.backupsDirty = false, true
		s.settingsUnit.state.settingsBackups = nil
		state.backupSelected, state.backupFailures, state.backupDeleteError = nil, nil, ""
		state.backupFocusReload = false
	}
	if s.settingsConfigRecoveryVisible() && !s.busy && state.backupsDirty && !state.backupsLoading && !state.backupDeleteLoading {
		s.act("config.backups", "", "", "", "", "", nil)
	}
}

// Run after each attempted import/restore, including a failed replacement: the
// backup may already have been created and the retention limit may have pruned.
func (s *app) settingsConfigMutationFinished(action application.Action) {
	if action.Kind != "config.import" && action.Kind != "config.restore-backup" {
		return
	}
	state := s.ensureSettingsConfigRecovery()
	s.cancelSettingsConfigRead("config.backups")
	state.backupsLoaded, state.backupsDirty, state.backupsError = false, true, ""
	s.settingsUnit.state.settingsBackups = nil
}

func (s *app) settingsConfigImportAction() (application.Action, bool) {
	state := s.settingsUnit.state.settingsConfigRecovery
	if state == nil || state.preview == nil || !s.materialsUnit.state.importPreview || s.busy ||
		!s.settingsConfigRecoveryVisible() || state.previewScope != s.settingsExtensionCurrentScope() {
		return application.Action{}, false
	}
	return application.Action{Kind: "config.import", Path: state.preview.Path}, true
}

func (s *app) confirmSettingsConfigImport() {
	action, valid := s.settingsConfigImportAction()
	if !valid {
		return
	}
	s.dispatch(action)
	if s.busy && s.operationKind == "config.import" {
		s.materialsUnit.state.importPreview = false
	}
}

func (s *settingsController) closeSettingsConfigReads(ctx context.Context) error {
	state := s.state.settingsConfigRecovery
	if state == nil {
		return nil
	}
	jobs := state.jobs
	jobs.mu.Lock()
	jobs.closed = true
	running := make([]*settingsExtensionReadJob, 0, len(jobs.running))
	for request := range jobs.running {
		request.cancel()
		running = append(running, request)
	}
	jobs.current = make(map[string]*settingsExtensionReadJob)
	jobs.mu.Unlock()
	for _, request := range running {
		select {
		case <-request.done:
		case <-ctx.Done():
			return fmt.Errorf("等待配置读取清理：%w", ctx.Err())
		}
	}
	return nil
}
