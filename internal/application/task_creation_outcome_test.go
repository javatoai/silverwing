package application

import (
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

type outcomeLauncher struct {
	calls int
	err   error
	open  func()
}

func (*outcomeLauncher) Descriptor() workspace.TaskWorkspaceToolDescriptor {
	return workspace.TaskWorkspaceToolDescriptor{ID: "fixture", DisplayName: "Fixture"}
}
func (*outcomeLauncher) Availability(context.Context) workspace.TaskWorkspaceToolAvailability {
	return workspace.TaskWorkspaceToolAvailability{Available: true}
}
func (l *outcomeLauncher) Open(context.Context, workspace.TaskWorkspaceContext) error {
	l.calls++
	if l.open != nil {
		l.open()
	}
	return l.err
}

func TestCreatedTaskFollowUpFailuresDoNotBecomeCreationFailures(t *testing.T) {
	for _, mode := range []string{"success", "materials", "tool", "changed", "status-save", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			service := workspace.NewTaskApplicationService(nil, nil, storage.NewPaths(t.TempDir()))
			manifest := domain.DefaultTaskManifest()
			manifest.FolderName, manifest.TaskDirectoryName = "task", "task"
			manifest.CreatedAt, manifest.UpdatedAt = "2026-10-20T00:00:00Z", "2026-10-20T00:00:00Z"
			if mode == "materials" {
				reason := "authentication failed"
				manifest.RequirementMaterials = domain.RequirementMaterialsDirectory{Status: domain.MaterialsFailed, FailureReason: &reason}
			}
			if err := service.Manifests.Save(root, manifest); err != nil {
				t.Fatal(err)
			}
			launcher := &outcomeLauncher{}
			if mode == "tool" {
				launcher.err = errors.New("fixture cannot open")
			}
			if mode == "changed" {
				changed := manifest
				changed.CreatedAt = "2026-10-21T00:00:00Z"
				if err := service.Manifests.Save(root, changed); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "status-save" {
				launcher.open = func() {
					name := filepath.Join(root, storage.TaskManifestFileName)
					if err := os.Rename(name, name+".preserved"); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(name, 0700); err != nil {
						t.Fatal(err)
					}
				}
			}
			registry, err := workspace.NewTaskWorkspaceToolRegistry([]workspace.TaskWorkspaceToolLauncher{launcher})
			if err != nil {
				t.Fatal(err)
			}
			service.WorkspaceTools.Registry = registry
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancelled" {
				cancel()
			}
			var final workspace.TaskCreationProgress
			result := finishCreatedTask(ctx, service, root, manifest, []string{"fixture"}, func(event workspace.TaskCreationProgress) { final = event })
			returned, ok := result.Data.(domain.TaskManifest)
			if !ok || returned.TaskDirectoryName != "task" || returned.CreatedAt != manifest.CreatedAt || result.Message != "任务已创建" || final.Phase != workspace.TaskCreationPhaseCompleted {
				t.Fatalf("lost committed task: %+v final=%+v", result, final)
			}
			if mode == "success" && result.Text != "" {
				t.Fatal(result.Text)
			}
			if mode != "success" && result.Text == "" {
				t.Fatal("follow-up warning missing")
			}
			if strings.Contains(result.Text, "创建未完成") {
				t.Fatal("follow-up was reported as a create failure")
			}
			if mode == "changed" && launcher.calls != 0 {
				t.Fatal("opened replaced task")
			}
		})
	}
}
