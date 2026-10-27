package application

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
	"silverwing-go/internal/taskscope"
	"silverwing-go/internal/workspace"
)

func taskContextScopeFixture(t *testing.T) (*App, domain.AppConfig, domain.TaskManifest, string) {
	t.Helper()
	paths := storage.NewPaths(t.TempDir())
	app := &App{Paths: paths, Config: storage.NewConfigStore(paths), Manifests: storage.NewManifestStore()}
	config := domain.DefaultAppConfig()
	config.TaskRoot = &paths.Tasks
	materialsRoot := filepath.Join(paths.Home, "materials", "2026", "123-需求", "requirements")
	materialsBase := filepath.Join(paths.Home, "materials")
	materialsSubdirectory := "requirements"
	config.RequirementMaterialsRoot = &materialsBase
	config.RequirementMaterialsSubdirectory = &materialsSubdirectory
	if err := os.MkdirAll(materialsRoot, 0755); err != nil {
		t.Fatal(err)
	}
	if err := app.Config.Save(config); err != nil {
		t.Fatal(err)
	}
	manifest := domain.DefaultTaskManifest()
	manifest.TaskDirectoryName = "task"
	manifest.FolderName = "Task"
	manifest.FeatureBranch = "feature/task"
	manifest.CreatedAt = "2026-10-19T10:00:00Z"
	manifest.RequirementLink = "https://project.feishu.cn/demo/userstory/detail/123"
	manifest.RequirementMaterials = domain.RequirementMaterialsDirectory{
		Status: domain.MaterialsReady, WriteRoot: &materialsRoot,
	}
	manifestPath := filepath.Join(*config.TaskRoot, manifest.TaskDirectoryName)
	if err := app.Manifests.Save(manifestPath, manifest); err != nil {
		t.Fatal(err)
	}
	return app, config, manifest, manifestPath
}

func taskContextActionForTest(t *testing.T, app *App, config domain.AppConfig, manifest domain.TaskManifest, path, kind string) Action {
	t.Helper()
	encoded, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	materialsPath := ""
	if manifest.RequirementMaterials.WriteRoot != nil {
		materialsPath = visibleMaterialsRoot(*manifest.RequirementMaterials.WriteRoot)
	}
	materialsStatus := string(manifest.RequirementMaterials.Status)
	if materialsStatus == "" {
		materialsStatus = string(domain.MaterialsNotRequested)
		if materialsPath != "" {
			materialsStatus = string(domain.MaterialsReady)
		}
	}
	archived := manifest.LifecycleStatus == domain.TaskArchived
	page := "tasks"
	if archived {
		page = "archive"
	}
	action := Action{Kind: kind, TaskID: manifest.TaskDirectoryName}
	switch kind {
	case "codex.run":
		action.Path = path
	case "system.open":
		action.Path = materialsPath
		action.Payload = map[string]any{"desktopSystemOpenDirectory": true}
	case "task.archive":
		action.Path = path
		action.Payload = map[string]any{"cleanup": false}
	case "task.restore":
		action.Path = path
	case "task.delete":
		action.Path = path
		action.Payload = map[string]any{"forceDiscard": false, "taskDeleteCreatedAt": manifest.CreatedAt}
	}
	action.Payload = clonePayload(action.Payload)
	action.Payload[desktopTaskContextPayloadKey] = map[string]any{
		"taskId": manifest.TaskDirectoryName, "taskPath": path, "taskCreatedAt": manifest.CreatedAt,
		"home": app.Paths.Home, "taskRoot": *config.TaskRoot, "page": page,
		"archived": archived, "configFingerprint": taskContextConfigFingerprint(string(encoded)),
		"groupId": manifest.GroupID, "branch": manifest.FeatureBranch,
		"materialsPath": materialsPath, "materialsStatus": materialsStatus,
		"materialsFingerprint": taskMaterialsFingerprint(manifest),
	}
	return action
}

func TestDesktopTaskContextPreservesLegacyMaterialsStatusProjection(t *testing.T) {
	app, config, manifest, path := taskContextScopeFixture(t)
	for _, withPath := range []bool{false, true} {
		t.Run(map[bool]string{false: "without-path", true: "with-path"}[withPath], func(t *testing.T) {
			legacy := manifest
			legacy.RequirementMaterials.Status = ""
			if !withPath {
				legacy.RequirementMaterials.WriteRoot = nil
			}
			action := taskContextActionForTest(t, app, config, legacy, path, "task.materials-retry")
			if err := app.validateDesktopTaskContext(action, config, legacy, path); err != nil {
				t.Fatalf("legacy materials projection rejected: %v", err)
			}
		})
	}
}

func clonePayload(payload map[string]any) map[string]any {
	cloned := make(map[string]any, len(payload)+1)
	for key, value := range payload {
		cloned[key] = value
	}
	return cloned
}

func TestDesktopTaskContextValidatesFreshTaskAndConfiguration(t *testing.T) {
	app, config, manifest, path := taskContextScopeFixture(t)
	for _, kind := range []string{"codex.run", "task.materials-retry", "system.open"} {
		t.Run(kind, func(t *testing.T) {
			action := taskContextActionForTest(t, app, config, manifest, path, kind)
			if err := app.validateDesktopTaskContext(action, config, manifest, path); err != nil {
				t.Fatalf("unchanged task context rejected: %v", err)
			}
			replacement := manifest
			replacement.CreatedAt += "-replacement"
			if err := app.validateDesktopTaskContext(action, config, replacement, path); !errors.Is(err, errDesktopTaskContextChanged) {
				t.Fatalf("same path replacement accepted: %v", err)
			}
			changedMaterials := manifest
			newRoot := *manifest.RequirementMaterials.WriteRoot + "-changed"
			changedMaterials.RequirementMaterials.WriteRoot = &newRoot
			if err := app.validateDesktopTaskContext(action, config, changedMaterials, path); !errors.Is(err, errDesktopTaskContextChanged) {
				t.Fatalf("changed material association accepted: %v", err)
			}
			changedConfig := config
			newMaterialsRoot := filepath.Join(app.Paths.Home, "different-materials")
			changedConfig.RequirementMaterialsRoot = &newMaterialsRoot
			if err := app.validateDesktopTaskContext(action, changedConfig, manifest, path); !errors.Is(err, errDesktopTaskContextChanged) {
				t.Fatalf("changed configuration accepted: %v", err)
			}
		})
	}
}

func TestDesktopTaskContextDispatchRejectsQueuedReplacementBeforeSideEffects(t *testing.T) {
	for _, kind := range []string{"codex.run", "task.materials-retry", "system.open"} {
		t.Run(kind, func(t *testing.T) {
			app, config, manifest, path := taskContextScopeFixture(t)
			action := taskContextActionForTest(t, app, config, manifest, path, kind)
			replacement := manifest
			replacement.CreatedAt += "-replacement"
			if err := app.Manifests.Save(path, replacement); err != nil {
				t.Fatal(err)
			}
			_, err := app.Dispatch(context.Background(), action)
			if !errors.Is(err, errDesktopTaskContextChanged) {
				t.Fatalf("queued action reached its side-effect path: %v", err)
			}
		})
	}
}

func runTaskContextMutationForTest(service *workspace.TaskApplicationService, ctx context.Context, config domain.AppConfig, path, kind string) error {
	switch kind {
	case "task.materials-retry":
		_, err := service.RetryRequirementMaterials(ctx, config, path)
		return err
	case "task.archive":
		_, err := service.Archive(ctx, config, path, false)
		return err
	case "task.restore":
		_, err := service.Restore(ctx, config, path)
		return err
	case "task.delete":
		return service.Delete(ctx, config, path, false)
	default:
		return errors.New("unsupported task mutation test action")
	}
}

func TestDesktopTaskMutationRechecksContextAfterTakingTaskLock(t *testing.T) {
	run := func(kind, drift string) {
		t.Run(kind+"/"+drift, func(t *testing.T) {
			app, config, manifest, path := taskContextScopeFixture(t)
			if kind == "task.restore" {
				manifest.LifecycleStatus = domain.TaskArchived
				if err := app.Manifests.Save(path, manifest); err != nil {
					t.Fatal(err)
				}
			}
			action := taskContextActionForTest(t, app, config, manifest, path, kind)
			if err := app.validateDesktopTaskContext(action, config, manifest, path); err != nil {
				t.Fatalf("initial UI-context validation failed: %v", err)
			}
			ctx := app.desktopTaskMutationContext(context.Background(), action)
			service := app.taskService(config)
			resolverCalls := 0
			service.RequirementMaterials = func(_ context.Context, _ domain.AppConfig, _ string, _ string) (domain.RequirementMaterialsDirectory, *string, error) {
				resolverCalls++
				return manifest.RequirementMaterials, manifest.RequirementID, nil
			}

			expectedManifest := manifest
			changedConfig := config
			switch drift {
			case "created-at":
				expectedManifest.CreatedAt += "-replacement"
				if err := app.Manifests.Save(path, expectedManifest); err != nil {
					t.Fatal(err)
				}
			case "materials":
				changedRoot := *manifest.RequirementMaterials.WriteRoot + "-replacement"
				expectedManifest.RequirementMaterials.WriteRoot = &changedRoot
				if err := app.Manifests.Save(path, expectedManifest); err != nil {
					t.Fatal(err)
				}
			case "configuration":
				changedRoot := filepath.Join(app.Paths.Home, "changed-materials")
				changedConfig.RequirementMaterialsRoot = &changedRoot
				if err := app.Config.Save(changedConfig); err != nil {
					t.Fatal(err)
				}
			}

			if err := runTaskContextMutationForTest(service, ctx, config, path, kind); !errors.Is(err, errDesktopTaskContextChanged) {
				t.Fatalf("stale %s context reached its mutation: %v", drift, err)
			}
			if resolverCalls != 0 {
				t.Fatal("stale materials retry reached its resolver")
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("stale %s action removed the task directory: %v", drift, err)
			}
			current, err := app.Manifests.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if current.CreatedAt != expectedManifest.CreatedAt || current.LifecycleStatus != expectedManifest.LifecycleStatus ||
				value(current.RequirementMaterials.WriteRoot) != value(expectedManifest.RequirementMaterials.WriteRoot) {
				t.Fatalf("stale %s action changed the current task: %+v", drift, current)
			}
		})
	}
	for _, kind := range []string{"task.materials-retry", "task.archive", "task.restore", "task.delete"} {
		run(kind, "created-at")
	}
	run("task.materials-retry", "materials")
	run("task.archive", "configuration")
}

func TestDesktopTaskMutationAllowsUnchangedContextAndPreservesLegacyActions(t *testing.T) {
	for _, kind := range []string{"task.materials-retry", "task.archive", "task.restore", "task.delete"} {
		t.Run(kind, func(t *testing.T) {
			app, config, manifest, path := taskContextScopeFixture(t)
			if kind == "task.restore" {
				manifest.LifecycleStatus = domain.TaskArchived
				if err := app.Manifests.Save(path, manifest); err != nil {
					t.Fatal(err)
				}
			}
			action := taskContextActionForTest(t, app, config, manifest, path, kind)
			if err := app.validateDesktopTaskContext(action, config, manifest, path); err != nil {
				t.Fatalf("valid UI context rejected before dispatch: %v", err)
			}
			ctx := app.desktopTaskMutationContext(context.Background(), action)
			service := app.taskService(config)
			resolverCalls := 0
			service.RequirementMaterials = func(_ context.Context, _ domain.AppConfig, _ string, _ string) (domain.RequirementMaterialsDirectory, *string, error) {
				resolverCalls++
				return manifest.RequirementMaterials, manifest.RequirementID, nil
			}
			if err := runTaskContextMutationForTest(service, ctx, config, path, kind); err != nil {
				t.Fatalf("unchanged captured context was rejected: %v", err)
			}
			if kind == "task.materials-retry" && resolverCalls != 1 {
				t.Fatalf("valid materials retry resolver calls=%d want 1", resolverCalls)
			}
			if kind == "task.delete" {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("valid delete did not remove its target: %v", err)
				}
				return
			}
			current, err := app.Manifests.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			wantArchived := kind == "task.archive"
			if (current.LifecycleStatus == domain.TaskArchived) != wantArchived {
				t.Fatalf("valid %s transition was not applied: %s", kind, current.LifecycleStatus)
			}
		})
	}

	t.Run("legacy archive restore and delete without private scope", func(t *testing.T) {
		app, config, manifest, path := taskContextScopeFixture(t)
		if _, err := app.Dispatch(context.Background(), Action{Kind: "task.archive", TaskID: manifest.TaskDirectoryName, Path: path}); err != nil {
			t.Fatalf("legacy archive behavior changed: %v", err)
		}
		if _, err := app.Dispatch(context.Background(), Action{Kind: "task.restore", TaskID: manifest.TaskDirectoryName, Path: path}); err != nil {
			t.Fatalf("legacy restore behavior changed: %v", err)
		}
		service := app.taskService(config)
		resolverCalls := 0
		service.RequirementMaterials = func(_ context.Context, _ domain.AppConfig, _ string, _ string) (domain.RequirementMaterialsDirectory, *string, error) {
			resolverCalls++
			return manifest.RequirementMaterials, manifest.RequirementID, nil
		}
		if _, err := service.RetryRequirementMaterials(context.Background(), config, path); err != nil {
			t.Fatalf("legacy materials retry behavior changed: %v", err)
		}
		if resolverCalls != 1 {
			t.Fatalf("legacy materials retry resolver calls=%d want 1", resolverCalls)
		}
		if _, err := app.Dispatch(context.Background(), Action{Kind: "task.delete", TaskID: manifest.TaskDirectoryName, Path: path}); err != nil {
			t.Fatalf("legacy delete behavior changed: %v", err)
		}
	})
}

func TestTaskDeleteCreatedAtIsRecheckedInsideTaskLockWithoutMenuScope(t *testing.T) {
	app, config, manifest, path := taskContextScopeFixture(t)
	action := Action{Kind: "task.delete", TaskID: manifest.TaskDirectoryName, Path: path,
		Payload: map[string]any{"taskDeleteCreatedAt": manifest.CreatedAt}}
	if err := app.validateDesktopTaskContext(action, config, manifest, path); err != nil {
		t.Fatalf("initial token validation failed: %v", err)
	}
	ctx := app.desktopTaskMutationContext(context.Background(), action)
	replacement := manifest
	replacement.CreatedAt += "-replacement"
	if err := app.Manifests.Save(path, replacement); err != nil {
		t.Fatal(err)
	}
	service := app.taskService(config)
	if err := service.Delete(ctx, config, path, false); !errors.Is(err, errDesktopTaskContextChanged) {
		t.Fatalf("stale taskDeleteCreatedAt reached delete: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stale taskDeleteCreatedAt removed its replacement: %v", err)
	}
	current, err := app.Manifests.Load(path)
	if err != nil || current.CreatedAt != replacement.CreatedAt {
		t.Fatalf("stale taskDeleteCreatedAt changed replacement identity: %+v %v", current, err)
	}
}

func TestTaskMutationScopeValidationRunsUnderLockAndHonorsCancellation(t *testing.T) {
	app, config, manifest, path := taskContextScopeFixture(t)
	service := app.taskService(config)
	rejected := errors.New("test precondition rejection")
	called := false
	ctx := taskscope.WithValidation(context.Background(), func(_ context.Context, _ domain.AppConfig, taskPath string, _ domain.TaskManifest) error {
		err := service.Locks.WithTask(taskPath, func() error { return nil })
		if !errors.Is(err, storage.ErrLockBusy) {
			return errors.New("task precondition ran without the task lock")
		}
		called = true
		return rejected
	})
	if err := runTaskContextMutationForTest(service, ctx, config, path, "task.archive"); !errors.Is(err, rejected) {
		t.Fatalf("task mutation did not preserve its locked precondition result: %v", err)
	}
	if !called {
		t.Fatal("task mutation precondition was not invoked under the lock")
	}
	current, err := app.Manifests.Load(path)
	if err != nil || current.LifecycleStatus != domain.TaskActive {
		t.Fatalf("rejected locked mutation changed task state: %+v %v", current, err)
	}

	canceled, cancel := context.WithCancel(app.desktopTaskMutationContext(context.Background(), taskContextActionForTest(t, app, config, manifest, path, "task.archive")))
	cancel()
	if err := runTaskContextMutationForTest(service, canceled, config, path, "task.archive"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled scoped mutation was not rejected: %v", err)
	}
	current, err = app.Manifests.Load(path)
	if err != nil || current.LifecycleStatus != domain.TaskActive {
		t.Fatalf("canceled mutation changed task state: %+v %v", current, err)
	}
}

func TestDesktopLifecycleContextRejectsRecreatedTasksBeforeMutation(t *testing.T) {
	for _, kind := range []string{"task.archive", "task.restore", "task.delete"} {
		t.Run(kind, func(t *testing.T) {
			app, config, manifest, path := taskContextScopeFixture(t)
			if kind == "task.restore" {
				manifest.LifecycleStatus = domain.TaskArchived
				if err := app.Manifests.Save(path, manifest); err != nil {
					t.Fatal(err)
				}
			}
			action := taskContextActionForTest(t, app, config, manifest, path, kind)
			replacement := manifest
			replacement.CreatedAt += "-replacement"
			if err := app.Manifests.Save(path, replacement); err != nil {
				t.Fatal(err)
			}
			if _, err := app.Dispatch(context.Background(), action); !errors.Is(err, errDesktopTaskContextChanged) {
				t.Fatalf("stale %s action was not rejected before mutation: %v", kind, err)
			}
			current, err := app.Manifests.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if current.CreatedAt != replacement.CreatedAt || current.LifecycleStatus != replacement.LifecycleStatus {
				t.Fatalf("stale %s action changed the replacement manifest: %+v", kind, current)
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("stale %s action removed the replacement task: %v", kind, err)
			}
		})
	}
}

func TestDesktopLifecycleContextRejectsChangedPathAndConfiguration(t *testing.T) {
	for _, kind := range []string{"task.archive", "task.restore", "task.delete"} {
		t.Run(kind, func(t *testing.T) {
			app, config, manifest, path := taskContextScopeFixture(t)
			if kind == "task.restore" {
				manifest.LifecycleStatus = domain.TaskArchived
				if err := app.Manifests.Save(path, manifest); err != nil {
					t.Fatal(err)
				}
			}
			action := taskContextActionForTest(t, app, config, manifest, path, kind)
			if err := app.validateDesktopTaskContext(action, config, manifest, path); err != nil {
				t.Fatalf("valid lifecycle context rejected: %v", err)
			}
			wrongPath := path + "-moved"
			if err := app.validateDesktopTaskContext(action, config, manifest, wrongPath); !errors.Is(err, errDesktopTaskContextChanged) {
				t.Fatalf("changed task path accepted: %v", err)
			}
			changedConfig := config
			newRoot := filepath.Join(app.Paths.Home, "different-materials")
			changedConfig.RequirementMaterialsRoot = &newRoot
			if err := app.validateDesktopTaskContext(action, changedConfig, manifest, path); !errors.Is(err, errDesktopTaskContextChanged) {
				t.Fatalf("changed application configuration accepted: %v", err)
			}
		})
	}
}

func TestDesktopArchiveContextSupportsExistingMaterialsRoute(t *testing.T) {
	app, config, manifest, path := taskContextScopeFixture(t)
	action := taskContextActionForTest(t, app, config, manifest, path, "task.archive")
	fields := action.Payload[desktopTaskContextPayloadKey].(map[string]any)
	fields["page"] = "materials"
	if err := app.validateDesktopTaskContext(action, config, manifest, path); err != nil {
		t.Fatalf("active materials-page archive was rejected: %v", err)
	}
	fields["page"] = "materials"
	action.Kind = "task.restore"
	if err := app.validateDesktopTaskContext(action, config, manifest, path); !errors.Is(err, errDesktopTaskContextChanged) {
		t.Fatalf("materials page was allowed for unrelated lifecycle action: %v", err)
	}
}

func TestTaskDeleteCreatedAtPreconditionWithoutPrivateScope(t *testing.T) {
	t.Run("supplied stale incarnation is rejected", func(t *testing.T) {
		app, _, manifest, path := taskContextScopeFixture(t)
		action := Action{Kind: "task.delete", TaskID: manifest.TaskDirectoryName, Path: path,
			Payload: map[string]any{"taskDeleteCreatedAt": manifest.CreatedAt}}
		replacement := manifest
		replacement.CreatedAt += "-replacement"
		if err := app.Manifests.Save(path, replacement); err != nil {
			t.Fatal(err)
		}
		if _, err := app.Dispatch(context.Background(), action); !errors.Is(err, errDesktopTaskContextChanged) {
			t.Fatalf("delete ignored taskDeleteCreatedAt: %v", err)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("stale plain delete removed the replacement task: %v", err)
		}
	})

	t.Run("omitted metadata retains ordinary delete behavior", func(t *testing.T) {
		app, _, manifest, path := taskContextScopeFixture(t)
		if _, err := app.Dispatch(context.Background(), Action{Kind: "task.delete", TaskID: manifest.TaskDirectoryName, Path: path}); err != nil {
			t.Fatalf("ordinary delete without optional precondition changed behavior: %v", err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("ordinary delete did not remove its target: %v", err)
		}
	})
}

func TestDesktopTaskContextAbsentPreservesOrdinaryActions(t *testing.T) {
	app, config, manifest, path := taskContextScopeFixture(t)
	if err := app.validateDesktopTaskContext(Action{Kind: "codex.run", TaskID: manifest.TaskDirectoryName, Path: path}, config, manifest, path); err != nil {
		t.Fatalf("ordinary action acquired a task-menu restriction: %v", err)
	}
}
