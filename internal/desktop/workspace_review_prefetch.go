package desktop

import (
	"context"
	"strings"

	"silverwing-go/internal/application"
	"silverwing-go/internal/workspace"
)

type workspaceReviewPrefetchJob struct {
	dialog   *workspaceHistoryState
	identity *workspaceHistoryJob
	request  application.WorkspaceDiffPrefetchRequest
	cancel   context.CancelFunc
	done     chan struct{}
}

func (s *app) cancelWorkspaceReviewPrefetch(state *workspaceHistoryState) {
	if state == nil {
		return
	}
	if state.Prefetch != nil {
		state.Prefetch.cancel()
		state.Prefetch = nil
	}
	state.PrefetchKey = ""
}

// updateWorkspaceReviewPrefetch may be called after building the visible file
// rows. Nil keeps the last visibility hint and fills spare slots with neighbors.
// It never shares the foreground history worker's single current-job slot.
func (s *app) updateWorkspaceReviewPrefetch(state *workspaceHistoryState, visible []workspace.CompareChange) {
	if state == nil || state != s.workspaceHistory || !state.Open || state.Mode != "review" {
		return
	}
	if visible != nil {
		state.PrefetchVisible = append(state.PrefetchVisible[:0], visible...)
	}
	backend, ok := s.backend.(application.WorkspaceDiffPrefetchBackend)
	if !ok || state.PreviewLoading || state.FilesLoading || !workspaceReviewSnapshotsReady(state) || state.SelectedPath == "" {
		return
	}
	current, found := workspaceReviewSelection(state.Files, state.SelectedPath, state.SelectedOldPath, state.SelectedKind)
	if !found {
		return
	}
	candidates := workspaceReviewPrefetchWindow(state.Files, current, state.PrefetchVisible)
	parts := []string{state.DiffSessionID, current.Path, current.OldPath, current.Kind}
	for _, file := range candidates {
		parts = append(parts, file.Path, file.OldPath, file.Kind)
	}
	key := strings.Join(parts, "\x00")
	if state.PrefetchKey == key {
		return
	}
	s.cancelWorkspaceReviewPrefetch(state)
	var compare workspace.CompareRequest
	if decodeWorkspaceValue(workspaceReviewComparePayload(state), &compare) != nil {
		return
	}
	compare.ExpectedBaseCommit, compare.ExpectedTargetCommit = state.BaseCommit, state.TargetCommit
	action := application.Action{TaskID: state.TaskID, ServiceID: state.ServiceID, ModuleID: state.ModuleID, Path: state.Path}
	task := s.taskByID(state.TaskID)
	item, exists := workspaceHistoryTaskWorkspace(task, action)
	if !exists || task.id == "" || s.currentTask().id != task.id {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	job := &workspaceReviewPrefetchJob{
		dialog: state, cancel: cancel, done: make(chan struct{}),
		identity: &workspaceHistoryJob{action: action, dialog: state, taskPath: task.path, taskCreatedAt: task.createdAt, home: s.snapshot.Home,
			gitExecutable: s.configString("gitExecutablePath"), workspaceIdentity: workspaceHistoryWorkspaceIdentity(item), branchConfigIdentity: s.configJSON, pageGeneration: state.ReviewGeneration, ctx: ctx},
		request: application.WorkspaceDiffPrefetchRequest{TaskID: state.TaskID, ServiceID: state.ServiceID, ModuleID: state.ModuleID, Path: state.Path,
			SessionID: state.DiffSessionID, Compare: compare, Current: current, Candidates: candidates},
	}
	jobs := s.ensureWorkspaceHistoryJobs()
	jobs.mu.Lock()
	if jobs.closed {
		jobs.mu.Unlock()
		cancel()
		return
	}
	if jobs.prefetchRunning == nil {
		jobs.prefetchRunning = make(map[*workspaceReviewPrefetchJob]struct{})
	}
	jobs.prefetchRunning[job] = struct{}{}
	jobs.mu.Unlock()
	state.Prefetch, state.PrefetchKey = job, key
	go func() {
		defer cancel()
		defer func() { jobs.mu.Lock(); delete(jobs.prefetchRunning, job); close(job.done); jobs.mu.Unlock() }()
		_ = backend.PrefetchWorkspaceDiff(ctx, job.request)
	}()
}

func workspaceReviewPrefetchWindow(files []workspace.CompareChange, current workspace.CompareChange, visible []workspace.CompareChange) []workspace.CompareChange {
	const limit = 16
	result := make([]workspace.CompareChange, 0, limit)
	seen := make(map[string]bool)
	add := func(file workspace.CompareChange) {
		key := file.Path + "\x00" + file.OldPath + "\x00" + file.Kind
		if !seen[key] && len(result) < limit {
			seen[key] = true
			result = append(result, file)
		}
	}
	add(current)
	for _, file := range visible {
		for _, candidate := range files {
			if candidate.Path == file.Path && candidate.OldPath == file.OldPath && candidate.Kind == file.Kind {
				add(candidate)
				break
			}
		}
	}
	index := 0
	for i, file := range files {
		if file.Path == current.Path && file.OldPath == current.OldPath && file.Kind == current.Kind {
			index = i
			break
		}
	}
	for distance := 1; len(result) < min(limit, len(files)) && distance < len(files); distance++ {
		if index+distance < len(files) {
			add(files[index+distance])
		}
		if index-distance >= 0 {
			add(files[index-distance])
		}
	}
	return result
}

func (s *app) observeWorkspaceReviewPrefetch() {
	state := s.workspaceHistory
	if state == nil || state.Prefetch == nil {
		return
	}
	job := state.Prefetch
	i := job.identity
	task := s.taskByID(i.action.TaskID)
	item, exists := workspaceHistoryTaskWorkspace(task, i.action)
	if !state.Open || !s.workspaceReviewPageVisible() || state.ReviewGeneration != i.pageGeneration || state.DiffSessionID != job.request.SessionID ||
		s.currentTask().id != i.action.TaskID || task.path != i.taskPath || task.createdAt != i.taskCreatedAt || s.snapshot.Home != i.home ||
		s.configString("gitExecutablePath") != i.gitExecutable || s.configJSON != i.branchConfigIdentity || !exists || workspaceHistoryWorkspaceIdentity(item) != i.workspaceIdentity {
		s.cancelWorkspaceReviewPrefetch(state)
	}
}
