package application

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
	"silverwing-go/internal/workspace"
)

type scopedOpenTool struct {
	calls     int
	directory string
	available func()
}

func (*scopedOpenTool) Descriptor() workspace.TaskWorkspaceToolDescriptor {
	return workspace.TaskWorkspaceToolDescriptor{ID: "codex", DisplayName: "Codex"}
}
func (l *scopedOpenTool) Availability(context.Context) workspace.TaskWorkspaceToolAvailability {
	if l.available != nil {
		l.available()
	}
	return workspace.TaskWorkspaceToolAvailability{Available: true}
}
func (l *scopedOpenTool) Open(_ context.Context, task workspace.TaskWorkspaceContext) error {
	l.calls++
	l.directory = task.TaskDirectory
	return nil
}

func TestTaskOpenToolsScopeRecheckedBeforeLaunch(t *testing.T) {
	for _, mode := range []string{"active", "archived", "disabled-before", "disabled-during", "canceled", "replaced-during"} {
		t.Run(mode, func(t *testing.T) {
			app, config, manifest, path := taskContextScopeFixture(t)
			if mode == "archived" {
				manifest.LifecycleStatus = domain.TaskArchived
				if err := app.Manifests.Save(path, manifest); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "disabled-before" {
				config.DefaultWorkspaceToolIDs = []string{}
				if err := app.Config.Save(config); err != nil {
					t.Fatal(err)
				}
			}
			action := taskContextActionForTest(t, app, config, manifest, path, "task.tools-launch")
			action.Path = path
			action.Payload["toolIds"] = []string{"codex"}
			validation := app.validateDesktopTaskContext(action, config, manifest, path)
			if mode == "disabled-before" {
				if !errors.Is(validation, errDesktopTaskContextChanged) {
					t.Fatalf("disabled tool accepted: %v", validation)
				}
				return
			}
			if validation != nil {
				t.Fatal(validation)
			}
			launcher := &scopedOpenTool{}
			if mode == "disabled-during" {
				launcher.available = func() {
					config.DefaultWorkspaceToolIDs = []string{}
					if err := app.Config.Save(config); err != nil {
						t.Fatal(err)
					}
				}
			}
			if mode == "replaced-during" {
				launcher.available = func() {
					changed := manifest
					changed.CreatedAt += "-other"
					if err := app.Manifests.Save(path, changed); err != nil {
						t.Fatal(err)
					}
				}
			}
			service := app.taskService(config)
			registry, err := workspace.NewTaskWorkspaceToolRegistry([]workspace.TaskWorkspaceToolLauncher{launcher})
			if err != nil {
				t.Fatal(err)
			}
			service.WorkspaceTools.Registry = registry
			ctx := app.desktopTaskMutationContext(context.Background(), action)
			if mode == "canceled" {
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			}
			updated, err := service.LaunchWorkspaceTools(ctx, config, path, manifest, []string{"codex"})
			if mode == "active" || mode == "archived" {
				if err != nil || launcher.calls != 1 || launcher.directory != path || len(updated.Services) != 0 {
					t.Fatalf("launch: %v %d %q", err, launcher.calls, launcher.directory)
				}
			} else if err == nil || launcher.calls != 0 {
				t.Fatalf("stale tool opened: %v calls=%d", err, launcher.calls)
			}
		})
	}
}

func TestRetiredWorkspaceToolActionsLeaveManifestIntact(t *testing.T) {
	app, _, manifest, path := taskContextScopeFixture(t)
	manifest.WorkspaceToolLaunches = []domain.WorkspaceToolLaunch{
		{ToolID: "cursor", Status: domain.WorkspaceToolFailed, UpdatedAt: manifest.CreatedAt},
	}
	if err := app.Manifests.Save(path, manifest); err != nil {
		t.Fatal(err)
	}
	manifestFile := filepath.Join(path, storage.TaskManifestFileName)
	before, err := os.ReadFile(manifestFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []Action{
		{Kind: "task.tools-launch", TaskID: manifest.TaskDirectoryName, Path: path, Payload: map[string]any{"toolIds": []string{"cursor"}}},
		{Kind: "task.tools-launch", TaskID: manifest.TaskDirectoryName, Path: path, Payload: map[string]any{"toolIds": []string{"codex", "cursor"}}},
		{Kind: "task.tools-retry", TaskID: manifest.TaskDirectoryName, Path: path, Payload: map[string]any{"toolId": "cursor"}},
	} {
		if _, err := app.Dispatch(context.Background(), action); err == nil || !strings.Contains(err.Error(), "Cursor") {
			t.Fatalf("retired workspace tool request returned %+v: %v", action, err)
		}
		after, err := os.ReadFile(manifestFile)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatalf("retired workspace tool changed the manifest: action=%+v err=%v", action, err)
		}
	}
}

func TestWorkspaceToolOptionsAdvertiseCodex(t *testing.T) {
	app, _, _, _ := taskContextScopeFixture(t)
	result, err := app.Dispatch(context.Background(), Action{Kind: "workspace.tools-options"})
	if err != nil {
		t.Fatal(err)
	}
	options, err := ResultData[[]taskCreationWorkspaceToolOption](result)
	if err != nil || len(options) != 1 || options[0].ID != "codex" {
		t.Fatalf("unexpected workspace tools: %+v, %v", options, err)
	}
}

func TestTaskCreationIgnoresRetiredWorkspaceTool(t *testing.T) {
	app, _, _, _ := taskContextScopeFixture(t)
	result, err := app.Dispatch(context.Background(), Action{Kind: "task.create", Payload: map[string]any{
		"title": "legacy-tool-choice", "workspaceToolIds": []string{"cursor"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := ResultData[domain.TaskManifest](result)
	if err != nil || manifest.TaskDirectoryName != "legacy-tool-choice" || len(manifest.WorkspaceToolLaunches) != 0 || result.Text != "" {
		t.Fatalf("retired workspace tool changed creation outcome: %+v, %v", result, err)
	}
}
