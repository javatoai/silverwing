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

func selectProfileTaskRoot(t *testing.T, app *App, root, expected string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	action := Action{Kind: "config.task-root.save", Path: root, Payload: map[string]any{
		"confirmed": true, "expectedTaskRoot": expected,
	}}
	result, err := app.Dispatch(context.Background(), action)
	if err != nil || !sameTaskContextPath(result.Path, root) {
		t.Fatalf("select task root %q: %v, %v", root, result.Path, err)
	}
}

func TestProfileRelocationRefreshesGeneratedInstructionsAndKeepsDraft(t *testing.T) {
	ctx := context.Background()
	parent := t.TempDir()
	oldHome := filepath.Join(parent, "旧位置")
	newHome := filepath.Join(parent, "新位置")
	app, err := New(oldHome)
	if err != nil {
		t.Fatal(err)
	}
	taskRoot := filepath.Join(parent, "tasks")
	selectProfileTaskRoot(t, app, taskRoot, "")
	config, err := app.Config.Load()
	if err != nil {
		t.Fatal(err)
	}
	config.ProjectInstructionsGitURL = "https://example.invalid/team/project.git"
	if err := app.Config.Save(config); err != nil {
		t.Fatal(err)
	}
	saveFixturePlatformAccount(t)
	tools := []string{}
	created, err := app.CreateTask(ctx, TaskCreateRequest{
		CreateTaskRequest: workspace.CreateTaskRequest{FolderName: "项目任务", FeatureBranch: "feature/test"},
		WorkspaceToolIDs:  &tools,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	taskName := created.Data.TaskDirectoryName
	taskPath := filepath.Join(taskRoot, taskName)
	oldRules := filepath.Join(taskPath, workspace.AgentWorkspaceDir, workspace.AgentDirectoryName, workspace.AgentRulesFileName)
	if err := os.MkdirAll(filepath.Dir(oldRules), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldRules, []byte("手写任务规则"), 0o600); err != nil {
		t.Fatal(err)
	}
	router := filepath.Join(taskPath, workspace.AgentRouterFileName)
	generated, err := os.ReadFile(router)
	if err != nil {
		t.Fatal(err)
	}
	projectRoot := filepath.Join(domain.ManagedProjectInstructionsRoot(oldHome, config.ProjectInstructionsGitURL), domain.ProjectInstructionsDirectory)
	if !strings.Contains(string(generated), projectRoot) {
		t.Fatalf("router is missing its project directory: %s", generated)
	}
	const addedNote = "\n用户保留的说明\n"
	if err := os.WriteFile(router, append(generated, addedNote...), 0o600); err != nil {
		t.Fatal(err)
	}
	identity, err := documents.NewMarkdownDraftIdentity("project-instructions", projectRoot, projectRoot, "说明.md")
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Documents.WriteMarkdownDraft(ctx, documents.MarkdownDraft{Identity: identity, Content: "待保存的草稿", BaselineHash: documents.Hash(nil)}); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(oldHome, newHome); err != nil {
		t.Fatal(err)
	}
	moved, err := NewTagCLI(newHome)
	if err != nil {
		t.Fatal(err)
	}
	if moved.Paths.ProfileID != app.Paths.ProfileID {
		t.Fatal("moving the data directory changed its identity")
	}
	connections, err := moved.PlatformConnections(ctx)
	if err != nil || !connections.Configured || connections.Username != "fixture" {
		t.Fatalf("moving the profile lost its shared platform account: %+v, %v", connections, err)
	}
	movedConfig, err := moved.Config.Load()
	if err != nil || value(movedConfig.TaskRoot) != taskRoot {
		t.Fatalf("moving the profile changed its task root: %v, %v", movedConfig.TaskRoot, err)
	}
	current, err := os.ReadFile(filepath.Join(taskPath, workspace.AgentRouterFileName))
	if err != nil || strings.Contains(string(current), oldHome) || !strings.Contains(string(current), newHome) || !strings.HasSuffix(string(current), addedNote) {
		t.Fatalf("generated paths or user content differ: %s, %v", current, err)
	}
	projectRoot = filepath.Join(domain.ManagedProjectInstructionsRoot(newHome, config.ProjectInstructionsGitURL), domain.ProjectInstructionsDirectory)
	identity, err = documents.NewMarkdownDraftIdentity("project-instructions", projectRoot, projectRoot, "说明.md")
	if err != nil {
		t.Fatal(err)
	}
	draft, err := moved.Documents.ReadMarkdownDraft(ctx, identity)
	if err != nil || draft == nil || draft.Content != "待保存的草稿" {
		t.Fatalf("draft wasn't restored: %#v, %v", draft, err)
	}
	service := workspace.NewAgentDocumentService(moved.Paths, moved.Manifests)
	if rules, err := service.ReadTaskNotes(taskPath); err != nil || rules != "手写任务规则" {
		t.Fatalf("task rules weren't preserved: %q, %v", rules, err)
	}
}
