package workspace

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

type normalizationLauncher struct {
	calls        int
	err          error
	availability func()
}

func (*normalizationLauncher) Descriptor() TaskWorkspaceToolDescriptor {
	return TaskWorkspaceToolDescriptor{ID: "fixture", DisplayName: "Fixture"}
}
func (l *normalizationLauncher) Availability(context.Context) TaskWorkspaceToolAvailability {
	if l.availability != nil {
		l.availability()
	}
	return TaskWorkspaceToolAvailability{Available: true}
}

func TestToolLaunchRejectsTaskRebuiltWhileDiscoveringTool(t *testing.T) {
	service, root, manifest, launcher := launchNormalizationFixture(t)
	launcher.availability = func() {
		changed := domain.NormalizeTaskManifest(manifest)
		changed.CreatedAt = "2026-10-21T00:00:00Z"
		if err := service.Manifests.Save(root, changed); err != nil {
			t.Fatal(err)
		}
	}
	_, err := service.Launch(context.Background(), root, manifest, []string{"fixture"})
	if err == nil || launcher.calls != 0 {
		t.Fatalf("opened a task replaced during discovery: %v calls=%d", err, launcher.calls)
	}
	stored, err := service.Manifests.Load(root)
	if err != nil || stored.CreatedAt == manifest.CreatedAt {
		t.Fatal("launch overwrote the replacement manifest")
	}
}
func (l *normalizationLauncher) Open(context.Context, TaskWorkspaceContext) error {
	l.calls++
	return l.err
}

func launchNormalizationFixture(t *testing.T) (*WorkspaceToolLaunchService, string, domain.TaskManifest, *normalizationLauncher) {
	t.Helper()
	root := t.TempDir()
	manifest := domain.DefaultTaskManifest()
	manifest.FolderName, manifest.TaskDirectoryName = "task", "task"
	manifest.CreatedAt, manifest.UpdatedAt = "2026-10-20T00:00:00Z", "2026-10-20T00:00:00Z"
	for _, id := range []string{"data-center", "operation-app", "operation-center"} {
		manifest.Services = append(manifest.Services, domain.ServiceWorkspace{RepositoryID: id, ServiceName: id, RepositoryPath: root, WorktreePath: filepath.Join(root, id), DevelopmentTool: domain.DevelopmentToolIntelliJIdea, Health: domain.WorkspaceReady, Strategy: domain.WorkspaceStandardWorktree, ModuleSource: domain.TaskModuleConfigured})
	}
	store := storage.NewManifestStore()
	if err := store.Save(root, manifest); err != nil {
		t.Fatal(err)
	}
	launcher := &normalizationLauncher{}
	registry, err := NewTaskWorkspaceToolRegistry([]TaskWorkspaceToolLauncher{launcher})
	if err != nil {
		t.Fatal(err)
	}
	return NewWorkspaceToolLaunchService(registry, store, NewOperationLocks(t.TempDir())), root, manifest, launcher
}

func TestToolLaunchAcceptsSavedEmptyWarningLists(t *testing.T) {
	service, root, manifest, launcher := launchNormalizationFixture(t)
	updated, err := service.Launch(context.Background(), root, manifest, []string{"fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if launcher.calls != 1 || len(updated.WorkspaceToolLaunches) != 1 || updated.WorkspaceToolLaunches[0].Status != domain.WorkspaceToolOpened {
		t.Fatalf("tool not opened: calls=%d manifest=%+v", launcher.calls, updated)
	}
}

func TestToolLaunchRejectsRealManifestChanges(t *testing.T) {
	for _, field := range []string{"branch", "createdAt", "warnings"} {
		t.Run(field, func(t *testing.T) {
			service, root, manifest, launcher := launchNormalizationFixture(t)
			changed := domain.NormalizeTaskManifest(manifest)
			switch field {
			case "branch":
				changed.FeatureBranch = "changed"
			case "createdAt":
				changed.CreatedAt = "2026-10-21T00:00:00Z"
			case "warnings":
				changed.Services[0].Warnings = []string{"new warning"}
			}
			if err := service.Manifests.Save(root, changed); err != nil {
				t.Fatal(err)
			}
			_, err := service.Launch(context.Background(), root, manifest, []string{"fixture"})
			if err == nil || !strings.Contains(err.Error(), "任务清单已发生变化") || launcher.calls != 0 {
				t.Fatalf("stale manifest accepted: %v calls=%d", err, launcher.calls)
			}
		})
	}
}

func TestToolLaunchFailureRemainsRetryable(t *testing.T) {
	service, root, manifest, launcher := launchNormalizationFixture(t)
	launcher.err = errors.New("fixture unavailable")
	updated, err := service.Launch(context.Background(), root, manifest, []string{"fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.WorkspaceToolLaunches[0].Status != domain.WorkspaceToolFailed {
		t.Fatal("failure not persisted")
	}
	launcher.err = nil
	updated, err = service.Retry(context.Background(), root, updated, "fixture")
	if err != nil || updated.WorkspaceToolLaunches[0].Status != domain.WorkspaceToolOpened || launcher.calls != 2 {
		t.Fatalf("retry failed: %v %+v", err, updated)
	}
}
