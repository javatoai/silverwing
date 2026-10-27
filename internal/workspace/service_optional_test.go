package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

type optionalServiceBranchValidator struct{ calls int }

func (v *optionalServiceBranchValidator) IsValid(string) bool { v.calls++; return false }

type acceptingOptionalBranchValidator struct{}

func (acceptingOptionalBranchValidator) IsValid(string) bool { return true }

func optionalServiceFixture(t *testing.T) (*TaskApplicationService, domain.AppConfig, string) {
	t.Helper()
	root := t.TempDir()
	tasks := filepath.Join(root, "tasks")
	config := domain.DefaultAppConfig()
	config.TaskRoot = &tasks
	service := NewTaskApplicationService(nil, NewGitClient(nil, filepath.Join(root, "git-must-not-run")), storage.NewPaths(filepath.Join(root, "profile")))
	return service, config, tasks
}

func TestServiceOptionalCreatePreservesTaskWithoutGit(t *testing.T) {
	for _, preset := range []string{"", "feature/later", "feature/{num}"} {
		t.Run(preset, func(t *testing.T) {
			service, config, root := optionalServiceFixture(t)
			validator := &optionalServiceBranchValidator{}
			service.BranchValidator = validator
			service.RequirementMaterials = func(context.Context, domain.AppConfig, string, string) (domain.RequirementMaterialsDirectory, *string, error) {
				return domain.RequirementMaterialsDirectory{Status: domain.MaterialsNotRequested}, ptr("7140000001"), nil
			}
			request := CreateTaskRequest{FolderName: "analysis", GroupID: domain.DefaultGroupID, FeatureBranch: preset, TaskNotes: "保留分析笔记", RequirementLink: "https://project.feishu.cn/obt/other_task/detail/7140000001"}
			conflicts, err := service.InspectCreateBranchReuse(context.Background(), config, request)
			if err != nil || len(conflicts) != 0 {
				t.Fatalf("preflight: %v %+v", err, conflicts)
			}
			manifest, err := service.CreateTask(context.Background(), config, request)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "analysis")
			saved, err := service.Manifests.Load(path)
			if err != nil || saved.Services == nil || len(saved.Services) != 0 || saved.FeatureBranch != preset || saved.RequirementLink != request.RequirementLink || validator.calls != 0 {
				t.Fatalf("unexpected task: %v %+v git validations=%d", err, saved, validator.calls)
			}
			if notes, err := service.Documents.ReadTaskNotes(path); err != nil || notes != request.TaskNotes {
				t.Fatalf("notes: %q %v", notes, err)
			}
			if _, err := service.Archive(context.Background(), config, path, false); err != nil {
				t.Fatal(err)
			}
			if _, err := service.Restore(context.Background(), config, path); err != nil {
				t.Fatal(err)
			}
			preview, err := service.PreviewAgentInstructions(context.Background(), config, request, manifest.RequirementMaterials)
			if err != nil {
				t.Fatal(err)
			}
			var content strings.Builder
			for _, file := range preview.Files {
				content.WriteString(file.Content)
			}
			if strings.Contains(content.String(), "feature/example") || !strings.Contains(content.String(), "任务目录中的分析和文档") {
				t.Fatal("empty-service agent instructions have a fake branch or prohibit task documents")
			}
		})
	}
}

func TestServiceOptionalRejectsMalformedSelection(t *testing.T) {
	service, config, _ := optionalServiceFixture(t)
	service.BranchValidator = acceptingOptionalBranchValidator{}
	config.Groups[0].Services = []domain.GroupServiceConfig{{ID: "service", Enabled: true}}
	for _, request := range []CreateTaskRequest{
		{ServiceIDs: []string{"missing"}},
		{ServiceSelections: []TaskServiceSelection{{ServiceID: "service"}}},
		{BaseOverrides: []ModuleBaseOverride{{ServiceID: "service", ModuleID: "module", BaseRef: "origin/main"}}},
	} {
		request.FolderName, request.GroupID, request.FeatureBranch = "analysis", domain.DefaultGroupID, "feature/example"
		if _, err := service.InspectCreateBranchReuse(context.Background(), config, request); err == nil {
			t.Fatal("malformed selection became a no-service task")
		}
	}
}

func TestServiceOptionalAddRemoveAndReaddLocalWorkspaces(t *testing.T) {
	for _, strategy := range []domain.WorkspaceStrategy{domain.WorkspaceStandardWorktree, domain.WorkspaceIndependentClone} {
		t.Run(string(strategy), func(t *testing.T) {
			root := t.TempDir()
			repo, remote, tasks := filepath.Join(root, "repo"), filepath.Join(root, "remote.git"), filepath.Join(root, "tasks")
			if err := os.MkdirAll(repo, 0700); err != nil {
				t.Fatal(err)
			}
			gitCompare(t, root, "init", "--bare", remote)
			gitCompare(t, repo, "init", "-b", "main")
			gitCompare(t, repo, "config", "user.name", "Optional Service Test")
			gitCompare(t, repo, "config", "user.email", "optional@example.invalid")
			writeCompareFile(t, repo, "base.txt", "base\n")
			commitCompare(t, repo, "base")
			gitCompare(t, repo, "remote", "add", "origin", remote)
			gitCompare(t, repo, "push", "-u", "origin", "main")
			before := gitCompareOutput(t, remote, "show-ref")
			config := domain.DefaultAppConfig()
			config.TaskRoot = &tasks
			config.Repositories = []domain.RepositoryConfig{{ID: "repo", Name: "fixture", RootPath: repo, GitCommonDirectory: filepath.Join(repo, ".git"), OriginURL: &remote}}
			for _, id := range []string{"one", "two"} {
				config.Groups[0].Services = append(config.Groups[0].Services, domain.GroupServiceConfig{ID: id, RepositoryID: "repo", DisplayName: id, Enabled: true, MasterBranch: "origin/main", DevelopmentTool: domain.DevelopmentToolIntelliJIdea, Modules: []domain.ServiceModuleConfig{{ID: "module", Name: "module", Strategy: strategy, CustomCommands: []domain.WorkspaceCommandConfig{}}}})
			}
			service := NewTaskApplicationService(nil, nil, storage.NewPaths(filepath.Join(root, "profile")))
			manifest, err := service.CreateTask(context.Background(), config, CreateTaskRequest{FolderName: "analysis", TaskNotes: "keep notes"})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(tasks, "analysis")
			add := func(id, branch string) {
				t.Helper()
				var err error
				manifest, err = service.AddServices(context.Background(), config, path, AddWorkspaceRequest{ServiceIDs: []string{id}, ServiceSelections: []TaskServiceSelection{{ServiceID: id, Modules: []TaskModuleSelection{{ID: "module", Name: "module", Strategy: strategy, BaseRef: "origin/main", BaseRemote: "origin", TargetBranch: branch, Source: domain.TaskModuleConfigured, TagMode: domain.TagBuildMergeToTargetBranch}}}}})
				if err != nil {
					t.Fatal(err)
				}
			}
			add("one", "feature/one")
			add("two", "feature/two")
			if len(manifest.Services) != 2 {
				t.Fatalf("expected 2 workspaces: %+v", manifest.Services)
			}
			for len(manifest.Services) > 0 {
				ws := manifest.Services[0]
				writeCompareFile(t, ws.WorktreePath, "untracked.txt", "user data")
				preview, err := service.ModuleRemoval.Inspect(config, path, ws.WorktreePath)
				if err != nil {
					t.Fatal(err)
				}
				if !preview.RequiresRiskConfirmation {
					t.Fatal("untracked data lost risk check")
				}
				if _, err := service.ModuleRemoval.Remove(context.Background(), config, path, preview, WorkspaceModuleRemovalConfirmation{Fingerprint: preview.Fingerprint}); err == nil {
					t.Fatal("removal skipped data loss acknowledgement")
				}
				writeCompareFile(t, ws.WorktreePath, "added-after-preview.txt", "changed after preview")
				if _, err := service.ModuleRemoval.Remove(context.Background(), config, path, preview, WorkspaceModuleRemovalConfirmation{Fingerprint: preview.Fingerprint, AcknowledgeDataLoss: true}); err == nil {
					t.Fatal("stale removal succeeded")
				}
				preview, err = service.ModuleRemoval.Inspect(config, path, ws.WorktreePath)
				if err != nil {
					t.Fatal(err)
				}
				result, err := service.ModuleRemoval.Remove(context.Background(), config, path, preview, WorkspaceModuleRemovalConfirmation{Fingerprint: preview.Fingerprint, AcknowledgeDataLoss: true})
				if err != nil || result.CleanupError != "" {
					t.Fatalf("remove: %v %+v", err, result)
				}
				manifest = result.Manifest
			}
			if notes, err := service.Documents.ReadTaskNotes(path); err != nil || notes != "keep notes" {
				t.Fatalf("notes lost: %q %v", notes, err)
			}
			if manifest.FeatureBranch != "" {
				t.Fatal("add/remove changed the branch preset")
			}
			add("one", "feature/readded")
			if len(manifest.Services) != 1 || gitCompareOutput(t, remote, "show-ref") != before {
				t.Fatal("re-add failed or changed remote refs")
			}
		})
	}
}

func TestServiceOptionalCanceledCreationDoesNotCreateDirectory(t *testing.T) {
	service, config, root := optionalServiceFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := service.CreateTask(ctx, config, CreateTaskRequest{FolderName: "analysis"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "analysis")); !os.IsNotExist(err) {
		t.Fatalf("canceled task directory exists: %v", err)
	}
}

func TestServiceOptionalRouterUpgradePreservesUserEdits(t *testing.T) {
	service, config, root := optionalServiceFixture(t)
	manifest, err := service.CreateTask(context.Background(), config, CreateTaskRequest{FolderName: "analysis"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "analysis")
	router := filepath.Join(path, AgentRouterFileName)
	generated := renderRootRouter(manifest, service.Paths)
	legacy := strings.Replace(generated, taskScopeRule, legacyTaskScopeRule, 1)
	for _, content := range []string{legacy, legacy + "\nUser rule: keep this file.\n"} {
		if err := os.WriteFile(router, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := service.Documents.WriteTaskDocument(path, manifest, config); err != nil {
			t.Fatal(err)
		}
		actual, err := os.ReadFile(router)
		if err != nil {
			t.Fatal(err)
		}
		want := content
		if content == legacy {
			want = generated
		}
		if string(actual) != want {
			t.Fatal("generated router not upgraded, or user edits changed")
		}
	}
}

func TestServiceOptionalFinalRemovalDocumentFailureRestoresRecord(t *testing.T) {
	service, config, root := optionalServiceFixture(t)
	manifest, err := service.CreateTask(context.Background(), config, CreateTaskRequest{FolderName: "analysis", TaskNotes: "keep notes"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "analysis")
	missing := filepath.Join(path, "missing-clone")
	manifest.Services = []domain.ServiceWorkspace{{RepositoryID: "repo", GroupServiceID: "service", ModuleID: "module", WorktreePath: missing, RepositoryPath: missing, Branch: "feature/task", Strategy: domain.WorkspaceIndependentClone, Health: domain.WorkspaceReady, ModuleSource: domain.TaskModuleConfigured, DevelopmentTool: domain.DevelopmentToolIntelliJIdea, TagMode: domain.TagBuildMergeToTargetBranch}}
	if err := service.Manifests.Save(path, manifest); err != nil {
		t.Fatal(err)
	}
	contextPath := filepath.Join(path, AgentWorkspaceDir, AgentDirectoryName, AgentContextFileName)
	if err := os.Remove(contextPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(contextPath, 0700); err != nil {
		t.Fatal(err)
	}
	preview, err := service.ModuleRemoval.Inspect(config, path, missing)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ModuleRemoval.Remove(context.Background(), config, path, preview, WorkspaceModuleRemovalConfirmation{Fingerprint: preview.Fingerprint}); err == nil {
		t.Fatal("document failure accepted")
	}
	saved, err := service.Manifests.Load(path)
	if err != nil || len(saved.Services) != 1 {
		t.Fatalf("failed removal lost record: %v %+v", err, saved)
	}
	if notes, err := service.Documents.ReadTaskNotes(path); err != nil || notes != "keep notes" {
		t.Fatal("failed removal lost notes")
	}
}
