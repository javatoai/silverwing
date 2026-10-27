package desktop

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"sync/atomic"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"

	"github.com/egoist/mygo/ui"
)

type taskCreationModuleDraft struct {
	ID, Name                       string
	LastName                       string
	Strategy                       domain.WorkspaceStrategy
	BaseRemote, BaseRef            string
	repositoryPicker               taskRepositoryPickerState
	TargetBranch                   string
	TargetEdited                   bool
	LastBaseRemote, LastBaseRef    string
	LastTargetBranch               string
	TagEnabled                     bool
	TagMode                        domain.TagBuildMode
	TagTargetRef, TagMessagePrefix string
	Source                         domain.TaskModuleSource
}

type taskCreationPreview struct {
	signature        string
	payload          map[string]any
	workspaceToolIDs []string
	conflicts        []workspace.BranchReuseConflict
	sharedAck        bool
}

type taskCreationWorkspaceToolOption struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
	Available   bool   `json:"available"`
	Reason      string `json:"reason"`
}

type taskCreationState struct {
	token             string
	title             string
	config            domain.AppConfig
	draft             domain.RequirementDraftState
	notes             string
	groupID           string
	groupQuery        string
	serviceSearch     string
	serviceListScroll map[string]*ui.ScrollState
	groupDrafts       map[string]*taskCreationGroupDraft
	activeServiceID   string
	requirements      *taskCreationRequirementsState
	services          map[string]bool
	modules           map[string]map[string]bool
	moduleData        map[string]map[string]*taskCreationModuleDraft
	moduleOrder       map[string][]string
	moduleExpanded    map[string]map[string]bool
	moduleFocus       string
	moduleFieldFocus  string
	taskFieldFocus    string
	validationTarget  *taskCreationValidationTarget

	phase                     string
	progressGeneration        uint64
	creationProgress          *taskCreationProgressState
	preview                   *taskCreationPreview
	readRequest               *taskCreationReadRequest
	preflight                 *taskCreationPreflightRequest
	discardConfirmation       bool
	pendingPreflight          bool
	inlineError               string
	aiError                   string
	aiPending                 bool
	directoryCheck            taskCreationDirectoryState
	aiNaming                  *taskCreationAINamingState
	aiEligible                bool
	lastName                  string
	lastBranch                string
	lastLink                  string
	configError               string
	rightTab                  string
	templates                 []domain.AgentTaskTemplate
	templatesLoading          bool
	templatesError            string
	selectedTemplate          string
	pendingTemplate           string
	workspaceTools            []taskCreationWorkspaceToolOption
	toolsLoading              bool
	toolsError                string
	selectedToolIDs           map[string]bool
	toolsLoaded               bool
	loadStep                  string
	agentPreview              *workspace.AgentDocumentPreview
	agentPreviewFile          string
	agentPreviewTab           string
	agentPreviewSig           string
	agentCodePreview          nativeCodePreviewState
	agentPreviewBusy          bool
	agentPreviewErr           string
	agentPreviewCopy          string
	materialsPreview          domain.RequirementMaterialsDirectory
	materialsPreviewInputSig  string
	materialsPreviewPending   bool
	materialsPreviewErr       string
	previewScheduleSig        string
	previewScheduleGeneration uint64
	previewScheduleTimer      *time.Timer
}

type taskCreationValidationTarget struct {
	serviceID string
	moduleID  string
	field     string
}

type taskCreationGroupDraft struct {
	services        map[string]bool
	modules         map[string]map[string]bool
	moduleData      map[string]map[string]*taskCreationModuleDraft
	moduleOrder     map[string][]string
	moduleExpanded  map[string]map[string]bool
	activeServiceID string
	serviceSearch   string
}

var taskCreationSequence atomic.Uint64

func (s *app) initTaskCreation(title, requirementTitle, requirementURL string) {
	s.cancelTaskCreationReads(s.taskCreation, false)
	s.closeTaskCreationRequirements()
	s.closeTaskCreationAINaming()
	cancelTaskRepositoryChoices(s.taskCreation)
	config := domain.DefaultAppConfig()
	configErr := json.Unmarshal([]byte(s.snapshot.ConfigJSON), &config)
	if configErr != nil {
		config = domain.DefaultAppConfig()
	}
	var selectedTitle *string
	if strings.TrimSpace(requirementTitle) != "" {
		selectedTitle = &requirementTitle
	}
	draft := domain.RequirementDraftState{TaskName: requirementTitle, Branch: config.DefaultBranchPrefix}
	draft = draft.ChangeRequirement(requirementURL, config.DefaultBranchPrefix, selectedTitle)
	state := &taskCreationState{
		token: fmt.Sprintf("%d", taskCreationSequence.Add(1)), title: title, config: config, draft: draft,
		services: make(map[string]bool), modules: make(map[string]map[string]bool), moduleData: make(map[string]map[string]*taskCreationModuleDraft), moduleOrder: make(map[string][]string),
		moduleExpanded: make(map[string]map[string]bool), groupDrafts: make(map[string]*taskCreationGroupDraft), phase: "editing", aiEligible: strings.TrimSpace(requirementURL) != "", lastName: draft.TaskName,
		selectedToolIDs: make(map[string]bool), agentPreviewTab: "preview",
		materialsPreview: domain.RequirementMaterialsDirectory{Status: domain.MaterialsNotRequested},
		lastBranch:       draft.Branch, lastLink: draft.RequirementLink,
	}
	if configErr != nil {
		state.configError = "无法读取当前任务配置：" + configErr.Error()
	}
	if len(config.Groups) > 0 {
		state.groupID = config.Groups[0].ID
		state.groupQuery = firstNonEmpty(config.Groups[0].Name, config.Groups[0].ID)
	} else if len(s.snapshot.Groups) > 0 {
		state.groupID = s.snapshot.Groups[0].ID
		state.groupQuery = firstNonEmpty(s.snapshot.Groups[0].Name, s.snapshot.Groups[0].ID)
		state.configError = firstNonEmpty(state.configError, "当前配置没有可用项目组。")
	}
	s.taskCreation = state
	s.syncTaskCreationLegacySelection()
	s.loadTaskCreationWorkspaceTools()
}

func (s *app) loadTaskCreationTemplates() {
	state := s.taskCreation
	if state == nil {
		return
	}
	if s.settingsTemplatesLoaded {
		state.templates = taskTemplatesFromMaps(s.settingsTemplates)
		s.loadTaskCreationWorkspaceTools()
		return
	}
	if state.templatesLoading {
		return
	}
	state.templatesLoading = true
	state.loadStep = "templates"
	s.startTaskCreationRead("instructions.templates.list", map[string]any{"taskCreateToken": state.token})
}

func taskTemplatesFromMaps(items []map[string]any) []domain.AgentTaskTemplate {
	templates := make([]domain.AgentTaskTemplate, 0, len(items))
	for _, item := range items {
		id, name := mapString(item, "id"), mapString(item, "name")
		if id == "" || name == "" {
			continue
		}
		templates = append(templates, domain.AgentTaskTemplate{ID: id, Name: name, Content: mapString(item, "content"), UpdatedAt: mapString(item, "updatedAt")})
	}
	return templates
}

func normalizeTaskCreationWorkspaceTools(items []taskCreationWorkspaceToolOption, configuredIDs []string) []taskCreationWorkspaceToolOption {
	result := make([]taskCreationWorkspaceToolOption, 0, len(items)+len(configuredIDs))
	seen := make(map[string]bool, len(items)+len(configuredIDs))
	for _, option := range items {
		option.ID = strings.TrimSpace(option.ID)
		if option.ID == "" || seen[option.ID] {
			continue
		}
		if option.DisplayName == "" {
			option.DisplayName = option.ID
		}
		seen[option.ID] = true
		result = append(result, option)
	}
	for _, id := range configuredIDs {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		result = append(result, taskCreationWorkspaceToolOption{ID: id, DisplayName: workspaceToolDisplayName(id), Reason: "当前版本未返回此工具的注册和可用状态"})
	}
	return result
}

func workspaceToolDisplayName(id string) string {
	switch id {
	case "codex":
		return "Codex"
	case "cursor":
		return "Cursor"
	default:
		return id
	}
}

func (state *taskCreationState) enabledWorkspaceTools() []taskCreationWorkspaceToolOption {
	tools := make([]taskCreationWorkspaceToolOption, 0, len(state.workspaceTools))
	for _, option := range state.workspaceTools {
		if containsString(state.config.DefaultWorkspaceToolIDs, option.ID) {
			tools = append(tools, option)
		}
	}
	return tools
}

func firstTaskAgentPreviewFile(files []workspace.AgentDocumentPreviewFile) string {
	for _, file := range files {
		name := path.Base(strings.ReplaceAll(file.RelativePath, `\`, "/"))
		if strings.EqualFold(name, "TASK-RULES.md") {
			return file.RelativePath
		}
	}
	return ""
}

func (s *app) loadTaskCreationWorkspaceTools() {
	state := s.taskCreation
	if state == nil || state.toolsLoading || state.toolsLoaded {
		return
	}
	state.toolsLoading = true
	state.loadStep = "workspace-tools"
	s.startTaskCreationRead("workspace.tools-options", map[string]any{"taskCreateToken": state.token})
}

func (s *app) finishTaskCreationInitialLoads() {
	state := s.taskCreation
	if state == nil || state.loadStep != "" || s.busy {
		return
	}
	if state.aiEligible && state.config.AIRequirementNamingEnabled {
		s.requestTaskCreationAINaming(false)
	}
}

func (s *app) continueTaskCreationInitialLoads() {
	state := s.taskCreation
	if state == nil {
		return
	}
	state.loadStep = ""
	s.loadTaskCreationWorkspaceTools()
	if state.loadStep == "" {
		s.finishTaskCreationInitialLoads()
	}
}

func (s *app) taskCreationGroup() *domain.GroupConfig {
	state := s.taskCreation
	if state == nil {
		return nil
	}
	for i := range state.config.Groups {
		if state.config.Groups[i].ID == state.groupID {
			return &state.config.Groups[i]
		}
	}
	return nil
}

func taskCreationModuleName(module domain.ServiceModuleConfig) string {
	if name := strings.TrimSpace(module.Name); name != "" {
		return name
	}
	if id := strings.TrimSpace(module.ID); id != "" {
		return id
	}
	return "default"
}

func taskCreationOptionalString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func taskCreationTargetBranch(feature string, service domain.GroupServiceConfig, module domain.ServiceModuleConfig) string {
	feature = strings.TrimSpace(feature)
	if feature == "" {
		return ""
	}
	return taskCreationTargetBranchForCount(feature, taskCreationModuleName(module), module.ID, len(service.Modules))
}

func taskCreationTargetBranchForCount(feature, moduleName, moduleID string, moduleCount int) string {
	feature = strings.TrimSpace(feature)
	if feature == "" {
		return ""
	}
	if moduleCount <= 1 {
		return feature
	}
	name := strings.TrimSpace(moduleName)
	if name == "" {
		name = strings.TrimSpace(moduleID)
	}
	if name == "" {
		name = "default"
	}
	return feature + "-" + name
}

func duplicateTaskCreationCloneTargets(order []string, drafts map[string]*taskCreationModuleDraft) []string {
	counts := make(map[string]int)
	labels := make(map[string]string)
	for _, id := range order {
		draft := drafts[id]
		if draft == nil || draft.Strategy != domain.WorkspaceIndependentClone {
			continue
		}
		target := strings.TrimSpace(draft.TargetBranch)
		if target == "" {
			continue
		}
		key := strings.ToLower(target)
		counts[key]++
		labels[key] = target
	}
	duplicates := make([]string, 0)
	seen := make(map[string]bool)
	for _, id := range order {
		draft := drafts[id]
		if draft == nil || draft.Strategy != domain.WorkspaceIndependentClone {
			continue
		}
		target := strings.TrimSpace(draft.TargetBranch)
		key := strings.ToLower(target)
		if target != "" && counts[key] > 1 && !seen[key] {
			duplicates = append(duplicates, labels[key])
			seen[key] = true
		}
	}
	return duplicates
}

func newTaskCreationModuleDraft(service domain.GroupServiceConfig, module domain.ServiceModuleConfig, feature string) *taskCreationModuleDraft {
	strategy := module.Strategy
	if strategy == "" {
		strategy = domain.WorkspaceStandardWorktree
	}
	tagMode := module.TagMode
	if tagMode == "" {
		tagMode = domain.TagBuildMergeToTargetBranch
	}
	messagePrefix := module.TagMessagePrefix
	if messagePrefix == "" {
		messagePrefix = "Tag"
	}
	draft := &taskCreationModuleDraft{
		ID: module.ID, Name: module.Name, Strategy: strategy,
		BaseRemote: service.EffectiveMasterRemote(module), BaseRef: service.EffectiveMasterBranch(module),
		TargetBranch: taskCreationTargetBranch(feature, service, module),
		TagEnabled:   module.TagEnabled, TagMode: tagMode, TagTargetRef: taskCreationOptionalString(service.EffectiveTagTargetRef(module)),
		TagMessagePrefix: messagePrefix, Source: domain.TaskModuleConfigured,
	}
	draft.LastName = draft.Name
	draft.LastBaseRemote, draft.LastBaseRef, draft.LastTargetBranch = draft.BaseRemote, draft.BaseRef, draft.TargetBranch
	return draft
}

func (s *app) taskCreationService(serviceID string) *domain.GroupServiceConfig {
	group := s.taskCreationGroup()
	if group == nil {
		return nil
	}
	for i := range group.Services {
		if group.Services[i].ID == serviceID {
			return &group.Services[i]
		}
	}
	return nil
}

func (s *app) ensureTaskCreationService(serviceID string) {
	state := s.taskCreation
	if state == nil || state.services[serviceID] {
		return
	}
	service := s.taskCreationService(serviceID)
	if service == nil {
		return
	}
	state.services[serviceID] = true
	if _, initialized := state.moduleData[serviceID]; initialized {
		s.retargetTaskCreationServiceModules(serviceID, state.draft.Branch)
		s.syncTaskCreationLegacySelection()
		return
	}
	state.modules[serviceID] = make(map[string]bool, len(service.Modules))
	state.moduleData[serviceID] = make(map[string]*taskCreationModuleDraft, len(service.Modules))
	state.moduleOrder[serviceID] = make([]string, 0, len(service.Modules))
	if state.moduleExpanded == nil {
		state.moduleExpanded = make(map[string]map[string]bool)
	}
	state.moduleExpanded[serviceID] = make(map[string]bool, len(service.Modules))
	for _, module := range service.Modules {
		state.modules[serviceID][module.ID] = true
		state.moduleData[serviceID][module.ID] = newTaskCreationModuleDraft(*service, module, state.draft.Branch)
		state.moduleOrder[serviceID] = append(state.moduleOrder[serviceID], module.ID)
		state.moduleExpanded[serviceID][module.ID] = true
	}
	s.syncTaskCreationLegacySelection()
}

func (s *app) addTaskCreationModule(serviceID string, strategy domain.WorkspaceStrategy) {
	state := s.taskCreation
	if state == nil {
		return
	}
	s.ensureTaskCreationService(serviceID)
	service := s.taskCreationService(serviceID)
	if service == nil {
		return
	}
	namePrefix := "module"
	if strategy == domain.WorkspaceIndependentClone {
		namePrefix = "clone"
	}
	name := fmt.Sprintf("%s-%d", namePrefix, len(state.moduleData[serviceID])+1)
	id := newDesktopEditorID(namePrefix)
	module := domain.ServiceModuleConfig{ID: id, Name: name, Strategy: strategy}
	draft := newTaskCreationModuleDraft(*service, module, state.draft.Branch)
	draft.Source = domain.TaskModuleTemporary
	draft.TagEnabled = false
	draft.TagTargetRef = ""
	state.moduleData[serviceID][id] = draft
	state.modules[serviceID][id] = true
	state.moduleOrder[serviceID] = append(state.moduleOrder[serviceID], id)
	s.setTaskCreationModuleExpanded(serviceID, id, true)
	state.moduleFocus = serviceID + ":" + id
	state.activeServiceID = serviceID
	s.retargetTaskCreationServiceModules(serviceID, state.draft.Branch)
	s.invalidateTaskCreationPreview("模块列表已变化，请重新预检。")
	s.syncTaskCreationLegacySelection()
}

func (s *app) removeTaskCreationModule(serviceID, moduleID string) {
	state := s.taskCreation
	if state == nil || len(state.moduleData[serviceID]) <= 1 {
		return
	}
	if draft := state.moduleData[serviceID][moduleID]; draft != nil {
		cancelTaskRepositoryPickerRequests(&draft.repositoryPicker)
	}
	if state.moduleFocus == serviceID+":"+moduleID {
		state.moduleFocus = ""
	}
	if strings.HasPrefix(state.moduleFieldFocus, serviceID+":"+moduleID+":") {
		state.moduleFieldFocus = ""
	}
	delete(state.moduleData[serviceID], moduleID)
	delete(state.modules[serviceID], moduleID)
	delete(state.moduleExpanded[serviceID], moduleID)
	order := state.moduleOrder[serviceID][:0]
	for _, id := range state.moduleOrder[serviceID] {
		if id != moduleID {
			order = append(order, id)
		}
	}
	state.moduleOrder[serviceID] = order
	if state.activeServiceID == serviceID && len(order) == 0 {
		state.activeServiceID = ""
	}
	s.retargetTaskCreationServiceModules(serviceID, state.draft.Branch)
	s.invalidateTaskCreationPreview("模块列表已变化，请重新预检。")
	s.syncTaskCreationLegacySelection()
}

func (s *app) setTaskCreationServiceSelected(serviceID string, selected bool) {
	state := s.taskCreation
	if state == nil {
		return
	}
	if state.services[serviceID] == selected {
		return
	}
	if !selected {
		for _, draft := range state.moduleData[serviceID] {
			if draft != nil {
				cancelTaskRepositoryPickerRequests(&draft.repositoryPicker)
			}
		}
		delete(state.services, serviceID)
		if state.activeServiceID == serviceID {
			state.activeServiceID = ""
			if group := s.taskCreationGroup(); group != nil {
				for _, candidate := range group.Services {
					if candidate.Enabled && state.services[candidate.ID] {
						state.activeServiceID = candidate.ID
						break
					}
				}
			}
		}
	} else {
		s.ensureTaskCreationService(serviceID)
	}
	state.validationTarget = nil
	s.invalidateTaskCreationPreview("任务服务选择已变化，请重新预检。")
	s.syncTaskCreationLegacySelection()
}

func (s *app) toggleTaskCreationService(serviceID string) {
	state := s.taskCreation
	if state == nil {
		return
	}
	s.setTaskCreationServiceSelected(serviceID, !state.services[serviceID])
}

func (s *app) focusTaskCreationService(serviceID string) {
	state := s.taskCreation
	if state == nil {
		return
	}
	if !state.services[serviceID] {
		s.setTaskCreationServiceSelected(serviceID, true)
		if !state.services[serviceID] {
			return
		}
		state.activeServiceID = serviceID
		for _, moduleID := range state.moduleOrder[serviceID] {
			s.taskCreationModuleExpanded(serviceID, moduleID)
		}
		return
	}
	if state.activeServiceID == serviceID {
		state.activeServiceID = ""
		return
	}
	state.activeServiceID = serviceID
	for _, moduleID := range state.moduleOrder[serviceID] {
		s.taskCreationModuleExpanded(serviceID, moduleID)
	}
}

func (s *app) setTaskCreationGroup(groupID string) {
	state := s.taskCreation
	if state == nil || state.groupID == groupID {
		return
	}
	cancelTaskRepositoryChoices(state)
	for _, modules := range state.moduleData {
		for _, draft := range modules {
			if draft == nil {
				continue
			}
			draft.repositoryPicker.branchOpen = false
			draft.repositoryPicker.remoteOpen = false
		}
	}
	if state.groupDrafts == nil {
		state.groupDrafts = make(map[string]*taskCreationGroupDraft)
	}
	state.groupDrafts[state.groupID] = &taskCreationGroupDraft{
		services: state.services, modules: state.modules, moduleData: state.moduleData,
		moduleOrder: state.moduleOrder, moduleExpanded: state.moduleExpanded,
		activeServiceID: state.activeServiceID, serviceSearch: state.serviceSearch,
	}
	state.groupID = groupID
	for _, group := range state.config.Groups {
		if group.ID == groupID {
			state.groupQuery = firstNonEmpty(group.Name, group.ID)
			break
		}
	}
	if draft := state.groupDrafts[groupID]; draft != nil {
		state.services = draft.services
		state.modules = draft.modules
		state.moduleData = draft.moduleData
		state.moduleOrder = draft.moduleOrder
		state.moduleExpanded = draft.moduleExpanded
		state.activeServiceID = draft.activeServiceID
		state.serviceSearch = draft.serviceSearch
	} else {
		state.serviceSearch = ""
		state.services = make(map[string]bool)
		state.modules = make(map[string]map[string]bool)
		state.moduleData = make(map[string]map[string]*taskCreationModuleDraft)
		state.moduleOrder = make(map[string][]string)
		state.moduleExpanded = make(map[string]map[string]bool)
		state.activeServiceID = ""
	}
	s.retargetUntouchedTaskCreationModules(state.draft.Branch)
	state.moduleFocus = ""
	state.moduleFieldFocus = ""
	state.taskFieldFocus = ""
	state.validationTarget = nil
	s.invalidateTaskCreationPreview("项目组已变化，请重新选择服务并预检。")
	s.syncTaskCreationLegacySelection()
}

func (s *app) syncTaskCreationLegacySelection() {
	state := s.taskCreation
	if state == nil {
		return
	}
	s.taskGroupID = state.groupID
	s.taskServices = make(map[string]bool, len(state.services))
	s.taskModules = make(map[string]map[string]bool, len(state.modules))
	for serviceID, selected := range state.services {
		s.taskServices[serviceID] = selected
	}
	for serviceID, modules := range state.modules {
		copyModules := make(map[string]bool, len(modules))
		for moduleID, selected := range modules {
			copyModules[moduleID] = selected
		}
		s.taskModules[serviceID] = copyModules
	}
}

func (s *app) retargetUntouchedTaskCreationModules(feature string) {
	state := s.taskCreation
	if state == nil {
		return
	}
	for serviceID := range state.moduleData {
		s.retargetTaskCreationServiceModules(serviceID, feature)
	}
}

func (s *app) retargetTaskCreationServiceModules(serviceID, feature string) {
	state := s.taskCreation
	if state == nil {
		return
	}
	modules := state.moduleData[serviceID]
	for _, module := range modules {
		if module == nil || module.TargetEdited {
			continue
		}
		module.TargetBranch = taskCreationTargetBranchForCount(feature, module.Name, module.ID, len(modules))
		module.LastTargetBranch = module.TargetBranch
	}
}

func (s *app) syncTaskCreationModuleDraft(serviceID string, draft *taskCreationModuleDraft) {
	if draft == nil {
		return
	}
	nameChanged := draft.Name != draft.LastName
	baseRefChanged := draft.BaseRef != draft.LastBaseRef
	baseRemoteChanged := draft.BaseRemote != draft.LastBaseRemote
	targetBranchChanged := draft.TargetBranch != draft.LastTargetBranch
	if baseRemoteChanged {
		remote := strings.TrimSpace(draft.BaseRemote)
		if remote != "" {
			if ref, err := domain.ParseRemoteBranchRef(draft.BaseRef); err == nil {
				draft.BaseRef = remote + "/" + ref.Branch
			} else if _, after, ok := strings.Cut(strings.TrimSpace(draft.BaseRef), "/"); ok && after != "" {
				draft.BaseRef = remote + "/" + after
			}
		}
	} else if baseRefChanged {
		if ref, err := domain.ParseRemoteBranchRef(draft.BaseRef); err == nil {
			draft.BaseRemote = ref.Remote
		}
	}
	if targetBranchChanged {
		draft.TargetEdited = true
	}
	draft.LastName = draft.Name
	if nameChanged {
		s.retargetTaskCreationServiceModules(serviceID, s.taskCreation.draft.Branch)
	}
	draft.LastBaseRemote, draft.LastBaseRef, draft.LastTargetBranch = draft.BaseRemote, draft.BaseRef, draft.TargetBranch
	if baseRefChanged || baseRemoteChanged || targetBranchChanged || nameChanged {
		s.clearTaskCreationFieldError(serviceID, draft.ID, "")
		s.invalidateTaskCreationPreview("模块基础分支或目标分支已变化，请重新预检。")
	}
}

func (s *app) syncTaskCreationDraft() {
	state := s.taskCreation
	if state == nil {
		return
	}
	if state.draft.TaskName != state.lastName {
		state.draft = state.draft.EditName(state.draft.TaskName)
		state.lastName = state.draft.TaskName
		if state.validationTarget != nil && state.validationTarget.field == "taskName" {
			state.validationTarget = nil
			state.inlineError = ""
		}
		if state.taskFieldFocus == "taskName" {
			state.taskFieldFocus = ""
		}
		s.invalidateTaskCreationPreview("任务名称已变化，请重新预检。")
	}
	if state.draft.Branch != state.lastBranch {
		state.draft = state.draft.EditBranch(state.draft.Branch)
		state.lastBranch = state.draft.Branch
		if state.validationTarget != nil && state.validationTarget.field == "taskBranch" {
			state.validationTarget = nil
			state.inlineError = ""
		}
		if state.taskFieldFocus == "taskBranch" {
			state.taskFieldFocus = ""
		}
		s.retargetUntouchedTaskCreationModules(state.draft.Branch)
		s.invalidateTaskCreationPreview("任务分支已变化，请重新预检。")
	}
	if state.draft.RequirementLink != state.lastLink {
		oldBranch := state.draft.Branch
		s.cancelTaskCreationAINaming()
		state.draft = state.draft.ChangeRequirement(state.draft.RequirementLink, state.config.DefaultBranchPrefix, nil)
		state.lastLink = state.draft.RequirementLink
		state.aiEligible = false // 手工输入链接不得触发对外 AI 命名请求。
		state.aiError = ""
		if state.validationTarget != nil && state.validationTarget.field == "requirement" {
			state.validationTarget = nil
			state.inlineError = ""
		}
		if state.draft.Branch != oldBranch {
			s.retargetUntouchedTaskCreationModules(state.draft.Branch)
		}
		state.lastBranch = state.draft.Branch
		s.invalidateTaskCreationPreview("需求链接已变化，请重新预检。")
	}
	if state.preview != nil && state.preview.signature != s.taskCreationSignature() {
		state.preview = nil
		state.phase = "editing"
		state.inlineError = "任务信息在预检后发生变化，请重新预检。"
	}
}

func (s *app) invalidateTaskCreationPreview(_ string) {
	state := s.taskCreation
	if state == nil {
		return
	}
	if state.preview != nil || state.phase == "confirm" || state.phase == "preview" {
		state.preview = nil
		if state.phase == "confirm" || state.phase == "preview" {
			state.phase = "editing"
		}
	}
	if !state.pendingPreflight {
		state.inlineError = ""
	}
}

func (s *app) taskCreationPayload() (map[string]any, error) {
	state := s.taskCreation
	if state == nil {
		return nil, fmt.Errorf("任务创建表单已关闭")
	}
	if state.requirements != nil && state.requirements.inputIsQuery {
		return nil, fmt.Errorf("请选择匹配需求，或填写完整需求链接。")
	}
	group := s.taskCreationGroup()
	if group == nil {
		return nil, fmt.Errorf("请选择有效项目组")
	}
	serviceIDs := make([]string, 0, len(state.services))
	selections := make([]workspace.TaskServiceSelection, 0, len(state.services))
	for _, service := range group.Services {
		if !service.Enabled || !state.services[service.ID] {
			continue
		}
		modules := make([]workspace.TaskModuleSelection, 0, len(state.modules[service.ID]))
		for _, moduleID := range state.moduleOrder[service.ID] {
			draft := state.moduleData[service.ID][moduleID]
			if draft == nil {
				continue
			}
			modules = append(modules, workspace.TaskModuleSelection{
				ID: draft.ID, Name: draft.Name, Strategy: draft.Strategy,
				BaseRef: strings.TrimSpace(draft.BaseRef), BaseRemote: strings.TrimSpace(draft.BaseRemote),
				TargetBranch: strings.TrimSpace(draft.TargetBranch), Source: draft.Source,
				TagEnabled: draft.TagEnabled, TagMode: draft.TagMode, TagTargetRef: strings.TrimSpace(draft.TagTargetRef),
				TagMessagePrefix: draft.TagMessagePrefix,
			})
		}
		if len(modules) > 0 {
			serviceIDs = append(serviceIDs, service.ID)
			selections = append(selections, workspace.TaskServiceSelection{ServiceID: service.ID, Modules: modules})
		}
	}
	payload := map[string]any{
		"folderName": state.draft.TaskName, "featureBranch": state.draft.Branch,
		"groupId": state.groupID, "serviceIds": serviceIDs, "serviceSelections": selections,
		"requirementLink": state.draft.RequirementLink, "taskNotes": state.notes,
	}
	return payload, nil
}

func (s *app) taskCreationSignature() string {
	payload, err := s.taskCreationPayload()
	if err != nil {
		return ""
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func (s *app) taskCreationAgentPreviewSignature() string {
	state := s.taskCreation
	payload, err := s.taskCreationPayload()
	if state == nil || err != nil {
		return ""
	}
	payload["requirementMaterials"] = state.materialsPreview
	encoded, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func (s *app) taskCreationMaterialsPreviewSignature() string {
	state := s.taskCreation
	if state == nil {
		return ""
	}
	data, err := json.Marshal(struct {
		RequirementLink string                       `json:"requirementLink"`
		FolderName      string                       `json:"folderName"`
		MaterialsRoot   *string                      `json:"materialsRoot"`
		Subdirectory    *string                      `json:"subdirectory"`
		Projects        []domain.MeegleProjectConfig `json:"meegleProjects"`
	}{state.draft.RequirementLink, state.draft.TaskName, state.config.RequirementMaterialsRoot, state.config.RequirementMaterialsSubdirectory, state.config.MeegleProjects})
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func (s *app) stopTaskCreationPreviewDebounce() {
	state := s.taskCreation
	if state == nil {
		return
	}
	state.previewScheduleGeneration++
	state.previewScheduleSig = ""
	if state.previewScheduleTimer != nil {
		state.previewScheduleTimer.Stop()
		state.previewScheduleTimer = nil
	}
}

func (s *app) scheduleTaskCreationPreviewRefresh() {
	// The creation form no longer previews task documents or material contents.
}

func (s *app) taskCreationValidationError() string {
	message, _ := s.taskCreationValidationFailure()
	return message
}

func (s *app) taskCreationValidationFailure() (string, *taskCreationValidationTarget) {
	state := s.taskCreation
	if state == nil {
		return "任务创建表单已关闭。", nil
	}
	if state.configError != "" {
		return state.configError, nil
	}
	if err := domain.DirectoryNameValidationError(state.draft.TaskName); err != nil {
		return err.Error(), &taskCreationValidationTarget{field: "taskName"}
	}
	if state.directoryCheck.signature == s.taskCreationDirectorySignature() && state.directoryCheck.message != "" {
		return state.directoryCheck.message, &taskCreationValidationTarget{field: "taskName"}
	}
	if taskCreationHasSelectedServices(state) && strings.TrimSpace(state.draft.Branch) == "" {
		return "请填写任务分支。", &taskCreationValidationTarget{field: "taskBranch"}
	}
	if taskCreationHasSelectedServices(state) && domain.ContainsUnresolvedBranchPlaceholder(state.draft.Branch) {
		return "任务分支仍包含未解析的 {num} 或 {ai}，请修改需求编号/链接或手工调整分支。", &taskCreationValidationTarget{field: "taskBranch"}
	}
	group := s.taskCreationGroup()
	if group == nil {
		return "请选择项目组。", &taskCreationValidationTarget{field: "service"}
	}
	for id, selected := range state.services {
		service := s.taskCreationService(id)
		if selected && (service == nil || !service.Enabled) {
			return "已选服务已不可用，请重新选择。", &taskCreationValidationTarget{field: "service"}
		}
	}
	for _, service := range group.Services {
		if !service.Enabled || !state.services[service.ID] {
			continue
		}
		selectedModules := 0
		for _, moduleID := range state.moduleOrder[service.ID] {
			selectedModules++
			draft := state.moduleData[service.ID][moduleID]
			if draft == nil {
				return "模块配置无效，请重新选择服务。", &taskCreationValidationTarget{serviceID: service.ID, moduleID: moduleID, field: "module"}
			}
			if strings.TrimSpace(draft.BaseRef) == "" {
				return "模块主分支不能为空：" + firstNonEmpty(draft.Name, draft.ID), &taskCreationValidationTarget{serviceID: service.ID, moduleID: moduleID, field: "baseRef"}
			}
			ref, err := domain.ParseRemoteBranchRef(draft.BaseRef)
			if err != nil {
				return err.Error(), &taskCreationValidationTarget{serviceID: service.ID, moduleID: moduleID, field: "baseRef"}
			}
			if strings.TrimSpace(draft.BaseRemote) == "" || ref.Remote != strings.TrimSpace(draft.BaseRemote) {
				return "模块主分支远程与引用不一致：" + draft.BaseRef, &taskCreationValidationTarget{serviceID: service.ID, moduleID: moduleID, field: "baseRef"}
			}
			if draft.Strategy != domain.WorkspaceStandardWorktree && draft.Strategy != domain.WorkspaceIndependentClone {
				return "请选择有效的模块工作区策略。", &taskCreationValidationTarget{serviceID: service.ID, moduleID: moduleID, field: "strategy"}
			}
			if draft.Strategy == domain.WorkspaceStandardWorktree && strings.TrimSpace(draft.TargetBranch) == "" {
				return "Worktree 模块目标分支不能为空：" + firstNonEmpty(draft.Name, draft.ID), &taskCreationValidationTarget{serviceID: service.ID, moduleID: moduleID, field: "targetBranch"}
			}
		}
		if selectedModules == 0 {
			return "已选服务至少需要一个模块：" + service.DisplayName, &taskCreationValidationTarget{serviceID: service.ID, field: "module"}
		}
	}
	return "", nil
}

func taskCreationHasSelectedServices(state *taskCreationState) bool {
	if state != nil {
		for _, selected := range state.services {
			if selected {
				return true
			}
		}
	}
	return false
}

func (s *app) focusTaskCreationValidationTarget(target *taskCreationValidationTarget) {
	state := s.taskCreation
	if state == nil || target == nil {
		return
	}
	state.validationTarget = target
	if target.serviceID == "" {
		state.taskFieldFocus = target.field
		return
	}
	s.ensureTaskCreationService(target.serviceID)
	if service := s.taskCreationService(target.serviceID); service != nil && strings.TrimSpace(state.serviceSearch) != "" {
		repositoryName, repositoryPath := s.repositoryDetails(service.RepositoryID)
		if !taskCreationServiceMatches(*service, repositoryName+" "+repositoryPath, state.serviceSearch) {
			state.serviceSearch = ""
		}
	}
	state.activeServiceID = target.serviceID
	if target.moduleID != "" {
		s.setTaskCreationModuleExpanded(target.serviceID, target.moduleID, true)
		if target.field == "baseRef" {
			if draft := state.moduleData[target.serviceID][target.moduleID]; draft != nil {
				draft.repositoryPicker.branchFocusPending = true
			}
		} else if target.field != "module" {
			state.moduleFieldFocus = target.serviceID + ":" + target.moduleID + ":" + target.field
		}
	} else {
		for _, moduleID := range state.moduleOrder[target.serviceID] {
			s.taskCreationModuleExpanded(target.serviceID, moduleID)
		}
	}
}

func (s *app) beginTaskCreationPreflight() bool {
	state := s.taskCreation
	if state == nil {
		return false
	}
	s.syncTaskCreationDraft()
	s.cancelTaskCreationAINaming()
	if s.busy {
		state.inlineError = "当前有操作正在执行，请等待完成或取消后重试。"
		return true
	}
	if state.pendingPreflight {
		return true
	}
	if s.startTaskCreationDirectoryCheck(true) {
		return true
	}
	if message, target := s.taskCreationValidationFailure(); message != "" {
		state.inlineError = message
		s.focusTaskCreationValidationTarget(target)
		return true
	}
	s.stopTaskCreationPreviewDebounce()
	payload, err := s.taskCreationPayload()
	if err != nil {
		state.inlineError = err.Error()
		if state.requirements != nil && state.requirements.inputIsQuery {
			s.focusTaskCreationValidationTarget(&taskCreationValidationTarget{field: "requirement"})
		}
		return true
	}
	cancelTaskRepositoryChoices(state)
	state.preview = s.snapshotTaskCreationPreview(payload)
	state.phase = "preview"
	state.inlineError = ""
	state.progressGeneration++
	state.creationProgress = newTaskCreationProgressState(s, state, state.progressGeneration)
	state.creationProgress.mode = "preflight"
	state.creationProgress.phase = workspace.TaskCreationPhaseChecking
	state.creationProgress.message = "正在检查任务信息与分支状态…"
	actionPayload := taskCreationActionPayload(state.preview.payload, state.token)
	s.startTaskCreationRead("task.create-preview", actionPayload)
	return true
}

func cloneTaskCreationWorkspaceToolIDs(ids []string) []string {
	result := make([]string, len(ids))
	copy(result, ids)
	return result
}

func (s *app) snapshotTaskCreationPreview(payload map[string]any) *taskCreationPreview {
	return &taskCreationPreview{
		signature:        s.taskCreationSignature(),
		payload:          payload,
		workspaceToolIDs: cloneTaskCreationWorkspaceToolIDs(s.taskCreationSelectedToolIDs()),
	}
}

func (s *app) resumeTaskCreationPreflightIfPending() bool {
	state := s.taskCreation
	if state == nil || !state.pendingPreflight {
		return false
	}
	state.pendingPreflight = false
	s.stopTaskCreationPreviewDebounce()
	s.beginTaskCreationPreflight()
	return true
}

func taskCreationActionPayload(payload map[string]any, token string) map[string]any {
	result := make(map[string]any, len(payload)+1)
	for key, value := range payload {
		result[key] = value
	}
	result["taskCreateToken"] = token
	return result
}

func (s *app) submitTaskCreation(keys []workspace.BranchReuseKey) {
	state := s.taskCreation
	if state == nil || state.preview == nil {
		return
	}
	if state.preview.signature != s.taskCreationSignature() {
		s.invalidateTaskCreationPreview("任务信息已变化，请重新预检。")
		return
	}
	if s.busy {
		state.phase = "confirm"
		state.inlineError = "当前有操作正在执行，预检结果已保留；完成后可继续创建。"
		return
	}
	s.cancelTaskCreationAINaming()
	payload := taskCreationSubmissionPayload(state, keys)
	state.progressGeneration++
	state.creationProgress = newTaskCreationProgressState(s, state, state.progressGeneration)
	state.creationProgress.mode = "create"
	state.phase = "creating"
	state.inlineError = "正在创建任务并准备工作区…"
	s.act("task.create", "", "", "", "", "", payload)
}

func taskCreationSubmissionPayload(state *taskCreationState, keys []workspace.BranchReuseKey) map[string]any {
	if state == nil || state.preview == nil {
		return nil
	}
	payload := taskCreationActionPayload(state.preview.payload, state.token)
	payload["confirmedBranchReuseKeys"] = append([]workspace.BranchReuseKey(nil), keys...)
	payload["workspaceToolIds"] = cloneTaskCreationWorkspaceToolIDs(state.preview.workspaceToolIDs)
	return payload
}

func (s *app) taskCreationSelectedToolIDs() []string {
	state := s.taskCreation
	if state == nil {
		return []string{}
	}
	ids := make([]string, 0, len(state.selectedToolIDs))
	for _, option := range state.enabledWorkspaceTools() {
		if option.Available && state.selectedToolIDs[option.ID] {
			ids = append(ids, option.ID)
		}
	}
	return ids
}

func (s *app) taskCreationTemplate(id string) *domain.AgentTaskTemplate {
	state := s.taskCreation
	if state == nil {
		return nil
	}
	for index := range state.templates {
		if state.templates[index].ID == id {
			return &state.templates[index]
		}
	}
	return nil
}

func taskCreationSelectedTemplateID(notes string, templates []domain.AgentTaskTemplate) string {
	selected := ""
	for _, template := range templates {
		if template.Content != notes {
			continue
		}
		if selected != "" {
			return ""
		}
		selected = template.ID
	}
	return selected
}

func (s *app) toggleTaskCreationTemplate(template domain.AgentTaskTemplate) {
	state := s.taskCreation
	if state == nil {
		return
	}
	var selected *domain.AgentTaskTemplate
	selectedID := state.selectedTemplate
	if selectedID == "" {
		selectedID = taskCreationSelectedTemplateID(state.notes, state.templates)
	}
	if selectedID != "" {
		selected = s.taskCreationTemplate(selectedID)
	}
	if selected != nil && selected.ID == template.ID {
		if state.notes == template.Content {
			state.notes = ""
		}
		state.selectedTemplate = ""
		state.pendingTemplate = ""
		return
	}
	if strings.TrimSpace(state.notes) == "" || selected != nil && state.notes == selected.Content {
		state.notes = template.Content
		state.selectedTemplate = template.ID
		state.pendingTemplate = ""
		return
	}
	state.pendingTemplate = template.ID
}

func (s *app) confirmTaskCreationTemplateReplacement(confirm bool) {
	state := s.taskCreation
	if state == nil || state.pendingTemplate == "" {
		return
	}
	template := s.taskCreationTemplate(state.pendingTemplate)
	if confirm && template != nil {
		state.notes = template.Content
		state.selectedTemplate = template.ID
	}
	state.pendingTemplate = ""
}

func (s *app) refreshTaskCreationAgentPreview() {
	state := s.taskCreation
	if state == nil || state.phase != "editing" || state.discardConfirmation || state.agentPreviewBusy || s.busy {
		return
	}
	signature := s.taskCreationAgentPreviewSignature()
	if signature == "" || state.agentPreviewSig == signature {
		return
	}
	payload, err := s.taskCreationPayload()
	if err != nil {
		state.agentPreviewErr = err.Error()
		state.agentPreviewSig = signature
		return
	}
	state.agentPreviewBusy = true
	state.agentPreviewErr = ""
	state.agentPreviewSig = signature
	request := taskCreationActionPayload(payload, state.token)
	request["taskCreatePreviewSignature"] = signature
	request["requirementMaterials"] = state.materialsPreview
	s.startTaskCreationRead("task.agent-preview", request)
}

func (s *app) refreshTaskCreationMaterialsPreview() {
	state := s.taskCreation
	if state == nil || state.phase != "editing" || state.discardConfirmation {
		return
	}
	signature := s.taskCreationMaterialsPreviewSignature()
	if signature != state.materialsPreviewInputSig {
		state.materialsPreview = domain.RequirementMaterialsDirectory{Status: domain.MaterialsNotRequested}
		state.materialsPreviewErr = ""
		state.agentPreviewSig = ""
		state.agentPreview = nil
		state.agentPreviewFile = ""
		state.agentPreviewErr = ""
	}
	if signature == "" || state.materialsPreviewInputSig == signature || state.materialsPreviewPending {
		return
	}
	if strings.TrimSpace(state.draft.RequirementLink) == "" || !state.config.RequirementMaterialsConfigured() {
		state.materialsPreview = domain.RequirementMaterialsDirectory{Status: domain.MaterialsNotRequested}
		state.materialsPreviewInputSig = signature
		state.materialsPreviewErr = ""
		state.agentPreviewSig = ""
		return
	}
	if s.busy {
		return
	}
	state.materialsPreviewPending = true
	request := map[string]any{
		"requirementLink":              state.draft.RequirementLink,
		"folderName":                   state.draft.TaskName,
		"taskCreateToken":              state.token,
		"taskCreateMaterialsSignature": signature,
	}
	s.startTaskCreationRead("task.materials-preview", request)
}

func (s *app) requestTaskCreationAINaming(force bool) {
	s.requestTaskCreationAINamingJob(force)
}

func decodeTaskCreationResult[T any](data any) (T, error) {
	var value T
	if data == nil {
		return value, fmt.Errorf("操作未返回可读取的数据")
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return value, err
	}
	if err := json.Unmarshal(encoded, &value); err != nil {
		return value, err
	}
	return value, nil
}

// taskCreationActionResult is called by the desktop result router before its generic result handling.
func (s *app) taskCreationActionResult(action application.Action, result application.ActionResult) bool {
	state := s.taskCreation
	if state == nil || payloadString(action.Payload, "taskCreateToken") != state.token {
		return payloadString(action.Payload, "taskCreateToken") != "" && taskCreationOwnedAction(action.Kind)
	}
	if !taskCreationOwnedAction(action.Kind) {
		return false
	}
	if s.finishTaskCreationRead(action) {
		return true
	}
	switch action.Kind {
	case "task.materials-preview":
		state.materialsPreviewPending = false
		if s.resumeTaskCreationPreflightIfPending() {
			return true
		}
		requestSignature := payloadString(action.Payload, "taskCreateMaterialsSignature")
		if requestSignature != s.taskCreationMaterialsPreviewSignature() {
			state.materialsPreviewInputSig = ""
			state.materialsPreview = domain.RequirementMaterialsDirectory{Status: domain.MaterialsNotRequested}
			state.materialsPreviewErr = ""
			state.agentPreviewSig = ""
			state.agentPreview = nil
			state.agentPreviewFile = ""
			return true
		}
		state.materialsPreviewInputSig = requestSignature
		state.materialsPreviewErr = ""
		materialDirectory, err := decodeTaskCreationResult[domain.RequirementMaterialsDirectory](result.Data)
		if err != nil {
			state.materialsPreview = domain.RequirementMaterialsDirectory{Status: domain.MaterialsNotRequested}
			state.materialsPreviewErr = "需求资料预览格式错误：" + err.Error()
		} else if materialDirectory.Status == domain.MaterialsReady && materialDirectory.WriteRoot != nil && strings.TrimSpace(*materialDirectory.WriteRoot) != "" {
			state.materialsPreview = materialDirectory
		} else {
			state.materialsPreview = domain.RequirementMaterialsDirectory{Status: domain.MaterialsNotRequested}
			if materialDirectory.FailureReason != nil {
				state.materialsPreviewErr = *materialDirectory.FailureReason
			}
		}
		state.agentPreviewSig = ""
		s.refreshTaskCreationAgentPreview()
		return true
	case "instructions.templates.list":
		state.templatesLoading = false
		state.templatesError = ""
		s.settingsTemplates = mapsFromData(result.Data)
		s.settingsTemplatesLoaded = true
		state.templates = taskTemplatesFromMaps(s.settingsTemplates)
		s.continueTaskCreationInitialLoads()
		return true
	case "workspace.tools-options":
		state.toolsLoading = false
		state.toolsLoaded = true
		state.toolsError = ""
		options, err := decodeData[taskCreationWorkspaceToolOption](result.Data)
		if err != nil {
			state.toolsError = "工作区工具列表格式错误：" + err.Error()
			options = nil
		}
		state.workspaceTools = normalizeTaskCreationWorkspaceTools(options, state.config.DefaultWorkspaceToolIDs)
		state.selectedToolIDs = make(map[string]bool, len(state.config.DefaultWorkspaceToolIDs))
		for _, id := range state.config.DefaultWorkspaceToolIDs {
			state.selectedToolIDs[id] = true
		}
		state.loadStep = ""
		s.finishTaskCreationInitialLoads()
		return true
	case "task.agent-preview":
		state.agentPreviewBusy = false
		if s.resumeTaskCreationPreflightIfPending() {
			return true
		}
		requestSignature := payloadString(action.Payload, "taskCreatePreviewSignature")
		if requestSignature != s.taskCreationAgentPreviewSignature() {
			state.agentPreview = nil
			state.agentPreviewSig = ""
			state.agentPreviewFile = ""
			state.agentPreviewErr = ""
			return true
		}
		preview, err := decodeTaskCreationResult[workspace.AgentDocumentPreview](result.Data)
		if err != nil {
			state.agentPreview = nil
			state.agentPreviewErr = "任务说明预览格式错误：" + err.Error()
			return true
		}
		state.agentPreview = &preview
		state.agentPreviewErr = ""
		state.agentPreviewFile = firstTaskAgentPreviewFile(preview.Files)
		return true
	case "requirements.ai-name":
		state.aiPending = false
		if state.pendingPreflight {
			state.pendingPreflight = false
			state.aiEligible = false
			state.aiError = ""
			s.beginTaskCreationPreflight()
			return true
		}
		requestedLink := payloadString(action.Payload, "requestLink")
		if requestedLink != state.draft.RequirementLink || !state.aiEligible {
			return true
		}
		suggestion, err := decodeTaskCreationResult[domain.RequirementAiNamingSuggestion](result.Data)
		if err != nil {
			state.aiError = "AI 命名返回格式错误：" + err.Error()
			return true
		}
		oldBranch := state.draft.Branch
		updated, err := state.draft.ApplyAINaming(requestedLink, suggestion, state.config.DefaultBranchPrefix)
		if err != nil {
			state.aiError = "AI 命名建议不可用：" + err.Error()
			return true
		}
		state.draft = updated
		state.lastName, state.lastBranch = state.draft.TaskName, state.draft.Branch
		if oldBranch != state.draft.Branch {
			s.retargetUntouchedTaskCreationModules(state.draft.Branch)
		}
		state.aiError = ""
		state.inlineError = ""
		s.invalidateTaskCreationPreview("AI 命名已更新，请重新预检。")
		return true
	case "task.create-preview":
		if state.preview == nil || state.preview.signature != s.taskCreationSignature() {
			state.preview = nil
			state.phase = "editing"
			state.inlineError = "任务信息在预检期间发生变化，请重新预检。"
			return true
		}
		conflicts, err := decodeData[workspace.BranchReuseConflict](result.Data)
		if err != nil {
			state.preview = nil
			state.phase = "editing"
			state.inlineError = "分支复用预检结果格式错误：" + err.Error()
			return true
		}
		state.preview.conflicts = conflicts
		state.inlineError = ""
		if state.creationProgress != nil && state.creationProgress.mode == "preflight" {
			state.creationProgress.finished = true
			state.creationProgress.cancelling = false
			state.creationProgress.message = "预检已完成。"
		}
		if len(conflicts) == 0 {
			s.submitTaskCreation(nil)
		} else {
			state.phase = "confirm"
		}
		return true
	case "task.create":
		s.closeTaskCreationRequirements()
		s.closeTaskCreationAINaming()
		s.stopTaskCreationPreviewDebounce()
		s.dialogOpen = false
		s.form = actionForm{}
		s.taskCreation = nil
		s.lastMessage = result.Message
		s.taskCreationWarning = ""
		if strings.TrimSpace(result.Text) != "" {
			s.taskCreationWarning = "任务已创建。\n" + result.Text
		}
		s.lastError = ""
		return true
	}
	return false
}

// taskCreationActionError is called by the desktop dispatcher before generic error handling.
func (s *app) taskCreationActionError(action application.Action, cause error) bool {
	state := s.taskCreation
	if state == nil || payloadString(action.Payload, "taskCreateToken") != state.token {
		return payloadString(action.Payload, "taskCreateToken") != "" && taskCreationOwnedAction(action.Kind)
	}
	if !taskCreationOwnedAction(action.Kind) {
		return false
	}
	if s.finishTaskCreationRead(action) {
		return true
	}
	message := cause.Error()
	switch action.Kind {
	case "task.materials-preview":
		state.materialsPreviewPending = false
		if s.resumeTaskCreationPreflightIfPending() {
			return true
		}
		requestSignature := payloadString(action.Payload, "taskCreateMaterialsSignature")
		if requestSignature != s.taskCreationMaterialsPreviewSignature() {
			state.materialsPreviewInputSig = ""
			state.materialsPreview = domain.RequirementMaterialsDirectory{Status: domain.MaterialsNotRequested}
			state.materialsPreviewErr = ""
			state.agentPreviewSig = ""
			state.agentPreview = nil
			state.agentPreviewFile = ""
			return true
		}
		state.materialsPreviewInputSig = requestSignature
		state.materialsPreview = domain.RequirementMaterialsDirectory{Status: domain.MaterialsNotRequested}
		state.materialsPreviewErr = message
		state.agentPreviewSig = ""
		s.refreshTaskCreationAgentPreview()
		return true
	case "instructions.templates.list":
		state.templatesLoading = false
		state.templatesError = message
		state.templates = nil
		s.continueTaskCreationInitialLoads()
		return true
	case "workspace.tools-options":
		state.toolsLoading = false
		state.toolsLoaded = true
		state.toolsError = "无法读取工作区工具状态：" + message
		state.workspaceTools = normalizeTaskCreationWorkspaceTools(nil, state.config.DefaultWorkspaceToolIDs)
		state.selectedToolIDs = make(map[string]bool, len(state.config.DefaultWorkspaceToolIDs))
		for _, id := range state.config.DefaultWorkspaceToolIDs {
			state.selectedToolIDs[id] = true
		}
		state.loadStep = ""
		s.finishTaskCreationInitialLoads()
		return true
	case "task.agent-preview":
		state.agentPreviewBusy = false
		if s.resumeTaskCreationPreflightIfPending() {
			return true
		}
		requestSignature := payloadString(action.Payload, "taskCreatePreviewSignature")
		if requestSignature != s.taskCreationAgentPreviewSignature() {
			state.agentPreviewSig = ""
			state.agentPreview = nil
			state.agentPreviewFile = ""
			state.agentPreviewErr = ""
			return true
		}
		state.agentPreview = nil
		state.agentPreviewErr = message
		return true
	case "requirements.ai-name":
		state.aiPending = false
		if state.pendingPreflight {
			state.pendingPreflight = false
			state.aiEligible = false
			state.aiError = ""
			s.beginTaskCreationPreflight()
			return true
		}
		if !state.aiEligible {
			return true
		}
		state.aiError = "AI 命名失败，可继续手工填写：" + message
		return true
	case "task.create-preview":
		s.failTaskCreationProgress(state, "预检未完成："+message)
		state.preview = nil
		state.phase = "editing"
		state.inlineError = "创建前预检失败：" + message
		return true
	case "task.create":
		s.failTaskCreationProgress(state, message)
		state.preview = nil
		state.phase = "editing"
		state.inlineError = "任务创建未完成，请修正后重新预检：" + message
		return true
	}
	return false
}

func taskCreationOwnedAction(kind string) bool {
	switch kind {
	case "instructions.templates.list", "workspace.tools-options", "task.materials-preview", "task.agent-preview", "requirements.ai-name", "task.create-preview", "task.create":
		return true
	default:
		return false
	}
}

// renderTaskCreationDialog replaces the generic action dialog for task creation.
func (s *app) renderTaskCreationDialog(c *ui.Context, p palette) bool {
	state := s.taskCreation
	if state == nil || !s.dialogOpen || s.form.mode != "task-create" {
		s.cancelTaskCreationReads(state, false)
		s.closeTaskCreationRequirements()
		s.closeTaskCreationAINaming()
		return false
	}
	s.syncTaskCreationDraft()
	s.startTaskCreationDirectoryCheck(false)
	s.ensureTaskCreationAINamingScope()
	inputsLocked := state.phase == "preview" || state.phase == "creating"
	open := s.dialogOpen
	dialog := ui.DialogBase(c.Key("task-create-dialog"), &open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.32))
		width, height := c.Size()
		panel.Width(min(1120, width-24)).Height(min(900, max(1, height-24))).Padding(16).Gap(10).Background(p.surface).Border(1, p.line).Radius(18).Label("创建任务表单")
		panel.Children(func() {
			ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).MinWidth(0).Shrink(0).Children(func() {
				label(c, state.title, 20, p.text).FontWeight(600).Grow(1).SingleLine()
				label(c, "名称、分支和模块设置只应用于本次任务。", 11, p.muted).SingleLine()
			})
			ui.Divider(c).Shrink(0)
			ui.Row(c).FillWidth().Grow(1).MinHeight(0).Gap(12).AlignItems(ui.Stretch).Children(func() {
				windowWidth, _ := c.Size()
				modalWidth := min(1120, windowWidth-24)
				leftWidth := max(250, (modalWidth-32-12)/2)
				ui.Column(c).Width(leftWidth).MinWidth(250).FillHeight().MinHeight(0).Gap(8).Children(func() {
					ui.Scroll(c).FillWidth().Grow(1).MinHeight(0).Padding(2).Gap(11).Label("任务基本信息").Focusable().Disabled(s.busy || inputsLocked).Children(func() {
						s.renderTaskCreationRequirements(c, p)
						s.syncTaskCreationDraft()
						ui.Column(c).Gap(6).Children(func() {
							ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
								label(c, "任务名称 / 文件夹名", 12, p.muted)
								if state.aiEligible && state.config.AIRequirementNamingEnabled {
									if iconButtonDisabled(c, "task-create-ai-name", "refresh", "生成任务名和分支", p, state.aiPending || s.busy).
										Width(28).Height(28).Radius(7).FocusRing(true).Clicked() {
										s.requestTaskCreationAINaming(true)
									}
								}
							})
							nameInput := ui.TextInput(c.Key("task-create-name"), &state.draft.TaskName).FillWidth().Height(36).Placeholder("输入任务名称").Label("任务名称")
							if state.taskFieldFocus == "taskName" {
								nameInput.Focus().ScrollIntoView()
								state.taskFieldFocus = ""
							}
							if state.aiPending {
								label(c, "正在获取命名建议…", 11, p.muted).FillWidth().Wrap()
							}
							if state.directoryCheck.message != "" && state.directoryCheck.signature == s.taskCreationDirectorySignature() {
								label(c, state.directoryCheck.message, 11, p.amber).FillWidth().Wrap()
							}
							if state.aiError != "" {
								label(c, state.aiError, 11, p.amber).FillWidth().Wrap()
							}
						})
						s.syncTaskCreationDraft()
						ui.Column(c).Gap(6).Children(func() {
							label(c, "任务分支", 12, p.muted)
							branchInput := ui.TextInput(c.Key("task-create-branch"), &state.draft.Branch).FillWidth().Height(36).Placeholder("例如 feature/12345_fix_issue").Label("任务分支输入")
							if state.taskFieldFocus == "taskBranch" {
								branchInput.Focus().ScrollIntoView()
								state.taskFieldFocus = ""
							}
							if state.draft.MetadataHint != nil {
								label(c, *state.draft.MetadataHint, 11, p.amber)
							}
						})
						s.syncTaskCreationDraft()
						s.renderTaskCreationWorkspaceTools(c, p)
						if state.inlineError != "" && !(state.phase == "creating" && state.creationProgress != nil) {
							if state.validationTarget == nil || state.validationTarget.serviceID == "" && state.validationTarget.field != "service" {
								label(c, state.inlineError, 11, p.amber).Wrap()
							}
						}
					})
					if state.creationProgress != nil {
						s.renderTaskCreationProgress(c, p, state.creationProgress)
					}
				})
				ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Gap(8).Label("任务服务与模块").Disabled(s.busy || inputsLocked).Children(func() {
					s.renderTaskCreationSelections(c, p)
				})
			})
			ui.Row(c).FillWidth().Gap(8).Shrink(0).Wrap().Justify(ui.End).Children(func() {
				if inputsLocked {
					canceling := state.phase == "preview" && state.preflight != nil && state.preflight.cancelRequested || state.phase == "creating" && s.operationCancelling
					buttonLabel := "中断创建"
					if canceling {
						buttonLabel = "中断中…"
					}
					button := ui.ButtonBase(c.Key("task-create-interrupt")).Row().Height(34).Padding(0, 16).Radius(18).Label(buttonLabel).Border(1, p.line).Disabled(canceling || state.phase == "preview" && (state.preflight == nil || state.preflight.cancelRequested) || state.phase == "creating" && !s.canCancelOperation())
					button.Children(func() { label(c, buttonLabel, 12, p.text) })
					if button.Clicked() {
						if state.phase == "preview" {
							s.interruptTaskCreationPreflight(state)
						} else if s.canCancelOperation() {
							s.cancelAction()
						}
					}
					return
				}
				if action(c, "task-create-cancel", "close", "取消", p, false) {
					open = false
					return
				}
				if state.phase == "confirm" && state.preview != nil && len(state.preview.conflicts) > 0 {
					label(c, "请在分支复用确认框中完成检查。", 11, p.muted).Shrink(0)
					return
				}
				createLabel := "创建任务"
				if taskCreationHasSelectedServices(state) {
					createLabel = "检查并创建任务"
				}
				button := ui.ButtonBase(c.Key("task-create-preflight")).Height(34).Padding(0, 16).Radius(18).Label(createLabel).Disabled(s.busy || state.pendingPreflight || state.phase == "preview" || state.phase == "creating")
				button.Background(p.blue)
				button.Children(func() { label(c, createLabel, 12, ui.Hex("#FFFFFF")) })
				if button.Clicked() {
					s.beginTaskCreationPreflight()
				}
			})
		})
	})
	if dialog.Dismissed() {
		if inputsLocked {
			open = true
			s.dialogOpen = true
		} else {
			open = false
			s.dialogOpen = false
		}
	}
	if !open && s.dialogOpen {
		s.dialogOpen = false
	}
	if !s.dialogOpen {
		s.requestTaskCreationDismiss()
		return true
	}
	s.renderTaskCreationBranchReuseDialog(c, p, state)
	return true
}

func (s *app) renderTaskCreationNotesAndPreview(c *ui.Context, p palette) {
	state := s.taskCreation
	if state == nil {
		return
	}
	ui.Column(c).FillWidth().Padding(12).Gap(9).Background(p.soft).Border(1, p.line).Radius(10).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			for _, tab := range []struct{ id, label string }{{"preview", "任务说明预览"}, {"notes", "编辑任务说明"}} {
				button := ui.ButtonBase(c.Key("task-create-right-tab-"+tab.id)).Height(30).Padding(0, 12).Radius(8).Label(tab.label)
				if state.rightTab == tab.id {
					button.Background(p.selected)
				}
				button.Children(func() { label(c, tab.label, 11, p.text) })
				if button.Clicked() {
					state.rightTab = tab.id
				}
			}
			if state.rightTab == "preview" {
				label(c, "实时更新", 10, p.muted)
			}
		})
		if state.rightTab == "notes" {
			ui.Column(c).Gap(6).Children(func() {
				label(c, "从模板填充（单选）", 11, p.muted)
				if state.templatesLoading {
					label(c, "正在读取任务说明模板…", 11, p.muted)
				} else if state.templatesError != "" {
					ui.Row(c).Gap(8).Children(func() {
						label(c, "模板读取失败："+state.templatesError, 11, p.amber).Grow(1)
						if action(c, "task-create-templates-retry", "refresh", "重试", p, false) {
							state.templatesError = ""
							s.loadTaskCreationTemplates()
						}
					})
				} else if len(state.templates) == 0 {
					label(c, "还没有可用模板，可在设置中创建任务说明模板。", 11, p.muted)
				} else {
					ui.Row(c).Gap(6).Children(func() {
						for _, template := range state.templates {
							button := ui.ButtonBase(c.Key("task-create-template-"+template.ID)).Height(30).Padding(0, 10).Radius(16).Label(template.Name).Tooltip(template.Name)
							if state.selectedTemplate == template.ID {
								button.Background(p.selected)
							}
							button.Children(func() { label(c, template.Name, 11, p.text).SingleLine() })
							if button.Clicked() {
								s.toggleTaskCreationTemplate(template)
							}
						}
					})
				}
				if state.pendingTemplate != "" {
					target := s.taskCreationTemplate(state.pendingTemplate)
					name := state.pendingTemplate
					if target != nil {
						name = target.Name
					}
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						label(c, "替换当前手工编辑的说明为“"+name+"”？", 11, p.amber).Grow(1)
						if action(c, "task-create-template-confirm", "check", "替换", p, true) {
							s.confirmTaskCreationTemplateReplacement(true)
						}
						if action(c, "task-create-template-cancel", "close", "保留当前内容", p, false) {
							s.confirmTaskCreationTemplateReplacement(false)
						}
					})
				}
				ui.TextArea(c.Key("task-create-notes"), &state.notes).FillWidth().Height(190).Padding(10).Background(p.surface).Border(1, p.line).Radius(8)
				label(c, "创建后可在任务详情中继续阅读和管理任务说明。", 10, p.muted)
			})
			s.syncTaskCreationDraft()
			return
		}
		if state.materialsPreviewPending {
			label(c, "正在解析需求资料目录…", 10, p.muted)
		} else if state.materialsPreviewErr != "" {
			label(c, "需求资料预览提示："+state.materialsPreviewErr, 10, p.amber)
		}
		if state.agentPreviewBusy {
			label(c, "正在生成任务说明预览…", 12, p.muted)
			return
		}
		if state.agentPreviewErr != "" {
			label(c, "预览失败："+state.agentPreviewErr, 12, p.amber)
			return
		}
		if state.agentPreview == nil || len(state.agentPreview.Files) == 0 {
			label(c, "尚未生成任务说明预览。", 12, p.muted)
			return
		}
		selected := -1
		for index := range state.agentPreview.Files {
			if state.agentPreview.Files[index].RelativePath == state.agentPreviewFile {
				selected = index
				break
			}
		}
		if selected < 0 {
			state.agentPreviewFile = firstTaskAgentPreviewFile(state.agentPreview.Files)
			for index := range state.agentPreview.Files {
				if state.agentPreview.Files[index].RelativePath == state.agentPreviewFile {
					selected = index
					break
				}
			}
		}
		if selected < 0 {
			settingsMuted(c, p, "当前没有可预览的任务说明。")
			return
		}
		ui.Row(c).Gap(6).Children(func() {
			for _, tab := range []struct{ id, label string }{{"preview", "预览"}, {"source", "源码"}} {
				button := ui.ButtonBase(c.Key("task-create-preview-mode-"+tab.id)).Height(26).Padding(0, 9).Radius(7).Label(tab.label)
				if state.agentPreviewTab == tab.id {
					button.Background(p.selected)
				}
				button.Children(func() { label(c, tab.label, 10, p.text) })
				if button.Clicked() {
					state.agentPreviewTab = tab.id
				}
			}
			if action(c, "task-create-preview-copy", "copy", "复制源码", p, false) {
				state.agentPreviewCopy = ""
				if s.copyTextToClipboard(c.Services(), state.agentPreview.Files[selected].Content, "") {
					state.agentPreviewCopy = "已复制 " + state.agentPreview.Files[selected].RelativePath
				}
			}
		})
		if state.agentPreviewTab == "source" {
			file := state.agentPreview.Files[selected]
			renderNativeCodePreview(c, p, &state.agentCodePreview,
				state.token+"\x00"+file.RelativePath, file.RelativePath, file.Content,
				"task-create-preview-content-"+file.RelativePath).Height(230)
		} else {
			ui.Scroll(c).FillWidth().Height(230).Padding(10).Background(p.surface).Border(1, p.line).Radius(8).Children(func() {
				renderMarkdown(c, p, state.agentPreview.Files[selected].Content)
			})
		}
		if state.agentPreviewCopy != "" {
			label(c, state.agentPreviewCopy, 10, p.muted)
		}
	})
}

func (s *app) renderTaskCreationWorkspaceTools(c *ui.Context, p palette) {
	state := s.taskCreation
	if state == nil || len(state.config.DefaultWorkspaceToolIDs) == 0 {
		return
	}
	tools := state.enabledWorkspaceTools()
	ui.Column(c).FillWidth().Gap(6).Children(func() {
		label(c, "创建后打开工作区工具", 11, p.muted).SingleLine()
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Wrap().Children(func() {
			for _, option := range tools {
				selected := state.selectedToolIDs[option.ID]
				name := option.DisplayName
				if !option.Available {
					name += "（不可用）"
				}
				ui.Checkbox(c.Key("task-create-tool-"+option.ID), &selected, name).Height(28).Label(option.DisplayName).
					Disabled(!option.Available || s.busy).Tooltip(firstNonEmpty(option.Description, option.Reason)).OnChange(func() {
					state.selectedToolIDs[option.ID] = selected
				})
			}
		})
		if state.toolsLoading {
			label(c, "正在检查可用工具…", 10, p.muted)
		} else if state.toolsError != "" {
			label(c, state.toolsError, 10, p.amber)
		}
		if len(tools) == 0 {
			label(c, "当前没有已注册的工作区工具。", 10, p.muted)
		}
	})
}

func (s *app) renderTaskCreationSelections(c *ui.Context, p palette) {
	state := s.taskCreation
	if state == nil {
		return
	}
	group := s.taskCreationGroup()
	if group == nil {
		emptyPanel(c, p, "请选择项目组", "当前任务配置中没有对应的项目组。")
		return
	}
	s.renderTaskCreationServiceList(c, p, group)
}

func (s *app) renderTaskCreationServiceList(c *ui.Context, p palette, group *domain.GroupConfig) {
	state := s.taskCreation
	if state == nil {
		return
	}
	if state.serviceListScroll == nil {
		state.serviceListScroll = make(map[string]*ui.ScrollState)
	}
	listScroll := state.serviceListScroll[group.ID]
	if listScroll == nil {
		listScroll = &ui.ScrollState{}
		state.serviceListScroll[group.ID] = listScroll
	}
	selectedCount := 0
	for _, selected := range state.services {
		if selected {
			selectedCount++
		}
	}
	ui.Column(c).FillWidth().FillHeight().MinWidth(0).MinHeight(0).Padding(10).Gap(8).
		Background(p.soft).Border(1, p.line).Radius(10).Label("任务服务与模块").Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(5).Children(func() {
			label(c, "服务与模块", 14, p.text).FontWeight(600).Grow(1)
			label(c, fmt.Sprintf("%d 已选", selectedCount), 10, p.muted).Shrink(0)
		})
		if len(state.config.Groups) > 1 {
			var combo ui.ComboboxParts
			ui.Row(c).FillWidth().Children(func() {
				combo = ui.ComboboxBase(c.Key("task-create-group-picker"), &state.groupQuery)
				combo.Input.FillWidth().MinWidth(0).Height(32).Padding(0, 28, 0, 8).Placeholder("选择项目组").Background(p.surface).Border(1, p.line).Radius(7).Label("项目组")
				drawTaskComboboxChevron(combo.Input, p)
			})
			combo.Popup(func(panel ui.Element) {
				panel.Padding(4).MaxHeight(240).Background(p.surface).Border(1, p.line).Radius(9).Shadow(0, 5, 16, 0, ui.RGBA(0, 0, 0, 0.14))
				ui.Scroll(c).FillWidth().MaxHeight(232).Gap(2).Children(func() {
					for _, candidate := range state.config.Groups {
						name := firstNonEmpty(candidate.Name, candidate.ID)
						value := candidate.ID + "\x00" + name
						if combo.Filtering() && !strings.Contains(strings.ToLower(value), strings.ToLower(strings.TrimSpace(state.groupQuery))) {
							continue
						}
						item := combo.Item(value).FillWidth().Height(32).Padding(0, 8).Radius(6)
						if item.Highlighted() {
							item.Background(p.selected)
						}
						item.Children(func() { label(c, name, 11, p.text).SingleLine() })
					}
				})
			})
			if chosen, ok := combo.Chosen(); ok {
				if groupID, name, found := strings.Cut(chosen, "\x00"); found {
					s.setTaskCreationGroup(groupID)
					state.groupQuery = name
				}
			}
		}
		ui.TextInput(c.Key("task-create-service-search"), &state.serviceSearch).FillWidth().Height(34).
			Placeholder("搜索名称、路径或 ID").Label("搜索服务名称、仓库路径或 ID")
		if state.inlineError != "" && state.validationTarget != nil && state.validationTarget.field == "service" {
			label(c, state.inlineError, 10, p.amber).FillWidth().Wrap()
		}
		activeVisible := false
		if state.activeServiceID != "" && strings.TrimSpace(state.serviceSearch) != "" {
			for _, service := range group.Services {
				if service.ID != state.activeServiceID || !service.Enabled {
					continue
				}
				repositoryName, repositoryPath := s.repositoryDetails(service.RepositoryID)
				activeVisible = taskCreationServiceMatches(service, repositoryName+" "+repositoryPath, state.serviceSearch)
				break
			}
		}
		if state.activeServiceID != "" && strings.TrimSpace(state.serviceSearch) != "" && !activeVisible {
			ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Shrink(0).Children(func() {
				label(c, "当前配置服务被搜索条件隐藏。", 10, p.muted).Grow(1).SingleLine()
				if action(c, "task-create-clear-service-search", "close", "清除搜索", p, false) {
					state.serviceSearch = ""
				}
			})
		}
		ui.Scroll(c).TrackScroll(listScroll).Grow(1).FillWidth().MinHeight(0).Gap(5).Label("任务服务列表").Children(func() {
			visible := 0
			for _, service := range group.Services {
				repositoryName, repositoryPath := s.repositoryDetails(service.RepositoryID)
				if !service.Enabled || !taskCreationServiceMatches(service, repositoryName+" "+repositoryPath, state.serviceSearch) {
					continue
				}
				visible++
				service := service
				serviceName := taskServiceDisplayName(s, service)
				root := s.repositoryRootPath(service.RepositoryID)
				moduleCount := len(service.Modules)
				if state.services[service.ID] {
					moduleCount = len(state.moduleOrder[service.ID])
				}
				detail := fmt.Sprintf("%d 个模块", moduleCount)
				if root != "" {
					detail += " · " + root
				}
				renderTaskServiceChoiceRow(c, p, taskServiceChoiceRowProps{
					key: "task-create-service-" + service.ID, name: serviceName, detail: detail,
					buttonLabel: "配置服务：" + serviceName,
					selected:    state.services[service.ID], active: state.services[service.ID] && state.activeServiceID == service.ID,
					disabled:          s.busy || state.pendingPreflight,
					onClick:           func() { s.focusTaskCreationService(service.ID) },
					onSelectionChange: func(selected bool) { s.setTaskCreationServiceSelected(service.ID, selected) },
				})
				if state.services[service.ID] && state.activeServiceID == service.ID {
					s.renderTaskCreationServiceModules(c.Key("task-create-service-modules-"+service.ID), p, service)
				}
			}
			if visible == 0 {
				if strings.TrimSpace(state.serviceSearch) != "" {
					emptyPanel(c, p, "没有匹配的服务", "可按名称、仓库路径或 ID 搜索。")
				} else {
					emptyPanel(c, p, "没有可选服务", "可直接创建任务，之后再添加服务。")
				}
			}
		})
	})
}

func (s *app) renderTaskCreationServiceModules(c *ui.Context, p palette, service domain.GroupServiceConfig) {
	state := s.taskCreation
	if state == nil {
		return
	}
	serviceID := service.ID
	moduleIDs := append([]string(nil), state.moduleOrder[serviceID]...)
	if state.inlineError != "" && state.validationTarget != nil && state.validationTarget.serviceID == serviceID && state.validationTarget.field == "module" {
		label(c, state.inlineError, 10, p.amber).FillWidth().Wrap()
	}
	if len(moduleIDs) == 0 {
		label(c, "此服务尚无模块。", 11, p.muted).FillWidth().Wrap()
	}
	for _, id := range moduleIDs {
		draft := state.moduleData[serviceID][id]
		if draft == nil {
			continue
		}
		expanded := s.taskCreationModuleExpanded(serviceID, id)
		s.renderTaskCreationModule(c.Key("task-create-module-editor-"+serviceID+"-"+id), p, service, draft, expanded)
		if state.preview != nil && state.preview.signature != s.taskCreationSignature() {
			s.invalidateTaskCreationPreview("模块配置已变化，请重新预检。")
		}
	}
	ui.Row(c).FillWidth().Padding(8, 0).Children(func() {
		if action(c, "task-create-module-add-"+serviceID, "plus", "添加临时模块", p, false) {
			s.addTaskCreationModule(serviceID, domain.WorkspaceStandardWorktree)
		}
	})
	if duplicates := duplicateTaskCreationCloneTargets(state.moduleOrder[serviceID], state.moduleData[serviceID]); len(duplicates) > 0 {
		label(c, "多个克隆模块使用相同目标分支："+strings.Join(duplicates, "、"), 10, p.amber).FillWidth().Wrap()
	}
}

func (s *app) taskCreationModuleExpanded(serviceID, moduleID string) bool {
	state := s.taskCreation
	if state == nil || moduleID == "" {
		return false
	}
	if state.moduleExpanded == nil {
		state.moduleExpanded = make(map[string]map[string]bool)
	}
	if state.moduleExpanded[serviceID] == nil {
		state.moduleExpanded[serviceID] = make(map[string]bool)
	}
	if expanded, initialized := state.moduleExpanded[serviceID][moduleID]; initialized {
		return expanded
	}
	state.moduleExpanded[serviceID][moduleID] = true
	return true
}

func (s *app) setTaskCreationModuleExpanded(serviceID, moduleID string, expanded bool) {
	state := s.taskCreation
	if state == nil || moduleID == "" {
		return
	}
	if state.moduleExpanded == nil {
		state.moduleExpanded = make(map[string]map[string]bool)
	}
	if state.moduleExpanded[serviceID] == nil {
		state.moduleExpanded[serviceID] = make(map[string]bool)
	}
	state.moduleExpanded[serviceID][moduleID] = expanded
}

func (s *app) renderTaskCreationModule(c *ui.Context, p palette, service domain.GroupServiceConfig, draft *taskCreationModuleDraft, expanded bool) {
	state := s.taskCreation
	id := service.ID + ":" + draft.ID
	s.syncTaskCreationModuleDraft(service.ID, draft)
	ui.Column(c.Key("task-create-module-section-"+id)).FillWidth().MinWidth(0).Gap(8).Padding(8, 0).
		Label("模块设置：" + service.ID + ":" + draft.ID).Children(func() {
		ui.Row(c.Key("task-create-module-heading-" + id)).FillWidth().MinWidth(0).Gap(8).AlignItems(ui.Center).Children(func() {
			if expanded {
				nameInput := ui.TextInput(c.Key("name"), &draft.Name).Grow(1).MinWidth(0).Height(34).Placeholder("例如 api-service").Label("模块名称 · " + id)
				if state.moduleFocus == id || state.moduleFieldFocus == id+":name" {
					nameInput.Focus().ScrollIntoView()
					state.moduleFocus = ""
					state.moduleFieldFocus = ""
				}
			} else {
				label(c, draft.Name, 12, p.text).Grow(1).SingleLine().FontWeight(600).Tooltip(draft.Name)
				if draft.Source == domain.TaskModuleTemporary {
					label(c, "临时", 10, p.amber).Shrink(0).NoWrap()
				}
			}
			toggleLabel := "展开模块：" + id
			toggleIcon := "chevron-right"
			if expanded {
				toggleLabel = "折叠模块：" + id
				toggleIcon = "chevron"
			}
			if iconButtonDisabled(c, "task-create-module-toggle-"+id, toggleIcon, toggleLabel, p, s.busy).Width(30).Height(30).Clicked() {
				s.setTaskCreationModuleExpanded(service.ID, draft.ID, !expanded)
			}
			if len(state.moduleOrder[service.ID]) > 1 && iconButtonDisabled(c, "task-create-module-delete-"+service.ID+"-"+draft.ID, "delete", "删除模块 "+draft.Name, p, s.busy).Width(30).Height(30).Clicked() {
				s.removeTaskCreationModule(service.ID, draft.ID)
			}
		})
		if expanded {
			if message := taskCreationModuleFieldError(state, service.ID, draft.ID, "name"); message != "" {
				label(c, message, 10, p.amber).FillWidth().Wrap()
			}
			s.syncTaskCreationModuleDraft(service.ID, draft)

			strategyRow := ui.Row(c.Key("task-create-strategy-" + id)).FillWidth().Gap(6).Wrap().AlignItems(ui.Center)
			if state.moduleFieldFocus == id+":strategy" {
				strategyRow.ScrollIntoView()
				state.moduleFieldFocus = ""
			}
			strategyRow.Children(func() {
				label(c, "工作区策略", 11, p.muted).Shrink(0)
				s.settingsChoice(c, p, "task-create-worktree-"+id, "Worktree", draft.Strategy == domain.WorkspaceStandardWorktree, func() {
					draft.Strategy = domain.WorkspaceStandardWorktree
					s.clearTaskCreationFieldError(service.ID, draft.ID, "strategy")
					s.invalidateTaskCreationPreview("模块策略已变化，请重新预检。")
				})
				s.settingsChoice(c, p, "task-create-clone-"+id, "独立克隆", draft.Strategy == domain.WorkspaceIndependentClone, func() {
					draft.Strategy = domain.WorkspaceIndependentClone
					s.clearTaskCreationFieldError(service.ID, draft.ID, "strategy")
					s.invalidateTaskCreationPreview("模块策略已变化，请重新预检。")
				})
			})
			if message := taskCreationModuleFieldError(state, service.ID, draft.ID, "strategy"); message != "" {
				label(c, message, 10, p.amber).FillWidth().Wrap()
			}

			if message := taskCreationModuleFieldError(state, service.ID, draft.ID, "baseRef"); message != "" {
				label(c, message, 10, p.amber).FillWidth().Wrap()
			}
			s.renderTaskRepositoryBranchPicker(c, p, taskRepositoryChoicesCreateOwner, state.token, "", service.ID, draft.ID, service.RepositoryID, draft, false)
			targetLabel := "目标分支"
			if draft.Strategy == domain.WorkspaceIndependentClone {
				targetLabel += "（可空）"
			} else {
				targetLabel += "（必填）"
			}
			ui.Column(c).FillWidth().Gap(4).Children(func() {
				label(c, targetLabel, 11, p.muted)
				target := ui.TextInput(c.Key("target"), &draft.TargetBranch).FillWidth().Height(34).
					Placeholder(firstNonEmpty(state.draft.Branch, "输入模块目标分支")).Label("模块目标分支 · " + id)
				if state.moduleFieldFocus == id+":targetBranch" {
					target.Focus().ScrollIntoView()
					state.moduleFieldFocus = ""
				}
			})
			if message := taskCreationModuleFieldError(state, service.ID, draft.ID, "targetBranch"); message != "" {
				label(c, message, 10, p.amber).FillWidth().Wrap()
			}
			s.syncTaskCreationModuleDraft(service.ID, draft)
		}
		ui.Divider(c)
	})
}

func taskCreationModuleFieldError(state *taskCreationState, serviceID, moduleID, field string) string {
	if state == nil || state.validationTarget == nil || state.validationTarget.serviceID != serviceID || state.validationTarget.moduleID != moduleID || state.validationTarget.field != field {
		return ""
	}
	return state.inlineError
}

func (s *app) clearTaskCreationFieldError(serviceID, moduleID, field string) {
	state := s.taskCreation
	if state == nil || state.validationTarget == nil || state.validationTarget.serviceID != serviceID || state.validationTarget.moduleID != moduleID {
		return
	}
	if field == "" || state.validationTarget.field == field {
		state.validationTarget = nil
		state.inlineError = ""
	}
}

func taskCreationServiceMatches(service domain.GroupServiceConfig, repositoryPath, query string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return true
	}
	for _, value := range []string{service.DisplayName, service.ID, service.RepositoryID, repositoryPath} {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}

func taskServiceDisplayName(s *app, service domain.GroupServiceConfig) string {
	if name := strings.TrimSpace(service.DisplayName); name != "" {
		return name
	}
	if s != nil {
		if name, _ := s.repositoryDetails(service.RepositoryID); strings.TrimSpace(name) != "" && name != service.RepositoryID {
			return name
		}
	}
	return "未命名服务"
}

func (s *app) focusTaskCreationConflict(conflict workspace.BranchReuseConflict) {
	state := s.taskCreation
	if state == nil {
		return
	}
	var moduleID string
	for _, candidateID := range state.moduleOrder[conflict.ServiceID] {
		draft := state.moduleData[conflict.ServiceID][candidateID]
		if draft == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(draft.Name), strings.TrimSpace(conflict.ModuleName)) || strings.EqualFold(strings.TrimSpace(draft.ID), strings.TrimSpace(conflict.ModuleName)) {
			moduleID = candidateID
			break
		}
		if strings.EqualFold(strings.TrimSpace(draft.TargetBranch), strings.TrimSpace(conflict.Key.Branch)) {
			moduleID = candidateID
		}
	}
	if moduleID == "" {
		s.focusTaskCreationValidationTarget(&taskCreationValidationTarget{serviceID: conflict.ServiceID, field: "module"})
		return
	}
	s.focusTaskCreationValidationTarget(&taskCreationValidationTarget{serviceID: conflict.ServiceID, moduleID: moduleID, field: "targetBranch"})
}
