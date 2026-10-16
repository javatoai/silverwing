package desktop

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"silverwing-go/internal/application"
)

func (s *app) openMaterialsFeishuDirectory(t task, target string) {
	s.openMaterialsFeishuEntry(t, target, "")
}

func (s *app) openMaterialsFeishuEntry(t task, target, relativePath string) {
	if s.backend == nil || s.win == nil || s.win.IsDestroyed() {
		s.lastError = "飞书资料目录服务或窗口不可用。"
		return
	}
	currentTarget, err := s.materialsFeishuSyncTarget()
	if err != nil || currentTarget != target {
		s.lastError = "飞书同步目标已变化，请刷新后重试。"
		return
	}
	if relativePath != "" {
		paths, pathErr := materialsFeishuSelectedPaths(map[string]any{"selectedPaths": []string{relativePath}})
		if pathErr != nil || s.validateMaterialsFeishuSelectedPaths(t, paths) != nil {
			s.lastError = "所选文件已变化，请刷新资料列表后重试。"
			return
		}
	}
	id, title, err := materialsFeishuRequirementIdentity(t)
	if err != nil {
		s.lastError = "当前任务缺少有效的需求编号或目录名称。"
		return
	}
	jobs := s.ensureMaterialsFeishuSyncJobs()
	jobs.mu.Lock()
	if jobs.closed {
		jobs.mu.Unlock()
		s.lastError = "飞书资料服务正在关闭。"
		return
	}
	if current := jobs.directory; current != nil && current.taskKey == materialsFeishuSyncTaskKey(t) && current.targetURL == target && current.relativePath == relativePath {
		jobs.mu.Unlock()
		return
	}
	if current := jobs.directory; current != nil {
		current.cancel()
		jobs.directory = nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	job := &materialsFeishuDirectoryJob{
		taskKey: materialsFeishuSyncTaskKey(t), taskID: t.id, taskPath: t.path,
		taskCreatedAt: t.createdAt, taskGroupID: t.groupID, taskRequirementID: t.requirementID,
		materialsRoot:   t.materialsPath,
		requirementLink: t.requirementURL, home: s.snapshot.Home, targetURL: target,
		requirementID: id, requirementTitle: title, relativePath: relativePath,
		backend: s.backend, window: s.win, ctx: ctx, cancel: cancel, done: make(chan struct{}),
		action: materialsFeishuResolveEntryAction(t, target, id, title, relativePath),
	}
	if jobs.directoryRunning == nil {
		jobs.directoryRunning = make(map[*materialsFeishuDirectoryJob]struct{})
	}
	jobs.directory = job
	jobs.directoryRunning[job] = struct{}{}
	jobs.mu.Unlock()
	s.lastError, s.lastMessage = "", ""
	if s.win != nil {
		s.win.Invalidate()
	}
	go s.runMaterialsFeishuDirectoryJob(job)
}

func (s *app) runMaterialsFeishuDirectoryJob(job *materialsFeishuDirectoryJob) {
	defer s.completeMaterialsFeishuDirectoryJob(job)
	var found bool
	var directoryURL string
	result, err := job.backend.Dispatch(job.ctx, job.action)
	if err == nil {
		var resolved struct {
			Found bool   `json:"found"`
			URL   string `json:"url"`
		}
		err = decodeMaterialsFeishuSyncData(result.Data, &resolved)
		found, directoryURL = resolved.Found, strings.TrimSpace(resolved.URL)
		if err == nil && found {
			err = validateMaterialsFeishuDirectoryURL(directoryURL)
		}
		if err == nil && found {
			canOpen := make(chan bool, 1)
			validationDone := make(chan struct{})
			job.window.Update(func() {
				defer close(validationDone)
				canOpen <- job.ctx.Err() == nil && s.materialsFeishuDirectoryJobRelevant(job)
			})
			if !waitMaterialsFeishuDirectoryUpdate(job, validationDone) {
				return
			}
			select {
			case valid := <-canOpen:
				if !valid {
					return
				}
			case <-job.ctx.Done():
				return
			}
			if job.ctx.Err() != nil || job.window == nil || job.window.IsDestroyed() {
				return
			}
			_, err = job.backend.Dispatch(job.ctx, application.Action{Kind: "system.open", Path: directoryURL})
		}
	}
	if job.ctx.Err() != nil {
		err = errors.Join(job.ctx.Err(), err)
	}
	if job.window == nil || job.window.IsDestroyed() {
		return
	}
	updateDone := make(chan struct{})
	job.window.Update(func() {
		defer close(updateDone)
		if !s.materialsFeishuDirectoryJobRelevant(job) {
			return
		}
		switch {
		case err != nil:
			s.lastError = "打开飞书资料失败：" + err.Error()
		case !found:
			if job.relativePath != "" {
				s.lastMessage = "该文件尚未同步到飞书，或远端资源不是在线文档"
			} else {
				s.lastMessage = "请先完成一次飞书同步"
			}
		default:
			if job.relativePath != "" {
				s.lastMessage = "已打开飞书在线文档"
			} else {
				s.lastMessage = "已打开飞书资料目录"
			}
		}
		job.window.Invalidate()
	})
	waitMaterialsFeishuDirectoryUpdate(job, updateDone)
}

func waitMaterialsFeishuDirectoryUpdate(job *materialsFeishuDirectoryJob, updateDone <-chan struct{}) bool {
	if job == nil || job.ctx == nil || job.window == nil {
		return false
	}
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-updateDone:
			return true
		case <-job.ctx.Done():
			return false
		case <-ticker.C:
			if job.window.IsDestroyed() {
				return false
			}
		}
	}
}

func materialsFeishuResolveEntryAction(t task, target, requirementID, requirementTitle, relativePath string) application.Action {
	kind := "materials.resolve-directory"
	payload := map[string]any{"targetUrl": target, "requirementId": requirementID, "requirementTitle": requirementTitle}
	if relativePath != "" {
		kind = "materials.resolve-entry"
		payload["relativePath"] = relativePath
	}
	return application.Action{Kind: kind, TaskID: t.id, Payload: payload}
}

func validateMaterialsFeishuDirectoryURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return fmt.Errorf("解析结果不是有效的 HTTP(S) 目录链接")
	}
	return nil
}

func (s *app) materialsFeishuDirectoryJobRelevant(job *materialsFeishuDirectoryJob) bool {
	if job == nil || job.window == nil || job.window.IsDestroyed() || s.snapshot.Home != job.home {
		return false
	}
	current := s.currentTask()
	if materialsFeishuSyncTaskKey(current) != job.taskKey || current.id != job.taskID || current.path != job.taskPath ||
		current.createdAt != job.taskCreatedAt || current.groupID != job.taskGroupID ||
		current.requirementID != job.taskRequirementID ||
		current.materialsPath != job.materialsRoot || current.requirementURL != job.requirementLink {
		return false
	}
	id, title, err := materialsFeishuRequirementIdentity(current)
	if err != nil || id != job.requirementID || title != job.requirementTitle {
		return false
	}
	if job.relativePath != "" {
		absolute := filepath.Clean(filepath.Join(current.materialsPath, filepath.FromSlash(job.relativePath)))
		entry, exists := s.findMaterialsEntry(absolute, s.documents)
		if !exists || entry.Directory || entry.Relative != job.relativePath {
			return false
		}
	}
	target, err := s.materialsFeishuSyncTarget()
	if err != nil || target != job.targetURL {
		return false
	}
	jobs := s.materialsFeishuSyncJobs
	if jobs == nil {
		return false
	}
	jobs.mu.Lock()
	defer jobs.mu.Unlock()
	return !jobs.closed && jobs.directory == job
}

func (s *app) observeMaterialsFeishuDirectoryJob(jobs *materialsFeishuSyncJobsState) {
	if jobs == nil {
		return
	}
	jobs.mu.Lock()
	job := jobs.directory
	jobs.mu.Unlock()
	if job == nil || s.materialsFeishuDirectoryJobRelevant(job) {
		return
	}
	jobs.mu.Lock()
	if jobs.directory == job {
		job.cancel()
		jobs.directory = nil
	}
	jobs.mu.Unlock()
}

func (s *app) completeMaterialsFeishuDirectoryJob(job *materialsFeishuDirectoryJob) {
	if job == nil {
		return
	}
	jobs := s.materialsFeishuSyncJobs
	if jobs != nil {
		jobs.mu.Lock()
		delete(jobs.directoryRunning, job)
		if jobs.directory == job {
			jobs.directory = nil
		}
		close(job.done)
		jobs.mu.Unlock()
	}
	job.cancel()
}

func (s *app) startMaterialsFeishuCommentRefresh(session *materialsFeishuSyncSession) {
	if session == nil || strings.TrimSpace(session.RequirementURL) == "" || s.backend == nil || s.win == nil || s.win.IsDestroyed() {
		return
	}
	reader := s.requirementReader(session.TaskID, session.RequirementURL)
	if state := s.requirementContentJobs; state != nil {
		key := requirementReaderKey(reader.taskID, reader.url) + "\x00requirements.comments"
		if previous := state.requests[key]; previous != nil {
			s.cancelRequirementContentJob(previous)
		}
	}
	reader.commentsLoading, reader.commentsError = true, ""
	action := application.Action{
		Kind: "requirements.comments", TaskID: session.TaskID, Path: session.RequirementURL,
		Payload: s.requirementReaderPayload(reader, "requirements.comments", true),
	}
	jobs := s.ensureMaterialsFeishuSyncJobs()
	ctx, cancel := context.WithCancel(context.Background())
	job := &materialsFeishuCommentRefreshJob{
		action: action, reader: reader, home: s.snapshot.Home,
		executable: s.configString("meegleExecutablePath"), configJSON: s.configJSON,
		backend: s.backend, window: s.win,
		ctx: ctx, cancel: cancel, done: make(chan struct{}),
	}
	if task := s.taskByID(session.TaskID); task.id != "" {
		job.taskPath, job.taskCreatedAt = task.path, task.createdAt
	}
	jobs.mu.Lock()
	if jobs.closed {
		jobs.mu.Unlock()
		cancel()
		reader.commentsLoading = false
		return
	}
	if jobs.commentRefreshRunning == nil {
		jobs.commentRefreshRunning = make(map[*materialsFeishuCommentRefreshJob]struct{})
	}
	jobs.commentRefreshRunning[job] = struct{}{}
	jobs.mu.Unlock()
	go s.runMaterialsFeishuCommentRefreshJob(job)
}

func (s *app) runMaterialsFeishuCommentRefreshJob(job *materialsFeishuCommentRefreshJob) {
	defer s.completeMaterialsFeishuCommentRefreshJob(job)
	result, err := job.backend.Dispatch(job.ctx, job.action)
	if job.ctx.Err() != nil {
		err = errors.Join(job.ctx.Err(), err)
	}
	if job.window == nil || job.window.IsDestroyed() {
		return
	}
	updateDone := make(chan struct{})
	job.window.Update(func() {
		defer close(updateDone)
		if !s.materialsFeishuCommentRefreshRelevant(job) {
			s.clearMaterialsFeishuCommentRefreshLoading(job)
			return
		}
		if err != nil {
			s.applyRequirementContentFailure(job.action, err)
		} else {
			s.applyRequirementContentResult(job.action, result)
		}
		job.window.Invalidate()
	})
	s.waitMaterialsFeishuWindowUpdate(job.window, updateDone)
}

func (s *app) materialsFeishuCommentRefreshRelevant(job *materialsFeishuCommentRefreshJob) bool {
	if job == nil || job.window == nil || job.window.IsDestroyed() || s.snapshot.Home != job.home ||
		s.configString("meegleExecutablePath") != job.executable || s.configJSON != job.configJSON ||
		s.requirementReaderForAction(job.action) != job.reader {
		return false
	}
	jobs := s.materialsFeishuSyncJobs
	if jobs == nil {
		return false
	}
	jobs.mu.Lock()
	_, tracked := jobs.commentRefreshRunning[job]
	closed := jobs.closed
	jobs.mu.Unlock()
	if !tracked || closed {
		return false
	}
	if job.action.TaskID != "" {
		task := s.taskByID(job.action.TaskID)
		if task.path != job.taskPath || task.createdAt != job.taskCreatedAt || task.requirementURL != job.reader.url {
			return false
		}
	}
	return true
}

func (s *app) clearMaterialsFeishuCommentRefreshLoading(job *materialsFeishuCommentRefreshJob) {
	if job == nil || job.reader == nil || s.requirementReaderForAction(job.action) != job.reader {
		return
	}
	delete(job.reader.requestIDs, job.action.Kind)
	job.reader.commentsLoading = false
}

// This cache refresh belongs to the task that published the comment, not the
// currently selected page or task. Retire it only when its reader/config
// identity changes or the application begins closing.
func (s *app) observeMaterialsFeishuCommentRefreshJobs(jobs *materialsFeishuSyncJobsState) {
	if jobs == nil {
		return
	}
	jobs.mu.Lock()
	active := make([]*materialsFeishuCommentRefreshJob, 0, len(jobs.commentRefreshRunning))
	for job := range jobs.commentRefreshRunning {
		active = append(active, job)
	}
	jobs.mu.Unlock()
	for _, job := range active {
		if s.materialsFeishuCommentRefreshRelevant(job) {
			continue
		}
		job.cancel()
		s.clearMaterialsFeishuCommentRefreshLoading(job)
	}
}

func (s *app) completeMaterialsFeishuCommentRefreshJob(job *materialsFeishuCommentRefreshJob) {
	if job == nil {
		return
	}
	jobs := s.materialsFeishuSyncJobs
	if jobs != nil {
		jobs.mu.Lock()
		delete(jobs.commentRefreshRunning, job)
		close(job.done)
		jobs.mu.Unlock()
	}
	job.cancel()
}
