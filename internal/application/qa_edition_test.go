package application

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
	"strings"
	"testing"
)

func qaTestApp(t *testing.T) *App {
	t.Helper()
	home := filepath.Join(t.TempDir(), "qa-profile")
	a, err := New(home)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Config.Update(func(c domain.AppConfig) (domain.AppConfig, error) {
		bad := filepath.Join(home, "git-must-not-run.exe")
		c.GitExecutablePath = &bad
		c.DefaultWorkspaceToolIDs = []string{}
		return c, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func TestQATaskLifecycleWithoutGit(t *testing.T) {
	a := qaTestApp(t)
	ctx := context.Background()
	config, err := a.Config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if config.Edition != "qa" || value(config.TaskRoot) != filepath.Join(a.Paths.Home, "tasks") || value(config.RequirementMaterialsRoot) != filepath.Join(a.Paths.Home, "materials") {
		t.Fatalf("QA roots not isolated: %+v", config)
	}
	result, err := a.Dispatch(ctx, Action{Kind: "task.create", Payload: map[string]any{"folderName": "需求分析", "taskNotes": "测试范围与验收标准", "workspaceToolIds": []string{}}})
	if err != nil {
		t.Fatal(err)
	}
	taskPath := result.Path
	manifest, err := a.Manifests.Load(taskPath)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Edition != "qa" {
		t.Fatal("missing edition")
	}
	raw, err := os.ReadFile(filepath.Join(taskPath, storage.TaskManifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"featureBranch", "services", "repositories"} {
		if _, present := fields[key]; present {
			t.Fatalf("developer field persisted: %s", key)
		}
	}
	for _, kind := range []string{"task.archive", "task.restore"} {
		if _, err := a.Dispatch(ctx, Action{Kind: kind, TaskID: manifest.TaskDirectoryName}); err != nil {
			t.Fatal(kind, err)
		}
	}
	notes, err := a.taskService(config).Documents.ReadTaskNotes(taskPath)
	if err != nil || !strings.Contains(notes, "测试范围与验收标准") {
		t.Fatalf("notes lost: %q %v", notes, err)
	}
	contextData, err := os.ReadFile(filepath.Join(taskPath, ".workspace", "agent", "TASK-CONTEXT.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"Worktree", "feature/", "代码仓库"} {
		if strings.Contains(string(contextData), forbidden) {
			t.Fatalf("developer instructions survived: %s", forbidden)
		}
	}
	if _, err := a.Dispatch(ctx, Action{Kind: "task.delete", TaskID: manifest.TaskDirectoryName}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(taskPath); !os.IsNotExist(err) {
		t.Fatalf("task was not deleted: %v", err)
	}
}
func TestQADeveloperActionsAndInvalidOptionsCannotCreateFiles(t *testing.T) {
	a := qaTestApp(t)
	ctx := context.Background()
	for _, kind := range []string{"repository.add", "service.save", "workspace.add", "workspace.commit", "workspace.push", "workspace.merge", "tag.build", "tag.history", "production.inspect", "agent.plan", "development-tools/detect", "system.ide-test"} {
		if _, err := a.Dispatch(ctx, Action{Kind: kind, Path: t.TempDir()}); err == nil {
			t.Errorf("retired action accepted: %s", kind)
		}
	}
	for _, payload := range []map[string]any{{"folderName": "不应创建", "featureBranch": "feature/test"}, {"folderName": "不应创建", "serviceIds": []string{"code"}}, {"folderName": "不应创建", "workspaceToolIds": []string{"cursor"}}} {
		if _, err := a.Dispatch(ctx, Action{Kind: "task.create", Payload: payload}); err == nil {
			t.Errorf("invalid creation accepted: %v", payload)
		}
	}
	c, err := a.Config.Load()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(value(c.TaskRoot))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("invalid options created tasks: %v", entries)
	}
}
func TestQAConfigurationRoundTripAndForeignImport(t *testing.T) {
	a := qaTestApp(t)
	c, err := a.Config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !c.ToolVisibility.JSON || !c.ToolVisibility.DateTime || !c.ToolVisibility.TextDiff || !c.ToolVisibility.KillPort {
		t.Fatal("existing utilities were disabled")
	}
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, word := range []string{"repositories", "defaultBranchPrefix", "developmentTools", "tagEnabled", "genbuExecutablePath", "production"} {
		if strings.Contains(string(raw), `"`+word+`"`) {
			t.Fatalf("developer configuration remains: %s", word)
		}
	}
	var roundTrip domain.AppConfig
	if err := json.Unmarshal(raw, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if err := roundTrip.Validate(); err != nil {
		t.Fatal(err)
	}
	var foreign map[string]any
	if err := json.Unmarshal(raw, &foreign); err != nil {
		t.Fatal(err)
	}
	delete(foreign, "edition")
	data, _ := json.Marshal(foreign)
	if err := json.Unmarshal(data, &roundTrip); err == nil {
		t.Fatal("developer JSON imported without a QA edition")
	}
}
