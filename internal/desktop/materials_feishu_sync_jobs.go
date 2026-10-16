package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"

	"github.com/egoist/mygo"
)

func (s *app) dispatchMaterialsFeishuSyncJob(action application.Action) bool {
	switch action.Kind {
	case "materials.sync":
		s.beginMaterialsFeishuSync(action)
	case "materials.sync.approve":
		s.approveMaterialsFeishuSync(payloadString(action.Payload, "runId"))
	case "materials.sync.cancel":
		s.stopMaterialsFeishuSync(payloadString(action.Payload, "runId"))
	case "materials.sync.retry-comment":
		s.retryMaterialsFeishuSyncComment(payloadString(action.Payload, "runId"))
	case "materials.sync.hide":
		s.hideMaterialsFeishuSync(s.sessionByRunID(payloadString(action.Payload, "runId")))
	case "materials.sync.reveal":
		s.revealMaterialsFeishuSync(s.sessionByRunID(payloadString(action.Payload, "runId")))
	default:
		return false
	}
	return true
}

func (s *app) beginMaterialsFeishuSync(action application.Action) {
	if s.backend == nil || s.win == nil || s.win.IsDestroyed() {
		s.lastError = "飞书资料同步后端或窗口不可用。"
		return
	}
	jobs := s.ensureMaterialsFeishuSyncJobs()
	jobs.mu.Lock()
	active := jobs.active
	jobs.mu.Unlock()
	if active != nil {
		s.revealMaterialsFeishuSync(active)
		return
	}
	t := s.taskByID(action.TaskID)
	if t.id == "" || !t.materialsReady() || !s.materialsFeishuSyncActionMatchesTask(action, t) {
		s.lastError = "同步任务身份或本地资料目录已变化，请重新打开任务后重试。"
		return
	}
	key := materialsFeishuSyncTaskKey(t)
	if current := s.currentMaterialsFeishuSyncSession(); current != nil && current.TaskKey == key && current.Stage != materialsFeishuSyncFinished {
		s.revealMaterialsFeishuSync(current)
		return
	}
	if previous := jobs.unread[key]; previous != nil && previous.Stage != materialsFeishuSyncFinished {
		s.revealMaterialsFeishuSync(previous)
		return
	}
	if s.materialsFeishuSyncHasUnsavedDraft(t) {
		s.lastError = materialsFeishuUnsavedDraftMessage
		if s.win != nil {
			s.win.Invalidate()
		}
		return
	}
	request := domain.MaterialsSyncRequest{
		TargetURL:        strings.TrimSpace(payloadString(action.Payload, "targetUrl")),
		WriteRoot:        strings.TrimSpace(payloadString(action.Payload, "writeRoot")),
		RequirementID:    strings.TrimSpace(payloadString(action.Payload, "requirementId")),
		RequirementTitle: strings.TrimSpace(payloadString(action.Payload, "requirementTitle")),
		Subdirectory:     strings.TrimSpace(payloadString(action.Payload, "subdirectory")),
	}
	selectedPaths, selectionErr := materialsFeishuSelectedPaths(action.Payload)
	if selectionErr != nil || len(selectedPaths) > 1 {
		s.lastError = "文件同步范围无效，请从当前任务资料文件列表重新操作。"
		return
	}
	if len(selectedPaths) > 0 {
		if err := s.validateMaterialsFeishuSelectedPaths(t, selectedPaths); err != nil {
			s.lastError = "文件同步范围已变化，请刷新后重试。"
			return
		}
	}
	request.SelectedPaths = append([]string(nil), selectedPaths...)
	if request.WriteRoot == "" || request.RequirementID == "" || request.RequirementTitle == "" || request.TargetURL == "" {
		s.lastError = "飞书同步请求缺少目标、需求编号或资料目录。"
		return
	}
	if _, err := domain.ValidateMaterialsFeishuTarget(request.TargetURL); err != nil {
		s.lastError = "飞书同步目标无效：" + err.Error()
		return
	}
	services := materialsFeishuSyncServices(action.Payload["services"])
	comment := application.MaterialsSyncCommentRequest{
		TargetURL: request.TargetURL, RequirementLink: t.requirementURL,
		RequirementID: request.RequirementID, RequirementTitle: request.RequirementTitle,
		WriteRoot: request.WriteRoot, Subdirectory: request.Subdirectory, Services: services,
	}
	session := &materialsFeishuSyncSession{
		RunID: newDesktopEditorID("materials-feishu-sync"), TaskKey: materialsFeishuSyncTaskKey(t),
		TaskID: t.id, TaskPath: t.path, TaskCreatedAt: t.createdAt, TaskGroupID: t.groupID,
		TaskRequirementID: t.requirementID,
		MaterialsRoot:     t.materialsPath, RequirementURL: t.requirementURL, TaskTitle: t.title,
		Home: s.snapshot.Home, Request: request, Comment: comment,
		CommentAvailable: s.materialsFeishuCommentAvailable(t),
		Stage:            materialsFeishuSyncPlanning, Visible: true, StartedAt: time.Now(),
		DeletionPaths: make(map[string]bool), OverwritePaths: make(map[string]bool),
		Progress: domain.MaterialsSyncProgress{ActivePaths: []string{}, WaitingRetries: []domain.MaterialsSyncCommandRetry{}},
		Done:     make(chan struct{}),
	}
	jobs.mu.Lock()
	if jobs.closed {
		jobs.mu.Unlock()
		s.lastError = "飞书资料同步服务正在关闭。"
		return
	}
	if jobs.active != nil {
		active := jobs.active
		jobs.mu.Unlock()
		s.revealMaterialsFeishuSync(active)
		return
	}
	if previous := jobs.current; previous != nil && previous.Stage == materialsFeishuSyncFinished {
		previous.Visible = false
		jobs.unread[previous.TaskKey] = previous
	}
	jobs.active = session
	jobs.releaseOnCleanup = ensureMaterialsFeishuSyncReleaseSet(jobs.releaseOnCleanup)
	delete(jobs.releaseOnCleanup, session)
	jobs.mu.Unlock()
	jobs.current = session
	if err := s.launchMaterialsFeishuSyncJob(session, materialsFeishuSyncPlanJob); err != nil {
		s.finishMaterialsFeishuSyncWithoutJob(session, "同步计划启动失败", err)
	}
	if s.win != nil {
		s.win.Invalidate()
	}
}

func (s *app) materialsFeishuSyncActionMatchesTask(action application.Action, t task) bool {
	return action.TaskID == t.id &&
		normalizedReadingPath(payloadString(action.Payload, "taskPath")) == normalizedReadingPath(t.path) &&
		payloadString(action.Payload, "taskCreatedAt") == t.createdAt &&
		payloadString(action.Payload, "taskGroupId") == t.groupID &&
		payloadString(action.Payload, "taskTitle") == t.title &&
		payloadString(action.Payload, "taskRequirementId") == t.requirementID &&
		normalizedReadingRoot(payloadString(action.Payload, "materialsRoot")) == normalizedReadingRoot(t.materialsPath) &&
		normalizedReadingRoot(payloadString(action.Payload, "writeRoot")) == normalizedReadingRoot(t.materialsPath) &&
		payloadString(action.Payload, "requirementLink") == t.requirementURL
}

func materialsFeishuSyncServices(value any) []application.MaterialsSyncServiceBranch {
	data, err := json.Marshal(value)
	if err != nil || len(data) == 0 || string(data) == "null" {
		return []application.MaterialsSyncServiceBranch{}
	}
	var result []application.MaterialsSyncServiceBranch
	if json.Unmarshal(data, &result) != nil {
		return []application.MaterialsSyncServiceBranch{}
	}
	return result
}

func ensureMaterialsFeishuSyncReleaseSet(value map[*materialsFeishuSyncSession]bool) map[*materialsFeishuSyncSession]bool {
	if value == nil {
		return make(map[*materialsFeishuSyncSession]bool)
	}
	return value
}

func (s *app) launchMaterialsFeishuSyncJob(session *materialsFeishuSyncSession, kind materialsFeishuSyncJobKind) error {
	if session == nil || s.backend == nil || s.win == nil || s.win.IsDestroyed() {
		return errors.New("同步执行窗口或后端不可用")
	}
	jobs := s.ensureMaterialsFeishuSyncJobs()
	ctx, cancel := context.WithCancel(context.Background())
	action := materialsFeishuSyncAction(session, kind)
	job := &materialsFeishuSyncJob{session: session, kind: kind, action: action, backend: s.backend, window: s.win, ctx: ctx, cancel: cancel, done: make(chan struct{})}
	jobs.mu.Lock()
	if jobs.closed || jobs.active != session || jobs.operation != nil {
		jobs.mu.Unlock()
		cancel()
		return errors.New("另一项飞书资料同步操作仍在清理")
	}
	jobs.operation = job
	jobs.running[job] = struct{}{}
	jobs.mu.Unlock()
	go s.runMaterialsFeishuSyncJob(job)
	return nil
}

func materialsFeishuSyncAction(session *materialsFeishuSyncSession, kind materialsFeishuSyncJobKind) application.Action {
	payload := map[string]any{
		"targetUrl": session.Request.TargetURL, "writeRoot": session.Request.WriteRoot,
		"requirementId": session.Request.RequirementID, "requirementTitle": session.Request.RequirementTitle,
		"subdirectory": session.Request.Subdirectory, "requirementLink": session.RequirementURL,
		"taskPath": session.TaskPath, "taskCreatedAt": session.TaskCreatedAt, "taskGroupId": session.TaskGroupID,
		"taskTitle": session.TaskTitle, "taskRequirementId": session.TaskRequirementID,
	}
	if len(session.Request.SelectedPaths) > 0 {
		payload["selectedPaths"] = append([]string(nil), session.Request.SelectedPaths...)
	}
	name := "materials.plan"
	switch kind {
	case materialsFeishuSyncConfirmJob:
		name = "materials.confirm"
		deletions, overwrites := []string{}, []string{}
		for path, selected := range session.DeletionPaths {
			if selected {
				deletions = append(deletions, path)
			}
		}
		for path, selected := range session.OverwritePaths {
			if selected {
				overwrites = append(overwrites, path)
			}
		}
		sort.Strings(deletions)
		sort.Strings(overwrites)
		payload["planId"], payload["deletionPaths"], payload["overwritePaths"] = session.Plan.ID, deletions, overwrites
	case materialsFeishuSyncUploadJob:
		name = "materials.sync"
		payload["planId"] = session.Plan.ID
	case materialsFeishuSyncCommentJob:
		name = "materials.comment"
		payload["targetUrl"] = session.Comment.TargetURL
		payload["writeRoot"] = session.Comment.WriteRoot
		payload["requirementId"] = session.Comment.RequirementID
		payload["requirementTitle"] = session.Comment.RequirementTitle
		payload["subdirectory"] = session.Comment.Subdirectory
		payload["requirementLink"] = session.Comment.RequirementLink
		payload["directoryUrl"] = session.DirectoryURL
		services := make([]map[string]any, 0, len(session.Comment.Services))
		for _, service := range session.Comment.Services {
			services = append(services, map[string]any{"serviceName": service.ServiceName, "branch": service.Branch})
		}
		payload["services"] = services
	}
	return application.Action{Kind: name, TaskID: session.TaskID, Payload: payload}
}

func (s *app) runMaterialsFeishuSyncJob(job *materialsFeishuSyncJob) {
	var result application.ActionResult
	var err error
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("飞书资料同步操作异常：%v", recovered)
		}
		s.postMaterialsFeishuSyncResult(job, result, err)
	}()
	if job.ctx.Err() != nil {
		err = job.ctx.Err()
		return
	}
	if job.kind == materialsFeishuSyncUploadJob {
		if progressBackend, ok := job.backend.(application.MaterialsSyncProgressBackend); ok {
			result, err = progressBackend.DispatchMaterialsSync(job.ctx, job.action, func(progress domain.MaterialsSyncProgress) {
				s.postMaterialsFeishuSyncProgress(job, progress)
			})
		} else {
			result, err = job.backend.Dispatch(job.ctx, job.action)
		}
		return
	}
	result, err = job.backend.Dispatch(job.ctx, job.action)
}

func (s *app) postMaterialsFeishuSyncProgress(job *materialsFeishuSyncJob, progress domain.MaterialsSyncProgress) {
	if job == nil || job.ctx.Err() != nil || job.window == nil || job.window.IsDestroyed() {
		return
	}
	copy := cloneMaterialsFeishuSyncProgress(progress)
	job.window.Update(func() {
		if !s.materialsFeishuSyncJobRelevant(job) || job.session.Stage != materialsFeishuSyncUploading {
			return
		}
		if copy.Sequence < job.session.Progress.Sequence && copy.Phase != domain.MaterialsSyncComplete {
			return
		}
		job.session.Progress = copy
		job.window.Invalidate()
	})
}

func cloneMaterialsFeishuSyncProgress(progress domain.MaterialsSyncProgress) domain.MaterialsSyncProgress {
	progress.ActivePaths = append([]string{}, progress.ActivePaths...)
	progress.WaitingRetries = append([]domain.MaterialsSyncCommandRetry{}, progress.WaitingRetries...)
	return progress
}

func (s *app) postMaterialsFeishuSyncResult(job *materialsFeishuSyncJob, result application.ActionResult, err error) {
	if job == nil {
		return
	}
	if job.window == nil || job.window.IsDestroyed() {
		s.completeMaterialsFeishuSyncJob(job)
		return
	}
	updateDone := make(chan struct{})
	var next materialsFeishuSyncJobKind
	job.window.Update(func() {
		defer close(updateDone)
		s.finishMaterialsFeishuSyncJob(job, result, err)
		next = job.session.NextStep
		job.session.NextStep = ""
	})
	if !s.waitMaterialsFeishuWindowUpdate(job.window, updateDone) {
		// The backend has returned, so external cleanup is complete. If the
		// window is closing, do not wait for a UI callback the event loop may
		// never deliver; removing the job also makes a late callback stale.
		s.completeMaterialsFeishuSyncJob(job)
		return
	}
	s.completeMaterialsFeishuSyncJob(job)
	if next != "" && job.window != nil && !job.window.IsDestroyed() {
		job.window.Update(func() {
			if !s.materialsFeishuSyncSessionRelevant(job.session) {
				return
			}
			if err := s.launchMaterialsFeishuSyncJob(job.session, next); err != nil {
				s.finishMaterialsFeishuSyncWithoutJob(job.session, "同步操作启动失败", err)
			}
		})
	}
}

func (s *app) waitMaterialsFeishuWindowUpdate(window *mygo.Window, updateDone <-chan struct{}) bool {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-updateDone:
			return true
		case <-ticker.C:
			if window == nil || window.IsDestroyed() {
				return false
			}
			if jobs := s.materialsFeishuSyncJobs; jobs != nil {
				jobs.mu.Lock()
				closed := jobs.closed
				jobs.mu.Unlock()
				if closed {
					return false
				}
			}
		}
	}
}

func (s *app) materialsFeishuSyncJobRelevant(job *materialsFeishuSyncJob) bool {
	if job == nil || job.window == nil || job.window.IsDestroyed() || !s.materialsFeishuSyncSessionRelevant(job.session) {
		return false
	}
	jobs := s.materialsFeishuSyncJobs
	if jobs == nil {
		return false
	}
	jobs.mu.Lock()
	defer jobs.mu.Unlock()
	return !jobs.closed && jobs.active == job.session && jobs.operation == job
}

func (s *app) materialsFeishuSyncSessionRelevant(session *materialsFeishuSyncSession) bool {
	jobs := s.materialsFeishuSyncJobs
	if session == nil || jobs == nil || jobs.current != session || s.snapshot.Home != session.Home {
		return false
	}
	if s.loading {
		return true
	}
	t := s.taskByID(session.TaskID)
	return t.id == session.TaskID && normalizedReadingPath(t.path) == normalizedReadingPath(session.TaskPath) &&
		t.createdAt == session.TaskCreatedAt && t.groupID == session.TaskGroupID &&
		t.title == session.TaskTitle && t.requirementID == session.TaskRequirementID &&
		normalizedReadingRoot(t.materialsPath) == normalizedReadingRoot(session.MaterialsRoot) &&
		t.requirementURL == session.RequirementURL
}

func (s *app) finishMaterialsFeishuSyncJob(job *materialsFeishuSyncJob, result application.ActionResult, err error) {
	if job == nil || !s.materialsFeishuSyncJobRelevant(job) {
		return
	}
	session := job.session
	if job.ctx.Err() != nil {
		err = errors.Join(job.ctx.Err(), err)
	}
	cleanupFailed := process.IsCleanupFailure(err)
	cancelled := !cleanupFailed && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded))
	if err != nil {
		if job.kind == materialsFeishuSyncUploadJob {
			var execution application.MaterialsSyncExecution
			if decodeMaterialsFeishuSyncData(result.Data, &execution) == nil && execution.Result.Progress.Phase != "" {
				session.Upload = &execution.Result
				session.DirectoryURL = execution.RequirementDirectoryURL
			} else {
				var upload domain.MaterialsSyncResult
				if decodeMaterialsFeishuSyncData(result.Data, &upload) == nil {
					session.Upload = &upload
				}
			}
			if session.Upload == nil {
				session.Upload = &domain.MaterialsSyncResult{Progress: cloneMaterialsFeishuSyncProgress(session.Progress)}
			}
			if cleanupFailed {
				session.Upload.Cancelled = false
			}
			if cancelled {
				session.Upload.Cancelled = true
			} else if session.Upload.Progress.Failed == 0 {
				session.Upload.Progress.Failed = 1
			}
			session.CommentResult = &domain.MaterialsCommentResult{Status: domain.MaterialsCommentSkipped, Message: "资料同步未完整成功，因此没有发布需求评论。"}
			if session.Upload.Cancelled {
				session.Error = ""
				s.finishMaterialsFeishuSyncSession(session, "同步已取消；没有发布需求评论。")
			} else {
				session.Error = err.Error()
				s.finishMaterialsFeishuSyncSession(session, "资料同步部分完成；未发布需求评论。")
			}
			return
		}
		if job.kind == materialsFeishuSyncCommentJob {
			status := domain.MaterialsCommentFailed
			if cancelled {
				status = domain.MaterialsCommentCancelled
			}
			session.CommentResult = &domain.MaterialsCommentResult{Status: status, Message: err.Error()}
			session.Error = ""
			if cleanupFailed {
				session.Error = err.Error()
			}
			s.finishMaterialsFeishuSyncSession(session, "资料已上传，但需求评论未完成。")
			return
		}
		if cancelled {
			if session.Upload != nil {
				session.Upload.Cancelled = true
			} else {
				session.CommentResult = &domain.MaterialsCommentResult{Status: domain.MaterialsCommentCancelled, Message: "同步已取消。"}
			}
			session.Error = ""
			s.finishMaterialsFeishuSyncSession(session, "同步已取消。")
			return
		}
		session.Error = err.Error()
		session.NextStep = ""
		s.finishMaterialsFeishuSyncSession(session, materialsFeishuSyncFailureSummary(job.kind))
		return
	}
	session.Error = ""
	switch job.kind {
	case materialsFeishuSyncPlanJob:
		var plan domain.MaterialsSyncPlan
		if decodeMaterialsFeishuSyncData(result.Data, &plan) != nil || plan.ID == "" {
			session.Error = "飞书同步计划返回格式无效。"
			s.finishMaterialsFeishuSyncSession(session, "同步计划失败。")
			return
		}
		session.Plan = &plan
		session.DeletionPaths = make(map[string]bool)
		session.OverwritePaths = make(map[string]bool)
		if materialsFeishuSyncPlanNeedsApproval(plan) {
			session.Stage = materialsFeishuSyncWaitingApproval
			session.Summary = "计划已生成，请确认要执行的删除和覆盖项。"
			return
		}
		session.Stage = materialsFeishuSyncUploading
		session.NextStep = materialsFeishuSyncConfirmJob
	case materialsFeishuSyncConfirmJob:
		var confirmed struct {
			Ready bool `json:"ready"`
		}
		if decodeMaterialsFeishuSyncData(result.Data, &confirmed) != nil || !confirmed.Ready {
			session.Error = "飞书同步确认记录未能保存。"
			s.finishMaterialsFeishuSyncSession(session, "同步确认失败。")
			return
		}
		session.Stage = materialsFeishuSyncUploading
		session.NextStep = materialsFeishuSyncUploadJob
	case materialsFeishuSyncUploadJob:
		var execution application.MaterialsSyncExecution
		if decodeMaterialsFeishuSyncData(result.Data, &execution) == nil && execution.Result.Progress.Phase != "" {
			session.Upload = &execution.Result
			session.DirectoryURL = execution.RequirementDirectoryURL
		} else {
			var upload domain.MaterialsSyncResult
			if decodeMaterialsFeishuSyncData(result.Data, &upload) == nil {
				session.Upload = &upload
			}
		}
		if session.Upload == nil {
			session.Upload = &domain.MaterialsSyncResult{Progress: session.Progress}
		}
		if session.Upload.Progress.Sequence < session.Progress.Sequence &&
			session.Upload.Progress.Phase != domain.MaterialsSyncComplete {
			session.Upload.Progress = cloneMaterialsFeishuSyncProgress(session.Progress)
		}
		if err != nil || session.Upload.Partial() {
			session.CommentResult = &domain.MaterialsCommentResult{Status: domain.MaterialsCommentSkipped, Message: "资料同步未完整成功，因此没有发布需求评论。"}
			session.Error = ""
			s.finishMaterialsFeishuSyncSession(session, "资料同步部分完成；未发布需求评论。")
			return
		}
		if len(session.Request.SelectedPaths) > 0 {
			session.CommentResult = &domain.MaterialsCommentResult{Status: domain.MaterialsCommentSkipped, Message: "文件级同步不会发布需求评论。"}
			s.finishMaterialsFeishuSyncSession(session, "所选资料及父级目录已同步。")
			return
		}
		if !session.CommentAvailable {
			session.CommentResult = &domain.MaterialsCommentResult{Status: domain.MaterialsCommentSkipped,
				Message: "任务没有可验证的飞书需求工作项，资料已上传但未发布评论。"}
			s.finishMaterialsFeishuSyncSession(session, "资料已上传；任务未关联可评论的需求工作项。")
			return
		}
		session.Stage = materialsFeishuSyncPublishing
		session.NextStep = materialsFeishuSyncCommentJob
	case materialsFeishuSyncCommentJob:
		var comment domain.MaterialsCommentResult
		if decodeMaterialsFeishuSyncData(result.Data, &comment) != nil || comment.Status == "" {
			comment = domain.MaterialsCommentResult{Status: domain.MaterialsCommentFailed, Message: "需求评论返回格式无效。"}
		}
		if comment.RequirementDirectoryURL != nil && strings.TrimSpace(*comment.RequirementDirectoryURL) != "" {
			session.DirectoryURL = *comment.RequirementDirectoryURL
		}
		if err != nil {
			comment.Status = domain.MaterialsCommentFailed
			if cancelled {
				comment.Status = domain.MaterialsCommentCancelled
			}
			comment.Message = err.Error()
		}
		session.CommentResult = &comment
		if comment.Status == domain.MaterialsCommentFailed || comment.Status == domain.MaterialsCommentCancelled {
			s.finishMaterialsFeishuSyncSession(session, "资料已上传，但需求评论未完成。")
		} else {
			if comment.Status == domain.MaterialsCommentCreated || comment.Status == domain.MaterialsCommentUpdated || comment.Status == domain.MaterialsCommentUnchanged {
				s.startMaterialsFeishuCommentRefresh(session)
			}
			s.finishMaterialsFeishuSyncSession(session, "资料已上传，需求评论已处理。")
		}
	}
}

func decodeMaterialsFeishuSyncData(value any, target any) error {
	if value == nil {
		return errors.New("missing result")
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func materialsFeishuSyncPlanNeedsApproval(plan domain.MaterialsSyncPlan) bool {
	for _, item := range plan.Items {
		if item.Action == domain.MaterialsSyncDelete || item.Action == domain.MaterialsSyncConflict {
			return true
		}
	}
	return false
}

func materialsFeishuSyncFailureSummary(kind materialsFeishuSyncJobKind) string {
	switch kind {
	case materialsFeishuSyncPlanJob:
		return "同步计划失败。"
	case materialsFeishuSyncConfirmJob:
		return "同步确认失败。"
	case materialsFeishuSyncUploadJob:
		return "资料上传失败。"
	default:
		return "需求评论发布失败。"
	}
}

func (s *app) finishMaterialsFeishuSyncSession(session *materialsFeishuSyncSession, summary string) {
	if session == nil {
		return
	}
	session.Stage = materialsFeishuSyncFinished
	session.Summary = summary
	session.NextStep = ""
	if !session.Visible && s.materialsFeishuSyncJobs != nil {
		s.materialsFeishuSyncJobs.unread[session.TaskKey] = session
	}
	jobs := s.ensureMaterialsFeishuSyncJobs()
	jobs.mu.Lock()
	jobs.releaseOnCleanup = ensureMaterialsFeishuSyncReleaseSet(jobs.releaseOnCleanup)
	jobs.releaseOnCleanup[session] = true
	jobs.mu.Unlock()
	if s.win != nil {
		s.win.Invalidate()
	}
}

func (s *app) finishMaterialsFeishuSyncWithoutJob(session *materialsFeishuSyncSession, summary string, err error) {
	if session == nil {
		return
	}
	if err != nil {
		session.Error = err.Error()
	}
	session.Stage = materialsFeishuSyncFinished
	session.Summary = summary
	session.NextStep = ""
	jobs := s.ensureMaterialsFeishuSyncJobs()
	jobs.mu.Lock()
	if jobs.active == session {
		jobs.active = nil
	}
	delete(jobs.releaseOnCleanup, session)
	closeMaterialsFeishuSyncSession(session)
	jobs.mu.Unlock()
	if !session.Visible {
		jobs.unread[session.TaskKey] = session
	}
	if s.win != nil {
		s.win.Invalidate()
	}
}

func closeMaterialsFeishuSyncSession(session *materialsFeishuSyncSession) {
	if session == nil || session.Done == nil {
		return
	}
	select {
	case <-session.Done:
	default:
		close(session.Done)
	}
}

func (s *app) completeMaterialsFeishuSyncJob(job *materialsFeishuSyncJob) {
	if job == nil {
		return
	}
	jobs := s.materialsFeishuSyncJobs
	if jobs == nil {
		job.cancel()
		return
	}
	jobs.mu.Lock()
	if _, exists := jobs.running[job]; exists {
		delete(jobs.running, job)
		if jobs.operation == job {
			jobs.operation = nil
		}
		close(job.done)
	}
	if jobs.closed || jobs.releaseOnCleanup[job.session] {
		otherJob := false
		for other := range jobs.running {
			if other.session == job.session {
				otherJob = true
				break
			}
		}
		if !otherJob && jobs.active == job.session {
			jobs.active = nil
			delete(jobs.releaseOnCleanup, job.session)
			closeMaterialsFeishuSyncSession(job.session)
		}
	}
	jobs.mu.Unlock()
	job.cancel()
}

func (s *app) approveMaterialsFeishuSync(runID string) {
	session := s.sessionByRunID(runID)
	if session == nil || session.Stage != materialsFeishuSyncWaitingApproval || session.Plan == nil || !session.Visible {
		return
	}
	session.Stage = materialsFeishuSyncUploading
	session.Summary = "正在保存确认并准备上传…"
	if err := s.launchMaterialsFeishuSyncJob(session, materialsFeishuSyncConfirmJob); err != nil {
		s.finishMaterialsFeishuSyncWithoutJob(session, "同步确认失败。", err)
	}
}

func (s *app) stopMaterialsFeishuSync(runID string) {
	session := s.sessionByRunID(runID)
	if session == nil || session.Stage == materialsFeishuSyncFinished || session.Stage == materialsFeishuSyncStopping {
		return
	}
	session.Stage = materialsFeishuSyncStopping
	session.NextStep = ""
	jobs := s.materialsFeishuSyncJobs
	if jobs == nil {
		s.finishMaterialsFeishuSyncWithoutJob(session, "同步已取消。", nil)
		return
	}
	jobs.mu.Lock()
	job := jobs.operation
	if job == nil || job.session != session {
		jobs.mu.Unlock()
		session.CommentResult = &domain.MaterialsCommentResult{Status: domain.MaterialsCommentCancelled, Message: "同步已取消。"}
		s.finishMaterialsFeishuSyncWithoutJob(session, "同步已取消。", nil)
		return
	}
	job.cancel()
	jobs.mu.Unlock()
	if s.win != nil {
		s.win.Invalidate()
	}
}

func (s *app) retryMaterialsFeishuSyncComment(runID string) {
	session := s.sessionByRunID(runID)
	if session == nil || session.Stage != materialsFeishuSyncFinished || session.Upload == nil || session.Upload.Partial() || session.CommentResult == nil ||
		(session.CommentResult.Status != domain.MaterialsCommentFailed && session.CommentResult.Status != domain.MaterialsCommentCancelled) {
		return
	}
	jobs := s.ensureMaterialsFeishuSyncJobs()
	jobs.mu.Lock()
	if jobs.closed || jobs.active != nil {
		jobs.mu.Unlock()
		return
	}
	jobs.active = session
	delete(jobs.releaseOnCleanup, session)
	session.Done = make(chan struct{})
	jobs.mu.Unlock()
	session.Stage = materialsFeishuSyncPublishing
	session.Error = ""
	session.Summary = "仅重试发布需求评论；不会重新上传资料。"
	session.Visible = true
	if err := s.launchMaterialsFeishuSyncJob(session, materialsFeishuSyncCommentJob); err != nil {
		s.finishMaterialsFeishuSyncWithoutJob(session, "需求评论重试失败。", err)
	}
}

func (s *app) sessionByRunID(runID string) *materialsFeishuSyncSession {
	if runID == "" || s.materialsFeishuSyncJobs == nil {
		return nil
	}
	if current := s.materialsFeishuSyncJobs.current; current != nil && current.RunID == runID {
		return current
	}
	for _, session := range s.materialsFeishuSyncJobs.unread {
		if session != nil && session.RunID == runID {
			return session
		}
	}
	return nil
}

func (s *app) observeMaterialsFeishuSyncIdentity() {
	jobs := s.materialsFeishuSyncJobs
	if jobs == nil {
		return
	}
	s.observeMaterialsFeishuDirectoryJob(jobs)
	s.observeMaterialsFeishuCommentRefreshJobs(jobs)
	if jobs.current == nil || jobs.current.Stage == materialsFeishuSyncFinished {
		return
	}
	session := jobs.current
	if s.materialsFeishuSyncSessionRelevant(session) {
		return
	}
	jobs.mu.Lock()
	job := jobs.operation
	if job != nil && job.session == session {
		job.cancel()
	}
	jobs.releaseOnCleanup = ensureMaterialsFeishuSyncReleaseSet(jobs.releaseOnCleanup)
	jobs.releaseOnCleanup[session] = true
	if job == nil && jobs.active == session {
		jobs.active = nil
		delete(jobs.releaseOnCleanup, session)
		closeMaterialsFeishuSyncSession(session)
	}
	jobs.mu.Unlock()
	jobs.current = nil
	delete(jobs.unread, session.TaskKey)
}

func (s *app) closeMaterialsFeishuSyncJobs(ctx context.Context) error {
	jobs := s.materialsFeishuSyncJobs
	if jobs == nil {
		return nil
	}
	jobs.mu.Lock()
	jobs.closed = true
	for job := range jobs.running {
		job.cancel()
	}
	for job := range jobs.directoryRunning {
		job.cancel()
	}
	for job := range jobs.commentRefreshRunning {
		job.cancel()
	}
	active := jobs.active
	operation := jobs.operation
	if active != nil && operation == nil {
		jobs.active = nil
		closeMaterialsFeishuSyncSession(active)
	}
	running := make([]*materialsFeishuSyncJob, 0, len(jobs.running))
	for job := range jobs.running {
		running = append(running, job)
	}
	directory := make([]*materialsFeishuDirectoryJob, 0, len(jobs.directoryRunning))
	for job := range jobs.directoryRunning {
		directory = append(directory, job)
	}
	commentRefresh := make([]*materialsFeishuCommentRefreshJob, 0, len(jobs.commentRefreshRunning))
	for job := range jobs.commentRefreshRunning {
		commentRefresh = append(commentRefresh, job)
	}
	jobs.mu.Unlock()
	for _, job := range running {
		select {
		case <-job.done:
		case <-ctx.Done():
			return fmt.Errorf("等待飞书资料同步清理：%w", ctx.Err())
		}
	}
	for _, job := range directory {
		select {
		case <-job.done:
		case <-ctx.Done():
			return fmt.Errorf("等待飞书目录打开清理：%w", ctx.Err())
		}
	}
	for _, job := range commentRefresh {
		select {
		case <-job.done:
		case <-ctx.Done():
			return fmt.Errorf("等待飞书评论缓存刷新：%w", ctx.Err())
		}
	}
	jobs.mu.Lock()
	if jobs.active != nil {
		closeMaterialsFeishuSyncSession(jobs.active)
		jobs.active = nil
	}
	jobs.releaseOnCleanup = nil
	jobs.directory = nil
	jobs.mu.Unlock()
	return nil
}
