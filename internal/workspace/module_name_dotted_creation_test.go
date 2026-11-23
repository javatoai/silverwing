package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

func TestCreateTaskPreservesDottedModuleDirectoryAndBranchSuffix(t *testing.T) {
	for _, strategy := range []domain.WorkspaceStrategy{domain.WorkspaceStandardWorktree, domain.WorkspaceIndependentClone} {
		t.Run(string(strategy), func(t *testing.T) {
			root := t.TempDir()
			repository := filepath.Join(root, "repository")
			remote := filepath.Join(root, "remote.git")
			taskRoot := filepath.Join(root, "tasks")
			if err := os.MkdirAll(repository, 0700); err != nil {
				t.Fatal(err)
			}
			gitCompare(t, root, "init", "--bare", "--initial-branch=main", remote)
			gitCompare(t, repository, "init", "--initial-branch=main")
			gitCompare(t, repository, "config", "user.name", "Dotted Module Test")
			gitCompare(t, repository, "config", "user.email", "dotted-module@example.invalid")
			writeCompareFile(t, repository, "base.txt", "base\n")
			commitCompare(t, repository, "base")
			gitCompare(t, repository, "remote", "add", "origin", remote)
			gitCompare(t, repository, "push", "-u", "origin", "main")

			config := domain.DefaultAppConfig()
			config.TaskRoot = &taskRoot
			config.Repositories = []domain.RepositoryConfig{{
				ID: "repo-dotted", Name: "dotted fixture", RootPath: repository,
				GitCommonDirectory: filepath.Join(repository, ".git"), OriginURL: &remote,
			}}
			config.Services = []domain.ServiceConfig{{
				ID: "service-dotted", RepositoryID: "repo-dotted", DisplayName: "sample", Enabled: true,
				MasterBranch: "origin/main", TestTagBaselineRef: "origin/main", DevelopmentTool: domain.DevelopmentToolIntelliJIdea,
				Modules: []domain.ServiceModuleConfig{
					{ID: "version", Name: "v4.1.5", Strategy: strategy, TagMode: domain.TagBuildMergeToTargetBranch, CustomCommands: []domain.WorkspaceCommandConfig{}},
					{ID: "api", Name: "api", Strategy: strategy, TagMode: domain.TagBuildMergeToTargetBranch, CustomCommands: []domain.WorkspaceCommandConfig{}},
				},
			}}

			paths := storage.NewPaths(filepath.Join(root, "profile"))
			if err := storage.NewConfigStore(paths).Save(config); err != nil {
				t.Fatalf("save service config with dotted module name: %v", err)
			}
			config, err := storage.NewConfigStore(paths).Load()
			if err != nil {
				t.Fatalf("reload service config with dotted module name: %v", err)
			}
			saved, err := config.Service("service-dotted")
			if err != nil {
				t.Fatal(err)
			}
			if got := saved.Modules[0].Name; got != "v4.1.5" {
				t.Fatalf("persisted module name = %q, want v4.1.5", got)
			}
			service := NewTaskApplicationService(nil, nil, paths)
			service.BranchValidator = acceptingOptionalBranchValidator{}
			manifest, err := service.CreateTask(context.Background(), config, CreateTaskRequest{
				FolderName: "dotted-modules", GroupID: domain.DefaultGroupID,
				FeatureBranch: "feature/dotted-modules", ServiceIDs: []string{"service-dotted"},
			})
			if err != nil {
				t.Fatal(err)
			}

			wantPaths := map[string]string{
				"version": filepath.Join(taskRoot, "dotted-modules", "sample-v4.1.5"),
				"api":     filepath.Join(taskRoot, "dotted-modules", "sample-api"),
			}
			wantBranches := map[string]string{
				"version": "feature/dotted-modules-v4.1.5",
				"api":     "feature/dotted-modules-api",
			}
			if len(manifest.Services) != len(wantPaths) {
				t.Fatalf("created %d workspaces, want %d: %+v", len(manifest.Services), len(wantPaths), manifest.Services)
			}
			for _, workspace := range manifest.Services {
				if workspace.WorktreePath != wantPaths[workspace.ModuleID] || workspace.Branch != wantBranches[workspace.ModuleID] || workspace.ModuleName == "" {
					t.Errorf("module %s did not preserve dotted naming: path=%q branch=%q name=%q", workspace.ModuleID, workspace.WorktreePath, workspace.Branch, workspace.ModuleName)
				}
				if info, err := os.Stat(workspace.WorktreePath); err != nil || !info.IsDir() {
					t.Errorf("module directory was not created at %q: %v", workspace.WorktreePath, err)
				}
				branch := gitCompareOutput(t, workspace.WorktreePath, "branch", "--show-current")
				if branch != wantBranches[workspace.ModuleID] {
					t.Errorf("checked out branch = %q, want %q", branch, wantBranches[workspace.ModuleID])
				}
			}
		})
	}
}
