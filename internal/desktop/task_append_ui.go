package desktop

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"

	"github.com/egoist/mygo/ui"
)

type taskAppendUIState struct {
	token, taskID, taskUpdated, taskPath, taskBranch  string
	mode, serviceID, serviceQuery                     string
	activeServiceID                                   string
	open, pending, sharedAck                          bool
	pendingAction, pendingSignature, previewSignature string
	inlineError                                       string
	group                                             domain.GroupConfig
	serviceOrder                                      []string
	services                                          map[string]domain.GroupServiceConfig
	selectedServices                                  map[string]bool
	moduleData                                        map[string]map[string]*taskCreationModuleDraft
	moduleOrder                                       map[string][]string
	moduleOptions                                     []domain.ServiceModuleConfig
	moduleDraftID                                     string
	nextModuleID                                      uint64
	conflicts                                         []workspace.BranchReuseConflict
}

func (s *app) taskAppendGroup(t task) (domain.GroupConfig, error) {
	if t.groupID == "" {
		return domain.GroupConfig{}, fmt.Errorf("任务没有关联项目组，无法添加服务或模块")
	}
	config := domain.DefaultAppConfig()
	if err := json.Unmarshal([]byte(s.snapshot.ConfigJSON), &config); err != nil {
		return domain.GroupConfig{}, fmt.Errorf("无法读取任务项目组配置：%w", err)
	}
	for _, group := range config.Groups {
		if group.ID == t.groupID {
			return group, nil
		}
	}
	return domain.GroupConfig{}, fmt.Errorf("找不到任务所属项目组：%s", t.groupID)
}

func taskAppendNewState(t task, mode string, group domain.GroupConfig) *taskAppendUIState {
	return &taskAppendUIState{
		token: newDesktopEditorID("task-append"), taskID: t.id, taskUpdated: t.updated,
		taskPath: t.path, taskBranch: t.branch, mode: mode, open: true, group: group,
		services: make(map[string]domain.GroupServiceConfig), selectedServices: make(map[string]bool),
		moduleData:  make(map[string]map[string]*taskCreationModuleDraft),
		moduleOrder: make(map[string][]string),
	}
}

func (s *app) openTaskAppendServices(t task) {
	if t.id == "" || t.archived {
		return
	}
	group, err := s.taskAppendGroup(t)
	if err != nil {
		s.lastError = err.Error()
		return
	}
	state := taskAppendNewState(t, "services", group)
	used := taskUsedServiceIDs(t)
	for _, service := range group.Services {
		if !service.Enabled || used[service.ID] {
			continue
		}
		state.serviceOrder = append(state.serviceOrder, service.ID)
		state.services[service.ID] = service
	}
	if len(state.serviceOrder) == 0 {
		s.lastError = "当前项目组没有可添加的服务。"
		return
	}
	state.activeServiceID = state.serviceOrder[0]
	cancelTaskAppendRepositoryChoices(s.workspaceAppend)
	s.workspaceAppend = state
	s.dialogOpen = false
	s.form = actionForm{}
	s.lastError, s.lastMessage = "", ""
}

func (s *app) openTaskAppendModules(t task, serviceID string) {
	if t.id == "" || t.archived {
		return
	}
	group, err := s.taskAppendGroup(t)
	if err != nil {
		s.lastError = err.Error()
		return
	}
	var service *domain.GroupServiceConfig
	for i := range group.Services {
		if group.Services[i].ID == serviceID {
			service = &group.Services[i]
			break
		}
	}
	if service == nil {
		s.lastError = "找不到当前服务的配置。"
		return
	}
	state := taskAppendNewState(t, "modules", group)
	state.serviceID = serviceID
	state.activeServiceID = serviceID
	state.services[serviceID] = *service
	used := taskUsedModuleIDs(t, serviceID)
	for _, module := range service.Modules {
		if !used[strings.ToLower(module.ID)] {
			state.moduleOptions = append(state.moduleOptions, module)
		}
	}
	if len(state.moduleOptions) > 0 {
		s.selectTaskAppendConfiguredModule(state, state.moduleOptions[0])
	} else {
		s.replaceTaskAppendModuleWithTemporary(state, domain.WorkspaceStandardWorktree)
	}
	cancelTaskAppendRepositoryChoices(s.workspaceAppend)
	s.workspaceAppend = state
	s.dialogOpen = false
	s.form = actionForm{}
	s.lastError, s.lastMessage = "", ""
}

func taskUsedServiceIDs(t task) map[string]bool {
	used := make(map[string]bool, len(t.serviceIDs)+len(t.workspaces))
	for _, id := range t.serviceIDs {
		used[id] = true
	}
	for _, item := range t.workspaces {
		if item.ServiceID != "" {
			used[item.ServiceID] = true
		}
	}
	return used
}

func taskUsedModuleIDs(t task, serviceID string) map[string]bool {
	used := make(map[string]bool)
	for _, item := range t.workspaces {
		if item.ServiceID == serviceID && item.ModuleID != "" {
			used[strings.ToLower(item.ModuleID)] = true
		}
	}
	return used
}

func (s *app) toggleTaskAppendService(serviceID string) {
	state := s.workspaceAppend
	if state == nil || state.mode != "services" || state.pending || s.busy {
		return
	}
	if state.selectedServices[serviceID] {
		for _, draft := range state.moduleData[serviceID] {
			if draft != nil {
				cancelTaskRepositoryPickerRequests(&draft.repositoryPicker)
			}
		}
		delete(state.selectedServices, serviceID)
		if state.activeServiceID == serviceID {
			state.activeServiceID = ""
			for _, candidate := range state.serviceOrder {
				if state.selectedServices[candidate] {
					state.activeServiceID = candidate
					break
				}
			}
		}
	} else {
		service, ok := state.services[serviceID]
		if !ok {
			return
		}
		state.selectedServices[serviceID] = true
		state.activeServiceID = serviceID
		if _, initialized := state.moduleData[serviceID]; !initialized {
			s.initializeTaskAppendServiceModules(state, service)
		}
	}
	s.invalidateTaskAppendPreview("服务或模块选择已变化，请重新预检。")
}

func (s *app) initializeTaskAppendServiceModules(state *taskAppendUIState, service domain.GroupServiceConfig) {
	state.moduleData[service.ID] = make(map[string]*taskCreationModuleDraft, len(service.Modules))
	state.moduleOrder[service.ID] = make([]string, 0, len(service.Modules))
	for _, module := range service.Modules {
		draft := newTaskCreationModuleDraft(service, module, state.taskBranch)
		state.moduleData[service.ID][module.ID] = draft
		state.moduleOrder[service.ID] = append(state.moduleOrder[service.ID], module.ID)
	}
}

func (s *app) addTaskAppendTemporaryModule(serviceID string, strategy domain.WorkspaceStrategy) {
	state := s.workspaceAppend
	if state == nil || state.mode != "services" || state.pending {
		return
	}
	service, ok := state.services[serviceID]
	if !ok || !state.selectedServices[serviceID] {
		return
	}
	if _, initialized := state.moduleData[serviceID]; !initialized {
		s.initializeTaskAppendServiceModules(state, service)
	}
	state.nextModuleID++
	namePrefix, idPrefix := "module", "module"
	if strategy == domain.WorkspaceIndependentClone {
		namePrefix, idPrefix = "clone", "clone"
	}
	name := fmt.Sprintf("%s-%d", namePrefix, len(state.moduleOrder[serviceID])+1)
	module := domain.ServiceModuleConfig{ID: newDesktopEditorID(idPrefix), Name: name, Strategy: strategy}
	draft := newTaskCreationModuleDraft(service, module, state.taskBranch)
	draft.Source, draft.TagEnabled, draft.TagTargetRef = domain.TaskModuleTemporary, false, ""
	state.moduleData[serviceID][draft.ID] = draft
	state.moduleOrder[serviceID] = append(state.moduleOrder[serviceID], draft.ID)
	s.retargetTaskAppendModules(state, serviceID)
	s.invalidateTaskAppendPreview("模块列表已变化，请重新预检。")
}

func (s *app) removeTaskAppendModule(serviceID, moduleID string) {
	state := s.workspaceAppend
	if state == nil || state.mode != "services" || state.pending || len(state.moduleOrder[serviceID]) <= 1 {
		return
	}
	if draft := state.moduleData[serviceID][moduleID]; draft != nil {
		cancelTaskRepositoryPickerRequests(&draft.repositoryPicker)
	}
	delete(state.moduleData[serviceID], moduleID)
	order := state.moduleOrder[serviceID][:0]
	for _, id := range state.moduleOrder[serviceID] {
		if id != moduleID {
			order = append(order, id)
		}
	}
	state.moduleOrder[serviceID] = order
	s.retargetTaskAppendModules(state, serviceID)
	s.invalidateTaskAppendPreview("模块列表已变化，请重新预检。")
}

func (s *app) retargetTaskAppendModules(state *taskAppendUIState, serviceID string) {
	order := state.moduleOrder[serviceID]
	for _, id := range order {
		draft := state.moduleData[serviceID][id]
		if draft == nil || draft.TargetEdited {
			continue
		}
		name := strings.TrimSpace(draft.Name)
		if name == "" {
			name = draft.ID
		}
		if state.mode == "modules" {
			draft.TargetBranch = taskCreationTargetBranchForCount(state.taskBranch, name, draft.ID, 2)
		} else {
			draft.TargetBranch = taskCreationTargetBranchForCount(state.taskBranch, name, draft.ID, len(order))
		}
		draft.LastTargetBranch = draft.TargetBranch
	}
}

func (s *app) selectTaskAppendConfiguredModule(state *taskAppendUIState, module domain.ServiceModuleConfig) {
	service := state.services[state.serviceID]
	for _, draft := range state.moduleData[state.serviceID] {
		if draft != nil {
			cancelTaskRepositoryPickerRequests(&draft.repositoryPicker)
		}
	}
	draft := newTaskCreationModuleDraft(service, module, state.taskBranch)
	draft.TargetBranch = taskCreationTargetBranchForCount(state.taskBranch, taskCreationModuleName(module), module.ID, 2)
	draft.LastTargetBranch = draft.TargetBranch
	state.moduleData[state.serviceID] = map[string]*taskCreationModuleDraft{draft.ID: draft}
	state.moduleOrder[state.serviceID] = []string{draft.ID}
	state.moduleDraftID = draft.ID
	s.invalidateTaskAppendPreview("模块选择已变化，请重新预检。")
}

func (s *app) replaceTaskAppendModuleWithTemporary(state *taskAppendUIState, strategy domain.WorkspaceStrategy) {
	service := state.services[state.serviceID]
	for _, draft := range state.moduleData[state.serviceID] {
		if draft != nil {
			cancelTaskRepositoryPickerRequests(&draft.repositoryPicker)
		}
	}
	name, prefix := "module", "module"
	if strategy == domain.WorkspaceIndependentClone {
		name, prefix = "clone", "clone"
	}
	module := domain.ServiceModuleConfig{ID: newDesktopEditorID(prefix), Name: name, Strategy: strategy}
	draft := newTaskCreationModuleDraft(service, module, state.taskBranch)
	draft.Source, draft.TagEnabled, draft.TagTargetRef = domain.TaskModuleTemporary, false, ""
	draft.TargetBranch = taskCreationTargetBranchForCount(state.taskBranch, name, draft.ID, 2)
	draft.LastTargetBranch = draft.TargetBranch
	state.moduleData[state.serviceID] = map[string]*taskCreationModuleDraft{draft.ID: draft}
	state.moduleOrder[state.serviceID] = []string{draft.ID}
	state.moduleDraftID = draft.ID
	s.invalidateTaskAppendPreview("模块选择已变化，请重新预检。")
}

func (s *app) syncTaskAppendModuleDraft(state *taskAppendUIState, serviceID string, draft *taskCreationModuleDraft) {
	if draft == nil {
		return
	}
	nameChanged := draft.Name != draft.LastName
	baseRefChanged := draft.BaseRef != draft.LastBaseRef
	baseRemoteChanged := draft.BaseRemote != draft.LastBaseRemote
	targetChanged := draft.TargetBranch != draft.LastTargetBranch
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
	if targetChanged {
		draft.TargetEdited = true
	}
	draft.LastName = draft.Name
	draft.LastBaseRemote, draft.LastBaseRef, draft.LastTargetBranch = draft.BaseRemote, draft.BaseRef, draft.TargetBranch
	if nameChanged {
		s.retargetTaskAppendModules(state, serviceID)
	}
	if nameChanged || baseRefChanged || baseRemoteChanged || targetChanged {
		s.invalidateTaskAppendPreview("模块基础分支或目标分支已变化，请重新预检。")
	}
}

func (s *app) invalidateTaskAppendPreview(_ string) {
	state := s.workspaceAppend
	if state == nil {
		return
	}
	state.previewSignature = ""
	state.conflicts = nil
	state.sharedAck = false
	if !state.pending {
		state.inlineError = ""
	}
}

func (s *app) taskAppendRequest() (map[string]any, string, error) {
	state := s.workspaceAppend
	if state == nil {
		return nil, "", fmt.Errorf("追加工作区表单已关闭")
	}
	t := s.taskByID(state.taskID)
	if t.id == "" || t.archived || t.updated != state.taskUpdated || t.path != state.taskPath || t.branch != state.taskBranch || s.currentTask().id != state.taskID {
		return nil, "", fmt.Errorf("任务状态已变化，请重新打开添加表单")
	}
	base := map[string]any{}
	switch state.mode {
	case "services":
		serviceIDs := make([]string, 0)
		selections := make([]workspace.TaskServiceSelection, 0)
		for _, serviceID := range state.serviceOrder {
			if !state.selectedServices[serviceID] {
				continue
			}
			service := state.services[serviceID]
			modules, err := s.taskAppendModuleSelections(state, serviceID)
			if err != nil {
				return nil, "", err
			}
			if len(modules) == 0 {
				return nil, "", fmt.Errorf("服务“%s”至少需要保留一个模块", firstNonEmpty(service.DisplayName, serviceID))
			}
			serviceIDs = append(serviceIDs, serviceID)
			selections = append(selections, workspace.TaskServiceSelection{ServiceID: serviceID, Modules: modules})
		}
		if len(serviceIDs) == 0 {
			return nil, "", fmt.Errorf("请选择至少一个服务")
		}
		base["serviceIds"], base["serviceSelections"] = serviceIDs, selections
		base["baseOverrides"] = []workspace.ModuleBaseOverride{}
	case "modules":
		modules, err := s.taskAppendModuleSelections(state, state.serviceID)
		if err != nil {
			return nil, "", err
		}
		if len(modules) != 1 {
			return nil, "", fmt.Errorf("请选择一个要添加的模块")
		}
		base["serviceId"], base["modules"] = state.serviceID, modules
	default:
		return nil, "", fmt.Errorf("未知的追加模式")
	}
	signatureInput := map[string]any{
		"taskId": t.id, "taskUpdated": t.updated, "taskPath": t.path, "taskBranch": t.branch,
		"mode": state.mode, "request": base,
	}
	encoded, err := json.Marshal(signatureInput)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(encoded)
	return base, hex.EncodeToString(digest[:]), nil
}

func (s *app) taskAppendModuleSelections(state *taskAppendUIState, serviceID string) ([]workspace.TaskModuleSelection, error) {
	selections := make([]workspace.TaskModuleSelection, 0, len(state.moduleOrder[serviceID]))
	for _, id := range state.moduleOrder[serviceID] {
		draft := state.moduleData[serviceID][id]
		if draft == nil {
			return nil, fmt.Errorf("模块配置无效，请重新选择服务")
		}
		if strings.TrimSpace(draft.Name) == "" {
			return nil, fmt.Errorf("模块名不能为空")
		}
		ref, err := domain.ParseRemoteBranchRef(strings.TrimSpace(draft.BaseRef))
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(draft.BaseRemote) == "" || ref.Remote != strings.TrimSpace(draft.BaseRemote) {
			return nil, fmt.Errorf("模块主分支远程与引用不一致：%s", draft.BaseRef)
		}
		if draft.Strategy != domain.WorkspaceStandardWorktree && draft.Strategy != domain.WorkspaceIndependentClone {
			return nil, fmt.Errorf("请选择有效的模块工作区策略")
		}
		if draft.Strategy == domain.WorkspaceStandardWorktree && strings.TrimSpace(draft.TargetBranch) == "" {
			return nil, fmt.Errorf("Worktree 模块目标分支不能为空：%s", draft.Name)
		}
		selections = append(selections, workspace.TaskModuleSelection{
			ID: draft.ID, Name: strings.TrimSpace(draft.Name), Strategy: draft.Strategy,
			BaseRef: strings.TrimSpace(draft.BaseRef), BaseRemote: strings.TrimSpace(draft.BaseRemote),
			TargetBranch: strings.TrimSpace(draft.TargetBranch), Source: draft.Source,
			TagEnabled: draft.TagEnabled, TagMode: draft.TagMode, TagTargetRef: strings.TrimSpace(draft.TagTargetRef),
			TagMessagePrefix: draft.TagMessagePrefix,
		})
	}
	return selections, nil
}

func (s *app) beginTaskAppendPreflight() {
	state := s.workspaceAppend
	if state == nil || state.pending || s.busy {
		return
	}
	payload, signature, err := s.taskAppendRequest()
	if err != nil {
		state.inlineError = err.Error()
		return
	}
	cancelTaskAppendRepositoryChoices(state)
	state.pending, state.pendingAction, state.pendingSignature = true, taskAppendPreviewAction(state.mode), signature
	state.previewSignature, state.conflicts, state.inlineError = "", nil, "正在检查分支复用和工作区冲突…"
	payload["taskAppendToken"], payload["taskAppendSignature"] = state.token, signature
	s.act(state.pendingAction, state.taskID, state.serviceID, "", "", "", payload)
}

func taskAppendPreviewAction(mode string) string {
	if mode == "modules" {
		return "task.modules-add-preview"
	}
	return "task.services-add-preview"
}

func taskAppendAddAction(mode string) string {
	if mode == "modules" {
		return "task.modules-add"
	}
	return "task.services-add"
}

func (s *app) submitTaskAppend(keys []workspace.BranchReuseKey) {
	state := s.workspaceAppend
	if state == nil || state.pending || s.busy {
		return
	}
	payload, signature, err := s.taskAppendRequest()
	if err != nil {
		state.inlineError = err.Error()
		return
	}
	if state.previewSignature != signature {
		s.invalidateTaskAppendPreview("表单内容已变化，请重新预检。")
		return
	}
	cancelTaskAppendRepositoryChoices(state)
	payload["confirmedBranchReuseKeys"] = append([]workspace.BranchReuseKey(nil), keys...)
	payload["taskAppendToken"], payload["taskAppendSignature"] = state.token, signature
	state.pending, state.pendingAction, state.pendingSignature = true, taskAppendAddAction(state.mode), signature
	state.inlineError = "正在添加服务和准备工作区…"
	s.act(state.pendingAction, state.taskID, state.serviceID, "", "", "", payload)
}

func (s *app) taskAppendActionResult(action application.Action, result application.ActionResult) bool {
	if !taskAppendOwnedAction(action.Kind) {
		return false
	}
	state := s.workspaceAppend
	if state == nil || payloadString(action.Payload, "taskAppendToken") != state.token {
		return true
	}
	if state.pendingAction != action.Kind || payloadString(action.Payload, "taskAppendSignature") != state.pendingSignature {
		return true
	}
	state.pending = false
	state.pendingAction = ""
	switch action.Kind {
	case "task.services-add-preview", "task.modules-add-preview":
		if !state.open {
			s.workspaceAppend = nil
			return true
		}
		_, currentSignature, err := s.taskAppendRequest()
		if err != nil || currentSignature != state.pendingSignature {
			state.previewSignature, state.conflicts = "", nil
			state.inlineError = "表单或任务状态在预检期间发生变化，请重新检查。"
			return true
		}
		conflicts, err := decodeData[workspace.BranchReuseConflict](result.Data)
		if err != nil {
			state.inlineError = "分支复用预检结果格式错误：" + err.Error()
			state.previewSignature = ""
			return true
		}
		state.previewSignature, state.conflicts, state.inlineError = currentSignature, conflicts, ""
		if len(conflicts) == 0 {
			s.submitTaskAppend(nil)
		}
	case "task.services-add", "task.modules-add":
		s.workspaceAppend = nil
		s.lastError = ""
		s.lastMessage = firstNonEmpty(result.Message, "工作区已添加。")
	case "task.services-retry":
		s.workspaceAppend = nil
		s.lastError = ""
		s.lastMessage = firstNonEmpty(result.Message, "失败服务已重试。")
	}
	return true
}

func (s *app) taskAppendActionError(action application.Action, cause error) bool {
	if !taskAppendOwnedAction(action.Kind) {
		return false
	}
	state := s.workspaceAppend
	if state == nil || payloadString(action.Payload, "taskAppendToken") != state.token {
		return true
	}
	if state.pendingAction != action.Kind || payloadString(action.Payload, "taskAppendSignature") != state.pendingSignature {
		return true
	}
	state.pending, state.pendingAction = false, ""
	state.inlineError = "操作失败：" + cause.Error()
	if action.Kind == "task.services-add" || action.Kind == "task.modules-add" {
		state.previewSignature, state.conflicts, state.sharedAck = "", nil, false
		state.inlineError += " 请重新预检后重试。"
	}
	if action.Kind == "task.services-retry" {
		s.lastError = state.inlineError
		s.workspaceAppend = nil
	}
	return true
}

func taskAppendOwnedAction(kind string) bool {
	switch kind {
	case "task.services-add-preview", "task.modules-add-preview", "task.services-add", "task.modules-add", "task.services-retry":
		return true
	default:
		return false
	}
}

func taskAppendRefreshesWorkspaceHealth(kind string) bool {
	switch kind {
	case "task.services-add", "task.modules-add", "task.services-retry", "task.tools-retry":
		return true
	default:
		return false
	}
}

func (s *app) taskFailedServiceIDs(t task) []string {
	seen := make(map[string]bool)
	failed := make([]string, 0)
	for _, item := range t.workspaces {
		if item.ServiceID == "" || seen[item.ServiceID] || !strings.EqualFold(item.State, "FAILED") {
			continue
		}
		seen[item.ServiceID] = true
		failed = append(failed, item.ServiceID)
	}
	return failed
}

func (s *app) retryFailedTaskServices(t task, serviceIDs []string) {
	if t.id == "" || t.archived || s.busy || len(s.taskFailedServiceIDs(t)) == 0 {
		return
	}
	state := taskAppendNewState(t, "retry", domain.GroupConfig{})
	state.open = false
	payload := map[string]any{"taskAppendToken": state.token}
	if len(serviceIDs) > 0 {
		payload["serviceIds"] = append([]string(nil), serviceIDs...)
	}
	signatureBytes, _ := json.Marshal(map[string]any{"taskId": t.id, "taskUpdated": t.updated, "serviceIds": serviceIDs})
	digest := sha256.Sum256(signatureBytes)
	state.pendingSignature = hex.EncodeToString(digest[:])
	state.pendingAction, state.pending = "task.services-retry", true
	payload["taskAppendSignature"] = state.pendingSignature
	cancelTaskAppendRepositoryChoices(s.workspaceAppend)
	s.workspaceAppend = state
	s.act("task.services-retry", t.id, "", "", "", "", payload)
}

func (s *app) renderTaskAppendDialog(c *ui.Context, p palette) bool {
	state := s.workspaceAppend
	if state == nil || !state.open {
		return false
	}
	t := s.taskByID(state.taskID)
	if t.id == "" || t.archived || t.updated != state.taskUpdated || t.path != state.taskPath || t.branch != state.taskBranch || s.currentTask().id != state.taskID {
		state.open = false
		state.inlineError = "任务状态已变化，请重新打开添加表单。"
		if !state.pending {
			cancelTaskAppendRepositoryChoices(state)
			s.workspaceAppend = nil
			return false
		}
	}
	open := state.open
	ui.DialogBase(c.Key("task-append-dialog"), &open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.32))
		width, height := c.Size()
		panel.Width(min(1120, width-24)).MaxHeight(min(900, height-24)).Padding(16).Gap(10).Background(p.surface).Border(1, p.line).Radius(18)
		panel.Children(func() {
			title := "添加服务"
			if state.mode == "modules" {
				title = "为服务添加模块"
			}
			ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Shrink(0).Children(func() {
				label(c, title, 20, p.text).FontWeight(600).Grow(1).SingleLine()
				label(c, "沿用任务分支", 11, p.muted).SingleLine().Tooltip("新增工作区沿用任务分支：" + state.taskBranch)
			})
			ui.Divider(c).Shrink(0)
			ui.Row(c).FillWidth().Grow(1).MinHeight(0).Gap(12).AlignItems(ui.Stretch).Children(func() {
				ui.Scroll(c).Width(280).MinWidth(248).MaxWidth(300).MinHeight(0).Padding(2).Gap(8).Children(func() {
					if state.mode == "services" {
						s.renderTaskAppendServices(c, p, state)
					} else {
						s.renderTaskAppendModules(c, p, state)
					}
				})
				ui.Scroll(c).Grow(1).MinWidth(0).MinHeight(0).Padding(2).Gap(10).Children(func() {
					if state.mode == "services" {
						s.renderTaskAppendServiceConfiguration(c, p, state)
					} else {
						s.renderTaskAppendModuleConfiguration(c, p, state)
					}
					if state.inlineError != "" {
						label(c, state.inlineError, 11, p.amber).Wrap()
					}
					if len(state.conflicts) > 0 {
						s.renderTaskAppendConflicts(c, p, state)
					}
				})
			})
			_, signature, signatureErr := s.taskAppendRequest()
			if !state.pending && state.previewSignature != "" && (signatureErr != nil || signature != state.previewSignature) {
				s.invalidateTaskAppendPreview("表单内容已变化，请重新预检。")
			}
			ui.Row(c).FillWidth().Gap(8).Shrink(0).Wrap().Justify(ui.End).Children(func() {
				cancel := ui.ButtonBase(c.Key("task-append-cancel")).Height(34).Padding(0, 16).Radius(18).Label("取消").Disabled(state.pending)
				cancel.Children(func() { label(c, "取消", 12, p.text) })
				if cancel.Clicked() {
					open = false
				}
				if state.previewSignature != "" && len(state.conflicts) > 0 {
					if taskAppendButton(c, "task-append-back", "返回修改", p, false, s.busy || state.pending) {
						s.invalidateTaskAppendPreview("")
						state.inlineError = ""
					}
					locked, occupied := taskAppendConflictFlags(state.conflicts)
					if occupied {
						s.settingsChoice(c, p, "task-append-shared-ack", "我了解已检出的分支会在多个 Worktree 间共享", state.sharedAck, func() {
							state.sharedAck = !state.sharedAck
						})
					}
					confirm := ui.ButtonBase(c.Key("task-append-confirm-reuse")).Height(34).Padding(0, 16).Radius(18).Label("确认复用并添加").Disabled(locked || occupied && !state.sharedAck || s.busy || state.pending)
					confirm.Background(p.blue)
					confirm.Children(func() { label(c, "确认复用并添加", 12, ui.Hex("#FFFFFF")) })
					if confirm.Clicked() {
						keys := make([]workspace.BranchReuseKey, 0, len(state.conflicts))
						for _, conflict := range state.conflicts {
							keys = append(keys, conflict.Key)
						}
						s.submitTaskAppend(keys)
					}
					return
				}
				buttonLabel := "检查并添加"
				if state.pending {
					buttonLabel = "正在检查…"
				}
				if taskAppendButton(c, "task-append-preflight", buttonLabel, p, true, s.busy || state.pending || state.mode == "services" && len(state.selectedServices) == 0) {
					state.inlineError = ""
					s.beginTaskAppendPreflight()
				}
			})
		})
	})
	state.open = open
	if !state.open && !state.pending {
		cancelTaskAppendRepositoryChoices(state)
		s.workspaceAppend = nil
	}
	if state.pending && !open {
		state.open = true
	}
	return true
}

func taskAppendButton(c *ui.Context, key, text string, p palette, primary, disabled bool) bool {
	button := ui.ButtonBase(c.Key(key)).Height(34).Padding(0, 16).Radius(18).Label(text).Disabled(disabled)
	color := p.text
	if primary {
		button.Background(p.blue)
		color = ui.Hex("#FFFFFF")
	} else {
		button.Border(1, p.line)
	}
	button.Children(func() { label(c, text, 12, color) })
	return button.Clicked()
}

func (s *app) renderTaskAppendServices(c *ui.Context, p palette, state *taskAppendUIState) {
	label(c, "选择服务", 13, p.text).FontWeight(600)
	ui.TextInput(c.Key("task-append-service-search"), &state.serviceQuery).FillWidth().Height(34).Placeholder("搜索名称、仓库路径或 ID").ReadOnly(state.pending)
	ui.Column(c).FillWidth().Gap(4).Children(func() {
		found := false
		for _, serviceID := range state.serviceOrder {
			service := state.services[serviceID]
			repositoryName, repositoryPath := s.repositoryDetails(service.RepositoryID)
			if !taskAppendServiceMatches(service, repositoryName+" "+repositoryPath, state.serviceQuery) {
				continue
			}
			found = true
			selected := state.selectedServices[serviceID]
			moduleCount := len(service.Modules)
			serviceName := taskServiceDisplayName(s, service)
			root := s.repositoryRootPath(service.RepositoryID)
			detail := fmt.Sprintf("%d 个模块", moduleCount)
			if root != "" {
				detail += " · " + root
			}
			renderTaskServiceChoiceRow(c, p, taskServiceChoiceRowProps{
				key: "task-append-service-" + serviceID, name: serviceName, detail: detail,
				buttonLabel: "选择或取消服务：" + serviceName,
				selected:    selected, active: selected && state.activeServiceID == serviceID,
				disabled:          state.pending || s.busy,
				onClick:           func() { s.toggleTaskAppendService(serviceID) },
				onSelectionChange: func(bool) { s.toggleTaskAppendService(serviceID) },
			})
		}
		if !found {
			emptyPanel(c, p, "没有匹配的服务", "已添加到此任务的服务不会出现在候选列表中。")
		}
	})
}

func taskAppendServiceMatches(service domain.GroupServiceConfig, repositoryPath, query string) bool {
	query = strings.TrimSpace(query)
	if query == "" {
		return true
	}
	query = strings.ToLower(query)
	for _, value := range []string{service.DisplayName, service.ID, service.RepositoryID, repositoryPath} {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}

func (s *app) renderTaskAppendModules(c *ui.Context, p palette, state *taskAppendUIState) {
	used := taskUsedModuleIDs(s.taskByID(state.taskID), state.serviceID)
	label(c, "选择未添加的模块", 13, p.text).FontWeight(600)
	for _, module := range state.moduleOptions {
		if used[strings.ToLower(module.ID)] {
			continue
		}
		selected := state.moduleDraftID == module.ID
		button := ui.ButtonBase(c.Key("task-append-module-option-"+module.ID)).FillWidth().Height(34).Padding(0, 9).Radius(7).Border(1, p.line).Label(taskCreationModuleName(module)).Disabled(state.pending)
		if selected {
			button.Background(p.selected)
		}
		button.Children(func() { label(c, taskCreationModuleName(module), 10, p.text).SingleLine() })
		if button.Clicked() && !state.pending {
			s.selectTaskAppendConfiguredModule(state, module)
		}
	}
	if len(state.moduleOptions) == 0 {
		label(c, "服务配置中的模块已全部添加。", 10, p.muted).Wrap()
	}
	if taskAppendButton(c, "task-append-module-temp", "添加临时模块", p, false, state.pending) {
		s.replaceTaskAppendModuleWithTemporary(state, domain.WorkspaceStandardWorktree)
	}
}

func (s *app) renderTaskAppendServiceConfiguration(c *ui.Context, p palette, state *taskAppendUIState) {
	if state.activeServiceID == "" || !state.selectedServices[state.activeServiceID] {
		emptyPanel(c, p, "选择要配置的服务", "在左侧选择一个服务；可连续选择多个服务并逐个设置模块。")
		return
	}
	serviceID := state.activeServiceID
	service := state.services[serviceID]
	root := s.repositoryRootPath(service.RepositoryID)
	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			label(c, taskServiceDisplayName(s, service), 15, p.text).FontWeight(600).SingleLine()
			if root != "" {
				label(c, root, 10, p.muted).SingleLine().Tooltip(root)
			}
		})
		if taskAppendButton(c, "task-append-add-module-"+serviceID, "添加临时模块", p, false, state.pending) {
			s.addTaskAppendTemporaryModule(serviceID, domain.WorkspaceStandardWorktree)
		}
	})
	moduleIDs := append([]string(nil), state.moduleOrder[serviceID]...)
	for _, moduleID := range moduleIDs {
		draft := state.moduleData[serviceID][moduleID]
		if draft != nil {
			s.renderTaskAppendModuleDraft(c, p, state, serviceID, draft, true)
		}
	}
	duplicates := duplicateTaskCreationCloneTargets(state.moduleOrder[serviceID], state.moduleData[serviceID])
	if len(duplicates) > 0 {
		label(c, "共享远程分支风险：多个克隆模块使用相同目标分支 "+strings.Join(duplicates, ", ")+"。", 11, p.amber).Wrap()
	}
}

func (s *app) renderTaskAppendModuleConfiguration(c *ui.Context, p palette, state *taskAppendUIState) {
	service := state.services[state.serviceID]
	label(c, taskServiceDisplayName(s, service), 15, p.text).FontWeight(600).SingleLine()
	draft := state.moduleData[state.serviceID][state.moduleDraftID]
	if draft == nil {
		emptyPanel(c, p, "选择一个模块", "从左侧选择已配置模块，或添加临时模块。")
		return
	}
	s.renderTaskAppendModuleDraft(c, p, state, state.serviceID, draft, false)
	if draft.Source == domain.TaskModuleConfigured && taskUsedModuleIDs(s.taskByID(state.taskID), state.serviceID)[strings.ToLower(draft.ID)] {
		state.inlineError = "所选模块已存在于任务中，请选择其他模块。"
	}
	if draft.Strategy == domain.WorkspaceIndependentClone && strings.TrimSpace(draft.TargetBranch) != "" {
		for _, existing := range s.taskByID(state.taskID).workspaces {
			if strings.EqualFold(existing.Strategy, string(domain.WorkspaceIndependentClone)) && strings.EqualFold(existing.Branch, draft.TargetBranch) {
				label(c, "共享远程分支风险：任务中已有克隆模块使用该目标分支。", 11, p.amber).Wrap()
				break
			}
		}
	}
}

func (s *app) renderTaskAppendModuleDraft(c *ui.Context, p palette, state *taskAppendUIState, serviceID string, draft *taskCreationModuleDraft, removable bool) {
	readOnly := state.pending
	ui.Column(c).FillWidth().Padding(9).Gap(7).Background(p.soft).Border(1, p.line).Radius(8).Children(func() {
		ui.Row(c).Gap(7).AlignItems(ui.Center).Children(func() {
			label(c, "模块名", 11, p.muted)
			ui.TextInput(c.Key("task-append-module-name-"+serviceID+"-"+draft.ID), &draft.Name).Grow(1).Height(34).Placeholder("模块名").ReadOnly(readOnly)
			if draft.Source == domain.TaskModuleTemporary {
				label(c, "临时", 10, p.amber)
			}
			if removable {
				button := iconButtonDisabled(c, "task-append-module-delete-"+serviceID+"-"+draft.ID, "delete", "删除模块", p, readOnly || len(state.moduleOrder[serviceID]) <= 1)
				if button.Clicked() {
					s.removeTaskAppendModule(serviceID, draft.ID)
				}
			}
		})
		ui.Row(c).Gap(6).Children(func() {
			s.settingsChoice(c, p, "task-append-strategy-worktree-"+serviceID+"-"+draft.ID, "Worktree", draft.Strategy == domain.WorkspaceStandardWorktree, func() {
				if state.pending {
					return
				}
				draft.Strategy = domain.WorkspaceStandardWorktree
				s.invalidateTaskAppendPreview("模块策略已变化，请重新预检。")
			})
			s.settingsChoice(c, p, "task-append-strategy-clone-"+serviceID+"-"+draft.ID, "独立克隆", draft.Strategy == domain.WorkspaceIndependentClone, func() {
				if state.pending {
					return
				}
				draft.Strategy = domain.WorkspaceIndependentClone
				s.invalidateTaskAppendPreview("模块策略已变化，请重新预检。")
			})
		})
		service := state.services[serviceID]
		s.renderTaskRepositoryBranchPicker(c, p, taskRepositoryChoicesAppendOwner, state.token, state.taskID, serviceID, draft.ID, service.RepositoryID, draft, readOnly)
		ui.Column(c).Gap(3).Children(func() {
			targetLabel := "目标分支（必填）"
			targetPlaceholder := "请输入目标分支"
			if draft.Strategy == domain.WorkspaceIndependentClone {
				targetLabel = "克隆目标分支（可空）"
				targetPlaceholder = "请输入目标分支（可选）"
			}
			label(c, targetLabel, 10, p.muted)
			ui.TextInput(c.Key("task-append-target-"+serviceID+"-"+draft.ID), &draft.TargetBranch).FillWidth().Height(34).Placeholder(targetPlaceholder).Label(targetPlaceholder).ReadOnly(readOnly)
		})
	})
	s.syncTaskAppendModuleDraft(state, serviceID, draft)
}

func taskAppendConflictFlags(conflicts []workspace.BranchReuseConflict) (locked, occupied bool) {
	for _, conflict := range conflicts {
		locked = locked || len(conflict.LockedWorktreePaths) > 0
		occupied = occupied || conflict.RequiresForceAttach
	}
	return locked, occupied
}

func (s *app) renderTaskAppendConflicts(c *ui.Context, p palette, state *taskAppendUIState) {
	locked, occupied := taskAppendConflictFlags(state.conflicts)
	ui.Column(c).FillWidth().Padding(12).Gap(8).Background(p.amberBg).Border(1, p.line).Radius(10).Children(func() {
		label(c, "确认复用已有分支", 14, p.text).FontWeight(600)
		label(c, "预检发现分支状态冲突。确认会在任务工作区中复用这些分支；执行时会再次校验状态指纹。", 11, p.muted)
		for _, conflict := range state.conflicts {
			renderBranchReuseConflict(c, p, conflict)
		}
		if occupied {
			label(c, "复用已检出的分支会使多个 Worktree 共享同一分支。", 11, p.amber)
		}
		if locked {
			label(c, "锁定的 Worktree 不能自动清理或强制复用；请先解锁并重新预检。", 11, p.amber)
		}
	})
}
