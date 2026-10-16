package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
	"silverwing-go/internal/storage"
)

type bootstrapInvocationRecorder struct{ calls int }

func (r *bootstrapInvocationRecorder) Run(context.Context, process.Spec) (process.Result, error) {
	r.calls++
	return process.Result{}, nil
}

func TestRerunWorkspaceBootstrapRejectsManifestPathOutsideTask(t *testing.T) {
	root := t.TempDir()
	tasks := filepath.Join(root, "tasks")
	task := filepath.Join(tasks, "task")
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	app := NewTaskApplicationService(nil, nil, storage.NewPaths(filepath.Join(root, "profile")))
	recorder := &bootstrapInvocationRecorder{}
	app.Bootstrap.Runner = recorder
	manifest := domain.DefaultTaskManifest()
	manifest.FolderName, manifest.TaskDirectoryName, manifest.GroupID = "task", "task", "group"
	manifest.Services = []domain.ServiceWorkspace{{GroupServiceID: "service", ModuleID: "module", WorktreePath: outside, RepositoryPath: outside, Strategy: domain.WorkspaceStandardWorktree, Health: domain.WorkspaceReady, DevelopmentTool: domain.DevelopmentToolIntelliJIdea, ModuleSource: domain.TaskModuleConfigured, TagMode: domain.TagBuildMergeToTargetBranch}}
	if err := app.Manifests.Save(task, manifest); err != nil {
		t.Fatal(err)
	}
	config := domain.DefaultAppConfig()
	config.TaskRoot = &tasks
	config.Groups = []domain.GroupConfig{{ID: "group", Name: "group", Services: []domain.GroupServiceConfig{{ID: "service", Bootstrap: domain.BootstrapConfig{Commands: []domain.BootstrapCommand{{Name: "record", Enabled: true, Executable: "record", WorkingDirectory: ".", TimeoutSeconds: 1}}}}}}}
	_, err := app.RerunWorkspaceBootstrap(context.Background(), config, task, outside)
	if err == nil || !strings.Contains(err.Error(), "直接子级") {
		t.Fatalf("expected task containment rejection, got %v", err)
	}
	if recorder.calls != 0 {
		t.Fatalf("bootstrap ran %d commands outside the task", recorder.calls)
	}
}

func TestRerunWorkspaceBootstrapUsesValidatedRepository(t *testing.T) {
	root := t.TempDir()
	tasks := filepath.Join(root, "tasks")
	task := filepath.Join(tasks, "task")
	repository := filepath.Join(root, "repository")
	worktree := filepath.Join(task, "service")
	for _, directory := range []string{repository, task} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	gitCompare(t, repository, "init", "-b", "main")
	gitCompare(t, repository, "config", "user.name", "Bootstrap Test")
	gitCompare(t, repository, "config", "user.email", "bootstrap@example.invalid")
	writeCompareFile(t, repository, "template.txt", "configured source\n")
	commitCompare(t, repository, "base")
	gitCompare(t, repository, "worktree", "add", "-b", "feature", worktree, "HEAD")
	forgedSource := filepath.Join(root, "forged-source")
	writeCompareFile(t, forgedSource, "template.txt", "incorrect source\n")
	app := NewTaskApplicationService(nil, nil, storage.NewPaths(filepath.Join(root, "profile")))
	recorder := &bootstrapInvocationRecorder{}
	app.Bootstrap.Runner = recorder
	manifest := domain.DefaultTaskManifest()
	manifest.FolderName, manifest.TaskDirectoryName, manifest.GroupID = "task", "task", "group"
	manifest.Services = []domain.ServiceWorkspace{{RepositoryID: "repo", GroupServiceID: "service", ModuleID: "module", Branch: "feature", WorktreePath: worktree, RepositoryPath: forgedSource, Strategy: domain.WorkspaceStandardWorktree, Health: domain.WorkspaceReady, DevelopmentTool: domain.DevelopmentToolIntelliJIdea, ModuleSource: domain.TaskModuleConfigured, TagMode: domain.TagBuildMergeToTargetBranch}}
	if err := app.Manifests.Save(task, manifest); err != nil {
		t.Fatal(err)
	}
	config := domain.DefaultAppConfig()
	config.TaskRoot = &tasks
	config.Repositories = []domain.RepositoryConfig{{ID: "repo", RootPath: repository, GitCommonDirectory: filepath.Join(repository, ".git")}}
	config.Groups = []domain.GroupConfig{{ID: "group", Name: "group", Services: []domain.GroupServiceConfig{{ID: "service", RepositoryID: "repo", Bootstrap: domain.BootstrapConfig{CopyRules: []domain.BootstrapCopyRule{{Source: "template.txt", Target: "copied.txt"}}, Commands: []domain.BootstrapCommand{{Name: "record", Enabled: true, Executable: "record", WorkingDirectory: ".", TimeoutSeconds: 1}}}}}}}
	if _, err := app.RerunWorkspaceBootstrap(context.Background(), config, task, worktree); err != nil {
		t.Fatal(err)
	}
	if recorder.calls != 1 {
		t.Fatalf("validated bootstrap ran %d commands", recorder.calls)
	}
	copied, err := os.ReadFile(filepath.Join(worktree, "copied.txt"))
	if err != nil || string(copied) != "configured source\n" {
		t.Fatalf("bootstrap source bypassed configured repository: %q, %v", copied, err)
	}
}
