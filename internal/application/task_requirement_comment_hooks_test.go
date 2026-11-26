package application

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

func TestTaskRequirementCommentSyncRunsAfterWorkspaceAppend(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip(err)
	}
	t.Setenv("GIT_ALLOW_PROTOCOL", "file")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	a, config, remote := taskCommentFixture(t)
	base := a.Paths.Home
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(base, "missing-git-config"))
	bare := filepath.Join(base, "seed.git")
	source := filepath.Join(base, "source")
	annualLocalGit(t, base, "init", "--bare", "--initial-branch=main", bare)
	annualLocalGit(t, base, "init", "--initial-branch=main", source)
	annualLocalGit(t, source, "commit", "--allow-empty", "-m", "seed")
	annualLocalGit(t, source, "remote", "add", "origin", bare)
	annualLocalGit(t, source, "push", "origin", "main")
	legacy := filepath.Join(base, "legacy-repository")
	annualLocalGit(t, base, "clone", bare, legacy)
	for _, repo := range []struct{ name, path string }{{"api", source}, {"legacy", legacy}} {
		config.Repositories = append(config.Repositories, domain.RepositoryConfig{
			ID: repo.name, Name: repo.name, RootPath: repo.path, GitCommonDirectory: filepath.Join(repo.path, ".git"),
		})
		config.Services = append(config.Services, domain.ServiceConfig{
			ID: repo.name, RepositoryID: repo.name, DisplayName: repo.name, Enabled: true,
			MasterBranch: "origin/main", TestTagBaselineRef: "origin/main", DevelopmentTool: domain.DevelopmentToolVisualStudioCode,
			Modules: []domain.ServiceModuleConfig{{ID: "default", Name: "default", Strategy: domain.WorkspaceStandardWorktree, TagMode: domain.TagBuildCurrentBranch}},
		})
	}
	if err := a.Config.Save(config); err != nil {
		t.Fatal(err)
	}
	manifest := domain.DefaultTaskManifest()
	manifest.FolderName, manifest.TaskDirectoryName = "append-fixture", "append-fixture"
	manifest.CreatedAt, manifest.UpdatedAt = "2026-11-20T08:10:26Z", "2026-11-20T08:10:26Z"
	manifest.RequirementLink = taskCommentFixtureURL
	manifest.FeatureBranch = "feature/base"
	taskPath := filepath.Join(*config.TaskRoot, manifest.TaskDirectoryName)
	saveTaskCommentFixture(t, a, taskPath, manifest)
	firstAppendReturned := make(chan struct{})
	remote.afterWrite = func(ctx context.Context, write taskCommentFixtureWrite) error {
		saved, err := a.Manifests.Load(taskPath)
		if err != nil {
			return err
		}
		if branches := workspace.FormatTaskBranchInfo(saved, false); branches == "" || !strings.Contains(write.input.Body, branches) {
			return fmt.Errorf("comment did not reflect the persisted workspaces: %q vs %q", write.input.Body, branches)
		}
		if write.input.CommentID == "" {
			select {
			case <-firstAppendReturned:
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
				return errors.New("workspace append waited for the comment reply")
			}
		}
		if strings.Contains(write.input.Body, "legacy：feature/legacy") {
			return context.DeadlineExceeded
		}
		return nil
	}
	a.StartTaskRequirementCommentSync()
	ctx, cancel := context.WithTimeout(t.Context(), 80*time.Second)
	defer cancel()
	appendAction := func(kind, service, module, branch string) Action {
		source := domain.TaskModuleConfigured
		if module != "default" {
			source = domain.TaskModuleTemporary
		}
		return Action{Kind: kind, TaskID: manifest.TaskDirectoryName, ServiceID: service, Payload: map[string]any{
			"modules": []workspace.TaskModuleSelection{{ID: module, Name: module, Strategy: domain.WorkspaceStandardWorktree,
				BaseRef: "origin/main", BaseRemote: "origin", TargetBranch: branch, Source: source, TagMode: domain.TagBuildCurrentBranch}},
		}}
	}
	if _, err := a.Dispatch(ctx, appendAction("task.services-add-preview", "api", "default", "feature/first")); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Dispatch(ctx, appendAction("task.services-add", "unconfigured", "default", "feature/missing")); err == nil {
		t.Fatal("adding an unknown service unexpectedly succeeded")
	}
	if status := a.TaskRequirementCommentSyncStatus(taskPath, manifest.CreatedAt); status.State != "" {
		t.Fatalf("preview or failed append enqueued a comment: %+v", status)
	}
	var id string
	wantLines := []string{}
	for index, operation := range []struct{ kind, service, module, branch, commentState string }{
		{"task.services-add", "api", "default", "feature/first", "synced"},
		{"task.modules-add", "api", "follow-up", "feature/module", "synced"},
		{"workspace.add", "legacy", "default", "feature/legacy", "uncertain"},
	} {
		_, err := a.Dispatch(ctx, appendAction(operation.kind, operation.service, operation.module, operation.branch))
		if index == 0 {
			close(firstAppendReturned)
		}
		if err != nil {
			t.Fatalf("%s: %v", operation.kind, err)
		}
		status := awaitTaskCommentFixture(t, a, taskPath, manifest.CreatedAt, "synced", "uncertain", "conflict", "failed")
		if status.State != operation.commentState || status.CommentID == "" || (status.State == "uncertain" && status.Message == "") {
			t.Fatalf("%s did not synchronize its saved workspace: %+v", operation.kind, status)
		}
		wantLines = append(wantLines, operation.service+"："+operation.branch)
		writes := remote.written()
		if len(writes) != index+1 || writes[index].id != status.CommentID || writes[index].input.CommentID != id ||
			!strings.Contains(writes[index].input.Body, strings.Join(wantLines, "\n")) || strings.Contains(writes[index].input.Body, "本地任务资料目录") {
			t.Fatalf("%s synchronized an unexpected comment: %+v", operation.kind, writes)
		}
		id = status.CommentID
	}
}

func TestTaskRequirementCommentSyncRunsAfterMaterialsRetry(t *testing.T) {
	a, config, remote := taskCommentFixture(t)
	manifest, path := savedTaskCommentFixture(t, a, config, "retry-materials")
	base, role := filepath.Join(a.Paths.Home, "materials"), "owned"
	created, err := domain.ParseSilverWingTime(manifest.CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "requirements", fmt.Sprint(created.In(time.Local).Year()), "123-retry-materials")
	if err := os.MkdirAll(filepath.Join(root, role), 0700); err != nil {
		t.Fatal(err)
	}
	config.RequirementMaterialsRoot, config.RequirementMaterialsSubdirectory = &base, &role
	if err := a.Config.Save(config); err != nil {
		t.Fatal(err)
	}
	a.StartTaskRequirementCommentSync()
	a.enqueueTaskRequirementComment(path, manifest)
	first := awaitTaskCommentFixture(t, a, path, manifest.CreatedAt, "synced")
	if first.CommentID == "" {
		t.Fatal("initial branch-only comment was not created")
	}
	result, err := a.RetryTaskMaterials(t.Context(), NewTaskTarget(manifest.TaskDirectoryName, path))
	if err != nil || result.Data.RequirementMaterials.Status != domain.MaterialsReady {
		t.Fatalf("materials retry failed: %+v, %v", result, err)
	}
	updated := awaitTaskCommentFixture(t, a, path, manifest.CreatedAt, "synced")
	writes := remote.written()
	if updated.CommentID != first.CommentID || len(writes) != 2 || writes[1].input.CommentID != first.CommentID ||
		!strings.Contains(writes[1].input.Body, "**本地任务资料目录**") || !strings.Contains(writes[1].input.Body, root) ||
		!strings.Contains(writes[1].input.Body, "api：feature/first") {
		t.Fatalf("materials retry did not update the existing comment with its visible path: %+v", writes)
	}
}
