package application

import (
	"context"
	"errors"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

type scopedOpenTool struct {
	calls     int
	directory string
	available func()
}

func (*scopedOpenTool) Descriptor() workspace.TaskWorkspaceToolDescriptor {
	return workspace.TaskWorkspaceToolDescriptor{ID: "cursor", DisplayName: "Cursor"}
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
			action.Payload["toolIds"] = []string{"cursor"}
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
			updated, err := service.LaunchWorkspaceTools(ctx, path, manifest, []string{"cursor"})
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
