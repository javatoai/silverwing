package desktop

import (
	"encoding/json"
	"fmt"
	"strings"

	"silverwing-go/internal/application"
)

func (s *app) ensureMeegleProjectCatalogState() *meegleProjectCatalogState {
	if s.settingsUnit.state.meegleProjectCatalog == nil {
		s.settingsUnit.state.meegleProjectCatalog = &meegleProjectCatalogState{}
	}
	return s.settingsUnit.state.meegleProjectCatalog
}

func (s *app) currentMeegleProjectCatalogIdentity() (string, bool) {
	status := s.settingsUnit.state.settingsCLIStatus["meegle"]
	authenticated := dataBool(status, "authenticated") || mapString(status, "authenticationState") == "AUTHENTICATED"
	if !authenticated {
		return "", false
	}
	return strings.Join([]string{s.snapshot.Home, s.cliConfiguredPath("meegle"), strings.TrimSpace(mapString(status, "host"))}, "\x00"), true
}

func (s *app) syncMeegleProjectCatalogIdentity(state *meegleProjectCatalogState) bool {
	if state == nil {
		return false
	}
	identity, authenticated := s.currentMeegleProjectCatalogIdentity()
	if !authenticated {
		if state.identity != "" || state.loaded || state.loading {
			s.resetMeegleProjectCatalog(state)
		}
		return false
	}
	if state.identity != identity {
		s.resetMeegleProjectCatalog(state)
		state.identity = identity
	}
	return true
}

func (s *app) resetMeegleProjectCatalog(state *meegleProjectCatalogState) {
	if state == nil {
		return
	}
	s.cancelMeegleProjectCatalogRead()
	state.generation++
	state.projects = nil
	state.loading, state.loaded = false, false
	state.error = ""
	state.identity = ""
	state.verifiedIdentity = ""
	state.pickerQuery, state.pickerSelection = "", ""
	state.pickerSaveError, state.pickerCommittedKey = "", ""
}

func (s *app) clearMeegleProjectCatalogCache() {
	state := s.settingsUnit.state.meegleProjectCatalog
	if state == nil {
		return
	}
	s.resetMeegleProjectCatalog(state)
	state.pickerOpen = false
	state.pickerPendingSave = 0
	state.pickerSaveGeneration++
}

func (s *app) closeMeegleProjectPicker() {
	state := s.settingsUnit.state.meegleProjectCatalog
	if state == nil {
		return
	}
	state.pickerOpen = false
	state.pickerFocusPending = false
	s.cancelMeegleProjectCatalogRead()
	if state.pickerPendingSave == 0 && state.pickerCommittedKey == "" {
		state.pickerQuery, state.pickerSelection = "", ""
		state.pickerSaveError = ""
	}
}

func (s *app) openMeegleProjectPicker() {
	state := s.ensureMeegleProjectCatalogState()
	state.pickerOpen = true
	state.pickerFocusPending = true
	state.pickerHighlight, state.pickerShown = 0, ""
	s.syncMeegleProjectCatalogContext(state)
	if state.pickerPendingSave == 0 && state.pickerSaveError == "" {
		state.pickerQuery = ""
		state.pickerSelection = ""
	}
	s.syncMeegleProjectCatalogIdentity(state)
	if _, authenticated := s.currentMeegleProjectCatalogIdentity(); authenticated {
		s.requestMeegleProjectCatalog(false)
	}
}

func (s *app) syncMeegleProjectCatalogContext(state *meegleProjectCatalogState) bool {
	next := s.currentRequirementIdentityContext()
	if state.context != next {
		s.resetMeegleProjectCatalog(state)
		state.context = next
		return false
	}
	return true
}

func (s *app) cancelMeegleProjectCatalogRead() {
	if state := s.settingsUnit.state.meegleProjectCatalog; state != nil && state.loading {
		state.generation++
		state.loading = false
	}
	if jobs := s.toolsUnit.state.cliJobs; jobs != nil {
		if request := jobs.requests["meegle.projects"]; request != nil {
			request.cancel()
			delete(jobs.requests, "meegle.projects")
		}
	}
}

type meegleProjectPickerOption struct {
	value   string
	project meegleCatalogProject
}

func (s *app) meegleProjectPickerOptions(state *meegleProjectCatalogState) []meegleProjectPickerOption {
	configured := s.meegleConfiguredProjectKeys()
	options := make([]meegleProjectPickerOption, 0, len(state.projects))
	for _, project := range state.projects {
		project.ProjectKey = strings.TrimSpace(project.ProjectKey)
		project.SimpleName = strings.TrimSpace(project.SimpleName)
		project.Name = strings.TrimSpace(project.Name)
		if project.ProjectKey == "" || project.SimpleName == "" || configured[project.ProjectKey] {
			continue
		}
		value := fmt.Sprintf("%s · %s · %s", firstNonEmpty(project.Name, project.SimpleName), project.SimpleName, project.ProjectKey)
		options = append(options, meegleProjectPickerOption{value: value, project: project})
	}
	return options
}

func (s *app) meegleProjectForPickerSelection(state *meegleProjectCatalogState, selection string) meegleCatalogProject {
	if state == nil || strings.TrimSpace(selection) == "" {
		return meegleCatalogProject{}
	}
	for _, option := range s.meegleProjectPickerOptions(state) {
		if option.value == selection {
			return option.project
		}
	}
	return meegleCatalogProject{}
}

func (s *app) meegleProjectKeyAlreadyConfigured(key string) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}
	for _, project := range s.meegleLatestProjectDraft() {
		if strings.TrimSpace(mapString(project, "project_key")) == key {
			return true
		}
	}
	return false
}

func (s *app) meegleProjectPickerEmptyMessage(state *meegleProjectCatalogState) string {
	valid, available := 0, 0
	for _, project := range state.projects {
		key := strings.TrimSpace(project.ProjectKey)
		if key == "" || strings.TrimSpace(project.SimpleName) == "" {
			continue
		}
		valid++
		if !s.meegleProjectKeyAlreadyConfigured(key) {
			available++
		}
	}
	if valid == 0 {
		return "项目目录中没有可用的 Key 和短名，请刷新目录重试。"
	}
	if available == 0 {
		return "所有可访问的项目都已添加。"
	}
	return "当前没有可添加的项目。"
}

func (s *app) meegleConfiguredProjectKeys() map[string]bool {
	keys := make(map[string]bool)
	for _, project := range s.meegleLatestProjectDraft() {
		if key := strings.TrimSpace(mapString(project, "project_key")); key != "" {
			keys[key] = true
		}
	}
	return keys
}

func (s *app) meegleLatestProjectDraft() []map[string]any {
	projects := mapsFromArray(s.configArray("meegleProjects"))
	for index, draft := range s.settingsMeegleProjectsDraftReader(projects)() {
		if index < len(projects) {
			projects[index]["project_key"] = draft["project_key"]
			projects[index]["simple_name"] = draft["simple_name"]
		}
	}
	return projects
}

func filterMeegleProjectPickerOptions(options []meegleProjectPickerOption, query string) []meegleProjectPickerOption {
	needle := strings.ToLower(strings.TrimSpace(query))
	if needle == "" {
		return options
	}
	var starts, contains []meegleProjectPickerOption
	for _, option := range options {
		value := strings.ToLower(option.value)
		switch {
		case strings.HasPrefix(value, needle):
			starts = append(starts, option)
		case strings.Contains(value, needle):
			contains = append(contains, option)
		}
	}
	return append(starts, contains...)
}

func (s *app) submitMeegleProjectPickerSelection(state *meegleProjectCatalogState, project meegleCatalogProject) {
	if state == nil || state.pickerPendingSave != 0 || s.busy {
		return
	}
	if !s.syncMeegleProjectCatalogContext(state) {
		state.pickerSaveError = "账号配置已变化，请刷新项目目录后重试。"
		return
	}
	identityReady := s.syncMeegleProjectCatalogIdentity(state)
	if !identityReady || !state.loaded || !meegleCatalogContainsProject(state, project) {
		state.pickerSaveError = "项目目录已失效，请重新加载后再添加。"
		return
	}
	key := strings.TrimSpace(project.ProjectKey)
	if key == "" || strings.TrimSpace(project.SimpleName) == "" {
		state.pickerSaveError = "所选项目缺少 Key 或短名，请刷新目录后重试。"
		return
	}
	projects := s.meegleLatestProjectDraft()
	for _, existing := range projects {
		if strings.TrimSpace(mapString(existing, "project_key")) == key {
			state.pickerSaveError = "此项目空间已添加。"
			return
		}
	}
	projects = append(projects, map[string]any{"project_key": key, "simple_name": strings.TrimSpace(project.SimpleName)})
	state.pickerSaveGeneration++
	token := fmt.Sprint(state.pickerSaveGeneration)
	state.pickerPendingSave = state.pickerSaveGeneration
	state.pickerSaveError = ""
	if !s.saveMeegleProjectSettingsWithPickerToken(projects, token, key) {
		state.pickerPendingSave = 0
	}
}

func meegleCatalogContainsProject(state *meegleProjectCatalogState, project meegleCatalogProject) bool {
	if state == nil {
		return false
	}
	for _, candidate := range state.projects {
		if strings.TrimSpace(candidate.ProjectKey) == strings.TrimSpace(project.ProjectKey) && strings.TrimSpace(candidate.SimpleName) == strings.TrimSpace(project.SimpleName) {
			return true
		}
	}
	return false
}

func (s *app) finishMeegleProjectPickerSave(action application.Action, result application.ActionResult, actionErr, refreshErr error) bool {
	if action.Kind != "config.fields.save" || strings.TrimSpace(payloadString(action.Payload, "meegleProjectPickerSaveToken")) == "" {
		return false
	}
	state := s.settingsUnit.state.meegleProjectCatalog
	pendingMatches := state != nil && state.pickerPendingSave != 0 && payloadString(action.Payload, "meegleProjectPickerSaveToken") == fmt.Sprint(state.pickerPendingSave)
	if pendingMatches {
		state.pickerPendingSave = 0
	}
	if actionErr != nil {
		if pendingMatches {
			state.pickerSaveError = "添加项目空间失败：" + actionErr.Error()
		}
		return true
	}
	// This successful write already includes the current project-row drafts.
	// Retire the old whole-list autosave before its completion callback flushes.
	s.cancelSettingsAutosave("settings:meegle-projects")
	if !s.syncMeegleProjectPickerCommittedConfig(action) {
		if pendingMatches {
			state.pickerCommittedKey = strings.TrimSpace(payloadString(action.Payload, "meegleProjectPickerSaveProjectKey"))
			state.pickerSaveError = "项目空间已保存，但本地设置状态未能同步；请关闭弹窗后重新读取设置。"
		}
		return true
	}
	if refreshErr != nil {
		if pendingMatches {
			state.pickerCommittedKey = strings.TrimSpace(payloadString(action.Payload, "meegleProjectPickerSaveProjectKey"))
			state.pickerSaveError = "项目空间已保存，但设置刷新失败：" + refreshErr.Error()
		}
		return true
	}
	if !pendingMatches {
		return true
	}
	state.pickerSaveError = ""
	state.pickerCommittedKey = ""
	state.pickerOpen = false
	state.pickerQuery = ""
	state.pickerSelection = ""
	return true
}

func (s *app) syncMeegleProjectPickerCommittedConfig(action application.Action) bool {
	var update application.ConfigFieldsUpdate
	switch value := action.Payload["update"].(type) {
	case application.ConfigFieldsUpdate:
		update = value
	case *application.ConfigFieldsUpdate:
		if value == nil {
			return false
		}
		update = *value
	default:
		return false
	}
	projectsJSON := update.Fields["meegleProjects"]
	if len(projectsJSON) == 0 {
		return false
	}
	var projects []map[string]any
	if err := json.Unmarshal(projectsJSON, &projects); err != nil || projects == nil {
		return false
	}

	previousConfig := s.settingsUnit.state.configJSON
	if strings.TrimSpace(previousConfig) == "" {
		previousConfig = s.snapshot.ConfigJSON
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal([]byte(previousConfig), &config); err != nil || config == nil {
		return false
	}
	config["meegleProjects"] = append(json.RawMessage(nil), projectsJSON...)
	nextJSON, err := json.Marshal(config)
	if err != nil {
		return false
	}

	// Treat only the project rows as changed so their inputs are synchronized to
	// the saved field without rewriting any unrelated setting controls.
	var beforeForInputs map[string]json.RawMessage
	if err := json.Unmarshal([]byte(previousConfig), &beforeForInputs); err != nil || beforeForInputs == nil {
		beforeForInputs = make(map[string]json.RawMessage)
	}
	beforeForInputs["meegleProjects"] = json.RawMessage("null")
	beforeJSON, err := json.Marshal(beforeForInputs)
	if err != nil {
		return false
	}

	s.settingsUnit.state.configJSON = string(nextJSON)
	s.snapshot.ConfigJSON = s.settingsUnit.state.configJSON
	s.syncSettingsConfigInputs(string(beforeJSON), s.settingsUnit.state.configJSON)
	return true
}
