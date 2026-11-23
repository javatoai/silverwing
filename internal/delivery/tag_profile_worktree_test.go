package delivery

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
	"silverwing-go/internal/workspace"
)

func TestProfileTagMove(t *testing.T) {
	ctx := context.Background()
	gitFixture, repository := newTagRecoveryTestRepository(t)
	git := gitFixture.git.(*workspace.GitClient)
	root := filepath.Dir(repository)
	paths := storage.NewPaths(filepath.Join(root, "data"))
	paths.ProfileID = "tag-profile"
	config := domain.DefaultAppConfig()
	taskRoot := filepath.Join(root, "tasks")
	config.TaskRoot = &taskRoot
	config.Groups = nil
	config.Repositories = []domain.RepositoryConfig{{ID: "repo", Name: "Project", RootPath: repository, GitCommonDirectory: filepath.Join(repository, ".git")}}
	module := domain.ServiceModuleConfig{ID: "default", Name: "default", Strategy: domain.WorkspaceStandardWorktree, TagEnabled: true, TagMode: domain.TagBuildMergeToTargetBranch}
	config.Services = []domain.ServiceConfig{{ID: "service", RepositoryID: "repo", DisplayName: "Project", MasterBranch: "origin/main", TestTagBaselineRef: "origin/uat", Enabled: true, DevelopmentTool: domain.DevelopmentToolIntelliJIdea, Modules: []domain.ServiceModuleConfig{module}}}
	store := storage.NewConfigStore(paths)
	if err := store.Save(config); err != nil {
		t.Fatal(err)
	}
	write := func(dir, name, value string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run := func(dir string, args ...string) string {
		t.Helper()
		return tagRecoveryGit(t, gitFixture, dir, args...)
	}
	remote := filepath.Join(root, "remote.git")
	run(root, "init", "--bare", remote)
	run(repository, "remote", "add", "origin", remote)
	write(repository, "base.txt", "base\n")
	run(repository, "add", "base.txt")
	run(repository, "commit", "-m", "base")
	base := run(repository, "rev-parse", "HEAD")
	run(repository, "tag", "-a", "1.2.3.beta-1", "-m", "seed UAT version")
	write(repository, "uat.txt", "uat\n")
	run(repository, "add", "uat.txt")
	run(repository, "commit", "-m", "UAT baseline")
	run(repository, "push", "origin", "HEAD:refs/heads/uat", "refs/tags/1.2.3.beta-1")

	const folder = "task"
	taskDirectory := filepath.Join(taskRoot, folder)
	worktree := filepath.Join(taskDirectory, "module")
	run(repository, "worktree", "add", "-b", "feature/task", worktree, base)
	write(worktree, "feature.txt", "feature\n")
	run(worktree, "add", "feature.txt")
	run(worktree, "commit", "-m", "feature")

	manifest := domain.DefaultTaskManifest()
	manifest.FolderName, manifest.TaskDirectoryName = folder, folder
	manifest.FeatureBranch, manifest.CreatedAt, manifest.UpdatedAt = "feature/task", "2026-11-20T10:00:00Z", "2026-11-20T10:00:00Z"
	manifest.RequirementMaterials.WriteRoot = stringPointer(filepath.Join(paths.Home, "materials", folder))
	manifest.Services = []domain.ServiceWorkspace{{RepositoryID: "repo", RepositoryPath: repository, ServiceName: "Project", WorktreePath: worktree,
		GroupServiceID: "service", ModuleID: module.ID, ModuleName: module.Name, Strategy: module.Strategy, ModuleSource: domain.TaskModuleConfigured,
		DevelopmentTool: domain.DevelopmentToolIntelliJIdea, Health: domain.WorkspaceReady, Branch: "feature/task", PushRemote: "origin",
		TagEnabled: true, TagMode: module.TagMode, TagTargetRef: stringPointer("origin/uat"), TagMessagePrefix: "test"}}
	manifests := storage.NewProfileManifestStore(paths)
	if err := manifests.Save(taskDirectory, manifest); err != nil {
		t.Fatal(err)
	}
	service := NewService(paths, store, manifests, git, nil)
	task, err := service.OpenTagTask(folder)
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{"service:default"}
	plan, err := task.PrepareBatch(ctx, "", keys, false)
	if err != nil || !plan.Ready || len(plan.Items) != 1 {
		t.Fatalf("preflight failed: %+v, %v", plan, err)
	}
	workingPath := filepath.Join(taskRoot, "silverwing-tag-profile", "tag-conflicts", plan.Items[0].OperationID)
	if plan.Items[0].Worktree != workingPath {
		t.Fatalf("preflight worktree %q, want %q", plan.Items[0].Worktree, workingPath)
	}
	if _, err := os.Stat(filepath.Join(paths.Home, "tag-preflights", plan.ID+".json")); err != nil {
		t.Fatal(err)
	}

	movedPaths := storage.NewPaths(filepath.Join(root, "moved data"))
	movedPaths.ProfileID = paths.ProfileID
	if err := os.Rename(paths.Home, movedPaths.Home); err != nil {
		t.Fatal(err)
	}
	moved := NewService(movedPaths, nil, storage.NewProfileManifestStore(movedPaths), git, nil)
	task, err = moved.OpenTagTask(folder)
	if err != nil {
		t.Fatal(err)
	}
	reports, err := task.ApplyBatch(ctx, plan.ID, keys, false)
	if err != nil || len(reports) != 1 || reports[0].Operation.State != domain.TagOperationSuccess {
		t.Fatalf("apply after move: %+v, %v", reports, err)
	}
	if _, err := os.Stat(workingPath); !os.IsNotExist(err) {
		t.Fatalf("completed isolated worktree remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(taskDirectory, "tag-build-history.jsonl")); err != nil {
		t.Fatal(err)
	}
}
