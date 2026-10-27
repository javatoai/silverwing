package application

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

func annualApplicationFixture(t *testing.T) (*App, documents.AnnualMaterialsScope, string) {
	t.Helper()
	a, _, taskPath := materialTextApplicationFixture(t)
	base := filepath.Join(a.Paths.Home, "annual")
	owned := filepath.Join(base, "2026", "123-需求", "研发")
	scope, _ := documents.AnnualScope(owned)
	if err := os.MkdirAll(base, 0755); err != nil {
		t.Fatal(err)
	}
	if err := scope.EnsureLocal(); err != nil {
		t.Fatal(err)
	}
	config, err := a.Config.Load()
	if err != nil {
		t.Fatal(err)
	}
	config.RequirementMaterialsRoot = &base
	sub := "研发"
	config.RequirementMaterialsSubdirectory = &sub
	if err := a.Config.Save(config); err != nil {
		t.Fatal(err)
	}
	m, err := a.Manifests.Load(taskPath)
	if err != nil {
		t.Fatal(err)
	}
	m.RequirementLink = "https://project.feishu.cn/obt/userstory/detail/123"
	m.RequirementMaterials = domain.RequirementMaterialsDirectory{Status: domain.MaterialsReady, WriteRoot: &owned}
	m.GroupID = "g"
	if err := a.Manifests.Save(taskPath, m); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.NewAgentDocumentService(a.Paths, a.Manifests).EnsureTaskDocuments(taskPath, m, config); err != nil {
		t.Fatal(err)
	}
	return a, scope, taskPath
}

func TestAnnualApplicationPeerReadOnlyAndMutationGuards(t *testing.T) {
	a, scope, _ := annualApplicationFixture(t)
	peer := filepath.Join(scope.Root, "测试", "case.txt")
	if err := os.MkdirAll(filepath.Dir(peer), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(peer, []byte("peer text"), 0644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	result, err := a.documentAction(ctx, Action{Kind: "document.read", TaskID: "task", Path: peer})
	if err != nil || !result.Data.(documents.Content).ReadOnly {
		t.Fatal(result, err)
	}
	for _, action := range []Action{
		{Kind: "document.save", Path: peer, Text: "overwrite"},
		{Kind: "document.draft", Path: peer, Text: "draft"},
		{Kind: "document.create", Path: filepath.Dir(peer), Payload: map[string]any{"name": "new.txt"}},
		{Kind: "document.mkdir", Path: filepath.Dir(peer), Payload: map[string]any{"name": "new"}},
		{Kind: "document.rename", Path: peer, Payload: map[string]any{"name": "renamed.txt"}},
		{Kind: "document.trash", Payload: map[string]any{"paths": []string{peer}, "confirmed": true}},
		{Kind: "document.copy", Path: peer, Payload: map[string]any{"destination": filepath.Join(filepath.Dir(peer), "copy.txt")}},
		{Kind: "document.import", Payload: map[string]any{"directory": filepath.Dir(peer), "paths": []string{peer}}},
		{Kind: "document.paste", Path: filepath.Dir(peer)},
		{Kind: "document.rename", Path: scope.Owned, Payload: map[string]any{"name": "newowned"}},
		{Kind: "document.trash-preview", Path: scope.Temporary},
		{Kind: "document.create", Path: scope.Temporary, Payload: map[string]any{"name": ".gitignore"}},
	} {
		action.TaskID = "task"
		if _, err := a.documentAction(ctx, action); err == nil {
			t.Fatalf("mutation accepted %+v", action)
		}
	}
	action := Action{TaskID: "task", Kind: "document.conflict-resolve", Payload: map[string]any{"conflict": documents.FileOperationConflict{Request: documents.FileOperationRequest{Kind: documents.FileOperationCreateFile, Directory: filepath.Dir(peer), Name: "case.txt"}}, "choice": "REPLACE"}}
	if _, err := a.documentAction(ctx, action); err == nil {
		t.Fatal("conflict replay bypassed permissions")
	}
	if data, _ := os.ReadFile(peer); string(data) != "peer text" {
		t.Fatal("peer file changed")
	}
	if _, err := a.documentAction(ctx, Action{Kind: "document.copy", TaskID: "task", Path: peer, Payload: map[string]any{"destination": filepath.Join(scope.Owned, "copied.txt")}}); err != nil {
		t.Fatal("copy from peer denied", err)
	}
	for _, dir := range []string{"", scope.Temporary} {
		if _, err := a.documentAction(ctx, Action{Kind: "document.create", TaskID: "task", Path: dir, Payload: map[string]any{"name": "owned.txt"}}); err != nil {
			t.Fatal("owned create", err)
		}
	}
	if _, err := os.Stat(filepath.Join(scope.Owned, "owned.txt")); err != nil {
		t.Fatal("default not owned", err)
	}
	if _, err := a.documentAction(ctx, Action{Kind: "document.search", TaskID: "task", Payload: map[string]any{"query": "peer"}}); err != nil {
		t.Fatal(err)
	}
}

func TestAnnualApplicationCatalogAllTasksAndLocalRetry(t *testing.T) {
	a, scope, taskPath := annualApplicationFixture(t)
	c, _ := a.Config.Load()
	m, _ := a.Manifests.Load(taskPath)
	archived := m
	archived.TaskDirectoryName = "archived"
	archived.FolderName = "archived"
	archived.LifecycleStatus = domain.TaskArchived
	archivedPath := filepath.Join(*c.TaskRoot, "archived")
	if err := a.Manifests.Save(archivedPath, archived); err != nil {
		t.Fatal(err)
	}
	agents := workspace.NewAgentDocumentService(a.Paths, a.Manifests)
	if _, err := agents.EnsureTaskDocuments(archivedPath, archived, c); err != nil {
		t.Fatal(err)
	}
	peer := filepath.Join(scope.Root, "测试")
	if err := os.Mkdir(peer, 0755); err != nil {
		t.Fatal(err)
	}
	expected := MaterialsGitScope(a.Paths.Home, c)
	if err := a.refreshMaterialsAgentCatalogs(context.Background(), expected); err != nil {
		t.Fatal(err)
	}
	for _, task := range []string{taskPath, archivedPath} {
		for _, file := range []string{"AGENTS.md", filepath.Join(".workspace", "agent", "TASK-CONTEXT.md")} {
			data, err := os.ReadFile(filepath.Join(task, file))
			if err != nil || !strings.Contains(string(data), peer) {
				t.Fatal(task, file, string(data), err)
			}
		}
	}
	target := filepath.Join(taskPath, "AGENTS.md")
	original, _ := os.ReadFile(target)
	if err := os.WriteFile(target, []byte("custom file"), 0644); err != nil {
		t.Fatal(err)
	}
	result, err := a.documentAction(context.Background(), Action{Kind: "document.list", TaskID: "task"})
	if err != nil || !strings.Contains(result.Message, "说明更新失败") {
		t.Fatal(result.Message, err)
	}
	if err := os.WriteFile(target, original, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(peer, filepath.Join(scope.Root, "测试新")); err != nil {
		t.Fatal(err)
	}
	if _, err := a.documentAction(context.Background(), Action{Kind: "document.list", TaskID: "task"}); err != nil {
		t.Fatal(err)
	}
	// No runner was installed: this local retry cannot run Git or a CLI.
	if a.Runner != nil {
		t.Fatal("unexpected runner")
	}
	if data, _ := os.ReadFile(target); !strings.Contains(string(data), filepath.Join(scope.Root, "测试新")) || strings.Contains(string(data), "`"+peer+"`") {
		t.Fatal(string(data))
	}
	c.MaterialsGitSync = &domain.MaterialsGitSyncSettings{Branch: "other", IntervalMinutes: 5}
	if err := a.Config.Save(c); err != nil {
		t.Fatal(err)
	}
	if err := a.refreshMaterialsAgentCatalogs(context.Background(), expected); err == nil {
		t.Fatal("late config scope accepted")
	}
}

func TestAnnualApplicationStaleTaskAndConfigRejected(t *testing.T) {
	a, scope, taskPath := annualApplicationFixture(t)
	c, _ := a.Config.Load()
	m, _ := a.Manifests.Load(taskPath)
	action := Action{Kind: "document.create", TaskID: "task", Path: scope.Owned, Payload: map[string]any{"name": "late.txt", "desktopRenameTaskCreatedAt": m.CreatedAt, "materialsConfigScope": MaterialsGitScope(a.Paths.Home, c)}}
	m.CreatedAt = "changed"
	if err := a.Manifests.Save(taskPath, m); err != nil {
		t.Fatal(err)
	}
	if _, err := a.documentAction(context.Background(), action); err == nil {
		t.Fatal("stale task accepted")
	}
	if _, err := os.Stat(filepath.Join(scope.Owned, "late.txt")); !os.IsNotExist(err) {
		t.Fatal("late write happened")
	}
}
