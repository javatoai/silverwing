package desktop

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"silverwing-go/internal/application"
)

const (
	taskRepositoryChoicesCreateOwner = "task-create"
	taskRepositoryChoicesAppendOwner = "task-append"
)

// Each module draft owns its picker state. Results can therefore only update the
// form/module that issued the request, even when the user switches forms while
// repository.choices is in flight.
type taskRepositoryPickerState struct {
	repositoryID, repositoryRoot string
	branchOpen, remoteOpen       bool
	query                        string
	remoteQuery                  string
	branchFocusPending           bool
	highlightedBranch            int
	branchChoiceSignature        string
	remotes                      []string
	remotesLoaded                bool
	remotesLoading               bool
	remotesError                 string
	branches                     map[string][]string
	branchesLoaded               map[string]bool
	branchesLoadedAt             map[string]time.Time
	branchesLoading              map[string]bool
	branchesError                map[string]string
	remoteRequestKey             string
	remoteCancel                 context.CancelFunc
	branchRequestKeys            map[string]string
	branchCancels                map[string]context.CancelFunc
}

type taskRepositoryChoiceRequest struct {
	owner, token, taskID string
	serviceID, moduleID  string
	repositoryID, remote string
	requestKey           string
}

func (p *taskRepositoryPickerState) ensureMaps() {
	if p.branches == nil {
		p.branches = make(map[string][]string)
	}
	if p.branchesLoaded == nil {
		p.branchesLoaded = make(map[string]bool)
	}
	if p.branchesLoadedAt == nil {
		p.branchesLoadedAt = make(map[string]time.Time)
	}
	if p.branchesLoading == nil {
		p.branchesLoading = make(map[string]bool)
	}
	if p.branchesError == nil {
		p.branchesError = make(map[string]string)
	}
	if p.branchRequestKeys == nil {
		p.branchRequestKeys = make(map[string]string)
	}
	if p.branchCancels == nil {
		p.branchCancels = make(map[string]context.CancelFunc)
	}
}

func (s *app) requestTaskRepositoryChoices(owner, token, taskID, serviceID, moduleID, repositoryID, remote string, force bool) {
	draft, currentRepositoryID, current := s.taskRepositoryChoiceDraft(owner, token, taskID, serviceID, moduleID)
	if !current || draft == nil || currentRepositoryID != repositoryID || repositoryID == "" {
		return
	}
	if s.backend == nil {
		picker := &draft.repositoryPicker
		setTaskRepositoryPickerError(picker, remote, "应用后端不可用。")
		return
	}
	picker := &draft.repositoryPicker
	picker.ensureMaps()
	remote = strings.TrimSpace(remote)
	if remote == "" {
		if picker.remotesLoading && !force || !force && picker.remotesLoaded {
			return
		}
		cancelTaskRepositoryRemoteRead(picker)
		picker.remotesLoading = true
		picker.remotesError = ""
	} else {
		if picker.branchesLoading[remote] && !force || !force && !taskRepositoryBranchChoicesExpired(picker, remote) {
			return
		}
		cancelTaskRepositoryBranchRead(picker, remote)
		picker.branchesLoading[remote] = true
		delete(picker.branchesError, remote)
	}
	requestKey := newDesktopEditorID("task-repository-choices")
	if remote == "" {
		picker.remoteRequestKey = requestKey
	} else {
		picker.branchRequestKeys[remote] = requestKey
	}
	payload := map[string]any{
		"repositoryId": repositoryID,
		"force":        force,
		"requestKey":   requestKey,
		"pickerOwner":  owner,
		"pickerToken":  token,
		"serviceId":    serviceID,
		"moduleId":     moduleID,
	}
	if taskID != "" {
		payload["pickerTaskId"] = taskID
	}
	if remote != "" {
		payload["remote"] = remote
	}
	action := application.Action{Kind: "repository.choices", TaskID: taskID, ServiceID: serviceID, ModuleID: moduleID, Payload: payload}
	ctx, cancel := context.WithCancel(context.Background())
	if remote == "" {
		picker.remoteCancel = cancel
	} else {
		picker.branchCancels[remote] = cancel
	}
	go s.runTaskRepositoryChoicesRead(ctx, action)
}

func (s *app) runTaskRepositoryChoicesRead(ctx context.Context, action application.Action) {
	var result application.ActionResult
	var err error
	if s.backend == nil {
		err = fmt.Errorf("应用后端不可用。")
	} else {
		result, err = s.backend.Dispatch(ctx, action)
	}
	if s.win == nil {
		return
	}
	s.win.Update(func() {
		s.applyTaskRepositoryChoices(action, result, err)
	})
}

func (s *app) taskRepositoryChoiceDraft(owner, token, taskID, serviceID, moduleID string) (*taskCreationModuleDraft, string, bool) {
	switch owner {
	case taskRepositoryChoicesCreateOwner:
		state := s.taskUnit.state.taskCreation
		if state == nil || state.token != token || !s.dialogOpen || s.form.mode != "task-create" || state.phase != "editing" || !state.services[serviceID] {
			return nil, "", false
		}
		service := s.taskCreationService(serviceID)
		if service == nil {
			return nil, "", false
		}
		draft := state.moduleData[serviceID][moduleID]
		return draft, service.RepositoryID, draft != nil
	case taskRepositoryChoicesAppendOwner:
		state := s.workspaceUnit.state.workspaceAppend
		if state == nil || state.token != token || !state.open || state.pending || state.taskID != taskID {
			return nil, "", false
		}
		task := s.taskUnit.taskByID(taskID)
		if task.id == "" || task.archived || task.updated != state.taskUpdated || task.path != state.taskPath || task.branch != state.taskBranch || s.taskUnit.currentTask().id != taskID {
			return nil, "", false
		}
		service, ok := state.services[serviceID]
		if !ok || state.mode == "services" && !state.selectedServices[serviceID] || state.mode == "modules" && (serviceID != state.serviceID || state.moduleDraftID != moduleID) {
			return nil, "", false
		}
		draft := state.moduleData[serviceID][moduleID]
		return draft, service.RepositoryID, draft != nil
	default:
		return nil, "", false
	}
}

func (s *app) taskRepositoryChoicesAction(action application.Action) bool {
	return action.Kind == "repository.choices" &&
		(actionString(action.Payload, "pickerOwner") == taskRepositoryChoicesCreateOwner || actionString(action.Payload, "pickerOwner") == taskRepositoryChoicesAppendOwner)
}

func (s *app) applyTaskRepositoryChoices(action application.Action, result application.ActionResult, actionErr error) bool {
	if !s.taskRepositoryChoicesAction(action) {
		return false
	}
	payload := action.Payload
	request := taskRepositoryChoiceRequest{
		owner: actionString(payload, "pickerOwner"), token: actionString(payload, "pickerToken"),
		taskID: actionString(payload, "pickerTaskId"), serviceID: actionString(payload, "serviceId"),
		moduleID: actionString(payload, "moduleId"), repositoryID: actionString(payload, "repositoryId"),
		remote: actionString(payload, "remote"), requestKey: actionString(payload, "requestKey"),
	}
	draft, repositoryID, current := s.taskRepositoryChoiceDraft(request.owner, request.token, request.taskID, request.serviceID, request.moduleID)
	if !current || draft == nil {
		return true
	}
	picker := &draft.repositoryPicker
	picker.ensureMaps()
	requestCurrent := request.remote == "" && picker.remoteRequestKey == request.requestKey
	if request.remote != "" {
		requestCurrent = picker.branchRequestKeys[request.remote] == request.requestKey
	}
	if request.requestKey == "" || !requestCurrent || request.repositoryID == "" || request.repositoryID != repositoryID {
		return true
	}
	data := mapData(result.Data)
	if result.Data != nil && !sameTaskRepositoryPath(s.repositoryRootPath(request.repositoryID), dataString(data, "rootPath")) {
		clearTaskRepositoryPickerLoading(picker, request.remote, request.requestKey)
		setTaskRepositoryPickerError(picker, request.remote, "仓库选择结果已过期，请重新读取远程分支。")
		return true
	}
	if result.Data != nil && dataString(data, "repositoryId") != request.repositoryID {
		clearTaskRepositoryPickerLoading(picker, request.remote, request.requestKey)
		setTaskRepositoryPickerError(picker, request.remote, "仓库选择结果与当前仓库不一致，请重新读取。")
		return true
	}
	if actionErr != nil {
		applyTaskRepositoryChoiceData(picker, request, data)
		clearTaskRepositoryPickerLoading(picker, request.remote, request.requestKey)
		if !errors.Is(actionErr, context.Canceled) {
			setTaskRepositoryPickerError(picker, request.remote, actionErr.Error())
		}
		return true
	}
	if result.Data == nil {
		clearTaskRepositoryPickerLoading(picker, request.remote, request.requestKey)
		setTaskRepositoryPickerError(picker, request.remote, "仓库选择结果为空，请重试。")
		return true
	}
	if request.remote != "" && dataString(data, "remote") != request.remote {
		clearTaskRepositoryPickerLoading(picker, request.remote, request.requestKey)
		setTaskRepositoryPickerError(picker, request.remote, "返回的远程与所选远程不一致，请重试。")
		return true
	}
	applyTaskRepositoryChoiceData(picker, request, data)
	clearTaskRepositoryPickerLoading(picker, request.remote, request.requestKey)
	if request.remote == "" {
		picker.remotesLoaded = true
		picker.remotesError = ""
	} else {
		picker.branchesLoaded[request.remote] = true
		picker.branchesLoadedAt[request.remote] = time.Now()
		delete(picker.branchesError, request.remote)
	}
	return true
}

func applyTaskRepositoryChoiceData(picker *taskRepositoryPickerState, request taskRepositoryChoiceRequest, data map[string]any) {
	if picker == nil || data == nil {
		return
	}
	picker.ensureMaps()
	if request.remote == "" && data["remotes"] != nil {
		picker.remotes = mapsString(data["remotes"])
		picker.remotesLoaded = true
		picker.remotesError = ""
	}
	if request.remote != "" && data["branches"] != nil {
		branches := make([]string, 0)
		prefix := request.remote + "/"
		for _, branch := range mapsString(data["branches"]) {
			if strings.HasPrefix(branch, prefix) && len(branch) > len(prefix) {
				branches = append(branches, branch)
			}
		}
		picker.branches[request.remote] = branches
	}
}

func clearTaskRepositoryPickerLoading(picker *taskRepositoryPickerState, remote, requestKey string) {
	if remote == "" {
		if picker.remoteRequestKey != requestKey {
			return
		}
		picker.remotesLoading = false
		picker.remoteRequestKey = ""
		picker.remoteCancel = nil
		return
	}
	picker.ensureMaps()
	if picker.branchRequestKeys[remote] != requestKey {
		return
	}
	picker.branchesLoading[remote] = false
	delete(picker.branchRequestKeys, remote)
	delete(picker.branchCancels, remote)
}

func cancelTaskRepositoryRemoteRead(picker *taskRepositoryPickerState) {
	if picker == nil {
		return
	}
	cancel := picker.remoteCancel
	picker.remoteCancel = nil
	picker.remoteRequestKey = ""
	picker.remotesLoading = false
	if cancel != nil {
		cancel()
	}
}

func cancelTaskRepositoryBranchRead(picker *taskRepositoryPickerState, remote string) {
	if picker == nil || remote == "" {
		return
	}
	picker.ensureMaps()
	cancel := picker.branchCancels[remote]
	delete(picker.branchCancels, remote)
	delete(picker.branchRequestKeys, remote)
	picker.branchesLoading[remote] = false
	if cancel != nil {
		cancel()
	}
}

func cancelTaskRepositoryPickerRequests(picker *taskRepositoryPickerState) {
	if picker == nil {
		return
	}
	picker.branchFocusPending = false
	cancelTaskRepositoryRemoteRead(picker)
	picker.ensureMaps()
	remotes := make(map[string]bool, len(picker.branchCancels)+len(picker.branchRequestKeys))
	for remote := range picker.branchCancels {
		remotes[remote] = true
	}
	for remote := range picker.branchRequestKeys {
		remotes[remote] = true
	}
	for remote := range remotes {
		cancelTaskRepositoryBranchRead(picker, remote)
	}
}

func cancelTaskRepositoryChoices(state *taskCreationState) {
	if state == nil {
		return
	}
	for _, modules := range state.moduleData {
		for _, draft := range modules {
			if draft != nil {
				cancelTaskRepositoryPickerRequests(&draft.repositoryPicker)
			}
		}
	}
}

func cancelTaskAppendRepositoryChoices(state *taskAppendUIState) {
	if state == nil {
		return
	}
	for _, modules := range state.moduleData {
		for _, draft := range modules {
			if draft != nil {
				cancelTaskRepositoryPickerRequests(&draft.repositoryPicker)
			}
		}
	}
}

func setTaskRepositoryPickerError(picker *taskRepositoryPickerState, remote, message string) {
	if remote == "" {
		picker.remotesError = message
		return
	}
	picker.ensureMaps()
	picker.branchesError[remote] = message
}

func sameTaskRepositoryPath(left, right string) bool {
	left = filepath.Clean(strings.TrimSpace(left))
	right = filepath.Clean(strings.TrimSpace(right))
	if left == "." || right == "." || left == "" || right == "" {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func (s *app) repositoryRootPath(repositoryID string) string {
	_, root := s.currentRepositoryDetails(repositoryID)
	return root
}

func taskBranchRemote(ref string) string {
	ref = strings.TrimSpace(ref)
	if remote, _, ok := strings.Cut(ref, "/"); ok && remote != "" {
		return remote
	}
	return "origin"
}

func taskRemoteBranchRef(remote, value string) string {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return value
	}
	if _, branch, ok := strings.Cut(strings.TrimSpace(value), "/"); ok {
		return remote + "/" + branch
	}
	return remote + "/" + strings.TrimSpace(value)
}

func filterTaskRepositoryBranches(branches []string, query string) []string {
	query = strings.ToLower(strings.TrimSpace(query))
	filtered := make([]string, 0, len(branches))
	for _, branch := range branches {
		if query == "" || strings.Contains(strings.ToLower(branch), query) {
			filtered = append(filtered, branch)
		}
	}
	return filtered
}

func taskRepositoryBranchChoicesExpired(picker *taskRepositoryPickerState, remote string) bool {
	if picker == nil || !picker.branchesLoaded[remote] {
		return true
	}
	loadedAt := picker.branchesLoadedAt[remote]
	return loadedAt.IsZero() || time.Since(loadedAt) >= 30*time.Second
}

func prepareTaskRepositoryPickerIdentity(picker *taskRepositoryPickerState, repositoryID, rootPath string) {
	if picker == nil {
		return
	}
	changed := picker.repositoryID != "" && (picker.repositoryID != repositoryID || !sameTaskRepositoryPathOrEmpty(picker.repositoryRoot, rootPath))
	if changed {
		cancelTaskRepositoryPickerRequests(picker)
		picker.remoteOpen, picker.branchOpen, picker.query, picker.remoteQuery = false, false, "", ""
		picker.highlightedBranch, picker.branchChoiceSignature = 0, ""
		picker.remotes, picker.remotesLoaded, picker.remotesLoading, picker.remotesError = nil, false, false, ""
		picker.branches = make(map[string][]string)
		picker.branchesLoaded = make(map[string]bool)
		picker.branchesLoadedAt = make(map[string]time.Time)
		picker.branchesLoading = make(map[string]bool)
		picker.branchesError = make(map[string]string)
	}
	picker.repositoryID, picker.repositoryRoot = repositoryID, rootPath
}

func sameTaskRepositoryPathOrEmpty(left, right string) bool {
	if strings.TrimSpace(left) == "" || strings.TrimSpace(right) == "" {
		return strings.TrimSpace(left) == strings.TrimSpace(right)
	}
	return sameTaskRepositoryPath(left, right)
}

func taskRepositoryRemotePickerVisible(picker *taskRepositoryPickerState, remote string) bool {
	if picker == nil {
		return false
	}
	if picker.remotesError != "" {
		return true
	}
	if !picker.remotesLoaded {
		return false
	}
	return len(picker.remotes) != 1 || !strings.EqualFold(picker.remotes[0], remote)
}

func actionString(values map[string]any, key string) string {
	if value, ok := values[key].(string); ok {
		return value
	}
	return ""
}
