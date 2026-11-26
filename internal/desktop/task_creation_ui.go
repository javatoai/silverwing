package desktop

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"

	"github.com/egoist/mygo/ui"
)

type taskCreationPreview struct {
	configurationIdentity, home string
	signature                   string
	payload                     map[string]any
	conflicts                   []workspace.BranchReuseConflict
	sharedAck                   bool
}

type taskCreationState struct {
	token             string
	title             string
	config            domain.AppConfig
	configRevision    uint64
	observerConfig    taskCreationObserverConfig
	renderingInputs   bool
	draft             domain.RequirementDraftState
	serviceSearch     string
	serviceListScroll ui.ScrollState
	activeServiceID   string
	requirements      *taskCreationRequirementsState
	services          map[string]bool
	modules           map[string]map[string]bool
	moduleData        map[string]map[string]*taskCreationModuleDraft
	moduleOrder       map[string][]string
	selectedModuleIDs map[string]string
	revealModuleTab   bool
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
	generatedPreview          *taskCreationGeneratedPreviewState
	agentPreview              *workspace.AgentDocumentPreview
	agentPreviewSig           string
	agentCodePreview          nativeCodePreviewState
	agentPreviewBusy          bool
	agentPreviewErr           string
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

var taskCreationSequence atomic.Uint64

func (s *app) initTaskCreation(title, requirementTitle, requirementURL string) {
	closeTaskCreationGeneratedPreview(s.taskUnit.state.taskCreation)
	s.cancelTaskCreationReads(s.taskUnit.state.taskCreation, false)
	s.closeTaskCreationRequirements()
	s.closeTaskCreationAINaming()
	cancelTaskRepositoryChoices(s.taskUnit.state.taskCreation)
	config := domain.DefaultAppConfig()
	configErr := json.Unmarshal([]byte(s.snapshot.ConfigJSON), &config)
	if configErr != nil {
		config = domain.DefaultAppConfig()
	}
	var selectedTitle *string
	if strings.TrimSpace(requirementTitle) != "" {
		selectedTitle = &requirementTitle
	}
	draft := domain.RequirementDraftState{RequirementLink: requirementURL, TaskName: requirementTitle}
	draft = draft.ChangeRequirement(requirementURL, config.DefaultBranchPrefix, selectedTitle)
	if strings.TrimSpace(requirementURL) == "" {
		draft.Branch = ""
		draft.MetadataHint = nil
	}
	state := &taskCreationState{
		token: fmt.Sprintf("%d", taskCreationSequence.Add(1)), title: title, config: config, configRevision: 1, draft: draft,
		services: make(map[string]bool), modules: make(map[string]map[string]bool), moduleData: make(map[string]map[string]*taskCreationModuleDraft), moduleOrder: make(map[string][]string),
		selectedModuleIDs: make(map[string]string), phase: "editing", aiEligible: strings.TrimSpace(requirementURL) != "", lastName: draft.TaskName,
		materialsPreview: domain.RequirementMaterialsDirectory{Status: domain.MaterialsNotRequested},
		lastBranch:       draft.Branch, lastLink: draft.RequirementLink,
	}
	if configErr != nil {
		state.configError = "无法读取当前任务配置：" + configErr.Error()
	}
	if strings.TrimSpace(requirementURL) == "" {
		state.taskFieldFocus = "taskName"
	}
	s.taskUnit.state.taskCreation = state
	s.syncTaskCreationLegacySelection()
	s.requestTaskCreationAINaming(false)
}

func duplicateTaskCreationCloneBranches(order []string, drafts map[string]*taskCreationModuleDraft) []string {
	counts := make(map[domain.RemoteBranchRef]int)
	var duplicates []string
	for _, id := range order {
		draft := drafts[id]
		if draft == nil || draft.Strategy != domain.WorkspaceIndependentClone {
			continue
		}
		ref, err := domain.ParseRemoteBranchRef(draft.BaseRef)
		if err == nil {
			counts[ref]++
			if counts[ref] == 2 {
				duplicates = append(duplicates, ref.String())
			}
		}
	}
	return duplicates
}

func (s *app) taskCreationService(serviceID string) *domain.ServiceConfig {
	state := s.taskUnit.state.taskCreation
	if state == nil {
		return nil
	}
	service, err := state.config.Service(serviceID)
	if err != nil {
		return nil
	}
	return &service
}

func (s *app) ensureTaskCreationService(serviceID string) {
	state := s.taskUnit.state.taskCreation
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
	for _, module := range service.Modules {
		state.modules[serviceID][module.ID] = true
		state.moduleData[serviceID][module.ID] = newTaskCreationModuleDraft(*service, module, state.draft.Branch)
		state.moduleOrder[serviceID] = append(state.moduleOrder[serviceID], module.ID)
	}
	if ids := state.moduleOrder[serviceID]; len(ids) > 0 {
		s.selectTaskCreationModule(serviceID, ids[0])
	}
	s.syncTaskCreationLegacySelection()
}

func (s *app) addTaskCreationModule(serviceID string, strategy domain.WorkspaceStrategy) {
	state := s.taskUnit.state.taskCreation
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
	s.setActiveTaskCreationService(serviceID)
	s.selectTaskCreationModule(serviceID, id)
	state.moduleFocus = serviceID + ":" + id
	s.retargetTaskCreationServiceModules(serviceID, state.draft.Branch)
	s.invalidateTaskCreationPreview("模块列表已变化，请重新预检。")
	s.syncTaskCreationLegacySelection()
}

func (s *app) removeTaskCreationModule(serviceID, moduleID string) {
	state := s.taskUnit.state.taskCreation
	if state == nil {
		return
	}
	order := state.moduleOrder[serviceID]
	index := slices.Index(order, moduleID)
	if len(order) <= 1 || index < 0 {
		return
	}
	wasSelected := state.selectedModuleIDs[serviceID] == moduleID
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
	order = slices.Delete(order, index, index+1)
	state.moduleOrder[serviceID] = order
	if wasSelected {
		nextID := order[min(index, len(order)-1)]
		s.selectTaskCreationModule(serviceID, nextID)
		if state.activeServiceID == serviceID {
			state.moduleFocus = serviceID + ":" + nextID
		}
	}
	s.retargetTaskCreationServiceModules(serviceID, state.draft.Branch)
	s.invalidateTaskCreationPreview("模块列表已变化，请重新预检。")
	s.syncTaskCreationLegacySelection()
}

func (s *app) setTaskCreationServiceSelected(serviceID string, selected bool) {
	state := s.taskUnit.state.taskCreation
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
			var nextServiceID string
			for _, candidate := range state.config.ServiceCatalog() {
				if candidate.Enabled && state.services[candidate.ID] {
					nextServiceID = candidate.ID
					break
				}
			}
			s.setActiveTaskCreationService(nextServiceID)
		}
	} else {
		s.ensureTaskCreationService(serviceID)
	}
	state.validationTarget = nil
	s.invalidateTaskCreationPreview("任务服务选择已变化，请重新预检。")
	s.syncTaskCreationLegacySelection()
}

func (s *app) toggleTaskCreationService(serviceID string) {
	state := s.taskUnit.state.taskCreation
	if state == nil {
		return
	}
	s.setTaskCreationServiceSelected(serviceID, !state.services[serviceID])
}

func (s *app) focusTaskCreationService(serviceID string) {
	state := s.taskUnit.state.taskCreation
	if state == nil {
		return
	}
	if state.activeServiceID == serviceID {
		s.setActiveTaskCreationService("")
		return
	}
	if !state.services[serviceID] {
		s.setTaskCreationServiceSelected(serviceID, true)
	}
	if state.services[serviceID] {
		s.setActiveTaskCreationService(serviceID)
	}
}

func (s *app) syncTaskCreationLegacySelection() {
	state := s.taskUnit.state.taskCreation
	if state == nil {
		return
	}
	s.taskUnit.state.taskServices = make(map[string]bool, len(state.services))
	s.taskUnit.state.taskModules = make(map[string]map[string]bool, len(state.modules))
	for serviceID, selected := range state.services {
		s.taskUnit.state.taskServices[serviceID] = selected
	}
	for serviceID, modules := range state.modules {
		copyModules := make(map[string]bool, len(modules))
		for moduleID, selected := range modules {
			copyModules[moduleID] = selected
		}
		s.taskUnit.state.taskModules[serviceID] = copyModules
	}
}

func (s *app) retargetUntouchedTaskCreationModules(feature string) {
	state := s.taskUnit.state.taskCreation
	if state == nil {
		return
	}
	for serviceID := range state.moduleData {
		s.retargetTaskCreationServiceModules(serviceID, feature)
	}
}

func (s *app) retargetTaskCreationServiceModules(serviceID, feature string) {
	state := s.taskUnit.state.taskCreation
	if state == nil {
		return
	}
	modules := state.moduleData[serviceID]
	for _, module := range modules {
		if module == nil {
			continue
		}
		module.taskModuleDraftValues = retargetTaskModuleDraftValues(module.taskModuleDraftValues, feature, len(modules))
	}
}

func (s *app) syncTaskCreationModuleDraft(serviceID string, draft *taskCreationModuleDraft) {
	if draft == nil {
		return
	}
	values, changes := syncTaskModuleDraftValues(draft.taskModuleDraftValues)
	draft.taskModuleDraftValues = values
	if changes.Name {
		s.retargetTaskCreationServiceModules(serviceID, s.taskUnit.state.taskCreation.draft.Branch)
	}
	if changes.any() {
		s.clearTaskCreationFieldError(serviceID, draft.ID, "")
		s.invalidateTaskCreationPreview("模块基础分支或目标分支已变化，请重新预检。")
	}
}

func (s *app) syncTaskCreationDraft() {
	state := s.taskUnit.state.taskCreation
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
		previous := state.draft
		previous.RequirementLink = state.lastLink
		s.cancelTaskCreationAINaming()
		state.draft = previous.ChangeRequirement(state.draft.RequirementLink, state.config.DefaultBranchPrefix, nil)
		state.lastLink = state.draft.RequirementLink
		state.lastName = state.draft.TaskName
		state.aiEligible = false // 手工输入的链接等需求读取成功后再启用 AI 命名。
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
	if !state.renderingInputs {
		s.checkTaskCreationPreviewSignature()
	}
}

func (s *app) checkTaskCreationPreviewSignature() {
	state := s.taskUnit.state.taskCreation
	if state != nil && state.preflight != nil && !s.taskCreationPreflightScopeRelevant(state.preflight) {
		s.retireStaleTaskCreationPreflight(state.preflight)
	}
	if state != nil && state.preview != nil && state.preview.configurationIdentity != "" {
		identity, err := s.taskCreationObserverFingerprint(state)
		if err != nil || identity != state.preview.configurationIdentity || state.preview.home != s.snapshot.Home {
			if state.preflight != nil {
				s.retireStaleTaskCreationPreflight(state.preflight)
			}
			s.invalidateTaskCreationPreview("任务配置已变化，请重新预检。")
			return
		}
	}
	if state != nil && state.preview != nil && state.preview.signature != s.taskCreationSignature() {
		if state.preflight != nil {
			s.retireStaleTaskCreationPreflight(state.preflight)
		}
		state.preview = nil
		state.phase = "editing"
		state.inlineError = "任务信息在预检后发生变化，请重新预检。"
	}
}

func (s *app) invalidateTaskCreationPreview(_ string) {
	state := s.taskUnit.state.taskCreation
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
	state := s.taskUnit.state.taskCreation
	if state == nil {
		return nil, fmt.Errorf("任务创建表单已关闭")
	}
	if state.requirements != nil && state.requirements.inputIsQuery {
		return nil, fmt.Errorf("请选择匹配需求，或填写完整需求链接。")
	}
	serviceIDs := make([]string, 0, len(state.services))
	selections := make([]workspace.TaskServiceSelection, 0, len(state.services))
	for _, service := range state.config.ServiceCatalog() {
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
				TargetBranch: draft.selectionTargetBranch(), Source: draft.Source,
				TagEnabled: draft.TagEnabled, TagMode: draft.TagMode, TagTargetRef: strings.TrimSpace(draft.TagTargetRef),
				TagMessagePrefix: draft.TagMessagePrefix,
			})
		}
		if len(modules) > 0 {
			serviceIDs = append(serviceIDs, service.ID)
			selections = append(selections, workspace.TaskServiceSelection{ServiceID: service.ID, Modules: modules})
		}
	}
	featureBranch := state.draft.Branch
	if domain.ContainsUnresolvedBranchPlaceholder(featureBranch) || (taskCreationHasSelectedServices(state) && !taskCreationHasSelectedWorktreeModules(state)) {
		featureBranch = ""
	}
	payload := map[string]any{
		"folderName": state.draft.TaskName, "featureBranch": featureBranch,
		"serviceIds": serviceIDs, "serviceSelections": selections,
		"requirementLink": state.draft.RequirementLink,
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
	state := s.taskUnit.state.taskCreation
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
	state := s.taskUnit.state.taskCreation
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
	state := s.taskUnit.state.taskCreation
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
	state := s.taskUnit.state.taskCreation
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
	hasWorktree := taskCreationHasSelectedWorktreeModules(state)
	if hasWorktree && strings.TrimSpace(state.draft.Branch) == "" {
		return "请填写任务分支。", &taskCreationValidationTarget{field: "taskBranch"}
	}
	if hasWorktree && domain.ContainsUnresolvedBranchPlaceholder(state.draft.Branch) {
		return "任务分支仍包含未解析的 {num} 或 {ai}，请修改需求编号/链接或手工调整分支。", &taskCreationValidationTarget{field: "taskBranch"}
	}
	for id, selected := range state.services {
		service := s.taskCreationService(id)
		if selected && (service == nil || !service.Enabled) {
			return "已选服务已不可用，请重新选择。", &taskCreationValidationTarget{field: "service"}
		}
	}
	for _, service := range state.config.ServiceCatalog() {
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
				label := "模块主分支"
				if draft.Strategy == domain.WorkspaceIndependentClone {
					label = "克隆分支"
				}
				return label + "不能为空：" + firstNonEmpty(draft.Name, draft.ID), &taskCreationValidationTarget{serviceID: service.ID, moduleID: moduleID, field: "baseRef"}
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
			if domain.ContainsUnresolvedBranchPlaceholder(draft.selectionTargetBranch()) {
				return "模块目标分支仍包含未解析的 {num} 或 {ai}：" + firstNonEmpty(draft.Name, draft.ID), &taskCreationValidationTarget{serviceID: service.ID, moduleID: moduleID, field: "targetBranch"}
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

func taskCreationHasSelectedWorktreeModules(state *taskCreationState) bool {
	for serviceID, selected := range state.services {
		if !selected {
			continue
		}
		for _, moduleID := range state.moduleOrder[serviceID] {
			if draft := state.moduleData[serviceID][moduleID]; draft != nil && draft.Strategy == domain.WorkspaceStandardWorktree {
				return true
			}
		}
	}
	return false
}

func (s *app) focusTaskCreationValidationTarget(target *taskCreationValidationTarget) {
	state := s.taskUnit.state.taskCreation
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
	s.setActiveTaskCreationService(target.serviceID)
	if target.moduleID != "" {
		s.selectTaskCreationModule(target.serviceID, target.moduleID)
		state.revealModuleTab = true
		if target.field == "baseRef" {
			if draft := state.moduleData[target.serviceID][target.moduleID]; draft != nil {
				draft.repositoryPicker.branchFocusPending = true
			}
		} else if target.field != "module" {
			state.moduleFieldFocus = target.serviceID + ":" + target.moduleID + ":" + target.field
		}
	}
}

func (s *app) beginTaskCreationPreflight() bool {
	state := s.taskUnit.state.taskCreation
	if state == nil {
		return false
	}
	s.syncTaskCreationDraft()
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
	s.cancelTaskCreationAINaming()
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

func (s *app) snapshotTaskCreationPreview(payload map[string]any) *taskCreationPreview {
	identity, _ := taskCreationSnapshotConfigFingerprint(s.snapshot, s.taskUnit.state.taskCreation.config)
	return &taskCreationPreview{
		configurationIdentity: identity, home: s.snapshot.Home,
		signature: s.taskCreationSignature(),
		payload:   payload,
	}
}

func (s *app) resumeTaskCreationPreflightIfPending() bool {
	state := s.taskUnit.state.taskCreation
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
	state := s.taskUnit.state.taskCreation
	if state == nil || state.preview == nil {
		return
	}
	if state.preview.signature != s.taskCreationSignature() {
		s.invalidateTaskCreationPreview("任务信息已变化，请重新预检。")
		return
	}
	if state.preview.configurationIdentity != "" {
		identity, err := taskCreationSnapshotConfigFingerprint(s.snapshot, state.config)
		if err != nil || identity != state.preview.configurationIdentity || state.preview.home != s.snapshot.Home {
			s.invalidateTaskCreationPreview("任务配置已变化，请重新预检。")
			return
		}
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
	return payload
}

func (s *app) refreshTaskCreationAgentPreview() {
	state := s.taskUnit.state.taskCreation
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
	state := s.taskUnit.state.taskCreation
	if state == nil || state.phase != "editing" || state.discardConfirmation {
		return
	}
	signature := s.taskCreationMaterialsPreviewSignature()
	if signature != state.materialsPreviewInputSig {
		state.materialsPreview = domain.RequirementMaterialsDirectory{Status: domain.MaterialsNotRequested}
		state.materialsPreviewErr = ""
		state.agentPreviewSig = ""
		state.agentPreview = nil
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
	state := s.taskUnit.state.taskCreation
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
	case "task.agent-preview":
		state.agentPreviewBusy = false
		if s.resumeTaskCreationPreflightIfPending() {
			return true
		}
		requestSignature := payloadString(action.Payload, "taskCreatePreviewSignature")
		if requestSignature != s.taskCreationAgentPreviewSignature() {
			state.agentPreview = nil
			state.agentPreviewSig = ""
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
		closeTaskCreationGeneratedPreview(state)
		s.closeTaskCreationRequirements()
		s.closeTaskCreationAINaming()
		s.stopTaskCreationPreviewDebounce()
		s.dialogOpen = false
		s.form = actionForm{}
		s.taskUnit.state.taskCreation = nil
		s.lastMessage = result.Message
		s.taskUnit.state.taskCreationWarning = ""
		if strings.TrimSpace(result.Text) != "" {
			s.taskUnit.state.taskCreationWarning = "任务已创建。\n" + result.Text
		}
		s.lastError = ""
		return true
	}
	return false
}

// taskCreationActionError is called by the desktop dispatcher before generic error handling.
func (s *app) taskCreationActionError(action application.Action, cause error) bool {
	state := s.taskUnit.state.taskCreation
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
			return true
		}
		state.materialsPreviewInputSig = requestSignature
		state.materialsPreview = domain.RequirementMaterialsDirectory{Status: domain.MaterialsNotRequested}
		state.materialsPreviewErr = message
		state.agentPreviewSig = ""
		s.refreshTaskCreationAgentPreview()
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
		s.failTaskCreationProgress(state, cause)
		state.preview = nil
		state.phase = "editing"
		state.inlineError = "创建前预检失败：" + message
		return true
	case "task.create":
		s.failTaskCreationProgress(state, cause)
		state.preview = nil
		state.phase = "editing"
		state.inlineError = "任务创建未完成，请修正后重新预检：" + message
		return true
	}
	return false
}

func taskCreationOwnedAction(kind string) bool {
	switch kind {
	case "task.materials-preview", "task.agent-preview", "requirements.ai-name", "task.create-preview", "task.create":
		return true
	default:
		return false
	}
}

// renderTaskCreationDialog replaces the generic action dialog for task creation.
func (s *app) renderTaskCreationDialog(c *ui.Context, p palette) bool {
	state := s.taskUnit.state.taskCreation
	if state == nil || !s.dialogOpen || s.form.mode != "task-create" {
		closeTaskCreationGeneratedPreview(state)
		s.cancelTaskCreationReads(state, false)
		s.closeTaskCreationRequirements()
		s.closeTaskCreationAINaming()
		return false
	}
	// Every input may synchronize dependent fields. Keep those synchronizations
	// cheap, then validate the complete live form once after all input builders.
	state.renderingInputs = true
	defer func() { state.renderingInputs = false }()
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
					fields := ui.Scroll(c).FillWidth().MinHeight(0).Padding(2).Gap(11).Label("任务基本信息").Focusable().Disabled(s.busy || inputsLocked)
					if state.creationProgress == nil {
						fields.Grow(1)
					}
					fields.Children(func() {
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
						if !taskCreationHasSelectedServices(state) || taskCreationHasSelectedWorktreeModules(state) {
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
						}
						s.syncTaskCreationDraft()
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
			s.checkTaskCreationPreviewSignature()
			inputsLocked = state.phase == "preview" || state.phase == "creating"
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
				ui.Row(c).Disabled(s.busy).Children(func() {
					if action(c, "task-create-generated-preview", "", "查看生成说明", p, false) {
						s.openTaskCreationGeneratedPreview(state)
					}
				})
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
	s.renderTaskCreationGeneratedPreview(c, p, state)
	return true
}

func (s *app) renderTaskCreationSelections(c *ui.Context, p palette) {
	state := s.taskUnit.state.taskCreation
	if state == nil {
		return
	}
	s.renderTaskCreationServiceList(c, p, state.config.ServiceCatalog())
}

func (s *app) renderTaskCreationServiceList(c *ui.Context, p palette, services []domain.ServiceConfig) {
	state := s.taskUnit.state.taskCreation
	if state == nil {
		return
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
		ui.TextInput(c.Key("task-create-service-search"), &state.serviceSearch).FillWidth().Height(34).
			Placeholder("搜索名称、路径或 ID").Label("搜索服务名称、仓库路径或 ID")
		if state.inlineError != "" && state.validationTarget != nil && state.validationTarget.field == "service" {
			label(c, state.inlineError, 10, p.amber).FillWidth().Wrap()
		}
		activeVisible := false
		if state.activeServiceID != "" && strings.TrimSpace(state.serviceSearch) != "" {
			for _, service := range services {
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
		ui.Scroll(c).TrackScroll(&state.serviceListScroll).Grow(1).FillWidth().MinHeight(0).Gap(5).Label("任务服务列表").Children(func() {
			visible := 0
			for _, service := range services {
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
					s.renderTaskCreationServiceModules(c, p, service)
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
	state := s.taskUnit.state.taskCreation
	if state == nil {
		return
	}
	serviceID := service.ID
	key := [3]string{"task-create-service-modules", state.token, serviceID}
	ui.Column(c.Key(key)).FillWidth().MinWidth(0).Shrink(0).Gap(5).Children(func() {
		moduleIDs := state.moduleOrder[serviceID]
		if len(moduleIDs) > 0 {
			selected := max(0, slices.Index(moduleIDs, state.selectedModuleIDs[serviceID]))
			s.selectTaskCreationModule(serviceID, moduleIDs[selected])
			s.renderTaskCreationModuleTabs(c, p, serviceID, selected)
		} else {
			state.revealModuleTab = false
		}
		if state.inlineError != "" && state.validationTarget != nil && state.validationTarget.serviceID == serviceID && state.validationTarget.field == "module" {
			label(c, state.inlineError, 10, p.amber).FillWidth().Wrap()
		}
		if len(moduleIDs) == 0 {
			label(c, "此服务尚无模块。", 11, p.muted).FillWidth().Wrap()
		} else if draft := state.moduleData[serviceID][state.selectedModuleIDs[serviceID]]; draft != nil {
			s.renderTaskCreationModule(c, p, service, draft)
		}
		ui.Row(c).FillWidth().Padding(8, 0).Children(func() {
			if action(c, "task-create-module-add-"+serviceID, "plus", "添加临时模块", p, false) {
				s.addTaskCreationModule(serviceID, domain.WorkspaceStandardWorktree)
				c.Invalidate()
			}
		})
		if duplicates := duplicateTaskCreationCloneBranches(state.moduleOrder[serviceID], state.moduleData[serviceID]); len(duplicates) > 0 {
			label(c, "多个克隆模块跟踪同一远程分支："+strings.Join(duplicates, "、"), 10, p.amber).FillWidth().Wrap()
		}
	})
}

func (s *app) renderTaskCreationModule(c *ui.Context, p palette, service domain.GroupServiceConfig, draft *taskCreationModuleDraft) {
	state := s.taskUnit.state.taskCreation
	id := service.ID + ":" + draft.ID
	remove := false
	s.syncTaskCreationModuleDraft(service.ID, draft)
	ui.Column(c.Key("task-create-module-section-"+id)).FillWidth().MinWidth(0).Gap(8).Padding(8, 0).
		Label("模块设置：" + id).Children(func() {
		ui.Row(c.Key("task-create-module-heading-" + id)).FillWidth().MinWidth(0).Gap(8).AlignItems(ui.Center).Children(func() {
			nameInput := ui.TextInput(c.Key("name"), &draft.Name).Grow(1).MinWidth(0).Height(34).Placeholder("例如 api-service").Label("模块名称 · " + id)
			if state.moduleFocus == id || state.moduleFieldFocus == id+":name" {
				nameInput.Focus().ScrollIntoView()
				state.moduleFocus = ""
				state.moduleFieldFocus = ""
			}
			remove = len(state.moduleOrder[service.ID]) > 1 && iconButtonDisabled(c, "task-create-module-delete-"+service.ID+"-"+draft.ID, "delete", "删除模块 "+draft.Name, p, s.busy).Width(30).Height(30).Clicked()
		})
		if remove {
			return
		}
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
				s.retargetTaskCreationServiceModules(service.ID, state.draft.Branch)
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
		if draft.Strategy == domain.WorkspaceStandardWorktree {
			ui.Column(c).FillWidth().Gap(4).Children(func() {
				label(c, "目标分支（必填）", 11, p.muted)
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
		}
		s.syncTaskCreationModuleDraft(service.ID, draft)
		ui.Divider(c)
	})
	if remove {
		s.removeTaskCreationModule(service.ID, draft.ID)
		c.Invalidate()
	}
}

func taskCreationModuleFieldError(state *taskCreationState, serviceID, moduleID, field string) string {
	if state == nil || state.validationTarget == nil || state.validationTarget.serviceID != serviceID || state.validationTarget.moduleID != moduleID || state.validationTarget.field != field {
		return ""
	}
	return state.inlineError
}

func (s *app) clearTaskCreationFieldError(serviceID, moduleID, field string) {
	state := s.taskUnit.state.taskCreation
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
	state := s.taskUnit.state.taskCreation
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
	field := "targetBranch"
	if state.moduleData[conflict.ServiceID][moduleID].Strategy == domain.WorkspaceIndependentClone {
		field = "baseRef"
	}
	s.focusTaskCreationValidationTarget(&taskCreationValidationTarget{serviceID: conflict.ServiceID, moduleID: moduleID, field: field})
}
