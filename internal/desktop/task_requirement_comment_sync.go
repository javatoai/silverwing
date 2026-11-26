package desktop

import (
	"context"
	"strings"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

type taskRequirementCommentSyncView struct {
	cancel context.CancelFunc
	done   <-chan struct{}
	tasks  map[string]*taskRequirementCommentSyncNotice
}

type taskRequirementCommentSyncNotice struct {
	status  application.TaskRequirementCommentSyncStatus
	failure string
}

func (s *app) startTaskRequirementCommentSync() {
	backend, ok := s.backend.(application.TaskRequirementCommentSyncBackend)
	if !ok || s.win == nil || s.taskUnit.state.requirementCommentSync != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.taskUnit.state.requirementCommentSync = &taskRequirementCommentSyncView{cancel: cancel, done: done}
	window, changes := s.win, backend.TaskRequirementCommentSyncChanges()
	backend.StartTaskRequirementCommentSync()
	s.observeTaskRequirementCommentSync()
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case _, open := <-changes:
				if !open || window.IsDestroyed() {
					return
				}
				window.Update(func() {
					if ctx.Err() == nil && !window.IsDestroyed() {
						s.observeTaskRequirementCommentSync()
					}
				})
			}
		}
	}()
}

func (s *taskController) stopTaskRequirementCommentSync() {
	if view := s.state.requirementCommentSync; view != nil && view.cancel != nil {
		view.cancel()
	}
}

func (s *app) closeTaskRequirementCommentSync(ctx context.Context) error {
	s.taskUnit.stopTaskRequirementCommentSync()
	if backend, ok := s.backend.(application.TaskRequirementCommentSyncBackend); ok {
		if err := backend.CloseTaskRequirementCommentSync(ctx); err != nil {
			return err
		}
	}
	if view := s.taskUnit.state.requirementCommentSync; view != nil && view.done != nil {
		select {
		case <-view.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func taskRequirementCommentStatus(backend application.TaskRequirementCommentSyncBackend, item task) application.TaskRequirementCommentSyncStatus {
	if item.path == "" || item.createdAt == "" {
		return application.TaskRequirementCommentSyncStatus{}
	}
	status := backend.TaskRequirementCommentSyncStatus(item.path, item.createdAt)
	if status.State != "conflict" && !sameRequirementReaderLink(status.RequirementURL, item.requirementURL) {
		return application.TaskRequirementCommentSyncStatus{}
	}
	return status
}

func taskRequirementCommentSyncNeedsAttention(state string) bool {
	switch state {
	case "failed", "uncertain", "conflict", "blocked":
		return true
	default:
		return false
	}
}

func (s *app) canRetryTaskRequirementCommentSync(item task) bool {
	backend, ok := s.backend.(application.TaskRequirementCommentSyncBackend)
	if !ok || item.path == "" || item.createdAt == "" || domain.ParseFeishuWorkItemLink(item.requirementURL) == nil {
		return false
	}
	state := taskRequirementCommentStatus(backend, item).State
	return state != "pending" && state != "syncing"
}

func (s *app) retryTaskRequirementCommentSync(item task) {
	backend, ok := s.backend.(application.TaskRequirementCommentSyncBackend)
	if !ok || !s.canRetryTaskRequirementCommentSync(item) {
		return
	}
	if err := backend.RetryTaskRequirementCommentSync(item.path, item.createdAt); err != nil {
		s.lastError = taskRequirementCommentSyncFailure(item, err.Error())
		return
	}
	s.observeTaskRequirementCommentSync()
}

func taskRequirementCommentSyncFailure(item task, reason string) string {
	message := "需求评论同步未完成 · " + item.title
	if reason = strings.TrimSpace(reason); reason != "" {
		message += "\n" + reason
	}
	return message
}

// The backend signals changes on a coalescing channel. Sample all current
// tasks, including a task just added to a refreshed snapshot.
func (s *app) observeTaskRequirementCommentSync() {
	view := s.taskUnit.state.requirementCommentSync
	backend, ok := s.backend.(application.TaskRequirementCommentSyncBackend)
	if view == nil || !ok {
		return
	}
	next := make(map[string]*taskRequirementCommentSyncNotice)
	var published []string
	for _, item := range s.taskUnit.state.tasks {
		status := taskRequirementCommentStatus(backend, item)
		if status.State == "" {
			continue
		}
		key := taskNavigationKey(item)
		previous := view.tasks[key]
		if previous != nil && previous.status == status {
			next[key] = previous
			continue
		}
		notice := &taskRequirementCommentSyncNotice{status: status}
		if taskRequirementCommentSyncNeedsAttention(status.State) {
			notice.failure = taskRequirementCommentSyncFailure(item, status.Message)
		}
		next[key] = notice
		if status.Revision != 0 && sameRequirementReaderLink(status.RequirementURL, item.requirementURL) &&
			(previous == nil || previous.status.Revision != status.Revision || previous.status.RequirementURL != status.RequirementURL) {
			published = append(published, status.RequirementURL)
		}
	}
	view.tasks = next
	if len(published) != 0 {
		s.refreshTaskRequirementCommentReaders(published)
	}
}

func (s *app) refreshTaskRequirementCommentReaders(links []string) {
	var visible *requirementReaderState
	jobs := s.requirementUnit.state.requirementContentJobs
	for key, reader := range s.requirementUnit.state.requirementReaders {
		for _, link := range links {
			if sameRequirementReaderLink(reader.url, link) {
				reader.commentsDirty, reader.commentsError = true, ""
				s.retireRequirementReaderRequest(reader, "requirements.comments")
				if jobs != nil && key == jobs.visible {
					visible = reader
				}
				break
			}
		}
	}
	if visible != nil {
		s.requestRequirementContent(visible, true, true)
	}
}

func (s *app) renderTaskRequirementCommentSyncNotifications(c *ui.Context) {
	view := s.taskUnit.state.requirementCommentSync
	if view == nil {
		return
	}
	const prefix = "task-requirement-comment-sync-"
	empty := ""
	for key, notice := range view.tasks {
		s.operationNotifications.present(c, prefix+key, &empty, &notice.failure)
	}
	for scope := range s.operationNotifications.shown {
		if key, ok := strings.CutPrefix(scope, prefix); ok && view.tasks[key] == nil {
			s.operationNotifications.present(c, scope, &empty, &empty)
			delete(s.operationNotifications.shown, scope)
		}
	}
}
