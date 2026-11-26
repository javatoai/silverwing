package application

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/integrations"
	"silverwing-go/internal/storage"
	"silverwing-go/internal/workspace"
)

const taskCommentFixtureURL = "https://project.feishu.cn/fixture/userstory/detail/123"

type taskCommentFixtureWrite struct {
	scope integrations.TaskRequirementCommentScope
	id    string
	input integrations.TaskRequirementCommentWrite
}

// The fake keeps its own remote state across App instances. A callback may
// interrupt a write after the remote state was changed but before any receipt.
type taskCommentFixtureRemote struct {
	mu             sync.Mutex
	account        string
	comments       map[string]taskCommentFixtureWrite
	writes         []taskCommentFixtureWrite
	checks         int
	readbackHidden bool
	afterWrite     func(context.Context, taskCommentFixtureWrite) error
}

func newTaskCommentFixtureRemote() *taskCommentFixtureRemote {
	return &taskCommentFixtureRemote{account: "fixture-account", comments: make(map[string]taskCommentFixtureWrite)}
}

func (r *taskCommentFixtureRemote) CachedTaskRequirementCommentAccount() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.account
}

func (r *taskCommentFixtureRemote) ResolveTaskRequirementCommentScope(_ context.Context, url string) (integrations.TaskRequirementCommentScope, error) {
	link := domain.ParseFeishuWorkItemLink(url)
	if link == nil {
		return integrations.TaskRequirementCommentScope{}, errors.New("invalid fixture requirement link")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return integrations.TaskRequirementCommentScope{
		ProjectKey: "project-" + link.Space, WorkItemID: link.WorkItemID, Account: r.account, AuthorKey: "author-" + r.account,
	}, nil
}

func (r *taskCommentFixtureRemote) SyncTaskRequirementComment(ctx context.Context, _ string, scope integrations.TaskRequirementCommentScope, input integrations.TaskRequirementCommentWrite, persist func(integrations.TaskRequirementCommentAttempt) error, guard func() error) (string, error) {
	r.mu.Lock()
	if scope.Account != r.account {
		r.mu.Unlock()
		return "", &integrations.TaskRequirementCommentConflictError{Message: "fixture account changed"}
	}
	if input.CommentID != "" {
		existing := r.comments[input.CommentID]
		if existing.id == "" || existing.scope != scope || existing.input.Body != input.PreviousBody {
			r.mu.Unlock()
			return "", &integrations.TaskRequirementCommentConflictError{Message: "fixture target comment was modified or deleted"}
		}
	}
	attempt := integrations.TaskRequirementCommentAttempt{CommentID: input.CommentID, Body: input.Body, StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	for id, comment := range r.comments {
		if comment.scope.ProjectKey == scope.ProjectKey && comment.scope.WorkItemID == scope.WorkItemID {
			attempt.BeforeIDs = append(attempt.BeforeIDs, id)
		}
	}
	r.mu.Unlock()
	if err := persist(attempt); err != nil {
		return "", err
	}
	if err := guard(); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	r.mu.Lock()
	id := input.CommentID
	if id == "" {
		id = fmt.Sprint(1000 + len(r.writes))
	}
	write := taskCommentFixtureWrite{scope: scope, id: id, input: input}
	r.writes = append(r.writes, write)
	r.comments[id] = write
	afterWrite := r.afterWrite
	r.mu.Unlock()
	if afterWrite != nil {
		if err := afterWrite(ctx, write); err != nil {
			return "", &integrations.TaskRequirementCommentUncertainError{Cause: err}
		}
	}
	return id, nil
}

func (r *taskCommentFixtureRemote) CheckTaskRequirementComment(_ context.Context, _ string, scope integrations.TaskRequirementCommentScope, attempt integrations.TaskRequirementCommentAttempt) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checks++
	if scope.Account != r.account {
		return "", &integrations.TaskRequirementCommentConflictError{Message: "fixture account changed"}
	}
	if r.readbackHidden {
		return "", &integrations.TaskRequirementCommentUncertainError{Cause: errors.New("fixture receipt not visible")}
	}
	var match string
	for id, comment := range r.comments {
		if comment.scope != scope || comment.input.Body != attempt.Body {
			continue
		}
		if (attempt.CommentID != "" && id != attempt.CommentID) || (attempt.CommentID == "" && slices.Contains(attempt.BeforeIDs, id)) {
			continue
		}
		if match != "" {
			return "", &integrations.TaskRequirementCommentConflictError{Message: "multiple fixture receipts"}
		}
		match = id
	}
	if match == "" {
		return "", &integrations.TaskRequirementCommentUncertainError{Cause: errors.New("fixture receipt not visible")}
	}
	return match, nil
}

func (r *taskCommentFixtureRemote) written() []taskCommentFixtureWrite {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.writes)
}

func taskCommentFixture(t *testing.T) (*App, domain.AppConfig, *taskCommentFixtureRemote) {
	t.Helper()
	paths := storage.NewPaths(t.TempDir())
	paths.Tasks = filepath.Join(paths.Home, "isolated-tasks")
	a := composeApp(paths, storage.NewManifestStore())
	config := domain.DefaultAppConfig()
	config.TaskRoot = &paths.Tasks
	// Do not let an incorrectly prepared fixture fall through to a real CLI.
	missingCLI := filepath.Join(paths.Home, "fixture-meegle-not-installed")
	config.MeegleExecutablePath = &missingCLI
	if err := a.Config.Save(config); err != nil {
		t.Fatal(err)
	}
	remote := newTaskCommentFixtureRemote()
	a.taskRequirementCommentClient = remote
	t.Cleanup(func() { closeTaskCommentFixture(t, a) })
	return a, config, remote
}

func closeTaskCommentFixture(t *testing.T, a *App) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.CloseTaskRequirementCommentSync(ctx); err != nil {
		t.Errorf("close task comment sync: %v", err)
	}
}

func savedTaskCommentFixture(t *testing.T, a *App, config domain.AppConfig, name string) (domain.TaskManifest, string) {
	t.Helper()
	path := filepath.Join(*config.TaskRoot, name)
	manifest := domain.DefaultTaskManifest()
	manifest.FolderName, manifest.TaskDirectoryName = name, name
	manifest.CreatedAt, manifest.UpdatedAt = "2026-11-20T08:10:26Z", "2026-11-20T08:10:26Z"
	manifest.RequirementLink = taskCommentFixtureURL
	manifest.Services = []domain.ServiceWorkspace{taskCommentFixtureService(path, "api", "feature/first")}
	saveTaskCommentFixture(t, a, path, manifest)
	return manifest, path
}

func taskCommentFixtureService(path, service, branch string) domain.ServiceWorkspace {
	return domain.ServiceWorkspace{
		RepositoryID: service, RepositoryPath: filepath.Join(filepath.Dir(path), service+"-repo"), WorktreePath: filepath.Join(path, service),
		GroupServiceID: service, ServiceName: service, Branch: branch, Health: domain.WorkspaceReady,
		DevelopmentTool: domain.DevelopmentToolVisualStudioCode, Strategy: domain.WorkspaceStandardWorktree,
		ModuleID: "default", ModuleName: "default", ModuleSource: domain.TaskModuleConfigured, TagMode: domain.TagBuildCurrentBranch,
	}
}

func saveTaskCommentFixture(t *testing.T, a *App, path string, manifest domain.TaskManifest) {
	t.Helper()
	if err := a.Manifests.Save(path, manifest); err != nil {
		t.Fatal(err)
	}
}

func taskCommentFixtureSection(title, text string) string {
	return "**" + title + "**\n```\n" + text + "\n```"
}

func awaitTaskCommentFixture(t *testing.T, a *App, path, createdAt string, states ...string) TaskRequirementCommentSyncStatus {
	t.Helper()
	timer := time.NewTimer(8 * time.Second)
	defer timer.Stop()
	changes := a.TaskRequirementCommentSyncChanges()
	for {
		status := a.TaskRequirementCommentSyncStatus(path, createdAt)
		if slices.Contains(states, status.State) {
			return status
		}
		select {
		case <-changes:
		case <-timer.C:
			t.Fatalf("wait for task comment state %v, last status: %+v", states, status)
		}
	}
}

func TestTaskRequirementCommentSyncManualFirstSyncAndRetry(t *testing.T) {
	a, config, remote := taskCommentFixture(t)
	manifest, path := savedTaskCommentFixture(t, a, config, "existing-task")
	if err := a.RetryTaskRequirementCommentSync(filepath.Join(t.TempDir(), manifest.TaskDirectoryName), manifest.CreatedAt); !errors.Is(err, errDesktopTaskContextChanged) {
		t.Fatalf("retry accepted a stale task path: %v", err)
	}
	if err := a.RetryTaskRequirementCommentSync(path, "2026-11-19T08:10:26Z"); !errors.Is(err, errDesktopTaskContextChanged) {
		t.Fatalf("retry accepted a replaced task: %v", err)
	}
	if status := a.TaskRequirementCommentSyncStatus(path, manifest.CreatedAt); status.State != "" {
		t.Fatalf("stale retries enqueued the task: %+v", status)
	}
	if err := a.RetryTaskRequirementCommentSync(path, manifest.CreatedAt); err != nil {
		t.Fatalf("queue the first manual sync: %v", err)
	}
	if status := a.TaskRequirementCommentSyncStatus(path, manifest.CreatedAt); status.State != "pending" || status.RequirementURL != taskCommentFixtureURL {
		t.Fatalf("first sync omitted the saved requirement: %+v", status)
	}
	if err := a.RetryTaskRequirementCommentSync(path, manifest.CreatedAt); err == nil {
		t.Fatal("manual sync accepted a duplicate while already pending")
	}
	a.StartTaskRequirementCommentSync()
	first := awaitTaskCommentFixture(t, a, path, manifest.CreatedAt, "synced")
	if first.CommentID == "" || len(remote.written()) != 1 {
		t.Fatalf("first sync did not create exactly one comment: %+v", first)
	}
	if err := a.RetryTaskRequirementCommentSync(path, manifest.CreatedAt); err != nil {
		t.Fatalf("retry a synced task: %v", err)
	}
	if status := awaitTaskCommentFixture(t, a, path, manifest.CreatedAt, "synced"); status != first || len(remote.written()) != 1 {
		t.Fatalf("unchanged retry added a remote write: %+v", status)
	}
	manifest.Services[0].Branch = "feature/manual-update"
	saveTaskCommentFixture(t, a, path, manifest)
	if err := a.RetryTaskRequirementCommentSync(path, manifest.CreatedAt); err != nil {
		t.Fatalf("queue manual sync after a saved edit: %v", err)
	}
	status := awaitTaskCommentFixture(t, a, path, manifest.CreatedAt, "synced")
	writes := remote.written()
	if status.CommentID != first.CommentID || len(writes) != 2 || writes[1].input.CommentID != first.CommentID || !strings.Contains(writes[1].input.Body, "feature/manual-update") {
		t.Fatalf("manual sync did not update the original comment: status=%+v writes=%+v", status, writes)
	}
}

func TestTaskRequirementCommentSyncReconcilesCommentWithoutSignature(t *testing.T) {
	a, config, remote := taskCommentFixture(t)
	manifest, path := savedTaskCommentFixture(t, a, config, "older-task")
	a.enqueueTaskRequirementComment(path, manifest)
	const oldBody = "**服务与分支**\n```\napi：feature/first\n```"
	scope, err := remote.ResolveTaskRequirementCommentScope(context.Background(), manifest.RequirementLink)
	if err != nil {
		t.Fatal(err)
	}
	journal := a.taskRequirementCommentSync()
	key := taskRequirementCommentKey(path, manifest.CreatedAt)
	id, err := remote.SyncTaskRequirementComment(context.Background(), manifest.RequirementLink, scope,
		integrations.TaskRequirementCommentWrite{Body: oldBody}, func(attempt integrations.TaskRequirementCommentAttempt) error {
			return journal.changeRecord(key, func(record *taskRequirementCommentRecord) {
				record.Scope, record.Attempt = scope, &attempt
			})
		}, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	a.StartTaskRequirementCommentSync()
	status := awaitTaskCommentFixture(t, a, path, manifest.CreatedAt, "synced")
	if status.CommentID != id || len(remote.written()) != 1 {
		t.Fatalf("recovering a previous attempt sent another write: %+v", status)
	}
	if err := a.RetryTaskRequirementCommentSync(path, manifest.CreatedAt); err != nil {
		t.Fatal(err)
	}
	awaitTaskCommentFixture(t, a, path, manifest.CreatedAt, "synced")
	writes := remote.written()
	if len(writes) != 2 || writes[1].input.CommentID != id || writes[1].input.Body != oldBody+"\n\n-来自Silverwing" {
		t.Fatalf("next sync did not sign the same comment: %+v", writes)
	}
}

func TestTaskRequirementCommentSyncUpdatesOneCommentPerTask(t *testing.T) {
	a, config, remote := taskCommentFixture(t)
	first, firstPath := savedTaskCommentFixture(t, a, config, "first-task")
	second, secondPath := savedTaskCommentFixture(t, a, config, "second-task")
	a.StartTaskRequirementCommentSync()
	a.enqueueTaskRequirementComment(firstPath, first)
	firstStatus := awaitTaskCommentFixture(t, a, firstPath, first.CreatedAt, "synced")
	a.enqueueTaskRequirementComment(secondPath, second)
	secondStatus := awaitTaskCommentFixture(t, a, secondPath, second.CreatedAt, "synced")
	if firstStatus.CommentID == "" || secondStatus.CommentID == "" || firstStatus.CommentID == secondStatus.CommentID {
		t.Fatalf("two tasks sharing a requirement must have independent comments: %+v %+v", firstStatus, secondStatus)
	}
	oldFirst := first
	first.Services = append(first.Services, taskCommentFixtureService(firstPath, "worker", "feature/change"))
	local, err := documents.PrepareLocalTaskMaterials(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	first.RequirementMaterials = domain.RequirementMaterialsDirectory{Status: domain.MaterialsReady, WriteRoot: &local.Owned}
	saveTaskCommentFixture(t, a, firstPath, first)
	// The event carries only task identity. Stale fields in the event may
	// never override the manifest that was durably saved later.
	a.enqueueTaskRequirementComment(firstPath, oldFirst)
	if got := awaitTaskCommentFixture(t, a, firstPath, first.CreatedAt, "synced"); got.CommentID != firstStatus.CommentID {
		t.Fatalf("update replaced the first comment: %+v", got)
	}
	writes := remote.written()
	if len(writes) != 3 || writes[0].input.CommentID != "" || writes[1].input.CommentID != "" || writes[2].input.CommentID != firstStatus.CommentID || writes[2].input.PreviousBody != writes[0].input.Body {
		t.Fatalf("wrong create/update sequence: %+v", writes)
	}
	if got, want := writes[0].input.Body, taskCommentFixtureSection("服务与分支", "api：feature/first")+"\n\n-来自Silverwing"; got != want {
		t.Fatalf("first comment body = %q, want %q", got, want)
	}
	want := taskCommentFixtureSection("服务与分支", "api：feature/first\nworker：feature/change") + "\n\n" + taskCommentFixtureSection("本地任务资料目录", local.Root) + "\n\n-来自Silverwing"
	if got := writes[2].input.Body; got != want {
		t.Fatalf("updated body = %q, want %q", got, want)
	}
	// A saved task field that is outside the two comment sections cannot
	// produce an extra update even though it reaches the same worker.
	first.UpdatedAt = "2026-11-22T08:10:26Z"
	saveTaskCommentFixture(t, a, firstPath, first)
	a.enqueueTaskRequirementComment(firstPath, first)
	awaitTaskCommentFixture(t, a, firstPath, first.CreatedAt, "synced")
	if got := len(remote.written()); got != 3 {
		t.Fatalf("identical content issued another write: %d", got)
	}
}

func TestTaskRequirementCommentSyncCoalescesQueuedChanges(t *testing.T) {
	a, config, remote := taskCommentFixture(t)
	manifest, path := savedTaskCommentFixture(t, a, config, "coalesced-task")
	started, release := make(chan struct{}), make(chan struct{})
	remote.afterWrite = func(ctx context.Context, write taskCommentFixtureWrite) error {
		if write.id == "1000" && write.input.CommentID == "" {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
	a.StartTaskRequirementCommentSync()
	a.enqueueTaskRequirementComment(path, manifest)
	select {
	case <-started:
	case <-time.After(8 * time.Second):
		t.Fatal("first mock write was not started")
	}
	for _, branch := range []string{"feature/obsolete", "feature/latest", "feature/latest"} {
		manifest.Services[0].Branch = branch
		saveTaskCommentFixture(t, a, path, manifest)
		a.enqueueTaskRequirementComment(path, manifest)
	}
	close(release)
	status := awaitTaskCommentFixture(t, a, path, manifest.CreatedAt, "synced")
	writes := remote.written()
	if len(writes) != 2 || writes[0].input.CommentID != "" || writes[1].input.CommentID != status.CommentID || writes[1].input.Body != taskCommentFixtureSection("服务与分支", "api：feature/latest")+"\n\n-来自Silverwing" {
		t.Fatalf("intermediate or duplicate writes survived coalescing: %+v", writes)
	}
}

func TestTaskRequirementCommentSyncRecoversRemoteWriteAfterRestart(t *testing.T) {
	a, config, remote := taskCommentFixture(t)
	manifest, path := savedTaskCommentFixture(t, a, config, "resumed-task")
	remote.afterWrite = func(_ context.Context, write taskCommentFixtureWrite) error {
		if write.input.CommentID == "" {
			return errors.New("fixture timed out after remote accepted create")
		}
		return nil
	}
	a.StartTaskRequirementCommentSync()
	a.enqueueTaskRequirementComment(path, manifest)
	awaitTaskCommentFixture(t, a, path, manifest.CreatedAt, "uncertain")
	closeTaskCommentFixture(t, a)
	writes := remote.written()
	if len(writes) != 1 {
		t.Fatalf("expected one ambiguous create, got %+v", writes)
	}
	b := composeApp(a.Paths, a.Manifests)
	b.taskRequirementCommentClient = remote
	t.Cleanup(func() { closeTaskCommentFixture(t, b) })
	manifest.Services[0].Branch = "feature/after-restart"
	saveTaskCommentFixture(t, b, path, manifest)
	b.StartTaskRequirementCommentSync()
	status := awaitTaskCommentFixture(t, b, path, manifest.CreatedAt, "synced")
	remote.mu.Lock()
	checks := remote.checks
	remote.mu.Unlock()
	writes = remote.written()
	if checks == 0 || len(writes) != 2 || status.CommentID != writes[0].id || writes[1].input.CommentID != writes[0].id || !strings.Contains(writes[1].input.Body, "feature/after-restart") {
		t.Fatalf("restart failed to recover the first comment before writing latest state: checks=%d status=%+v writes=%+v", checks, status, writes)
	}
}

func TestTaskRequirementCommentSyncRejectsConflictsWithoutCreatingReplacement(t *testing.T) {
	for _, mode := range []string{"account", "edited", "deleted", "target"} {
		t.Run(mode, func(t *testing.T) {
			a, config, remote := taskCommentFixture(t)
			manifest, path := savedTaskCommentFixture(t, a, config, "conflict-task")
			a.StartTaskRequirementCommentSync()
			a.enqueueTaskRequirementComment(path, manifest)
			first := awaitTaskCommentFixture(t, a, path, manifest.CreatedAt, "synced")
			remote.mu.Lock()
			switch mode {
			case "account":
				remote.account = "another-account"
			case "edited":
				comment := remote.comments[first.CommentID]
				comment.input.Body = "teammate added unrelated text"
				remote.comments[first.CommentID] = comment
			case "deleted":
				delete(remote.comments, first.CommentID)
			case "target":
				manifest.RequirementLink = "https://project.feishu.cn/fixture/userstory/detail/456"
			}
			remote.mu.Unlock()
			manifest.Services[0].Branch = "feature/updated"
			saveTaskCommentFixture(t, a, path, manifest)
			a.enqueueTaskRequirementComment(path, manifest)
			failed := awaitTaskCommentFixture(t, a, path, manifest.CreatedAt, "conflict", "blocked")
			if failed.Message == "" || len(remote.written()) != 1 {
				t.Fatalf("conflict did not preserve remote comment: status=%+v writes=%+v", failed, remote.written())
			}
			stored, err := a.Manifests.Load(path)
			if err != nil || stored.Services[0].Branch != "feature/updated" {
				t.Fatalf("comment conflict changed task manifest: %+v %v", stored, err)
			}
		})
	}
}

func TestTaskRequirementCommentSyncRejectsAccountChangeBeforeFirstWrite(t *testing.T) {
	a, config, remote := taskCommentFixture(t)
	manifest, path := savedTaskCommentFixture(t, a, config, "account-switch-task")
	original := remote.CachedTaskRequirementCommentAccount()
	a.enqueueTaskRequirementComment(path, manifest)
	remote.mu.Lock()
	remote.account = "another-account"
	remote.mu.Unlock()
	a.StartTaskRequirementCommentSync()
	status := awaitTaskCommentFixture(t, a, path, manifest.CreatedAt, "conflict")
	if status.Message == "" || len(remote.written()) != 0 {
		t.Fatalf("first comment escaped the original account: status=%+v writes=%+v", status, remote.written())
	}
	remote.mu.Lock()
	remote.account = original
	remote.mu.Unlock()
	if err := a.RetryTaskRequirementCommentSync(path, manifest.CreatedAt); err != nil {
		t.Fatal(err)
	}
	confirmed := awaitTaskCommentFixture(t, a, path, manifest.CreatedAt, "synced")
	writes := remote.written()
	if len(writes) != 1 || writes[0].id != confirmed.CommentID || writes[0].scope.Account != original {
		t.Fatalf("retry did not use the original account: status=%+v writes=%+v", confirmed, writes)
	}
}

func TestTaskRequirementCommentSyncResumesBlockedSameRequirementAfterRetry(t *testing.T) {
	a, config, remote := taskCommentFixture(t)
	first, firstPath := savedTaskCommentFixture(t, a, config, "first-task")
	second, secondPath := savedTaskCommentFixture(t, a, config, "second-task")
	remote.readbackHidden = true
	remote.afterWrite = func(_ context.Context, write taskCommentFixtureWrite) error {
		if write.id == "1000" {
			return context.DeadlineExceeded
		}
		return nil
	}
	a.StartTaskRequirementCommentSync()
	a.enqueueTaskRequirementComment(firstPath, first)
	awaitTaskCommentFixture(t, a, firstPath, first.CreatedAt, "uncertain")
	a.enqueueTaskRequirementComment(secondPath, second)
	awaitTaskCommentFixture(t, a, secondPath, second.CreatedAt, "blocked")
	if got := len(remote.written()); got != 1 {
		t.Fatalf("another task created an ambiguous comment before reconciliation: %d", got)
	}
	deadline := time.After(8 * time.Second)
	for {
		remote.mu.Lock()
		checked := remote.checks != 0
		remote.mu.Unlock()
		if checked {
			break
		}
		select {
		case <-a.TaskRequirementCommentSyncChanges():
		case <-deadline:
			t.Fatal("blocked sibling did not trigger the pending create's readback")
		}
	}
	awaitTaskCommentFixture(t, a, firstPath, first.CreatedAt, "uncertain")
	remote.mu.Lock()
	remote.readbackHidden = false
	remote.mu.Unlock()
	if err := a.RetryTaskRequirementCommentSync(firstPath, first.CreatedAt); err != nil {
		t.Fatal(err)
	}
	confirmed := awaitTaskCommentFixture(t, a, firstPath, first.CreatedAt, "synced")
	other := awaitTaskCommentFixture(t, a, secondPath, second.CreatedAt, "synced")
	writes := remote.written()
	if len(writes) != 2 || confirmed.CommentID != writes[0].id || other.CommentID != writes[1].id || writes[1].input.CommentID != "" {
		t.Fatalf("matching comment content crossed task ownership: %+v %+v writes=%+v", confirmed, other, writes)
	}
}

func TestTaskRequirementCommentSyncReconcilesRemovedTaskBeforeSiblingCreate(t *testing.T) {
	a, config, remote := taskCommentFixture(t)
	first, firstPath := savedTaskCommentFixture(t, a, config, "removed-task")
	second, secondPath := savedTaskCommentFixture(t, a, config, "remaining-task")
	remote.readbackHidden = true
	remote.afterWrite = func(_ context.Context, write taskCommentFixtureWrite) error {
		if write.id == "1000" {
			return context.DeadlineExceeded
		}
		return nil
	}
	a.StartTaskRequirementCommentSync()
	a.enqueueTaskRequirementComment(firstPath, first)
	awaitTaskCommentFixture(t, a, firstPath, first.CreatedAt, "uncertain")
	if err := a.RetryTaskRequirementCommentSync(firstPath, first.CreatedAt); err != nil {
		t.Fatal(err)
	}
	awaitTaskCommentFixture(t, a, firstPath, first.CreatedAt, "uncertain")
	if err := os.Rename(firstPath, filepath.Join(a.Paths.Home, "removed-task-fixture")); err != nil {
		t.Fatal(err)
	}
	remote.mu.Lock()
	remote.readbackHidden = false
	checksBefore := remote.checks
	remote.mu.Unlock()
	a.enqueueTaskRequirementComment(secondPath, second)
	secondStatus := awaitTaskCommentFixture(t, a, secondPath, second.CreatedAt, "synced")
	writes := remote.written()
	remote.mu.Lock()
	checksAfter := remote.checks
	remote.mu.Unlock()
	if checksAfter <= checksBefore || len(writes) != 2 || writes[1].id != secondStatus.CommentID || writes[1].input.CommentID != "" {
		t.Fatalf("removed task's readback did not unblock independent create: checks %d→%d, status=%+v writes=%+v", checksBefore, checksAfter, secondStatus, writes)
	}
}

func TestTaskRequirementCommentSyncRunsAfterCreateAndLateLink(t *testing.T) {
	a, config, remote := taskCommentFixture(t)
	base := filepath.Join(a.Paths.Home, "materials")
	role := "owned"
	config.RequirementMaterialsRoot, config.RequirementMaterialsSubdirectory = &base, &role
	annualPath := filepath.Join(base, "requirements", fmt.Sprint(time.Now().Year()), "123-original")
	if err := os.MkdirAll(filepath.Join(annualPath, role), 0700); err != nil {
		t.Fatal(err)
	}
	if err := a.Config.Save(config); err != nil {
		t.Fatal(err)
	}
	releaseCreateResponse := make(chan struct{})
	remote.afterWrite = func(ctx context.Context, write taskCommentFixtureWrite) error {
		if write.id == "1000" {
			select {
			case <-releaseCreateResponse:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return errors.New("fixture comment timeout")
	}
	a.StartTaskRequirementCommentSync()
	create := func(name, link string) (domain.TaskManifest, string) {
		t.Helper()
		tools := []string{}
		type creationResult struct {
			manifest domain.TaskManifest
			err      error
		}
		created := make(chan creationResult, 1)
		go func() {
			result, err := a.CreateTask(t.Context(), TaskCreateRequest{
				CreateTaskRequest: workspace.CreateTaskRequest{FolderName: name, RequirementLink: link}, WorkspaceToolIDs: &tools,
			}, nil)
			created <- creationResult{manifest: result.Data, err: err}
		}()
		select {
		case result := <-created:
			if result.err != nil || result.manifest.TaskDirectoryName != name {
				t.Fatalf("creation failed before comment sync: %+v", result)
			}
			return result.manifest, filepath.Join(*config.TaskRoot, name)
		case <-time.After(8 * time.Second):
			t.Fatal("task creation waited for the mock comment response")
			return domain.TaskManifest{}, ""
		}
	}
	linked, path := create("original", taskCommentFixtureURL)
	close(releaseCreateResponse)
	if got := awaitTaskCommentFixture(t, a, path, linked.CreatedAt, "uncertain"); got.Message == "" {
		t.Fatalf("create's comment failure was not exposed: %+v", got)
	}
	if got, want := remote.written()[0].input.Body, taskCommentFixtureSection("本地任务资料目录", annualPath)+"\n\n-来自Silverwing"; got != want {
		t.Fatalf("create comment did not use the task's visible materials directory: %q, want %q", got, want)
	}
	if err := a.RetryTaskRequirementCommentSync(path, linked.CreatedAt); err != nil {
		t.Fatal(err)
	}
	awaitTaskCommentFixture(t, a, path, linked.CreatedAt, "synced")
	unlinked, unlinkedPath := create("linked-later", "")
	if status := a.TaskRequirementCommentSyncStatus(unlinkedPath, unlinked.CreatedAt); status.State != "" {
		t.Fatalf("unlinked task was enqueued: %+v", status)
	}
	result, err := a.LinkTaskRequirement(t.Context(), TaskRequirementLinkRequest{Target: NewTaskTarget(unlinked.TaskDirectoryName, unlinkedPath), RequirementLink: taskCommentFixtureURL})
	if err != nil || result.Data.RequirementLink != taskCommentFixtureURL {
		t.Fatalf("link did not succeed: %+v %v", result, err)
	}
	awaitTaskCommentFixture(t, a, unlinkedPath, unlinked.CreatedAt, "uncertain")
	writes := remote.written()
	if len(writes) != 2 || writes[0].id == writes[1].id || writes[1].input.CommentID != "" || writes[1].input.Body != taskCommentFixtureSection("本地任务资料目录", documents.LocalTaskMaterials(unlinkedPath).Root)+"\n\n-来自Silverwing" {
		t.Fatalf("late association did not create its own path-only comment: %+v", writes)
	}
}
