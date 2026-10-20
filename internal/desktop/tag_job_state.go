package desktop

import (
	"context"
	"sync"

	"silverwing-go/internal/application"
)

// Views and subscriptions belong to the window thread. Only the worker set is
// shared with background requests, so closing never races the presentation map.
type tagDesktopJobs struct {
	window         tagGenbuAutoProbeWindow
	views          map[application.TagJobScope]*tagDesktopJobView
	current        *tagDesktopJobSubscription
	mu             sync.Mutex
	workers        map[*tagDesktopJobWorker]struct{}
	closed         bool
	nextSubmission uint64
}

type tagDesktopJobView struct {
	snapshot       application.TagJobSnapshot
	commands       []*tagDesktopJobCommand
	commandRunning bool
	focusBatch     string
	focusGroup     string
}

type tagDesktopJobCommand struct {
	action  application.Action
	pending *PendingTagSubmission
}

type tagDesktopJobWorker struct {
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

type tagDesktopJobSubscription struct {
	*tagDesktopJobWorker
	scope    application.TagJobScope
	view     *tagDesktopJobView
	finished bool
	loaded   bool
}

type tagJobProgress struct {
	Total, Running, Completed, Failed int
	Busy                              bool
}

func (s *app) tagJobScope() application.TagJobScope {
	t := s.currentTask()
	account := ""
	if identity := s.requirementIdentityJobs; identity != nil && identity.confirmed {
		account = identity.identity
	}
	return application.TagJobScope{Home: s.snapshot.Home, TaskRoot: s.configString("taskRoot"),
		TaskID: t.id, TaskPath: t.path, TaskCreatedAt: t.createdAt,
		ConfigIdentity: s.tagConfigurationIdentity(), AccountIdentity: account}
}

func (s *app) ensureTagDesktopJobs() *tagDesktopJobs {
	if s.tagJobs == nil {
		s.tagJobs = &tagDesktopJobs{views: make(map[application.TagJobScope]*tagDesktopJobView), workers: make(map[*tagDesktopJobWorker]struct{})}
	}
	return s.tagJobs
}

func (s *app) tagJobView(scope application.TagJobScope) *tagDesktopJobView {
	state := s.ensureTagDesktopJobs()
	if state.views[scope] == nil {
		state.views[scope] = &tagDesktopJobView{}
	}
	return state.views[scope]
}

func (s *app) currentTagJobView() *tagDesktopJobView {
	if s == nil || s.tagJobs == nil {
		return nil
	}
	return s.tagJobs.views[s.tagJobScope()]
}

// Job records own local execution state. Preserve independently newer Genbu
// results when a phase event carries an earlier copy of the same Tag version.
func (s *app) upsertTagJobRecords(snapshot application.TagJobSnapshot) {
	for _, item := range snapshot.Items {
		if item.Record == nil || item.Record.ID == "" || item.Record.TaskID != snapshot.Scope.TaskID {
			continue
		}
		incoming := *item.Record
		if incoming.ID != item.OperationID || incoming.ServiceID != item.ServiceID || incoming.ModuleID != item.ModuleID {
			continue
		}
		found := false
		for i, existing := range s.snapshot.TagHistory {
			if existing.ID != incoming.ID || existing.TaskID != incoming.TaskID {
				continue
			}
			if existing.ServiceID != incoming.ServiceID || existing.ModuleID != incoming.ModuleID {
				found = true
				break
			}
			if existing.Tag == incoming.Tag && !tagGenbuStatusNewer(incoming, existing) {
				incoming.GenbuStatus = existing.GenbuStatus
			}
			s.snapshot.TagHistory[i] = incoming
			found = true
			break
		}
		if !found {
			s.snapshot.TagHistory = append(s.snapshot.TagHistory, incoming)
		}
	}
}

func tagGenbuStatusNewer(incoming, existing application.TagRecord) bool {
	if incoming.GenbuStatus.CheckedAt == nil {
		return false
	}
	if existing.GenbuStatus.CheckedAt == nil {
		return true
	}
	return tagRecordTime(*incoming.GenbuStatus.CheckedAt).After(tagRecordTime(*existing.GenbuStatus.CheckedAt))
}

func (s *app) restoreActiveTagJobRecords() {
	if view := s.currentTagJobView(); view != nil && view.snapshot.Busy {
		s.upsertTagJobRecords(view.snapshot)
	}
}

func (s *app) acceptTagJobSnapshot(view *tagDesktopJobView, scope application.TagJobScope, snapshot application.TagJobSnapshot, newJob bool) bool {
	if view == nil || snapshot.Scope != scope || snapshot.ID == "" {
		return false
	}
	if snapshot.ID == view.snapshot.ID && snapshot.Sequence <= view.snapshot.Sequence {
		return false
	}
	if !newJob && view.snapshot.ID != "" && snapshot.ID != view.snapshot.ID {
		return false
	}
	previousItems := make(map[string]bool, len(view.snapshot.Items))
	if !newJob {
		for _, item := range view.snapshot.Items {
			previousItems[item.ID] = true
		}
	}
	for _, item := range snapshot.Items {
		if !previousItems[item.ID] {
			if group := tagJobItemGroup(snapshot, item); group != "" {
				view.focusBatch, view.focusGroup = group, group
			}
		}
	}
	completed := view.snapshot.Busy && !snapshot.Busy
	view.snapshot = snapshot
	if s.tagJobScope() == scope {
		s.upsertTagJobRecords(snapshot)
		if completed {
			// The task lease has been released. Read history again so a newly
			// completed batch also observes the configured retention allowance.
			s.ensureTagGenbuAutoProbeState().refreshPending = true
		}
		for _, item := range snapshot.Items {
			if item.Kind != "tag.workspace-check" || !item.Done {
				continue
			}
			state := s.tagWorkspaceCheckState(item.OperationID)
			if state.OperationID != item.OperationID || state.TaskID != scope.TaskID {
				continue
			}
			state.Loading, state.Error, state.Check = false, item.Error, nil
			if item.Check != nil {
				state.Check = &application.TagWorkspaceCheck{Clean: item.Check.Clean, Changes: append([]string(nil), item.Check.Changes...)}
			}
			s.saveTagWorkspaceCheckState(state)
		}
	}
	return true
}

func (s *app) tagRecordJobItem(record application.TagRecord) (application.TagJobItem, bool) {
	view := s.currentTagJobView()
	if view == nil || record.TaskID != view.snapshot.Scope.TaskID {
		return application.TagJobItem{}, false
	}
	for i := len(view.snapshot.Items) - 1; i >= 0; i-- {
		item := view.snapshot.Items[i]
		if item.OperationID == record.ID && item.ServiceID == record.ServiceID && item.ModuleID == record.ModuleID {
			return item, true
		}
	}
	return application.TagJobItem{}, false
}

func (s *app) tagWorkspaceBusy(t task, workspace application.Workspace) bool {
	view := s.currentTagJobView()
	if view == nil || t.id != s.currentTask().id {
		return false
	}
	for _, item := range view.snapshot.Items {
		if !(item.Busy || item.Queued) {
			continue
		}
		if item.WorkspacePath != "" && workspaceHealthPathKey(item.WorkspacePath) == workspaceHealthPathKey(workspace.Path) || item.ServiceID == workspace.ServiceID && item.ModuleID == workspace.ModuleID {
			return true
		}
		for _, other := range t.workspaces {
			if other.ServiceID == item.ServiceID && other.ModuleID == item.ModuleID && tagWorkspaceRepositoryKey(other) == tagWorkspaceRepositoryKey(workspace) {
				return true
			}
		}
	}
	for _, command := range view.commands {
		action := command.action
		if action.ServiceID == workspace.ServiceID && action.ModuleID == workspace.ModuleID {
			return true
		}
		for _, key := range tagJobSelectionKeys(action.Payload) {
			if key == tagBuildWorkspaceKey(workspace) {
				return true
			}
		}
	}
	return false
}

func tagWorkspaceRepositoryKey(workspace application.Workspace) string {
	return firstNonEmpty(workspace.Record.RepositoryID, workspaceHealthPathKey(workspace.Path), workspace.ServiceID+":"+workspace.ModuleID)
}

func tagJobSelectionKeys(payload map[string]any) []string {
	if keys, ok := payload["selectionKeys"].([]string); ok {
		return keys
	}
	keys := []string{}
	if values, ok := payload["selectionKeys"].([]any); ok {
		for _, value := range values {
			if key, ok := value.(string); ok {
				keys = append(keys, key)
			}
		}
	}
	return keys
}

func (s *app) tagRecordBusy(record application.TagRecord) bool {
	t := s.taskByID(record.TaskID)
	for _, workspace := range t.workspaces {
		if workspace.ServiceID == record.ServiceID && workspace.ModuleID == record.ModuleID {
			return s.tagWorkspaceBusy(t, workspace)
		}
	}
	item, ok := s.tagRecordJobItem(record)
	return ok && (item.Busy || item.Queued)
}

func (s *app) tagRecordPhase(record application.TagRecord) string {
	item, ok := s.tagRecordJobItem(record)
	if !ok {
		return ""
	}
	switch item.Phase {
	case application.TagJobPhaseQueued:
		return "等待执行"
	case application.TagJobPhaseChecking:
		return "检查工作区"
	case application.TagJobPhasePushingSource:
		return "推送来源分支"
	case application.TagJobPhaseMerging:
		return "合并到目标分支"
	case application.TagJobPhasePushingTarget:
		return "推送目标分支"
	case application.TagJobPhaseCreatingTag:
		return "创建 Tag"
	case application.TagJobPhasePushingTag:
		return "推送 Tag"
	case application.TagJobPhaseCompleted:
		return "已完成"
	case application.TagJobPhaseConflict:
		return "存在冲突"
	case application.TagJobPhaseFailed:
		return "执行失败"
	case application.TagJobPhaseCanceled:
		return "已取消"
	}
	return ""
}

func (s *app) tagBatchProgress(batchID string) tagJobProgress {
	var progress tagJobProgress
	view := s.currentTagJobView()
	if view == nil {
		return progress
	}
	for _, item := range view.snapshot.Items {
		if batchID == "" || tagJobItemGroup(view.snapshot, item) != batchID {
			continue
		}
		progress.Total++
		if item.Busy || item.Queued {
			progress.Running++
			progress.Busy = true
		} else if item.Done {
			if item.Phase == application.TagJobPhaseCompleted {
				progress.Completed++
			} else {
				progress.Failed++
			}
		}
	}
	return progress
}

// A job can retry/check records in several existing history groups. Its own
// BatchID belongs only to new build records; historical ownership never moves.
func tagJobItemGroup(snapshot application.TagJobSnapshot, item application.TagJobItem) string {
	if item.Record != nil {
		if item.Record.TaskID != snapshot.Scope.TaskID {
			return ""
		}
		return firstNonEmpty(item.Record.BatchID, item.Record.ID)
	}
	if item.Kind == "tag.build" {
		return snapshot.BatchID
	}
	return ""
}

func (s *app) tagBatchFocused(batchID string) bool {
	view := s.currentTagJobView()
	if view == nil {
		return false
	}
	if view.focusGroup != "" {
		return view.focusGroup == batchID
	}
	if len(view.snapshot.Items) == 0 {
		return false
	}
	return tagJobItemGroup(view.snapshot, view.snapshot.Items[len(view.snapshot.Items)-1]) == batchID
}

func (s *app) consumeTagBatchFocus(batchID string) bool {
	view := s.currentTagJobView()
	if view == nil || view.focusBatch != batchID {
		return false
	}
	view.focusBatch = ""
	return true
}
