package application

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/integrations"
	"silverwing-go/internal/workspace"
)

// TaskRequirementCommentSyncBackend exposes the desktop lifecycle and scoped
// controls for the background publisher.
type TaskRequirementCommentSyncBackend interface {
	StartTaskRequirementCommentSync()
	CloseTaskRequirementCommentSync(context.Context) error
	TaskRequirementCommentSyncStatus(taskPath, createdAt string) TaskRequirementCommentSyncStatus
	RetryTaskRequirementCommentSync(taskPath, createdAt string) error
	TaskRequirementCommentSyncChanges() <-chan struct{}
}

type TaskRequirementCommentSyncStatus struct {
	State          string `json:"state"`
	Message        string `json:"message,omitempty"`
	RequirementURL string `json:"requirementUrl"`
	CommentID      string `json:"commentId,omitempty"`
	Revision       uint64 `json:"revision,omitempty"`
}

type taskRequirementCommentClient interface {
	ResolveTaskRequirementCommentScope(context.Context, string) (integrations.TaskRequirementCommentScope, error)
	SyncTaskRequirementComment(context.Context, string, integrations.TaskRequirementCommentScope, integrations.TaskRequirementCommentWrite, func(integrations.TaskRequirementCommentAttempt) error, func() error) (string, error)
	CheckTaskRequirementComment(context.Context, string, integrations.TaskRequirementCommentScope, integrations.TaskRequirementCommentAttempt) (string, error)
}

type taskRequirementCommentSync struct {
	app     *App
	client  taskRequirementCommentClient
	mu      sync.Mutex
	store   taskRequirementCommentStore
	loadErr error
	queued  map[string]bool
	wake    chan struct{}
	changes chan struct{}
	done    chan struct{}
	cancel  context.CancelFunc
	closed  bool
}

var _ TaskRequirementCommentSyncBackend = (*App)(nil)

var errTaskRequirementCommentSuperseded = errors.New("任务分支或资料目录已更新")

const taskRequirementCommentSignature = "-来自Silverwing"

func (a *App) taskRequirementCommentSync() *taskRequirementCommentSync {
	a.taskRequirementCommentsMu.Lock()
	defer a.taskRequirementCommentsMu.Unlock()
	if a.taskRequirementComments == nil {
		manager := &taskRequirementCommentSync{app: a, client: a.taskRequirementCommentClient,
			queued: make(map[string]bool), wake: make(chan struct{}, 1), changes: make(chan struct{}, 1)}
		if manager.client != nil {
			manager.store, manager.loadErr = loadTaskRequirementCommentStore(a.Paths.Home)
		}
		a.taskRequirementComments = manager
	}
	return a.taskRequirementComments
}

func (a *App) StartTaskRequirementCommentSync() {
	m := a.taskRequirementCommentSync()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.done != nil || m.client == nil || m.loadErr != nil {
		return
	}
	for key, record := range m.store.records {
		if record.State == "pending" || record.State == "syncing" || record.Attempt != nil || record.BlockedBy != "" {
			m.queued[key] = true
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel, m.done = cancel, make(chan struct{})
	go m.run(ctx)
}

func (a *App) CloseTaskRequirementCommentSync(ctx context.Context) error {
	m := a.taskRequirementCommentSync()
	m.mu.Lock()
	m.closed = true
	if m.cancel != nil {
		m.cancel()
	}
	done := m.done
	m.mu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *App) TaskRequirementCommentSyncChanges() <-chan struct{} {
	return a.taskRequirementCommentSync().changes
}

func (a *App) TaskRequirementCommentSyncStatus(taskPath, createdAt string) TaskRequirementCommentSyncStatus {
	m := a.taskRequirementCommentSync()
	m.mu.Lock()
	defer m.mu.Unlock()
	record := m.store.records[taskRequirementCommentKey(taskPath, createdAt)]
	if sameTaskContextPath(record.TaskPath, taskPath) {
		return record.TaskRequirementCommentSyncStatus
	}
	return TaskRequirementCommentSyncStatus{}
}

func (a *App) RetryTaskRequirementCommentSync(taskPath, createdAt string) error {
	if !filepath.IsAbs(taskPath) || createdAt == "" {
		return errDesktopTaskContextChanged
	}
	manifest, path, err := a.task(Action{TaskID: filepath.Base(taskPath)})
	if err != nil {
		return fmt.Errorf("核对评论所属任务失败：%w", err)
	}
	if !sameTaskContextPath(path, taskPath) || manifest.CreatedAt != createdAt {
		return errDesktopTaskContextChanged
	}
	return a.queueTaskRequirementComment(path, manifest, false)
}

func (a *App) enqueueTaskRequirementComment(taskPath string, manifest domain.TaskManifest) {
	_ = a.queueTaskRequirementComment(taskPath, manifest, true)
}

func (a *App) queueTaskRequirementComment(taskPath string, manifest domain.TaskManifest, coalesce bool) error {
	if !filepath.IsAbs(taskPath) || manifest.CreatedAt == "" {
		return errDesktopTaskContextChanged
	}
	link := strings.TrimSpace(manifest.RequirementLink)
	if domain.ParseFeishuWorkItemLink(link) == nil {
		return errors.New("请先关联飞书需求，再同步评论")
	}
	m := a.taskRequirementCommentSync()
	var account string
	if cached, ok := m.client.(interface{ CachedTaskRequirementCommentAccount() string }); ok {
		account = cached.CachedTaskRequirementCommentAccount()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.client == nil || m.closed {
		return errors.New("评论同步当前不可用")
	}
	key := taskRequirementCommentKey(taskPath, manifest.CreatedAt)
	record := m.store.records[key]
	if !coalesce {
		if record.TaskPath != "" && (!sameTaskContextPath(record.TaskPath, taskPath) || record.RequirementURL != link) {
			return errDesktopTaskContextChanged
		}
		if record.State == "pending" || record.State == "syncing" || m.queued[key] {
			return errors.New("评论同步正在进行")
		}
	}
	if record.RequirementURL == "" {
		record.RequirementURL = link
		record.ExpectedAccount = account
	}
	record.TaskPath, record.TaskCreatedAt = taskPath, manifest.CreatedAt
	return m.queueLocked(key, record)
}

func (m *taskRequirementCommentSync) queueLocked(key string, record taskRequirementCommentRecord) error {
	record.Generation++
	record.State, record.Message, record.BlockedBy = "pending", "", ""
	err := m.loadErr
	if err == nil {
		err = m.store.save(key, record)
	}
	if err != nil {
		// An unsuccessful local write must never authorize a remote write.
		record.State, record.Message = "failed", err.Error()
		m.store.records[key] = record
	} else {
		m.queued[key] = true
		wakeTaskRequirementCommentSync(m.wake)
	}
	wakeTaskRequirementCommentSync(m.changes)
	return err
}

func wakeTaskRequirementCommentSync(signal chan struct{}) {
	select {
	case signal <- struct{}{}:
	default:
	}
}

func (m *taskRequirementCommentSync) run(ctx context.Context) {
	defer close(m.done)
	for ctx.Err() == nil {
		key, record := m.next()
		if key == "" {
			select {
			case <-ctx.Done():
				return
			case <-m.wake:
			}
			continue
		}
		state, message, blockedBy := m.sync(ctx, key, record)
		m.finish(key, record.Generation, state, message, blockedBy)
	}
}

func (m *taskRequirementCommentSync) next() (string, taskRequirementCommentRecord) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key := range m.queued {
		delete(m.queued, key)
		record := m.store.records[key]
		record.State, record.Message = "syncing", ""
		m.store.records[key] = record
		wakeTaskRequirementCommentSync(m.changes)
		return key, record
	}
	return "", taskRequirementCommentRecord{}
}

func (m *taskRequirementCommentSync) finish(key string, generation uint64, state, message, blockedBy string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	record := m.store.records[key]
	if record.Generation != generation {
		// A newer committed manifest already has its own queued work.
		return
	}
	record.State, record.Message, record.BlockedBy = state, message, blockedBy
	if err := m.store.save(key, record); err != nil {
		record.State, record.Message = "failed", err.Error()
		m.store.records[key] = record
	}
	if blockedBy != "" && m.store.records[blockedBy].Attempt != nil {
		// The original task may have disappeared from the sidebar. A sibling's
		// retry can still check that attempt without writing to the old task.
		m.queued[blockedBy] = true
	}
	wakeTaskRequirementCommentSync(m.changes)
}

func (m *taskRequirementCommentSync) sync(ctx context.Context, key string, record taskRequirementCommentRecord) (state, message, blockedBy string) {
	state, blockedBy, err := m.syncRecord(ctx, key, record)
	if ctx.Err() != nil {
		return "pending", "", ""
	}
	if errors.Is(err, errTaskRequirementCommentSuperseded) {
		m.mu.Lock()
		m.queued[key] = true
		m.mu.Unlock()
		return "pending", "", ""
	}
	var conflict *integrations.TaskRequirementCommentConflictError
	if errors.As(err, &conflict) {
		return "conflict", conflict.Error(), ""
	}
	if err != nil {
		m.mu.Lock()
		pending := m.store.records[key].Attempt != nil
		m.mu.Unlock()
		if pending {
			return "uncertain", err.Error(), ""
		}
		return "failed", err.Error(), ""
	}
	if state == "blocked" {
		return state, "同一需求的另一条同步评论正在核对，请等待其结果", blockedBy
	}
	return state, "", ""
}

func (m *taskRequirementCommentSync) syncRecord(ctx context.Context, key string, record taskRequirementCommentRecord) (string, string, error) {
	var body string
	if record.Attempt == nil {
		manifest, config, err := m.loadTask(record)
		if err != nil {
			return "", "", err
		}
		body = taskRequirementCommentBody(manifest, config)
		if body == "" || record.CommentID != "" && body == record.ConfirmedBody {
			return "synced", "", nil
		}
	}
	scope, err := m.client.ResolveTaskRequirementCommentScope(ctx, record.RequirementURL)
	if err != nil {
		return "", "", err
	}
	if record.ExpectedAccount != "" && record.ExpectedAccount != scope.Account {
		return "", "", &integrations.TaskRequirementCommentConflictError{Message: "Meegle 账号已变化，请使用原账号重试同步"}
	}
	if record.Scope != (integrations.TaskRequirementCommentScope{}) && record.Scope != scope {
		return "", "", &integrations.TaskRequirementCommentConflictError{Message: "飞书需求或发布账号已变化，请使用原账号核对同步评论"}
	}
	if record.Scope != scope {
		if err := m.changeRecord(key, func(current *taskRequirementCommentRecord) { current.Scope = scope }); err != nil {
			return "", "", err
		}
	}
	if record.Attempt != nil {
		// Verify an earlier write against its recorded target even when the task
		// was removed. Sending fresh content still requires the current task.
		id, err := m.client.CheckTaskRequirementComment(ctx, record.RequirementURL, scope, *record.Attempt)
		if err != nil {
			return "", "", err
		}
		if err := m.confirm(key, scope, id, record.Attempt.Body); err != nil {
			return "", "", err
		}
		record.CommentID, record.ConfirmedBody = id, record.Attempt.Body
		manifest, config, err := m.loadTask(record)
		if err != nil {
			return "", "", err
		}
		body = taskRequirementCommentBody(manifest, config)
		// A pending pre-signature request is just being reconciled. Add the
		// signature on its next sync or when there are newer task details.
		if body == record.ConfirmedBody || body == record.ConfirmedBody+"\n\n"+taskRequirementCommentSignature || body == "" {
			return "synced", "", nil
		}
	}
	if record.CommentID == "" {
		if blocker := m.unresolvedCreate(key, scope); blocker != "" {
			return "blocked", blocker, nil
		}
	}
	guard := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		latest, currentConfig, err := m.loadTask(record)
		if err != nil {
			return err
		}
		if taskRequirementCommentBody(latest, currentConfig) != body {
			return errTaskRequirementCommentSuperseded
		}
		return nil
	}
	write := integrations.TaskRequirementCommentWrite{CommentID: record.CommentID, PreviousBody: record.ConfirmedBody, Body: body}
	id, err := m.client.SyncTaskRequirementComment(ctx, record.RequirementURL, scope, write, func(attempt integrations.TaskRequirementCommentAttempt) error {
		return m.changeRecord(key, func(current *taskRequirementCommentRecord) { current.Attempt = &attempt })
	}, guard)
	if err != nil {
		var notSent interface{ RequestNotSent() }
		if errors.As(err, &notSent) {
			clearErr := m.changeRecord(key, func(current *taskRequirementCommentRecord) { current.Attempt = nil })
			err = errors.Join(err, clearErr)
		}
		return "", "", err
	}
	return "synced", "", m.confirm(key, scope, id, body)
}

func (m *taskRequirementCommentSync) loadTask(record taskRequirementCommentRecord) (domain.TaskManifest, domain.AppConfig, error) {
	config, err := m.app.Config.Load()
	if err != nil {
		return domain.TaskManifest{}, config, err
	}
	manifest, path, err := m.app.taskInConfig(Action{TaskID: filepath.Base(record.TaskPath)}, config)
	if err != nil {
		return domain.TaskManifest{}, config, fmt.Errorf("核对评论所属任务失败：%w", err)
	}
	if !sameTaskContextPath(path, record.TaskPath) || manifest.CreatedAt != record.TaskCreatedAt || strings.TrimSpace(manifest.RequirementLink) != record.RequirementURL {
		return domain.TaskManifest{}, config, &integrations.TaskRequirementCommentConflictError{Message: "任务或关联需求已变化，评论同步已暂停"}
	}
	return manifest, config, nil
}

func (m *taskRequirementCommentSync) changeRecord(key string, update func(*taskRequirementCommentRecord)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	record := m.store.records[key]
	update(&record)
	return m.store.save(key, record)
}

func (m *taskRequirementCommentSync) unresolvedCreate(key string, scope integrations.TaskRequirementCommentScope) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	for otherKey, record := range m.store.records {
		if otherKey != key && record.CommentID == "" && record.Attempt != nil && sameTaskRequirementCommentTarget(record.Scope, scope) {
			return otherKey
		}
	}
	return ""
}

func (m *taskRequirementCommentSync) confirm(key string, scope integrations.TaskRequirementCommentScope, id, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	record := m.store.records[key]
	if id == "" || record.CommentID != "" && record.CommentID != id {
		return &integrations.TaskRequirementCommentConflictError{Message: "回读结果与任务的同步评论不一致"}
	}
	for otherKey, other := range m.store.records {
		if otherKey != key && other.CommentID == id && sameTaskRequirementCommentTarget(other.Scope, scope) {
			return &integrations.TaskRequirementCommentConflictError{Message: "回读评论已关联另一个任务"}
		}
	}
	record.CommentID, record.ConfirmedBody, record.Attempt = id, body, nil
	record.Revision++
	if err := m.store.save(key, record); err != nil {
		return err
	}
	for otherKey, other := range m.store.records {
		if other.BlockedBy == key {
			m.queued[otherKey] = true
		}
	}
	return nil
}

func sameTaskRequirementCommentTarget(left, right integrations.TaskRequirementCommentScope) bool {
	return left.ProjectKey == right.ProjectKey && left.WorkItemID == right.WorkItemID
}

func taskRequirementCommentBody(manifest domain.TaskManifest, config domain.AppConfig) string {
	var sections []string
	branches := domain.TaskManifest{}
	for _, service := range manifest.Services {
		if strings.TrimSpace(service.ServiceName) != "" && strings.TrimSpace(service.Branch) != "" {
			branches.Services = append(branches.Services, service)
		}
	}
	if len(branches.Services) > 0 {
		sections = append(sections, taskRequirementCommentSection("服务与分支", workspace.FormatTaskBranchInfo(branches, false)))
	}
	manifest = documents.ResolveBoundTaskMaterials(manifest, config)
	if manifest.RequirementMaterials.Status == domain.MaterialsReady && validAnnualTaskMaterials(manifest, config) == nil {
		if root := taskMaterialsPermissions(manifest, config).ReadRoot; root != "" {
			sections = append(sections, taskRequirementCommentSection("本地任务资料目录", root))
		}
	}
	if len(sections) > 0 {
		sections = append(sections, taskRequirementCommentSignature)
	}
	return strings.Join(sections, "\n\n")
}

func taskRequirementCommentSection(title, text string) string {
	fence := "```"
	for strings.Contains(text, fence) {
		fence += "`"
	}
	return "**" + title + "**\n" + fence + "\n" + text + "\n" + fence
}
