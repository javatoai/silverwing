package workspace

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

func TestWorkspaceMainBranchMergeBatchMergesWorktreeAndIndependentCloneWithoutPushing(t *testing.T) {
	fixture := newMergeBatchGitFixture(t)
	worktree, err := fixture.addWorktree("service-worktree", "feature/worktree")
	if err != nil {
		t.Fatal(err)
	}
	clone, err := fixture.addIndependentClone("service-clone", "feature/clone", "upstream/main")
	if err != nil {
		t.Fatal(err)
	}
	for _, branch := range []string{"feature/worktree", "feature/clone"} {
		fixture.pushRemoteBranch(fixture.baseCommit, branch)
	}
	sourceCommit := fixture.advanceMain("source.txt", "main source update\n")
	fixture.saveManifest(worktree, clone)

	result := fixture.service.MergeBatch(context.Background(), fixture.config, fixture.taskPath, fixture.manifest, []domain.ServiceWorkspace{worktree, clone})
	if len(result.Items) != 2 {
		t.Fatalf("MergeBatch returned %d items, want 2: %#v", len(result.Items), result.Items)
	}
	if result.Items[0].Outcome != MainBranchFastForwarded || result.Items[0].SourceRef != "origin/main" {
		t.Fatalf("worktree result did not use its configured remote-tracking main: %#v", result.Items[0])
	}
	if result.Items[1].Outcome != MainBranchFastForwarded || result.Items[1].SourceRef != "upstream/main" {
		t.Fatalf("independent-clone result did not preserve configured display ref: %#v", result.Items[1])
	}
	for _, item := range []domain.ServiceWorkspace{worktree, clone} {
		if head := fixture.resolve(item.WorktreePath, "HEAD"); head != sourceCommit {
			t.Errorf("%s HEAD = %s, want fetched main commit %s", item.WorktreePath, head, sourceCommit)
		}
		if tracked := fixture.resolve(item.WorktreePath, "origin/main"); tracked != sourceCommit {
			t.Errorf("%s origin/main = %s, want fetched commit %s", item.WorktreePath, tracked, sourceCommit)
		}
		remoteFeature := fixture.resolve(fixture.remote, "refs/heads/"+item.Branch)
		if remoteFeature != fixture.baseCommit {
			t.Errorf("remote feature branch %s changed to %s; batch merge must not push", item.Branch, remoteFeature)
		}
	}
}

func TestWorkspaceMainBranchMergeBatchContinuesAfterConflictAndDoesNotPush(t *testing.T) {
	fixture := newMergeBatchGitFixture(t)
	conflict, err := fixture.addWorktree("service-conflict", "feature/conflict")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(conflict.WorktreePath, "shared.txt"), []byte("feature side\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture.commit(conflict.WorktreePath, "feature side edit")
	success, err := fixture.addWorktree("service-success", "feature/success")
	if err != nil {
		t.Fatal(err)
	}
	fixture.pushRemoteBranch(fixture.baseCommit, conflict.Branch)
	fixture.pushRemoteBranch(fixture.baseCommit, success.Branch)
	sourceCommit := fixture.advanceMain("shared.txt", "main side\n")
	fixture.saveManifest(conflict, success)

	result := fixture.service.MergeBatch(context.Background(), fixture.config, fixture.taskPath, fixture.manifest, []domain.ServiceWorkspace{conflict, success})
	if len(result.Items) != 2 {
		t.Fatalf("MergeBatch returned %d items, want 2: %#v", len(result.Items), result.Items)
	}
	conflictResult, successResult := result.Items[0], result.Items[1]
	if conflictResult.Outcome != MainBranchConflict || !reflect.DeepEqual(conflictResult.ConflictFiles, []string{"shared.txt"}) {
		t.Fatalf("first module did not retain its merge conflict: %#v", conflictResult)
	}
	if want := "我将origin/main合并到" + conflict.Branch + "时出现冲突，请你帮忙解决。"; conflictResult.Message != want {
		t.Fatalf("conflict message = %q, want %q", conflictResult.Message, want)
	}
	if successResult.Outcome != MainBranchFastForwarded {
		t.Fatalf("the item after a conflict did not continue successfully: %#v", successResult)
	}
	if head := fixture.resolve(success.WorktreePath, "HEAD"); head != sourceCommit {
		t.Fatalf("next worktree HEAD = %s, want fetched main commit %s", head, sourceCommit)
	}
	for _, branch := range []string{conflict.Branch, success.Branch} {
		if remoteFeature := fixture.resolve(fixture.remote, "refs/heads/"+branch); remoteFeature != fixture.baseCommit {
			t.Errorf("remote feature branch %s changed to %s; batch merge must not push", branch, remoteFeature)
		}
	}
}

type mergeBatchGitFixture struct {
	root, taskRoot, taskPath, remote, seed, repository string
	baseCommit                                         string
	t                                                  *testing.T
	git                                                *GitClient
	config                                             domain.AppConfig
	manifest                                           domain.TaskManifest
	manifests                                          *storage.ManifestStore
	service                                            *WorkspaceMainBranchMergeService
}

func newMergeBatchGitFixture(t *testing.T) *mergeBatchGitFixture {
	t.Helper()
	root := t.TempDir()
	f := &mergeBatchGitFixture{
		root: root, taskRoot: filepath.Join(root, "tasks"), remote: filepath.Join(root, "remote.git"),
		seed: filepath.Join(root, "seed"), repository: filepath.Join(root, "repository"), t: t,
		manifests: storage.NewManifestStore(),
	}
	f.taskPath = filepath.Join(f.taskRoot, "batch-task")
	if err := os.MkdirAll(f.taskPath, 0o700); err != nil {
		t.Fatal(err)
	}
	globalConfig := filepath.Join(root, "empty-gitconfig")
	if err := os.WriteFile(globalConfig, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	f.git = NewGitClient(nil, "git")
	f.git.Environment = map[string]string{
		"GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": globalConfig, "GIT_TERMINAL_PROMPT": "0",
	}
	f.git.RemoveEnvironment = []string{
		"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE", "GIT_NAMESPACE",
		"GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_CONFIG_COUNT",
		"GIT_CONFIG_PARAMETERS", "GIT_CONFIG", "GIT_TEMPLATE_DIR", "GIT_EXEC_PATH",
		"GIT_SSH_COMMAND", "GIT_SSH",
	}
	f.run(root, "init", "--bare", "--initial-branch=main", f.remote)
	f.run(root, "init", "--initial-branch=main", f.seed)
	f.setCommitIdentity(f.seed)
	if err := os.WriteFile(filepath.Join(f.seed, "shared.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.commit(f.seed, "base")
	f.baseCommit = f.resolve(f.seed, "HEAD")
	f.run(f.seed, "remote", "add", "origin", f.remote)
	f.run(f.seed, "push", "origin", "main:refs/heads/main")
	f.run(root, "clone", "--no-local", "--branch=main", f.remote, f.repository)
	f.setCommitIdentity(f.repository)

	common, err := f.git.CommonDirectory(f.repository)
	if err != nil {
		t.Fatal(err)
	}
	config := domain.DefaultAppConfig()
	config.TaskRoot = &f.taskRoot
	config.Repositories = []domain.RepositoryConfig{{ID: "repo", Name: "fixture", RootPath: f.repository, GitCommonDirectory: common, OriginURL: mergeBatchPointer(f.remote)}}
	f.config = config
	f.manifest = domain.DefaultTaskManifest()
	f.manifest.FolderName = "batch-task"
	f.manifest.TaskDirectoryName = "batch-task"
	f.manifest.FeatureBranch = "feature/batch"
	f.manifest.GroupID = "batch-group"
	f.manifest.CreatedAt = "2026-10-16 11:00:00"
	f.manifest.UpdatedAt = f.manifest.CreatedAt
	locks := NewOperationLocks(filepath.Join(root, "locks"))
	lifecycle := NewWorkspaceLifecycle(f.manifests, f.git, locks)
	f.service = NewWorkspaceMainBranchMergeService(f.git, f.manifests, locks, lifecycle)
	return f
}

func (f *mergeBatchGitFixture) addWorktree(moduleID, branch string) (domain.ServiceWorkspace, error) {
	path := filepath.Join(f.taskPath, moduleID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return domain.ServiceWorkspace{}, err
	}
	if _, err := f.git.Run(f.repository, "worktree", "add", "--no-track", "-b", branch, path, f.baseCommit); err != nil {
		return domain.ServiceWorkspace{}, err
	}
	f.setCommitIdentity(path)
	return domain.ServiceWorkspace{
		RepositoryID: "repo", ServiceName: "fixture-service", RepositoryPath: f.repository, WorktreePath: path,
		DevelopmentTool: domain.DevelopmentToolVisualStudioCode, Branch: branch, Health: domain.WorkspaceReady,
		Warnings: []string{}, GroupServiceID: "service-" + moduleID, ModuleID: moduleID, ModuleName: moduleID,
		Strategy: domain.WorkspaceStandardWorktree, ModuleSource: domain.TaskModuleConfigured,
		OriginURL: mergeBatchPointer(f.remote), BaseRef: mergeBatchPointer("origin/main"), TargetBranch: mergeBatchPointer(branch),
		PushRemote: "origin",
	}, nil
}

func (f *mergeBatchGitFixture) addIndependentClone(moduleID, branch, baseRef string) (domain.ServiceWorkspace, error) {
	path := filepath.Join(f.taskPath, moduleID)
	if _, err := f.git.Run(f.root, "clone", "--no-local", f.remote, path); err != nil {
		return domain.ServiceWorkspace{}, err
	}
	f.setCommitIdentity(path)
	if _, err := f.git.Run(path, "checkout", "-b", branch, f.baseCommit); err != nil {
		return domain.ServiceWorkspace{}, err
	}
	if err := os.MkdirAll(filepath.Dir(cloneMarker(path)), 0o700); err != nil {
		return domain.ServiceWorkspace{}, err
	}
	owner := cloneOwnership(f.taskPath, "repo", "service-"+moduleID, moduleID)
	if err := os.WriteFile(cloneMarker(path), []byte(owner), 0o600); err != nil {
		return domain.ServiceWorkspace{}, err
	}
	return domain.ServiceWorkspace{
		RepositoryID: "repo", ServiceName: "fixture-service", RepositoryPath: path, WorktreePath: path,
		DevelopmentTool: domain.DevelopmentToolVisualStudioCode, Branch: branch, Health: domain.WorkspaceReady,
		Warnings: []string{}, GroupServiceID: "service-" + moduleID, ModuleID: moduleID, ModuleName: moduleID,
		Strategy: domain.WorkspaceIndependentClone, ModuleSource: domain.TaskModuleTemporary,
		OriginURL: mergeBatchPointer(f.remote), BaseRef: mergeBatchPointer(baseRef), TargetBranch: mergeBatchPointer(branch),
		PushRemote: "origin",
	}, nil
}

func (f *mergeBatchGitFixture) saveManifest(workspaces ...domain.ServiceWorkspace) {
	f.manifest.Services = append([]domain.ServiceWorkspace(nil), workspaces...)
	if err := f.manifests.Save(f.taskPath, f.manifest); err != nil {
		f.t.Fatal(err)
	}
}

func (f *mergeBatchGitFixture) advanceMain(path, content string) string {
	if err := os.WriteFile(filepath.Join(f.seed, path), []byte(content), 0o600); err != nil {
		f.t.Fatal(err)
	}
	f.commit(f.seed, "advance main")
	commit := f.resolve(f.seed, "HEAD")
	f.run(f.seed, "push", "origin", "main:refs/heads/main")
	return commit
}

func (f *mergeBatchGitFixture) pushRemoteBranch(commit, branch string) {
	f.run(f.seed, "push", "origin", commit+":refs/heads/"+branch)
}

func (f *mergeBatchGitFixture) setCommitIdentity(repository string) {
	f.run(repository, "config", "user.name", "Silverwing Batch Merge Test")
	f.run(repository, "config", "user.email", "silverwing-batch-merge@example.invalid")
}

func (f *mergeBatchGitFixture) commit(repository, message string) {
	f.run(repository, "add", "--all")
	f.run(repository, "commit", "-m", message)
}

func (f *mergeBatchGitFixture) resolve(repository, ref string) string {
	f.t.Helper()
	result, err := f.git.Resolve(repository, ref)
	if err != nil {
		f.t.Fatalf("resolve %s in %s: %v", ref, repository, err)
	}
	return result
}

func (f *mergeBatchGitFixture) run(repository string, arguments ...string) {
	f.t.Helper()
	if _, err := f.git.Run(repository, arguments...); err != nil {
		f.t.Fatalf("git %v in %s: %v", arguments, repository, err)
	}
}

func mergeBatchPointer(value string) *string { return &value }
