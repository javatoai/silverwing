package workspace

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

func TestTaskIndependentCloneCreateAndAppendTracksSelectedRemoteBranch(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	remote, source := filepath.Join(root, "remote.git"), filepath.Join(root, "source")
	gitCompare(t, root, "init", "--bare", "--initial-branch=main", remote)
	gitCompare(t, root, "init", "--initial-branch=main", source)
	gitCompare(t, source, "config", "user.name", "Clone Test")
	gitCompare(t, source, "config", "user.email", "clone@example.invalid")
	writeCompareFile(t, source, "base.txt", "main\n")
	commitCompare(t, source, "main")
	gitCompare(t, source, "switch", "-c", "release/one")
	writeCompareFile(t, source, "base.txt", "selected branch\n")
	commitCompare(t, source, "selected branch")
	gitCompare(t, source, "remote", "add", "mirror", remote)
	gitCompare(t, source, "push", "mirror", "main", "release/one")
	gitCompare(t, source, "switch", "main")
	before := gitCompareOutput(t, remote, "show-ref")
	secondary := filepath.Join(root, "secondary")
	gitCompare(t, root, "clone", "--origin", "mirror", remote, secondary)

	taskRoot := filepath.Join(root, "tasks")
	config := domain.DefaultAppConfig()
	config.TaskRoot = &taskRoot
	for _, repo := range []struct{ id, path string }{{"primary", source}, {"secondary", secondary}} {
		config.Repositories = append(config.Repositories, domain.RepositoryConfig{ID: repo.id, Name: repo.id, RootPath: repo.path, GitCommonDirectory: filepath.Join(repo.path, ".git")})
		config.Groups[0].Services = append(config.Groups[0].Services, domain.GroupServiceConfig{
			ID: repo.id, RepositoryID: repo.id, DisplayName: repo.id, Enabled: true,
			MasterBranch: "mirror/main", DevelopmentTool: domain.DevelopmentToolIntelliJIdea,
			Modules: []domain.ServiceModuleConfig{{ID: "default", Name: "default", Strategy: domain.WorkspaceIndependentClone, TagMode: domain.TagBuildCurrentBranch}},
		})
	}
	service := NewTaskApplicationService(nil, nil, storage.NewPaths(filepath.Join(root, "profile")))
	clone := func(id, ref string) TaskModuleSelection {
		return TaskModuleSelection{ID: id, Name: id, Strategy: domain.WorkspaceIndependentClone, BaseRef: ref, BaseRemote: "mirror", TagMode: domain.TagBuildCurrentBranch}
	}
	create := CreateTaskRequest{
		FolderName: "direct-clone", GroupID: config.Groups[0].ID,
		ServiceSelections: []TaskServiceSelection{{ServiceID: "primary", Modules: []TaskModuleSelection{clone("first", "mirror/release/one")}}},
	}
	if conflicts, err := service.InspectCreateBranchReuse(ctx, config, create); err != nil || len(conflicts) != 0 {
		t.Fatalf("selected existing remote branch should require no reuse confirmation: %+v, %v", conflicts, err)
	}
	preview, err := service.PreviewAgentInstructions(ctx, config, create, domain.RequirementMaterialsDirectory{})
	if err != nil {
		t.Fatal(err)
	}
	var previewContent strings.Builder
	for _, file := range preview.Files {
		previewContent.WriteString(file.Content)
	}
	if !strings.Contains(previewContent.String(), "release/one") || strings.Contains(previewContent.String(), "feature/example") {
		t.Fatalf("preview did not use the selected clone branch: %s", previewContent.String())
	}
	manifest, err := service.CreateTask(ctx, config, create)
	if err != nil {
		t.Fatal(err)
	}
	assertClone := func(record domain.ServiceWorkspace, branch string) {
		t.Helper()
		if record.Branch != branch || record.TargetBranch != nil || record.PushRemote != "origin" || stringValue(record.BaseRef) != "mirror/"+branch {
			t.Fatalf("incorrect clone branch metadata: %+v", record)
		}
		if got := gitCompareOutput(t, record.WorktreePath, "branch", "--show-current"); got != branch {
			t.Fatalf("checked out %q, want %q", got, branch)
		}
		if got := gitCompareOutput(t, record.WorktreePath, "rev-parse", "--abbrev-ref", "@{upstream}"); got != "origin/"+branch {
			t.Fatalf("tracks %q, want origin/%s", got, branch)
		}
		if got := gitCompareOutput(t, record.WorktreePath, "for-each-ref", "--format=%(refname:short)", "refs/heads"); got != branch {
			t.Fatalf("unexpected local branches: %q", got)
		}
		if got := gitCompareOutput(t, record.WorktreePath, "rev-parse", "HEAD"); got != gitCompareOutput(t, remote, "rev-parse", "refs/heads/"+branch) {
			t.Fatal("clone did not check out the selected remote head")
		}
	}
	assertClone(manifest.Services[0], "release/one")
	if got := gitCompareOutput(t, manifest.Services[0].WorktreePath, "for-each-ref", "--format=%(refname:short)", "refs/remotes/origin/main"); got != "origin/main" {
		t.Fatalf("ordinary remote refs should remain available: %q", got)
	}
	taskPath := filepath.Join(taskRoot, create.FolderName)
	manifest, err = service.AddServices(ctx, config, taskPath, AddWorkspaceRequest{
		ServiceSelections: []TaskServiceSelection{{ServiceID: "secondary", Modules: []TaskModuleSelection{clone("extra", "mirror/main")}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertClone(manifest.Services[1], "main")
	manifest, err = service.AddModules(ctx, config, taskPath, AddWorkspaceRequest{ServiceID: "primary", Modules: []TaskModuleSelection{clone("second", "mirror/release/one")}})
	if err != nil {
		t.Fatal(err)
	}
	assertClone(manifest.Services[2], "release/one")
	worktree := TaskModuleSelection{ID: "worktree", Name: "worktree", Strategy: domain.WorkspaceStandardWorktree, BaseRef: "mirror/main", BaseRemote: "mirror", TargetBranch: "feature/per-module", TagMode: domain.TagBuildCurrentBranch}
	manifest, err = service.AddModules(ctx, config, taskPath, AddWorkspaceRequest{ServiceID: "primary", Modules: []TaskModuleSelection{worktree}})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.FeatureBranch != "" || len(manifest.Services) != 4 || manifest.Services[3].Branch != worktree.TargetBranch {
		t.Fatalf("appended Worktree did not use a per-module target: %+v", manifest)
	}
	saved, err := service.Manifests.Load(taskPath)
	if err != nil || saved.FeatureBranch != "" || len(saved.Services) != 4 || saved.Validate() != nil {
		t.Fatalf("task without branch preset did not round trip: %+v, %v", saved, err)
	}

	mixed := create
	mixed.FolderName = "mixed-strategy"
	worktree.TargetBranch = "feature/mixed-worktree"
	mixed.ServiceSelections = []TaskServiceSelection{{ServiceID: "primary", Modules: []TaskModuleSelection{clone("clone", "mirror/release/one"), worktree}}}
	if _, err := service.InspectCreateBranchReuse(ctx, config, mixed); err == nil {
		t.Fatal("mixed task accepted an empty task branch preset")
	}
	mixed.FeatureBranch = "feature/mixed"
	manifest, err = service.CreateTask(ctx, config, mixed)
	if err != nil {
		t.Fatal(err)
	}
	assertClone(manifest.Services[0], "release/one")
	if manifest.FeatureBranch != mixed.FeatureBranch || manifest.Services[1].Branch != worktree.TargetBranch {
		t.Fatalf("mixed task lost its Worktree branch preset or override: %+v", manifest)
	}
	if got := gitCompareOutput(t, remote, "show-ref"); got != before {
		t.Fatalf("creating workspaces changed remote refs: %s", got)
	}
}
