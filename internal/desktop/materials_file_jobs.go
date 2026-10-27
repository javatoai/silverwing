package desktop

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/egoist/mygo"
	"silverwing-go/internal/application"
	"silverwing-go/internal/documents"
	"silverwing-go/internal/editor"
)

type materialsFileJobScope struct {
	home, taskID, taskPath, createdAt, root string
}

type materialsFileJob struct {
	key              string
	scope            materialsFileJobScope
	action           application.Action
	mutation         bool
	backend          application.Backend
	window           *mygo.Window
	ctx              context.Context
	cancel           context.CancelFunc
	done             chan struct{}
	rename           *markdownAutosaveRenamePlan
	leaseMu          sync.Mutex
	lease            *markdownAutosaveRenameLease
	renamed, settled bool
	barrier          *materialsFileJobBarrier
	result           application.ActionResult
	resultErr        error
	resultReady      bool
	consumed         bool
	textDelete       *materialTextDeletePlan
	textDeleteLease  *editor.AutosaveLease
}

type materialsFileJobState struct {
	mu      sync.Mutex
	current map[string]*materialsFileJob
	running map[*materialsFileJob]struct{}
	barrier *materialsFileJobBarrier
	closed  bool
}

type materialsFileJobBarrier struct {
	owner              *materialsFileJobState
	jobs               []*materialsFileJob
	resumed, abandoned bool // protected by owner.mu
}

func materialsFileJobMutationAction(kind string) bool {
	return isMaterialsFileManagementAction(kind) || kind == "document.rename"
}

func (s *app) ensureMaterialsFileJobs() *materialsFileJobState {
	if s.materialsFileJobs == nil {
		s.materialsFileJobs = &materialsFileJobState{current: make(map[string]*materialsFileJob), running: make(map[*materialsFileJob]struct{})}
	}
	return s.materialsFileJobs
}

func (s *app) materialsFileScope(taskID string) (materialsFileJobScope, bool) {
	task := s.taskByID(taskID)
	return materialsFileJobScope{home: s.snapshot.Home, taskID: task.id, taskPath: task.path, createdAt: task.createdAt, root: task.materialsPath}, task.id != "" && task.path != "" && task.materialsPath != ""
}

func materialsFileScopeKey(scope materialsFileJobScope) string {
	return scope.home + "\x00" + scope.taskID + "\x00" + normalizedReadingPath(scope.taskPath) + "\x00" + scope.createdAt + "\x00" + normalizedReadingRoot(scope.root)
}

func (s *app) materialsFileScopeMatches(scope materialsFileJobScope) bool {
	current, valid := s.materialsFileScope(scope.taskID)
	return valid && materialsFileScopeKey(current) == materialsFileScopeKey(scope)
}

// This feature's mutation occupies its controls. Ordinary Git/Tag work retains
// its own global operation ownership while materials requests proceed.
func (s *app) materialsFileJobsActive() bool {
	state := s.materialsFileJobs
	if state == nil {
		return false
	}
	scope, valid := s.materialsFileScope(s.currentTask().id)
	if !valid {
		return false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.current["mutation\x00"+materialsFileScopeKey(scope)] != nil
}

// Closing documents must also account for a previous task's mutation until
// its backend and queued response have released their ownership.
func (s *app) materialsFileMutationRunning() bool {
	state := s.materialsFileJobs
	if state == nil {
		return false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	for _, request := range state.current {
		if request.mutation {
			return true
		}
	}
	for request := range state.running {
		if request.mutation {
			return true
		}
	}
	return false
}

func (s *app) materialsFilePathBlocked() bool {
	if s.materialsGitRunning() {
		return true
	}
	if state := s.materialsFileJobs; state != nil {
		state.mu.Lock()
		blocked := state.closed || state.barrier != nil
		state.mu.Unlock()
		if blocked {
			return true
		}
	}
	// Retain the watcher boundary for root migration/recovery, task deletion
	// and confirmed workspace removal.
	return s.materialsLiveRefreshLifecycle != nil && s.materialsLiveRefreshLifecycle.barrier != nil
}

func (s *app) materialsFileInputBusy() bool {
	return s.materialsFilePathBlocked() || s.materialsFileJobsActive()
}

func (s *app) materialsFileReadBlocked() bool {
	return s.materialsFilePathBlocked() || s.materialsFileJobsActive()
}

func cloneMaterialsFileJobAction(action application.Action) application.Action {
	payload := make(map[string]any, len(action.Payload))
	for key, value := range action.Payload {
		switch typed := value.(type) {
		case []string:
			payload[key] = append([]string(nil), typed...)
		case []documents.FileOperationRequest:
			payload[key] = append([]documents.FileOperationRequest(nil), typed...)
		default:
			payload[key] = value
		}
	}
	action.Payload = payload
	return action
}

func (s *app) dispatchIndependentMaterialsFileAction(action application.Action) bool {
	mutation := materialsFileJobMutationAction(action.Kind)
	if !mutation && action.Kind != "document.list" && action.Kind != "document.read" {
		return false
	}
	if s.backend == nil || s.win == nil || s.win.IsDestroyed() {
		s.rejectMaterialsFileJob(action, errors.New("资料后端或窗口不可用。"))
		return true
	}
	if s.materialsFileReadBlocked() {
		s.rejectMaterialsFileJob(action, errors.New("资料文件操作或目录变更仍在执行，请稍后重试。"))
		return true
	}
	scope, valid := s.materialsFileScope(action.TaskID)
	if !valid {
		s.rejectMaterialsFileJob(action, errors.New("资料所属任务或根目录已变化，请重新打开任务。"))
		return true
	}
	action = cloneMaterialsFileJobAction(action)
	action.Payload["draftTaskPath"] = scope.taskPath
	action.Payload["draftMaterialsRoot"] = scope.root
	action.Payload["desktopRenameTaskCreatedAt"] = scope.createdAt
	action.Payload["materialsHome"] = scope.home
	if config, ok := s.materialsGitConfig(); ok {
		action.Payload["materialsConfigScope"] = application.MaterialsGitScope(scope.home, config)
	}
	textDelete, deleteErr := s.captureMaterialTextDelete(action)
	if deleteErr != nil {
		s.rejectMaterialsFileJob(action, deleteErr)
		return true
	}
	var rename *markdownAutosaveRenamePlan
	if action.Kind == "document.rename" {
		name, _ := action.Payload["name"].(string)
		var err error
		if err = documents.ValidateFileName(name); err == nil {
			rename, err = s.captureMarkdownAutosaveRename(action.TaskID, scope.root, action.Path, filepath.Join(filepath.Dir(action.Path), name))
		}
		if err != nil {
			s.rejectMaterialsFileJob(action, err)
			return true
		}
		action.Payload["desktopRenameTaskPath"] = scope.taskPath
		action.Payload["desktopRenameTaskCreatedAt"] = scope.createdAt
		action.Payload["desktopRenameMaterialsRoot"] = scope.root
	}
	state := s.ensureMaterialsFileJobs()
	prefix := action.Kind
	if mutation {
		prefix = "mutation"
	}
	key := prefix + "\x00" + materialsFileScopeKey(scope)
	ctx, cancel := context.WithCancel(context.Background())
	request := &materialsFileJob{key: key, scope: scope, action: action, mutation: mutation, backend: s.backend, window: s.win, ctx: ctx, cancel: cancel, done: make(chan struct{}), rename: rename, textDelete: textDelete}
	state.mu.Lock()
	if state.closed || state.barrier != nil {
		state.mu.Unlock()
		cancel()
		s.rejectMaterialsFileJob(action, errors.New("资料文件操作正在关闭或等待目录变更。"))
		return true
	}
	previous := state.current[key]
	if mutation && previous != nil {
		state.mu.Unlock()
		cancel()
		s.rejectMaterialsFileJob(action, errors.New("另一项资料文件操作仍在清理，请稍后重试。"))
		return true
	}
	var canceledReads []*materialsFileJob
	if mutation {
		for readKey, read := range state.current {
			if !read.mutation && materialsFileScopeKey(read.scope) == materialsFileScopeKey(scope) {
				canceledReads = append(canceledReads, read)
				delete(state.current, readKey)
			}
		}
	}
	state.current[key] = request
	state.running[request] = struct{}{}
	state.mu.Unlock()
	if previous != nil {
		previous.cancel()
	}
	for _, read := range canceledReads {
		read.cancel()
		s.restoreMaterialsFileReadRequest(read.action, false)
	}
	s.win.Invalidate()
	go s.runMaterialsFileJob(state, request)
	return true
}

func (s *app) runMaterialsFileJob(state *materialsFileJobState, request *materialsFileJob) {
	var result application.ActionResult
	var resultErr error
	defer func() {
		if recovered := recover(); recovered != nil {
			resultErr = fmt.Errorf("资料文件操作异常：%v", recovered)
			request.leaseMu.Lock()
			request.renamed = request.rename != nil
			request.leaseMu.Unlock()
		}
		request.leaseMu.Lock()
		request.renamed = request.renamed || materialsRenameCompleted(result)
		request.result, request.resultErr, request.resultReady = result, resultErr, true
		request.leaseMu.Unlock()
		if request.window.IsDestroyed() || request.ctx.Err() != nil && !request.barrierOwnsResult() {
			request.abandonRenameLease()
		}
		if !request.window.IsDestroyed() {
			request.window.Update(func() { s.finishMaterialsFileJob(state, request, result, resultErr) })
		}
		// A path barrier joins the backend before its UI callback settles the
		// retained result. Shutdown may still abandon without touching UI state.
		state.mu.Lock()
		delete(state.running, request)
		close(request.done)
		state.mu.Unlock()
	}()
	if err := request.ctx.Err(); err != nil {
		resultErr = err
		return
	}
	if request.rename != nil {
		lease, err := s.prepareMarkdownAutosaveRename(request.ctx, request.rename)
		if err != nil {
			resultErr = err
			return
		}
		request.leaseMu.Lock()
		request.lease = lease
		request.leaseMu.Unlock()
	}
	if request.textDelete != nil {
		lease, err := request.textDelete.autosave.Quiesce(request.ctx, request.textDelete.keys)
		if err != nil {
			resultErr = err
			return
		}
		request.leaseMu.Lock()
		request.textDeleteLease = lease
		request.leaseMu.Unlock()
	}
	if err := request.ctx.Err(); err != nil {
		resultErr = err
		return
	}
	result, resultErr = request.backend.Dispatch(request.ctx, request.action)
}

func (request *materialsFileJob) abandonRenameLease() {
	request.leaseMu.Lock()
	defer request.leaseMu.Unlock()
	request.finishMaterialTextDeleteLeaseLocked(request.result)
	if request.lease != nil && !request.settled {
		request.lease.Abandon(request.renamed)
		request.settled = true
	}
}

func (request *materialsFileJob) completeRenameLease(result application.ActionResult) error {
	request.leaseMu.Lock()
	defer request.leaseMu.Unlock()
	if request.lease == nil || request.settled {
		return nil
	}
	request.settled = true
	return completeMaterialsRenameLease(request.lease, request.scope.root, request.action, result)
}

func (s *app) finishMaterialsFileJob(state *materialsFileJobState, request *materialsFileJob, result application.ActionResult, resultErr error) {
	request.leaseMu.Lock()
	if request.consumed {
		request.leaseMu.Unlock()
		return
	}
	barrier := request.barrier
	if barrier == nil {
		request.consumed = true
	}
	request.leaseMu.Unlock()
	if barrier != nil {
		barrier.owner.mu.Lock()
		deferred := !barrier.resumed && !barrier.abandoned && !barrier.owner.closed
		barrier.owner.mu.Unlock()
		if !deferred {
			// A root action may have stopped waiting before this worker ended.
			// Its real barrier still owns the result until this fallback consumes it.
			s.reportMaterialsFileBarrierError(s.settleMaterialsFileBarrierRequest(barrier, request))
		}
		return
	}
	state.mu.Lock()
	current := state.current[request.key] == request
	closed := state.closed
	if current {
		delete(state.current, request.key)
	}
	state.mu.Unlock()
	defer request.cancel()
	identityMatches := s.materialsFileScopeMatches(request.scope)
	if !current || closed || request.window.IsDestroyed() || !identityMatches || request.ctx.Err() != nil {
		request.abandonRenameLease()
		if current && identityMatches && !closed {
			s.restoreMaterialsFileReadRequest(request.action, false)
		}
		return
	}
	s.retireMaterialTextDeleteResult(request, result)
	defer request.finishMaterialTextDeleteLease(result)
	if leaseErr := request.completeRenameLease(result); leaseErr != nil {
		s.lastError = "名称已更新，但草稿迁移未完成：" + leaseErr.Error()
	}
	if !s.actionResultRelevant(request.action) {
		return
	}
	if resultErr != nil {
		s.restoreMaterialsFileReadRequest(request.action, true)
		s.rejectMaterialsFileJob(request.action, resultErr)
		return
	}
	// Reuse existing feature result routing. A material result does not
	// replace the Snapshot owned by a concurrent global operation.
	s.applyActionResult(request.action, result)
	if request.action.Kind == "document.rename" && materialsRenameCompleted(result) && s.currentTask().id == request.scope.taskID {
		s.reloadMaterialsDocuments()
	}
}

func (s *app) restoreMaterialsFileReadRequest(action application.Action, settled bool) {
	if !s.actionResultRelevant(action) {
		return
	}
	switch action.Kind {
	case "document.list":
		s.documentsRequested = false
		if settled {
			s.documentsLoaded = true
		}
	case "document.read":
		s.documentReadPending = false
	}
}

func (s *app) rejectMaterialsFileJob(action application.Action, err error) {
	s.restoreMaterialsFileReadRequest(action, false)
	if action.TaskID != s.currentTask().id {
		return
	}
	if !s.applyMaterialsFileActionFailure(action, err) {
		s.lastError = err.Error()
	}
}

// Root-changing actions synchronously close the launch boundary before their
// background worker waits. Lease abandonment never inspects app/UI maps.
func (s *app) pauseMaterialsFileJobs(action application.Action) *materialsFileJobBarrier {
	if !materialsMutationNeedsWatchBarrier(action) {
		return nil
	}
	state := s.ensureMaterialsFileJobs()
	state.mu.Lock()
	barrier := &materialsFileJobBarrier{owner: state, jobs: materialsFileJobRequestsLocked(state)}
	state.barrier = barrier
	for _, request := range barrier.jobs {
		request.leaseMu.Lock()
		if !request.consumed {
			request.barrier = barrier
		}
		request.leaseMu.Unlock()
	}
	state.mu.Unlock()
	for _, request := range barrier.jobs {
		request.cancel()
		s.restoreMaterialsFileReadRequest(request.action, false)
	}
	return barrier
}

func materialsFileJobRequestsLocked(state *materialsFileJobState) []*materialsFileJob {
	seen := make(map[*materialsFileJob]bool)
	requests := make([]*materialsFileJob, 0, len(state.current)+len(state.running))
	for request := range state.running {
		seen[request] = true
		requests = append(requests, request)
	}
	for _, request := range state.current {
		if !seen[request] {
			requests = append(requests, request)
		}
	}
	return requests
}

func (barrier *materialsFileJobBarrier) wait(ctx context.Context) error {
	if barrier == nil {
		return nil
	}
	for _, request := range barrier.jobs {
		select {
		case <-request.done:
			if barrier.owner == nil {
				// closeMaterialsFileJobs has no later UI settlement callback.
				request.abandonRenameLease()
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return ctx.Err()
}

func (barrier *materialsFileJobBarrier) Abandon() {
	if barrier == nil {
		return
	}
	if barrier.owner != nil {
		barrier.owner.mu.Lock()
		barrier.abandoned = true
		barrier.owner.mu.Unlock()
	}
	for _, request := range barrier.jobs {
		request.cancel()
		select {
		case <-request.done:
			request.abandonRenameLease()
		default:
		}
	}
}

func (s *app) resumeMaterialsFileJobs(barrier *materialsFileJobBarrier) {
	if barrier == nil {
		return
	}
	barrier.owner.mu.Lock()
	if barrier.owner.barrier == barrier {
		barrier.owner.barrier = nil
	}
	barrier.resumed = true
	barrier.owner.mu.Unlock()
	var settlementErr error
	for _, request := range barrier.jobs {
		request.leaseMu.Lock()
		ready := request.resultReady
		request.leaseMu.Unlock()
		if ready {
			settlementErr = errors.Join(settlementErr, s.settleMaterialsFileBarrierRequest(barrier, request))
		}
	}
	s.reportMaterialsFileBarrierError(settlementErr)
}

func (s *app) stopMaterialsFileJobs() {
	state := s.materialsFileJobs
	if state == nil {
		return
	}
	state.mu.Lock()
	state.closed = true
	requests := materialsFileJobRequestsLocked(state)
	state.mu.Unlock()
	for _, request := range requests {
		request.cancel()
	}
}

func (s *app) closeMaterialsFileJobs(ctx context.Context) error {
	s.stopMaterialsFileJobs()
	state := s.materialsFileJobs
	if state == nil {
		return nil
	}
	state.mu.Lock()
	requests := materialsFileJobRequestsLocked(state)
	state.mu.Unlock()
	return (&materialsFileJobBarrier{jobs: requests}).wait(ctx)
}
