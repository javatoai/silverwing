package desktop

import (
	"fmt"
	"path/filepath"
	"strings"

	"silverwing-go/internal/application"
)

func extensionOwnershipLabel(ownership string) string {
	switch ownership {
	case "MANAGED":
		return "由 SilverWing 管理"
	case "EXTERNAL":
		return "外部安装"
	case "NOT_INSTALLED":
		return "未安装"
	default:
		return "归属状态尚未读取"
	}
}

type settingsExtensionRequestState struct {
	serial                uint64
	branchToken           string
	branchURL, branchForm string
	branchLoading         bool
	branchError           string
	previewToken          string
	previewLoading        bool
	previewError          string
	previewPath           string
	previewMarkdown       *markdownEditorState
	previewPluginSkills   []string
	catalogToken          string
	filesToken            string
	workspaceToolsToken   string
	workspaceToolsLoading bool
	workspaceToolsLoaded  bool
	workspaceToolsError   string
	workspaceToolsScope   settingsExtensionReadScope
	workspaceTools        []taskCreationWorkspaceToolOption
	readActions           map[string]application.Action
	readScopes            map[string]settingsExtensionReadScope
	jobs                  *settingsExtensionJobs
}

func (s *app) ensureSettingsExtensionRequests() *settingsExtensionRequestState {
	if s.settingsUnit.state.settingsExtensionRequests == nil {
		s.settingsUnit.state.settingsExtensionRequests = &settingsExtensionRequestState{}
	}
	state := s.settingsUnit.state.settingsExtensionRequests
	if state.readActions == nil {
		state.readActions = make(map[string]application.Action)
		state.readScopes = make(map[string]settingsExtensionReadScope)
	}
	return state
}

func settingsExtensionRead(action application.Action) bool {
	if payloadString(action.Payload, "browserRequestID") != "" {
		return false
	}
	switch action.Kind {
	case "workspace.tools-options":
		return payloadString(action.Payload, "taskCreateToken") == "settings-workspace-tools"
	case "skills.catalog", "skills.files/list", "skills.sources/branches/list", "codex.extensions/branches/list", "skills.files/preview", "skills.preview", "codex.extensions/plugin/preview", "codex.extensions/plugin/skill-preview":
		return true
	}
	return false
}

// The settings UI has one preview dialog. Replacing its skill, source or plugin
// retires that dialog's read; branches and local catalog/file reads are separate.
func settingsExtensionReadKey(action application.Action) string {
	if extensionBranchesRead(action.Kind) {
		return "branches"
	}
	switch action.Kind {
	case "workspace.tools-options":
		return "workspace-tools"
	case "skills.catalog":
		return "catalog"
	case "skills.files/list":
		return "files"
	default:
		return "preview"
	}
}

func settingsExtensionReadToken(state *settingsExtensionRequestState, key string) string {
	if state == nil {
		return ""
	}
	switch key {
	case "workspace-tools":
		return state.workspaceToolsToken
	case "branches":
		return state.branchToken
	case "catalog":
		return state.catalogToken
	case "files":
		return state.filesToken
	default:
		return state.previewToken
	}
}

func extensionBranchesRead(kind string) bool {
	return kind == "skills.sources/branches/list" || kind == "codex.extensions/branches/list"
}

func (s *app) prepareSettingsExtensionRead(action *application.Action) {
	if !settingsExtensionRead(*action) {
		return
	}
	state := s.ensureSettingsExtensionRequests()
	key := settingsExtensionReadKey(*action)
	s.cancelSettingsExtensionRead(settingsExtensionReadToken(state, key))
	state.serial++
	token := fmt.Sprint(state.serial)
	payload := make(map[string]any, len(action.Payload)+1)
	for key, value := range action.Payload {
		payload[key] = value
	}
	payload["extensionRequestToken"] = token
	action.Payload = payload
	state.readActions[key] = *action
	state.readScopes[key] = s.settingsExtensionCurrentScope()
	switch key {
	case "workspace-tools":
		state.workspaceToolsToken, state.workspaceToolsLoading, state.workspaceToolsError = token, true, ""
		state.workspaceToolsScope = s.settingsExtensionCurrentScope()
	case "branches":
		state.branchToken, state.branchURL, state.branchForm = token, strings.TrimSpace(payloadString(payload, "repositoryUrl")), s.form.kind
		state.branchLoading, state.branchError = true, ""
		s.settingsUnit.state.settingsBranchChoices = nil
	case "catalog":
		state.catalogToken = token
		s.settingsUnit.state.settingsOutput = ""
	case "files":
		state.filesToken = token
		s.settingsUnit.state.settingsSkillFilesDirectory = payloadString(payload, "directoryName")
		s.settingsUnit.state.settingsSkillFiles, s.settingsUnit.state.settingsSkillFilesTruncated = nil, false
		s.settingsUnit.state.settingsOutput = ""
		if preview := state.readActions["preview"]; preview.Kind == "skills.files/preview" && payloadString(preview.Payload, "directoryName") != s.settingsUnit.state.settingsSkillFilesDirectory {
			s.invalidateSettingsExtensionRead("preview", "")
		}
	default:
		state.previewToken, state.previewLoading, state.previewError = token, true, ""
		closeMarkdownPreviewState(state.previewMarkdown)
		state.previewMarkdown = nil
		state.previewPluginSkills = nil
		state.previewPath = ""
		s.settingsUnit.state.settingsSkillPreviewText = ""
		s.settingsUnit.state.settingsSkillPreviewName = firstNonEmpty(payloadString(payload, "relativePath"), payloadString(payload, "skillName"), payloadString(payload, "name"), "Skill 预览")
		s.settingsUnit.state.settingsSkillPreviewOpen = true
	}
}

func (s *app) settingsExtensionActionRelevant(action application.Action) bool {
	state := s.settingsUnit.state.settingsExtensionRequests
	if state == nil || !settingsExtensionRead(action) {
		return false
	}
	key := settingsExtensionReadKey(action)
	token := payloadString(action.Payload, "extensionRequestToken")
	scope, exists := state.readScopes[key]
	if !exists || scope != s.settingsExtensionCurrentScope() || s.page != "settings" || token == "" || token != settingsExtensionReadToken(state, key) {
		return false
	}
	switch key {
	case "branches":
		return s.dialogOpen && state.branchForm == s.form.kind && state.branchURL == strings.TrimSpace(*s.input("form:repositoryUrl", ""))
	case "catalog", "workspace-tools":
		return true
	case "files":
		return s.settingsUnit.state.settingsSkillFilesDirectory == payloadString(action.Payload, "directoryName")
	default:
		return s.settingsUnit.state.settingsSkillPreviewOpen
	}
}

func (s *app) cancelSettingsExtensionRead(token string) {
	state := s.settingsUnit.state.settingsExtensionRequests
	if state == nil || state.jobs == nil || token == "" {
		return
	}
	for key, request := range state.jobs.current {
		if payloadString(request.action.Payload, "extensionRequestToken") == token {
			delete(state.jobs.current, key)
			request.cancel()
			return
		}
	}
}

func (s *app) invalidateSettingsExtensionRead(key, reason string) {
	state := s.settingsUnit.state.settingsExtensionRequests
	if state == nil {
		return
	}
	s.cancelSettingsExtensionRead(settingsExtensionReadToken(state, key))
	delete(state.readActions, key)
	delete(state.readScopes, key)
	switch key {
	case "workspace-tools":
		state.workspaceToolsToken, state.workspaceToolsLoading, state.workspaceToolsLoaded, state.workspaceToolsError = "", false, false, reason
		state.workspaceTools = nil
	case "branches":
		state.branchToken, state.branchLoading, state.branchError = "", false, reason
		s.settingsUnit.state.settingsBranchChoices = nil
	case "catalog":
		state.catalogToken = ""
	case "files":
		state.filesToken = ""
		s.settingsUnit.state.settingsSkillFiles, s.settingsUnit.state.settingsSkillFilesDirectory, s.settingsUnit.state.settingsSkillFilesTruncated = nil, "", false
	default:
		state.previewToken, state.previewLoading, state.previewError = "", false, reason
		closeMarkdownPreviewState(state.previewMarkdown)
		state.previewMarkdown = nil
		state.previewPluginSkills = nil
		state.previewPath = ""
		s.settingsUnit.state.settingsSkillPreviewText = ""
		if reason == "" {
			s.settingsUnit.state.settingsSkillPreviewOpen = false
		}
	}
}

func (s *app) observeSettingsExtensionRequests() {
	state := s.settingsUnit.state.settingsExtensionRequests
	if state == nil {
		return
	}
	for key, action := range state.readActions {
		if settingsExtensionReadToken(state, key) != "" && !s.settingsExtensionActionRelevant(action) {
			reason := ""
			previous := state.readScopes[key]
			if previous.page == s.page && previous.settingsPage == s.settingsUnit.state.settingsPage && (previous.home != s.snapshot.Home || previous.config != s.settingsUnit.state.configJSON) {
				reason = "应用或扩展配置已变化，请重新读取。"
			}
			s.invalidateSettingsExtensionRead(key, reason)
		}
	}
}

func (s *app) resetSettingsExtensionForm() {
	state := s.ensureSettingsExtensionRequests()
	s.invalidateSettingsExtensionRead("branches", "")
	state.branchToken, state.branchURL, state.branchForm = "", "", ""
	state.branchLoading, state.branchError = false, ""
	s.settingsUnit.state.settingsBranchChoices = nil
}

func (s *app) applySettingsExtensionFailure(action application.Action, err error) bool {
	if !settingsExtensionRead(action) {
		return false
	}
	if s.settingsExtensionActionRelevant(action) {
		state := s.ensureSettingsExtensionRequests()
		switch settingsExtensionReadKey(action) {
		case "workspace-tools":
			state.workspaceToolsLoading, state.workspaceToolsError = false, err.Error()
		case "branches":
			state.branchLoading, state.branchError = false, err.Error()
		case "catalog", "files":
			s.settingsUnit.state.settingsOutput = err.Error()
		default:
			state.previewLoading, state.previewError = false, err.Error()
		}
	}
	return true
}

func (s *app) applySettingsExtensionPreview(action application.Action, result application.ActionResult) {
	if !settingsExtensionRead(action) || !s.settingsExtensionActionRelevant(action) {
		return
	}
	state := s.ensureSettingsExtensionRequests()
	state.previewLoading, state.previewError = false, ""
	closeMarkdownPreviewState(state.previewMarkdown)
	state.previewMarkdown = nil
	state.previewPluginSkills = nil
	s.settingsUnit.state.settingsSkillPreviewName = firstNonEmpty(dataString(result.Data, "name"), payloadString(action.Payload, "relativePath"), payloadString(action.Payload, "skillName"), payloadString(action.Payload, "name"))
	s.settingsUnit.state.settingsSkillPreviewText = resultText(result)
	if data, ok := result.Data.(map[string]any); ok {
		if text, ok := data["text"].(string); ok {
			s.settingsUnit.state.settingsSkillPreviewText = text
		}
	}
	state.previewPath = dataString(result.Data, "path")
	if strings.HasPrefix(action.Kind, "codex.extensions/plugin/") {
		s.settingsUnit.state.settingsSkillPreviewText = pluginPreviewText(result.Data)
	}
	if action.Kind == "codex.extensions/plugin/preview" {
		seen := make(map[string]bool)
		add := func(name string) {
			if name != "" && !seen[name] {
				seen[name] = true
				state.previewPluginSkills = append(state.previewPluginSkills, name)
			}
		}
		for _, skill := range mapsFromData(dataValue(result.Data, "skills")) {
			add(mapString(skill, "name"))
		}
		for _, marketplace := range s.settingsUnit.state.settingsMarketplaces {
			if mapString(mapData(marketplace["source"]), "id") != payloadString(action.Payload, "sourceId") {
				continue
			}
			for _, plugin := range mapsFromData(mapData(marketplace["status"])["plugins"]) {
				if mapString(plugin, "name") == payloadString(action.Payload, "name") {
					for _, name := range stringArray(mustArray(plugin["bundledSkills"])) {
						add(name)
					}
				}
			}
		}
	}
	markdown := action.Kind != "skills.files/preview" || strings.HasSuffix(strings.ToLower(s.settingsUnit.state.settingsSkillPreviewName), ".md")
	if markdown {
		state.previewMarkdown = newReadonlyMarkdownState("extension-preview:"+state.previewToken, s.settingsUnit.state.settingsSkillPreviewText, nil)
		switch action.Kind {
		case "skills.preview", "codex.extensions/plugin/skill-preview":
			if state.previewPath != "" {
				bindSkillMarkdownPreviewImages(state.previewMarkdown, state.previewPath, filepath.Dir(state.previewPath), s.settingsUnit.previewResourceResolver())
			}
		case "skills.files/preview":
			if s.settingsUnit.state.settingsSkillRoot != "" {
				root := filepath.Join(s.settingsUnit.state.settingsSkillRoot, payloadString(action.Payload, "directoryName"))
				bindSkillMarkdownPreviewImages(state.previewMarkdown, filepath.Join(root, filepath.FromSlash(payloadString(action.Payload, "relativePath"))), root, s.settingsUnit.previewResourceResolver())
			}
		}
	}
}

func (s *app) settingsExtensionPreviewRetryAction() (application.Action, bool) {
	state := s.settingsUnit.state.settingsExtensionRequests
	if state == nil || state.previewLoading || state.previewError == "" {
		return application.Action{}, false
	}
	action := state.readActions["preview"]
	return action, s.settingsExtensionActionRelevant(action)
}

func (s *app) navigateSettingsPluginPreview(skillName string) {
	state := s.settingsUnit.state.settingsExtensionRequests
	if state == nil || state.previewLoading || state.previewError != "" {
		return
	}
	current := state.readActions["preview"]
	if !s.settingsExtensionActionRelevant(current) {
		return
	}
	sourceID := payloadString(current.Payload, "sourceId")
	if current.Kind == "codex.extensions/plugin/skill-preview" && skillName == "" {
		s.act("codex.extensions/plugin/preview", "", "", "", "", "", map[string]any{"sourceId": sourceID, "name": payloadString(current.Payload, "pluginName")})
		return
	}
	if current.Kind != "codex.extensions/plugin/preview" {
		return
	}
	for _, discovered := range state.previewPluginSkills {
		if discovered == skillName {
			s.act("codex.extensions/plugin/skill-preview", "", "", "", "", "", map[string]any{"sourceId": sourceID, "pluginName": payloadString(current.Payload, "name"), "skillName": skillName})
			return
		}
	}
}
