package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

// These jobs belong to a requirement/account, not a page, selected task or
// reader. All view mutations are dispatched onto the window thread.
type requirementRefreshWindow interface {
	Update(func())
	Invalidate()
	IsDestroyed() bool
}

type requirementRefreshJob struct {
	key, url, sprint string
	configScope      string
	identity         requirementListIdentity
	identityContext  requirementIdentityContext
	payload          map[string]any
	ctx              context.Context
	cancel           context.CancelFunc
	done             chan struct{}
}

func requirementRefreshConfiguration(configJSON string) string {
	var config domain.AppConfig
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		return configJSON
	}
	encoded, _ := json.Marshal([]any{config.MeegleExecutablePath, config.MeegleProjects, config.MeegleDefaultSprintProjectKey,
		config.CommandProxyURL, config.CommandProxyNoProxy, config.CommandProxyUsername, config.CommandProxyPassword, config.CommandProxyTargets})
	return string(encoded)
}

type requirementRefreshState struct {
	current     map[string]*requirementRefreshJob
	metadata    map[string]application.RequirementMetadata
	window      requirementRefreshWindow
	mu          sync.Mutex
	running     map[*requirementRefreshJob]struct{}
	closed      bool
	identity    requirementListIdentity
	hasIdentity bool
}

type requirementRefreshResult struct {
	metadata      *application.RequirementMetadata
	body          *application.RequirementContent
	comments      *application.RequirementComments
	failures      []string
	identityError *application.RequirementIdentityChangedError
}

func requirementRefreshKey(url string) string {
	if link := domain.ParseFeishuWorkItemLink(url); link != nil {
		return link.Space + ":" + link.Kind + ":" + link.WorkItemID
	}
	return strings.TrimSpace(url)
}

func (s *app) ensureRequirementRefreshJobs() *requirementRefreshState {
	if s.requirementUnit.state.requirementRefreshJobs == nil {
		s.requirementUnit.state.requirementRefreshJobs = &requirementRefreshState{current: make(map[string]*requirementRefreshJob), metadata: make(map[string]application.RequirementMetadata), running: make(map[*requirementRefreshJob]struct{})}
	}
	return s.requirementUnit.state.requirementRefreshJobs
}

func (s *app) requirementRefreshing(url string) bool {
	state := s.requirementUnit.state.requirementRefreshJobs
	if state == nil {
		return false
	}
	job := state.current[requirementRefreshKey(url)]
	return job != nil && job.ctx.Err() == nil && job.identity == s.currentRequirementListIdentity()
}

func (s *app) requirementRefreshLabel(url string) string {
	if s.requirementRefreshing(url) {
		return "刷新中…"
	}
	return "刷新"
}

func (s *app) startRequirementRefresh(url string) {
	if domain.ParseFeishuWorkItemLink(url) == nil || s.backend == nil || s.requirementRefreshing(url) {
		return
	}
	state := s.ensureRequirementRefreshJobs()
	if state.hasIdentity && state.identity != s.currentRequirementListIdentity() {
		s.invalidateRequirementRefreshJobs()
	}
	state.identity, state.hasIdentity = s.currentRequirementListIdentity(), true
	window := state.window
	if s.win != nil {
		window = s.win
	}
	if window == nil || window.IsDestroyed() {
		s.lastError = "需求刷新窗口不可用，请重试。"
		return
	}
	state.mu.Lock()
	closed := state.closed
	state.mu.Unlock()
	if closed {
		return
	}
	key := requirementRefreshKey(url)
	if previous := state.current[key]; previous != nil {
		previous.cancel()
	}
	payload := s.bindRequirementAccount(map[string]any{"url": url, "force": true, "fullRefresh": true})
	// Estimate only the captured current Sprint, and only for its own project.
	projectKey := ""
	if item, ok := s.requirementForURL(url); ok {
		projectKey = firstNonEmpty(item.ProjectKey, item.WorkItem.ProjectKey)
	}
	if projectKey == "" {
		link := domain.ParseFeishuWorkItemLink(url)
		for _, raw := range s.readConfigArray("meegleProjects") {
			if project, ok := raw.(map[string]any); ok && strings.EqualFold(mapString(project, "simple_name"), link.Space) {
				projectKey = mapString(project, "project_key")
				break
			}
		}
	}
	sprint := ""
	if project, _, ok := strings.Cut(s.requirementUnit.state.requirementsSelectedSprintKey, ":"); ok && project != "" && project == projectKey {
		sprint = s.requirementUnit.state.requirementsSelectedSprintKey
		payload["sprintKey"] = sprint
	}
	ctx, cancel := context.WithCancel(context.Background())
	job := &requirementRefreshJob{key: key, url: url, sprint: sprint, configScope: requirementRefreshConfiguration(s.settingsUnit.state.configJSON), identity: s.currentRequirementListIdentity(), identityContext: s.currentRequirementIdentityContext(), payload: payload, ctx: ctx, cancel: cancel, done: make(chan struct{})}
	state.current[key] = job
	state.mu.Lock()
	state.running[job] = struct{}{}
	state.mu.Unlock()
	for _, reader := range s.requirementUnit.state.requirementReaders {
		if sameRequirementReaderLink(reader.url, url) {
			s.retireRequirementReaderRequest(reader, "requirements.read")
			s.retireRequirementReaderRequest(reader, "requirements.comments")
		}
	}
	for _, task := range s.taskUnit.state.tasks {
		if sameRequirementReaderLink(task.requirementURL, url) {
			previous, exists := s.requirementUnit.state.taskRequirementMetadata[task.id]
			s.invalidateTaskRequirementMetadataIdentity(task.id)
			if exists {
				if previous.Status == requirementMetadataLoading {
					previous.Status = requirementMetadataNotLoaded
					if requirementMetadataHasValues(previous.Metadata) {
						previous.Status = requirementMetadataLoaded
					}
				}
				s.requirementUnit.state.taskRequirementMetadata[task.id] = previous
			}
		}
	}
	backend := s.backend
	window.Invalidate()
	go func() {
		defer func() { cancel(); state.mu.Lock(); delete(state.running, job); close(job.done); state.mu.Unlock() }()
		result := executeRequirementRefresh(backend, job)
		if window.IsDestroyed() || ctx.Err() != nil {
			return
		}
		applied := make(chan struct{})
		window.Update(func() { defer close(applied); s.finishRequirementRefresh(job, result) })
		select {
		case <-applied:
		case <-ctx.Done():
		}
	}()
}

func executeRequirementRefresh(backend application.Backend, job *requirementRefreshJob) requirementRefreshResult {
	var result requirementRefreshResult
	for _, part := range []struct {
		kind, label string
		target      any
	}{
		{"requirements.metadata", "标题、状态和角色", new(application.RequirementMetadata)},
		{"requirements.read", "正文", new(application.RequirementContent)},
		{"requirements.comments", "评论", new(application.RequirementComments)},
	} {
		if job.ctx.Err() != nil {
			break
		}
		response, err := backend.Dispatch(job.ctx, application.Action{Kind: part.kind, Payload: job.payload})
		if errors.As(err, &result.identityError) {
			break
		}
		if err == nil {
			var encoded []byte
			encoded, err = json.Marshal(response.Data)
			if err == nil {
				err = json.Unmarshal(encoded, part.target)
			}
		}
		if err != nil {
			result.failures = append(result.failures, part.label+"："+err.Error())
			continue
		}
		problem := ""
		switch data := part.target.(type) {
		case *application.RequirementMetadata:
			problem = firstNonEmpty(data.RefreshError, data.CacheError)
			if data.Stale && problem == "" {
				problem = "远端读取失败，保留缓存"
			}
			if data.ID == "" && problem == "" {
				problem = "返回数据为空"
			}
			if problem == "" {
				result.metadata = data
			}
			if data.EstimateError != "" {
				result.failures = append(result.failures, "Sprint 估时："+data.EstimateError)
			}
		case *application.RequirementContent:
			problem = firstNonEmpty(data.RefreshError, data.CacheError)
			if data.Stale && problem == "" {
				problem = "远端读取失败，保留缓存"
			}
			if data.Generation == "" && problem == "" {
				problem = "正文尚未保存"
			}
			if problem == "" {
				result.body = data
			}
		case *application.RequirementComments:
			problem = firstNonEmpty(data.RefreshError, data.CacheError)
			if data.Stale && problem == "" {
				problem = "远端读取失败，保留缓存"
			}
			if data.FetchedAt == 0 && problem == "" {
				problem = "评论尚未保存"
			}
			if problem == "" {
				result.comments = data
			}
			if len(data.Warnings) > 0 {
				result.failures = append(result.failures, "评论："+strings.Join(data.Warnings, "；"))
			}
		}
		if problem != "" {
			result.failures = append(result.failures, part.label+"："+problem)
		}
	}
	return result
}

func (s *app) finishRequirementRefresh(job *requirementRefreshJob, result requirementRefreshResult) {
	state := s.requirementUnit.state.requirementRefreshJobs
	if state == nil || state.current[job.key] != job {
		return
	}
	delete(state.current, job.key)
	if job.ctx.Err() != nil || s.currentRequirementListIdentity() != job.identity || s.currentRequirementIdentityContext() != job.identityContext || requirementRefreshConfiguration(s.settingsUnit.state.configJSON) != job.configScope {
		return
	}
	if result.identityError != nil {
		s.applyRequirementListIdentityFailure(result.identityError.Snapshot, result.identityError)
		return
	}
	if result.metadata != nil {
		state.metadata[job.key] = *result.metadata
		s.overlayRefreshedRequirements(s.requirementUnit.state.requirements, s.requirementUnit.state.requirementsSelectedSprintKey, s.requirementUnit.state.requirementsFetchedAt)
		// Reconcile copies without dropping their selection or browsing state.
		// Request start times keep an older single-item result from replacing
		// a newer whole-list refresh that already completed.
		if lists := s.requirementUnit.state.requirementListJobs; lists != nil {
			for key, snapshot := range lists.snapshots {
				if key.identity != job.identity {
					continue
				}
				snapshot.items = append([]application.Requirement(nil), snapshot.items...)
				s.overlayRefreshedRequirements(snapshot.items, key.sprint, snapshot.fetchedAt)
				if snapshot.loaded && snapshot.loadError == "" && len(snapshot.failures) == 0 {
					snapshot.estimate = requirementsEstimateTotal(snapshot.items)
				}
				lists.snapshots[key] = snapshot
			}
		}
		for _, task := range s.taskUnit.state.tasks {
			if !sameRequirementReaderLink(task.requirementURL, job.url) {
				continue
			}
			s.invalidateTaskRequirementMetadataIdentity(task.id)
			if s.requirementUnit.state.taskRequirementMetadata == nil {
				s.requirementUnit.state.taskRequirementMetadata = make(map[string]taskRequirementMetadataState)
			}
			metadata := result.metadata
			participants := metadata.Participants
			if metadata.WorkItem != nil {
				participants.QCOwners, participants.ProductManagers = metadata.WorkItem.QCOwners, metadata.WorkItem.ProductManagers
			}
			s.requirementUnit.state.taskRequirementMetadata[task.id] = taskRequirementMetadataState{URL: task.requirementURL, Status: requirementMetadataLoaded, Metadata: requirementMetadata{ID: metadata.ID, ProjectKey: metadata.ProjectKey, Type: metadata.Type, Title: metadata.Title, Status: metadata.Status, Participants: participants, WorkItem: metadata.WorkItem}}
		}
	}
	for _, reader := range s.requirementUnit.state.requirementReaders {
		if !sameRequirementReaderLink(reader.url, job.url) {
			continue
		}
		if result.body != nil {
			s.retireRequirementReaderRequest(reader, "requirements.read")
			s.updateRequirementReaderBody(reader, *result.body)
		}
		if result.comments != nil {
			s.retireRequirementReaderRequest(reader, "requirements.comments")
			s.updateRequirementReaderComments(reader, *result.comments)
		}
	}
	if len(result.failures) == 0 {
		s.lastMessage = "需求刷新成功，已保存到本地"
		s.lastError = ""
	} else {
		s.lastMessage = ""
		s.lastError = "需求刷新未全部完成；失败部分保留上次有效内容。" + strings.Join(result.failures, "；")
	}
}

func (s *app) overlayRefreshedRequirements(items []application.Requirement, sprint string, fetchedAt int64) {
	state := s.requirementUnit.state.requirementRefreshJobs
	if state == nil {
		return
	}
	if state.hasIdentity && state.identity != s.currentRequirementListIdentity() {
		return
	}
	for i := range items {
		item := &items[i]
		metadata, ok := state.metadata[requirementRefreshKey(item.URL)]
		if !ok {
			continue
		}
		if metadata.FetchedAt >= fetchedAt && metadata.WorkItem != nil {
			fresh := metadata.WorkItem
			if metadata.Title != nil {
				item.Title, item.WorkItem.Title = *metadata.Title, *metadata.Title
			}
			item.Status = ""
			if metadata.Status != nil {
				item.Status = *metadata.Status
			}
			item.WorkItem.Status = fresh.Status
			item.WorkItem.Developers, item.WorkItem.QCOwners, item.WorkItem.ProductManagers = fresh.Developers, fresh.QCOwners, fresh.ProductManagers
			item.Participants = nil
			for _, people := range [][]domain.RequirementPerson{fresh.Developers, fresh.QCOwners, fresh.ProductManagers} {
				for _, person := range people {
					item.Participants = append(item.Participants, person.Name)
				}
			}
		}
		if estimate, ok := metadata.SprintEstimates[sprint]; ok && estimate.FetchedAt >= fetchedAt {
			item.WorkItem.MySprintEstimateDays = estimate.Days
		}
	}
}

func (s *app) invalidateRequirementRefreshJobs() {
	state := s.requirementUnit.state.requirementRefreshJobs
	if state == nil {
		return
	}
	for key, job := range state.current {
		job.cancel()
		delete(state.current, key)
	}
	state.metadata = make(map[string]application.RequirementMetadata)
}

func (s *app) observeRequirementRefreshJobs() {
	state := s.requirementUnit.state.requirementRefreshJobs
	if state == nil {
		return
	}
	identity := s.currentRequirementListIdentity()
	if state.hasIdentity && state.identity != identity {
		s.invalidateRequirementRefreshJobs()
		state.identity = identity
		return
	}
	for _, job := range state.current {
		if job.identity != identity {
			s.invalidateRequirementRefreshJobs()
			return
		}
	}
}

func (s *app) closeRequirementRefreshJobs(ctx context.Context) error {
	state := s.requirementUnit.state.requirementRefreshJobs
	if state == nil {
		return nil
	}
	s.invalidateRequirementRefreshJobs()
	state.mu.Lock()
	state.closed = true
	running := make([]*requirementRefreshJob, 0, len(state.running))
	for job := range state.running {
		job.cancel()
		running = append(running, job)
	}
	state.mu.Unlock()
	for _, job := range running {
		select {
		case <-job.done:
		case <-ctx.Done():
			return fmt.Errorf("等待单项需求刷新清理：%w", ctx.Err())
		}
	}
	return nil
}
