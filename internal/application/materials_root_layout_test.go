package application

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
	"silverwing-go/internal/systemtrash"
	"silverwing-go/internal/workspace"
)

func TestMaterialsRootSavePreservesExactPathAndDoesNotOverwrite(t *testing.T) {
	paths := storage.NewPaths(t.TempDir())
	a := &App{Paths: paths, Config: storage.NewConfigStore(paths), Manifests: storage.NewManifestStore()}
	config, err := a.Config.Load()
	if err != nil {
		t.Fatal(err)
	}
	settings := domain.DefaultMaterialsGitSyncSettings()
	settings.RepositoryURL = "https://example.invalid/materials.git"
	settings.Enabled = true
	config.MaterialsGitSync = &settings
	if err := a.Config.Save(config); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "requirements")
	for _, selected := range []string{root, root + string(filepath.Separator)} {
		result, err := a.saveMaterialsRoot(Action{Path: selected})
		if err != nil || result.Path != root {
			t.Fatalf("save %s: %+v %v", selected, result, err)
		}
		current, err := a.Config.Load()
		if err != nil || current.RequirementMaterialsRoot == nil || *current.RequirementMaterialsRoot != root || current.MaterialsGitSettings().Enabled {
			t.Fatalf("saved root or pause state: %+v %v", current.RequirementMaterialsRoot, err)
		}
		if info, err := os.Stat(filepath.Join(root, "requirements")); err != nil || !info.IsDir() {
			t.Fatalf("collection not created: %v", err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "requirements", "requirements")); !os.IsNotExist(err) {
		t.Fatal("duplicated collection")
	}
	blocked := t.TempDir()
	if err := os.WriteFile(filepath.Join(blocked, "requirements"), []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.saveMaterialsRoot(Action{Path: blocked}); err == nil || !strings.Contains(err.Error(), filepath.Join(blocked, "requirements")) {
		t.Fatalf("bad collection layout diagnostic: %v", err)
	}
	if current, err := a.Config.Load(); err != nil || *current.RequirementMaterialsRoot != root {
		t.Fatalf("failed save changed config: %v", err)
	}
}

func TestTaskDeleteActionCarriesMaterialsApproval(t *testing.T) {
	request := TaskDeleteRequestFromAction(Action{Kind: "task.delete", TaskID: "task", Payload: map[string]any{"deleteMaterials": true, "materialsGuard": "captured-directory", "forceGuard": "force-preview"}})
	if !request.DeleteMaterials || request.MaterialsGuard != "captured-directory" || request.ForceGuard != "force-preview" || request.ForceDiscard {
		t.Fatal("separate task and materials approvals were not preserved")
	}
	if TaskDeleteRequestFromAction(Action{}).DeleteMaterials {
		t.Fatal("materials enabled by default")
	}
}

func TestMaterialsGitDoesNotBlockLocalWritesWithoutGlobalRoot(t *testing.T) {
	paths := storage.NewPaths(t.TempDir())
	a := &App{Paths: paths, Config: storage.NewConfigStore(paths)}
	config := domain.DefaultAppConfig()
	config.TaskRoot = &paths.Tasks
	config.RequirementMaterialsRoot = nil
	if MaterialsGitScope(paths.Home, config).Root != "" {
		t.Fatal("missing root resolved to a repository")
	}
	if err := a.Config.Save(config); err != nil {
		t.Fatal(err)
	}
	release, err := a.lockMaterialsGitDocumentMutation("document.save", filepath.Join(paths.Tasks, "task", ".workspace", "materials"))
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestTaskDeleteRiskResponseContainsAuthoritativeMaterials(t *testing.T) {
	a, scope, task := annualApplicationFixture(t)
	result, err := a.Dispatch(context.Background(), Action{Kind: "task.delete-risks", TaskID: filepath.Base(task), Path: task})
	if err != nil {
		t.Fatal(err)
	}
	preview, ok := result.Data.(workspace.TaskDeletePreview)
	if !ok || !preview.Materials.CanDelete || preview.Materials.Root != scope.Root || preview.Materials.Guard == "" {
		t.Fatalf("bad preview: %+v", result.Data)
	}
}

type applicationFixtureRecycler struct {
	root  string
	calls []string
}

func (r *applicationFixtureRecycler) Recycle(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	rel, err := filepath.Rel(r.root, path)
	if err != nil || rel == ".." || filepath.IsAbs(rel) || len(rel) > 3 && rel[:3] == ".."+string(filepath.Separator) {
		return os.ErrPermission
	}
	bin := filepath.Join(r.root, "fixture-recycle")
	if err := os.MkdirAll(bin, 0700); err != nil {
		return err
	}
	r.calls = append(r.calls, path)
	return os.Rename(path, filepath.Join(bin, filepath.Base(path)))
}
func TestTaskDeleteScopedMaterialsApprovalSurvivesItsOwnRecycle(t *testing.T) {
	a, scope, task := annualApplicationFixture(t)
	recycler := &applicationFixtureRecycler{root: a.Paths.Home}
	a.directoryRecycler = recycler
	config, err := a.Config.Load()
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := a.Manifests.Load(task)
	if err != nil {
		t.Fatal(err)
	}
	manifest.GroupID = config.Groups[0].ID
	if err := a.Manifests.Save(task, manifest); err != nil {
		t.Fatal(err)
	}
	preview, err := a.taskService(config).InspectDeletion(context.Background(), config, task)
	if err != nil || !preview.Materials.CanDelete {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	action := taskContextActionForTest(t, a, config, manifest, task, "task.delete")
	result, err := a.DeleteTask(context.Background(), TaskDeleteRequest{Target: TaskTargetFromAction(action), DeleteMaterials: true, MaterialsGuard: preview.Materials.Guard})
	if err != nil || result.Message != "任务已移入"+systemtrash.Label() {
		t.Fatalf("own move invalidated guard: %+v %v", result, err)
	}
	if len(recycler.calls) != 2 || recycler.calls[0] != scope.Root || recycler.calls[1] != task {
		t.Fatalf("wrong order: %v", recycler.calls)
	}
}
