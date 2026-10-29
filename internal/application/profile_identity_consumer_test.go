package application

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"silverwing-go/internal/productprofile"
	"silverwing-go/internal/storage"
	"silverwing-go/internal/workspace"
)

func TestProfileIdentityRefusalsPreserveLegacyBytesAndTaskData(t *testing.T) {
	labels, ok := map[string]struct{ edition, task string }{
		"qa":      {"测试版", "测试任务"},
		"product": {"产品版", "产品任务"},
	}[productprofile.Edition]
	if !ok {
		t.Fatalf("restricted task consumer used in edition %q", productprofile.Edition)
	}
	app, _, manifest, path := taskContextScopeFixture(t)
	manifestPath := filepath.Join(path, storage.TaskManifestFileName)
	before, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	invalidTools := []string{"cursor"}
	checks := []struct {
		name, want string
		run        func() error
	}{
		{"unknown action", labels.edition + "不支持操作：profile.invalid", func() error {
			_, err := app.Dispatch(ctx, Action{Kind: "profile.invalid"})
			return err
		}},
		{"unknown task action", labels.edition + "不支持任务操作：task.profile-invalid", func() error {
			_, err := app.Dispatch(ctx, Action{Kind: "task.profile-invalid", TaskID: manifest.TaskDirectoryName})
			return err
		}},
		{"create service parameter", labels.task + "不接受服务或模块参数", func() error {
			_, err := TaskCreateRequestFromAction(Action{ServiceID: "service"})
			return err
		}},
		{"create module parameter", labels.task + "不接受服务或模块参数", func() error {
			_, err := TaskCreateRequestFromAction(Action{ModuleID: "module"})
			return err
		}},
		{"create workspace parameter", labels.task + "不接受代码工作区参数：featureBranch", func() error {
			_, err := TaskCreateRequestFromAction(Action{Payload: map[string]any{"featureBranch": "feature/not-allowed"}})
			return err
		}},
		{"typed create tool", labels.edition + "只支持在 Codex 中打开任务", func() error {
			_, err := app.CreateTask(ctx, TaskCreateRequest{CreateTaskRequest: workspace.CreateTaskRequest{FolderName: "must-not-create"}, WorkspaceToolIDs: &invalidTools}, nil)
			return err
		}},
		{"typed launch tool", labels.edition + "只支持 Codex", func() error {
			_, err := app.LaunchTaskTools(ctx, TaskToolsRequest{Target: NewTaskTarget(manifest.TaskDirectoryName, path), ToolIDs: invalidTools})
			return err
		}},
		{"typed retry tool", labels.edition + "只支持 Codex", func() error {
			_, err := app.RetryTaskTool(ctx, TaskToolRetryRequest{Target: NewTaskTarget(manifest.TaskDirectoryName, path), ToolID: "cursor"})
			return err
		}},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := check.run(); err == nil || err.Error() != check.want {
				t.Fatalf("refusal = %v, want exact bytes %q", err, check.want)
			}
		})
	}
	after, err := os.ReadFile(manifestPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("rejected operation changed the task manifest: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 || entries[0].Name() != manifest.TaskDirectoryName {
		t.Fatalf("rejected developer operation created task data: %v %v", entries, err)
	}
}
