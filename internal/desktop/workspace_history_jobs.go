package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/workspace"
)

type workspaceHistoryJob struct {
	action                                 application.Action
	dialog                                 *workspaceHistoryState
	taskPath, taskCreatedAt                string
	home, gitExecutable, workspaceIdentity string
	branchConfigIdentity                   string
	branchConfigTracked                    bool
	generation                             uint64
	pageGeneration                         uint64
	ctx                                    context.Context
	cancel                                 context.CancelFunc
	done                                   chan struct{}
}

type workspaceHistoryJobState struct {
	current         *workspaceHistoryJob // owned by the window thread
	generation      uint64
	pageGeneration  uint64
	mu              sync.Mutex
	running         map[*workspaceHistoryJob]struct{}
	closed          bool
	prefetchRunning map[*workspaceReviewPrefetchJob]struct{}
}

func independentWorkspaceHistoryAction(kind string) bool {
	return kind == "workspace.history" || kind == "workspace.file-preview" ||
		kind == "workspace.diff-files" || kind == "workspace.diff-preview"
}

func (s *app) ensureWorkspaceHistoryJobs() *workspaceHistoryJobState {
	if s.workspaceHistoryJobs == nil {
		s.workspaceHistoryJobs = &workspaceHistoryJobState{running: make(map[*workspaceHistoryJob]struct{})}
	} else if s.workspaceHistoryJobs.running == nil {
		s.workspaceHistoryJobs.running = make(map[*workspaceHistoryJob]struct{})
	}
	return s.workspaceHistoryJobs
}

func cloneWorkspaceHistoryAction(action application.Action) application.Action {
	if action.Payload == nil {
		return action
	}
	payload := make(map[string]any, len(action.Payload))
	for key, value := range action.Payload {
		if nested, ok := value.(map[string]any); ok {
			copyNested := make(map[string]any, len(nested))
			for nestedKey, nestedValue := range nested {
				copyNested[nestedKey] = nestedValue
			}
			value = copyNested
		}
		payload[key] = value
	}
	action.Payload = payload
	return action
}

func (s *app) beginWorkspaceHistoryJob(action application.Action) (*workspaceHistoryJob, error) {
	dialog := s.workspaceHistory
	if !independentWorkspaceHistoryAction(action.Kind) || dialog == nil || !dialog.Open || !dialog.matches(action) {
		return nil, errors.New("工作区读取身份已变化，请重新打开后重试。")
	}
	task := s.taskByID(action.TaskID)
	item, exists := workspaceHistoryTaskWorkspace(task, action)
	if task.id == "" || s.currentTask().id != action.TaskID || !exists {
		return nil, errors.New("任务或工作区已切换，请重新打开后重试。")
	}
	state := s.ensureWorkspaceHistoryJobs()
	state.mu.Lock()
	closed := state.closed
	state.mu.Unlock()
	if closed {
		return nil, errors.New("工作区读取已关闭。")
	}
	if previous := state.current; previous != nil {
		previous.cancel()
		state.current = nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	state.generation++
	request := &workspaceHistoryJob{
		action: cloneWorkspaceHistoryAction(action), dialog: dialog,
		taskPath: task.path, taskCreatedAt: task.createdAt,
		home: s.snapshot.Home, gitExecutable: s.configString("gitExecutablePath"),
		workspaceIdentity: workspaceHistoryWorkspaceIdentity(item),
		generation:        state.generation, ctx: ctx, cancel: cancel, done: make(chan struct{}),
	}
	if dialog.Mode == "review" {
		request.branchConfigIdentity = s.configJSON
		request.branchConfigTracked = true
		request.pageGeneration = dialog.ReviewGeneration
	} else if workspaceHistoryTracksCurrentBranchConfig(action) {
		// The history baseline always comes from the current module/service
		// configuration. A task's historical BaseRef does not define this read.
		request.branchConfigIdentity = s.configJSON
		request.branchConfigTracked = true
	}
	state.current = request
	state.mu.Lock()
	if state.closed {
		state.mu.Unlock()
		cancel()
		state.current = nil
		return nil, errors.New("工作区读取已关闭。")
	}
	state.running[request] = struct{}{}
	state.mu.Unlock()
	return request, nil
}

func workspaceHistoryTaskWorkspace(task task, action application.Action) (application.Workspace, bool) {
	for _, item := range task.workspaces {
		if item.ServiceID == action.ServiceID && item.ModuleID == action.ModuleID &&
			workspaceHealthPathKey(item.Path) == workspaceHealthPathKey(action.Path) {
			return item, true
		}
	}
	return application.Workspace{}, false
}

func workspaceHistoryWorkspaceIdentity(item application.Workspace) string {
	content, err := json.Marshal(item)
	if err != nil {
		return fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00%s", item.ID, item.ServiceID, item.ModuleID, item.Path, item.BaseRef, item.Branch)
	}
	return string(content)
}

func (s *app) workspaceHistoryJobRelevant(request *workspaceHistoryJob) bool {
	state := s.workspaceHistoryJobs
	if state == nil || request == nil || state.current != request || state.generation != request.generation || request.ctx.Err() != nil ||
		s.workspaceHistory != request.dialog || request.dialog == nil || !request.dialog.Open ||
		!request.dialog.matches(request.action) || s.currentTask().id != request.action.TaskID {
		return false
	}
	if request.dialog.Mode == "review" && (request.dialog.ReviewGeneration != request.pageGeneration || !s.workspaceReviewPageVisible()) {
		return false
	}
	if request.dialog.Mode == "history" && request.dialog.TaskPath != "" && !s.workspaceCommitHistoryDialogVisible(request.dialog) {
		return false
	}
	state.mu.Lock()
	closed := state.closed
	state.mu.Unlock()
	if closed {
		return false
	}
	task := s.taskByID(request.action.TaskID)
	item, exists := workspaceHistoryTaskWorkspace(task, request.action)
	return task.id != "" && task.path == request.taskPath && task.createdAt == request.taskCreatedAt &&
		s.snapshot.Home == request.home && s.configString("gitExecutablePath") == request.gitExecutable && exists &&
		workspaceHistoryWorkspaceIdentity(item) == request.workspaceIdentity &&
		(!request.branchConfigTracked || s.configJSON == request.branchConfigIdentity)
}

func workspaceHistoryAllScope(action application.Action) bool {
	all, _ := action.Payload["allHistory"].(bool)
	return all
}

func workspaceHistoryTracksCurrentBranchConfig(action application.Action) bool {
	return action.Kind == "workspace.history" && !workspaceHistoryAllScope(action)
}

// dispatchIndependentWorkspaceHistory runs local Git history and file-preview
// reads before the app-wide busy gate. The backend receives only this request's
// context, so another task operation cannot block or cancel it.
func (s *app) dispatchIndependentWorkspaceHistory(action application.Action) bool {
	if !independentWorkspaceHistoryAction(action.Kind) {
		return false
	}
	if s.backend == nil || s.win == nil || s.win.IsDestroyed() {
		s.applyWorkspaceHistoryFailure(action, errors.New("工作区读取后端或窗口不可用，请重试。"))
		return true
	}
	request, err := s.beginWorkspaceHistoryJob(action)
	if err != nil {
		s.applyWorkspaceHistoryFailure(action, err)
		return true
	}
	backend, win := s.backend, s.win
	win.Invalidate()
	go func() {
		result, err := executeWorkspaceHistoryJob(backend, request)
		if win == nil || win.IsDestroyed() {
			if request.action.Kind == "workspace.diff-files" {
				s.releaseWorkspaceDiffSessionResult(result)
			}
			request.cancel()
			s.completeWorkspaceHistoryJob(request)
			return
		}
		updateDone := make(chan struct{})
		win.Update(func() {
			defer close(updateDone)
			s.finishWorkspaceHistoryJob(request, result, err)
		})
		// MyGO v0.4.2 Window.Update posts to the main thread and returns before
		// the callback runs. Keep the context alive until the UI has applied the
		// result; cancellation still releases this worker during shutdown.
		select {
		case <-updateDone:
		case <-request.ctx.Done():
		}
		s.completeWorkspaceHistoryJob(request)
	}()
	return true
}

func executeWorkspaceHistoryJob(backend application.Backend, request *workspaceHistoryJob) (application.ActionResult, error) {
	if backend == nil || request == nil {
		return application.ActionResult{}, errors.New("工作区读取缺少后端或请求。")
	}
	if err := request.ctx.Err(); err != nil {
		return application.ActionResult{}, err
	}
	result, err := backend.Dispatch(request.ctx, request.action)
	if request.ctx.Err() != nil {
		// Preserve opaque session IDs created just before cancellation. The
		// stale-result path owns retiring them after the backend call returns.
		return result, errors.Join(request.ctx.Err(), err)
	}
	return result, err
}

func (s *app) finishWorkspaceHistoryJob(request *workspaceHistoryJob, result application.ActionResult, err error) {
	if request != nil {
		defer request.cancel()
	}
	if !s.workspaceHistoryJobRelevant(request) {
		if request != nil && request.action.Kind == "workspace.diff-files" {
			s.releaseWorkspaceDiffSessionResult(result)
		}
		return
	}
	s.workspaceHistoryJobs.current = nil
	if err != nil {
		if request.action.Kind == "workspace.diff-files" {
			s.releaseWorkspaceDiffSessionResult(result)
		}
		s.applyWorkspaceHistoryFailure(request.action, err)
	} else {
		s.applyWorkspaceHistoryResult(request.action, result)
	}
}

func (s *app) completeWorkspaceHistoryJob(request *workspaceHistoryJob) {
	if request == nil {
		return
	}
	state := s.workspaceHistoryJobs
	if state == nil {
		return
	}
	state.mu.Lock()
	if _, exists := state.running[request]; exists {
		delete(state.running, request)
		close(request.done)
	}
	state.mu.Unlock()
}

func (s *app) cancelWorkspaceHistoryRead() {
	state := s.workspaceHistoryJobs
	if state == nil || state.current == nil {
		return
	}
	request := state.current
	state.current = nil
	request.cancel()
	if s.workspaceHistory == request.dialog {
		switch request.action.Kind {
		case "workspace.history":
			request.dialog.Loading = false
			request.dialog.HistoryLoading = false
		case "workspace.file-preview":
			request.dialog.Loading = false
		case "workspace.diff-files":
			request.dialog.FilesLoading = false
		case "workspace.diff-preview":
			request.dialog.PreviewLoading = false
		}
	}
}

func (s *app) releaseWorkspaceReviewDiffSession(state *workspaceHistoryState) {
	if state == nil {
		return
	}
	s.cancelWorkspaceReviewPrefetch(state)
	id := state.DiffSessionID
	state.DiffSessionID = ""
	s.releaseWorkspaceGitDiffSession(id)
}

func (s *app) releaseWorkspaceDiffSessionResult(result application.ActionResult) {
	var files workspace.CompareFilesResult
	if decodeWorkspaceValue(result.Data, &files) == nil {
		s.releaseWorkspaceGitDiffSession(files.SessionID)
	}
}

func (s *app) releaseWorkspaceGitDiffSession(id string) {
	if s == nil || s.backend == nil || id == "" {
		return
	}
	backend := s.backend
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	go func() {
		defer cancel()
		_, _ = backend.Dispatch(ctx, application.Action{
			Kind:    workspace.CompareSessionCloseAction,
			Payload: map[string]any{"sessionId": id},
		})
	}()
}

func (s *app) observeWorkspaceHistoryJobs() {
	s.observeWorkspaceReviewPrefetch()
	if review := s.workspaceHistory; review != nil && review.Mode == "review" && review.Open && !s.workspaceReviewPageVisible() {
		s.saveWorkspaceReviewSession(review)
		s.releaseWorkspaceReviewDiffSession(review)
		s.cancelWorkspaceReviewDebounce(review)
		review.Open = false
		review.releaseWorkspaceReviewRenderData()
		review.ReviewGeneration++
	}
	state := s.workspaceHistoryJobs
	if state == nil || state.current == nil {
		return
	}
	request := state.current
	if s.workspaceHistory != request.dialog || !request.dialog.Open || !s.workspaceHistoryJobRelevant(request) {
		s.cancelWorkspaceHistoryRead()
	}
}

func (s *app) closeWorkspaceHistoryJobs(ctx context.Context) error {
	state := s.workspaceHistoryJobs
	if state == nil {
		return nil
	}
	state.mu.Lock()
	state.closed = true
	prefetchRunning := make([]*workspaceReviewPrefetchJob, 0, len(state.prefetchRunning))
	for request := range state.prefetchRunning {
		request.cancel()
		prefetchRunning = append(prefetchRunning, request)
	}
	if state.current != nil {
		state.current.cancel()
		state.current = nil
	}
	running := make([]*workspaceHistoryJob, 0, len(state.running))
	for request := range state.running {
		request.cancel()
		running = append(running, request)
	}
	state.mu.Unlock()
	for _, request := range prefetchRunning {
		select {
		case <-request.done:
		case <-ctx.Done():
			return fmt.Errorf("等待工作区预读清理：%w", ctx.Err())
		}
	}
	for _, request := range running {
		select {
		case <-request.done:
		case <-ctx.Done():
			return fmt.Errorf("等待工作区历史/预览读取清理：%w", ctx.Err())
		}
	}
	return nil
}
