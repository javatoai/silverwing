package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
)

func TestWorktreeRollbackReportsBranchDeletionFailure(t *testing.T) {
	repo := newCompareTestRepo(t)
	writeCompareFile(t, repo.path, "base.txt", "base")
	commitCompare(t, repo.path, "base")
	task := t.TempDir()
	target := filepath.Join(task, "module")
	gitCompare(t, repo.path, "worktree", "add", "--detach", target, "HEAD")

	git := NewGitClient(nil, "git")
	common, err := git.CommonDirectory(repo.path)
	if err != nil {
		t.Fatal(err)
	}
	provisioner := &StandardWorktreeProvisioner{Git: git, Locks: NewOperationLocks(repo.locks)}
	request := WorkspaceProvisionRequest{
		TaskDirectory: task,
		Repository:    domain.RepositoryConfig{RootPath: repo.path, GitCommonDirectory: common},
	}
	// The checked-out branch cannot be deleted even after the detached worktree is removed.
	worktree := domain.ServiceWorkspace{WorktreePath: target, Branch: "main", BranchCreatedByTask: true, GroupServiceID: "service", ModuleID: "module"}
	var events []TaskCreationProgress
	err = provisioner.RollbackWithProgress(context.Background(), request, []domain.ServiceWorkspace{worktree}, func(event TaskCreationProgress) {
		events = append(events, event)
	})
	var cleanup *workspaceProvisionCleanupError
	if !errors.As(err, &cleanup) || cleanup.Path != target || !strings.Contains(cleanup.Error(), "删除任务分支 main") {
		t.Fatalf("branch deletion failure was not reported with its workspace path: %v", err)
	}
	if len(events) != 2 || events[1].Status != TaskCreationProgressFailed || events[1].ModuleID != "module" || !strings.Contains(events[1].Message, target) {
		t.Fatalf("branch deletion failure was not reported for the module: %+v", events)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("failed branch deletion left the worktree checkout: %v", err)
	}
	if got := gitCompareOutput(t, repo.path, "branch", "--show-current"); got != "main" {
		t.Fatalf("the original branch was changed: %s", got)
	}
}

func newWorktreeFailureFixture(t *testing.T) (*GitClient, *OperationLocks, WorkspaceProvisionRequest) {
	t.Helper()
	repo := newCompareTestRepo(t)
	writeCompareFile(t, repo.path, "base.txt", "base")
	commitCompare(t, repo.path, "base")
	remote := t.TempDir()
	gitCompare(t, remote, "init", "--bare", "-b", "main")
	gitCompare(t, repo.path, "remote", "add", "origin", remote)
	gitCompare(t, repo.path, "push", "origin", "main")
	git := NewGitClient(nil, "git")
	common, err := git.CommonDirectory(repo.path)
	if err != nil {
		t.Fatal(err)
	}
	task := t.TempDir()
	owner, err := os.Lstat(task)
	if err != nil {
		t.Fatal(err)
	}
	return git, NewOperationLocks(repo.locks), WorkspaceProvisionRequest{
		TaskDirectory: task,
		Repository:    domain.RepositoryConfig{ID: "repository", RootPath: repo.path, GitCommonDirectory: common},
		Service: domain.GroupServiceConfig{
			ID: "service", DisplayName: "service", MasterBranch: "origin/main",
			Modules: []domain.ServiceModuleConfig{{ID: "module", Name: "module", Strategy: domain.WorkspaceStandardWorktree}},
		},
		RequestedFeatureBranch: "feature/provisioning",
		taskDirectoryOwner:     owner,
		deferWorkspaceRollback: true,
	}
}

func TestNewTaskRetainsRegisteredWorktreeForRollbackWhenTrackingSetupFails(t *testing.T) {
	git, locks, request := newWorktreeFailureFixture(t)
	installTestPostCheckoutHook(t, request.Repository.RootPath, "common=$(git rev-parse --path-format=absolute --git-common-dir) || exit 1\n: > \"$common/config.lock\"\n")
	provisioner := NewWorkspaceProvisioningService(git, NewBootstrapService(git.Runner, git), locks)
	workspaces, err := provisioner.Provision(context.Background(), request, nil)
	if err == nil || !strings.Contains(err.Error(), "branch."+request.RequestedFeatureBranch+".remote") {
		t.Fatalf("expected a tracking config error after worktree registration: %v", err)
	}
	if len(workspaces) != 1 || !workspaces[0].BranchCreatedByTask {
		t.Fatalf("registered worktree was not returned for batch rollback: %+v", workspaces)
	}
	target := filepath.Join(request.TaskDirectory, "service-module")
	if _, err := os.Stat(filepath.Join(target, "base.txt")); err != nil {
		t.Fatalf("worktree was removed before batch rollback: %v", err)
	}
	if err := os.Remove(filepath.Join(request.Repository.GitCommonDirectory, "config.lock")); err != nil {
		t.Fatal(err)
	}
	if err := provisioner.Rollback(context.Background(), request, workspaces); err != nil {
		t.Fatalf("batch could not roll back the registered worktree: %v", err)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("batch rollback did not remove the worktree: %v", err)
	}
	if got := gitCompareOutput(t, request.Repository.RootPath, "branch", "--list", request.RequestedFeatureBranch); got != "" {
		t.Fatalf("batch rollback left the task branch: %s", got)
	}
}

func TestNewTaskRetainsRegisteredWorktreeForRollbackWhenCheckoutHookFails(t *testing.T) {
	git, locks, request := newWorktreeFailureFixture(t)
	installTestPostCheckoutHook(t, request.Repository.RootPath, "exit 1\n")
	ctx := context.Background()
	provisioner := NewWorkspaceProvisioningService(git, NewBootstrapService(git.Runner, git), locks)
	workspaces, err := provisioner.Provision(ctx, request, nil)
	if err == nil || !strings.Contains(err.Error(), "worktree add") {
		t.Fatalf("expected the checkout hook to fail during worktree add: %v", err)
	}
	if len(workspaces) != 1 || !workspaces[0].BranchCreatedByTask {
		t.Fatalf("partially registered worktree was not returned for batch rollback: %+v", workspaces)
	}
	if err := provisioner.Rollback(ctx, request, workspaces); err != nil {
		t.Fatalf("batch could not roll back the partial worktree: %v", err)
	}
	if _, err := os.Lstat(workspaces[0].WorktreePath); !os.IsNotExist(err) {
		t.Fatalf("batch rollback did not remove the partial worktree: %v", err)
	}
	if got := gitCompareOutput(t, request.Repository.RootPath, "branch", "--list", request.RequestedFeatureBranch); got != "" {
		t.Fatalf("batch rollback left the task branch: %s", got)
	}
}

func installTestPostCheckoutHook(t *testing.T, repo, body string) {
	t.Helper()
	hooks := t.TempDir()
	gitCompare(t, repo, "config", "core.hooksPath", hooks)
	if err := os.WriteFile(filepath.Join(hooks, "post-checkout"), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestNewTaskRejectsDisappearedConfirmedWorktreeBranch(t *testing.T) {
	git, locks, request := newWorktreeFailureFixture(t)
	ctx := context.Background()
	gitCompare(t, request.Repository.RootPath, "branch", request.RequestedFeatureBranch)
	inspector := NewWorkspaceBranchReuseInspector(git, locks)
	conflicts, err := inspector.Inspect(ctx, request.Repository, request.Service, request.RequestedFeatureBranch, nil)
	if err != nil || len(conflicts) != 1 {
		t.Fatalf("preflight did not find the original branch: %+v, %v", conflicts, err)
	}
	request.ConfirmedBranchReuseKeys = []BranchReuseKey{conflicts[0].Key}
	gitCompare(t, request.Repository.RootPath, "branch", "-D", request.RequestedFeatureBranch)

	provisioner := NewWorkspaceProvisioningService(git, NewBootstrapService(git.Runner, git), locks)
	workspaces, err := provisioner.Provision(ctx, request, nil)
	if len(workspaces) != 0 || err == nil || !strings.Contains(err.Error(), "分支复用状态已变化") {
		t.Fatalf("creation did not reject a vanished confirmation: %+v, %v", workspaces, err)
	}
	if got := gitCompareOutput(t, request.Repository.RootPath, "branch", "--list", request.RequestedFeatureBranch); got != "" {
		t.Fatalf("creation recreated the branch from an unconfirmed base: %s", got)
	}
}

func TestFailedCloneStagingDoesNotDeleteExistingTarget(t *testing.T) {
	task := t.TempDir()
	target := filepath.Join(task, "module")
	want := errors.New("clone failed before publishing")
	owner := "shared-clone-owner"
	err := cloneIntoPlace(task, target, owner, func(staging string) error {
		writeCompareFile(t, staging, "partially-cloned.txt", "partial")
		writeCompareFile(t, target, ".git/silverwing-owner", owner)
		writeCompareFile(t, target, "existing.txt", "preserve")
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("clone returned %v, want %v", err, want)
	}
	data, err := os.ReadFile(filepath.Join(target, "existing.txt"))
	if err != nil || string(data) != "preserve" {
		t.Fatalf("clone failure did not retain the existing target: %q, %v", data, err)
	}
	entries, err := os.ReadDir(task)
	if err != nil || len(entries) != 1 || entries[0].Name() != "module" {
		t.Fatalf("clone failure left an unneeded staging directory: %v, %v", entries, err)
	}
}
