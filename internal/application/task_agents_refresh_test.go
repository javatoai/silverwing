package application

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
	"silverwing-go/internal/workspace"
)

func TestCreateAndRegenerateTaskWithoutTaskRules(t *testing.T) {
	ctx := context.Background()
	app, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	taskRoot := t.TempDir()
	selectProfileTaskRoot(t, app, taskRoot, "")
	const name = "standalone-task"
	if _, err := app.Dispatch(ctx, Action{Kind: "task.create", Payload: map[string]any{
		"folderName": name, "taskNotes": "old request field",
	}}); err != nil {
		t.Fatal(err)
	}
	taskPath := filepath.Join(taskRoot, name)
	assertDocuments := func() {
		t.Helper()
		if _, err := os.Lstat(filepath.Join(taskPath, workspace.AgentWorkspaceDir, workspace.AgentDirectoryName, workspace.AgentRulesFileName)); !os.IsNotExist(err) {
			t.Fatalf("task rules file was generated: %v", err)
		}
		agents, err := os.ReadFile(filepath.Join(taskPath, workspace.AgentRouterFileName))
		if err != nil || strings.Contains(string(agents), workspace.AgentRulesFileName) || strings.Contains(string(agents), "TASK-CONTEXT.md") ||
			!strings.Contains(string(agents), "本任务可改动的 Worktree") || !strings.Contains(string(agents), app.Paths.GlobalAgents) {
			t.Fatalf("unexpected task instructions: %q %v", agents, err)
		}
		if _, err := os.Lstat(filepath.Join(taskPath, workspace.AgentWorkspaceDir, workspace.AgentDirectoryName, "TASK-CONTEXT.md")); !os.IsNotExist(err) {
			t.Fatalf("obsolete task context file was generated: %v", err)
		}
	}
	assertDocuments()
	if _, err := app.Dispatch(ctx, Action{Kind: "task.refresh-agents", TaskID: name, Path: taskPath}); err != nil {
		t.Fatal(err)
	}
	assertDocuments()
}

func TestRefreshTaskAgentsDispatchPreservesHandwrittenFiles(t *testing.T) {
	for _, archived := range []bool{false, true} {
		name := "active task"
		if archived {
			name = "archived task"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			base := t.TempDir()
			a, err := New(filepath.Join(base, "profile"))
			if err != nil {
				t.Fatal(err)
			}
			taskRoot := filepath.Join(base, "tasks")
			selectProfileTaskRoot(t, a, taskRoot, "")
			config, err := a.Config.Load()
			if err != nil {
				t.Fatal(err)
			}
			config.ProjectInstructionsGitURL = "https://example.invalid/team/instructions.git"
			if err := a.Config.Save(config); err != nil {
				t.Fatal(err)
			}
			config, err = a.Config.Load()
			if err != nil {
				t.Fatal(err)
			}
			write := func(path, content string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			read := func(path string) string {
				t.Helper()
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				return string(data)
			}
			manifest := domain.DefaultTaskManifest()
			manifest.FolderName, manifest.TaskDirectoryName, manifest.FeatureBranch = "Existing task", "existing", "feature/existing"
			manifest.CreatedAt, manifest.UpdatedAt = "2026-11-10T00:00:00Z", "2026-11-10T00:00:00Z"
			manifest.RequirementLink = "https://project.feishu.cn/project/userstory/detail/123"
			taskPath := filepath.Join(taskRoot, manifest.TaskDirectoryName)
			routerPath := filepath.Join(taskPath, workspace.AgentRouterFileName)
			const manual = "# 手写说明\r\n保留原文\r\n"
			if archived {
				manifest.LifecycleStatus = domain.TaskArchived
			}
			if err := a.Manifests.Save(taskPath, manifest); err != nil {
				t.Fatal(err)
			}
			savedBefore, err := a.Manifests.Load(taskPath)
			if err != nil {
				t.Fatal(err)
			}
			routerBefore := manual
			if archived {
				routerBefore += "\n<!-- silverwing:instructions:start -->\n旧规则：读取 TASK-RULES.md\n<!-- silverwing:instructions:end -->\n"
			}
			write(routerPath, routerBefore)
			rulesPath := filepath.Join(taskPath, workspace.AgentWorkspaceDir, workspace.AgentDirectoryName, workspace.AgentRulesFileName)
			legacyContextPath := filepath.Join(taskPath, workspace.AgentWorkspaceDir, workspace.AgentDirectoryName, "TASK-CONTEXT.md")
			handoffPath := filepath.Join(taskPath, workspace.AgentWorkspaceDir, workspace.HandoffFileName)
			write(rulesPath, manual)
			write(legacyContextPath, manual)
			write(handoffPath, manual)
			action := taskContextActionForTest(t, a, config, savedBefore, taskPath, "task.refresh-agents")
			action.Path = taskPath
			if err := workspace.NewTaskOperationLocks(a.Paths).WithTask(taskPath, func() error {
				_, err := a.Dispatch(ctx, action)
				return err
			}); !errors.Is(err, storage.ErrLockBusy) {
				t.Fatalf("regenerate ignored the task lock: %v", err)
			}
			result, err := a.Dispatch(ctx, action)
			if err != nil || result.Path != routerPath {
				t.Fatalf("task menu action failed: %+v %v", result, err)
			}
			projectPath := filepath.Join(domain.ManagedProjectInstructionsRoot(a.Paths.Home, config.ProjectInstructionsGitURL), domain.ProjectInstructionsDirectory)
			generated := read(routerPath)
			if !strings.HasPrefix(generated, manual) || !strings.Contains(generated, a.Paths.GlobalAgents) || !strings.Contains(generated, projectPath) {
				t.Fatalf("router lost existing text or omitted latest rule paths: %q", generated)
			}
			if strings.Contains(generated, workspace.AgentRulesFileName) || strings.Contains(generated, "TASK-CONTEXT.md") {
				t.Fatalf("instructions still reference obsolete generated files: %q", generated)
			}
			if !strings.Contains(generated, manifest.RequirementLink) || read(legacyContextPath) != manual || read(rulesPath) != manual || read(handoffPath) != manual {
				t.Fatal("regeneration lost task context or handwritten files")
			}
			savedAfter, err := a.Manifests.Load(taskPath)
			savedBefore.ProjectInstructionsGitURL = config.ProjectInstructionsGitURL
			if err != nil || !reflect.DeepEqual(savedBefore, savedAfter) {
				t.Fatalf("regeneration changed unrelated task metadata or lost the association: %+v %v", savedAfter, err)
			}
		})
	}
}
