package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

func TestQATaskRootMigrationPreservesLocalFilesAndExternalMaterialsWithoutGit(t *testing.T) {
	for _, archived := range []bool{false, true} {
		t.Run(map[bool]string{false: "active", true: "archived"}[archived], func(t *testing.T) {
			paths := storage.NewPaths(t.TempDir())
			store := storage.NewConfigStore(paths)
			config, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			badGit := filepath.Join(paths.Home, "git-must-not-run.exe")
			config.GitExecutablePath = &badGit
			config.DefaultWorkspaceToolIDs = []string{}
			base, sub := filepath.Join(paths.Home, "materials"), "测试"
			config.RequirementMaterialsRoot, config.RequirementMaterialsSubdirectory = &base, &sub
			if err := store.Save(config); err != nil {
				t.Fatal(err)
			}
			service := NewTaskApplicationService(nil, NewGitClientWithConfig(nil, config), paths)
			ctx := context.Background()
			manifest, err := service.CreateTask(ctx, config, CreateTaskRequest{FolderName: "资料任务", TaskNotes: "需求与验收范围"})
			if err != nil {
				t.Fatal(err)
			}
			oldPath := filepath.Join(*config.TaskRoot, manifest.TaskDirectoryName)
			owned := filepath.Join(base, "2026", "123-资料任务", sub)
			if err := os.MkdirAll(owned, 0755); err != nil {
				t.Fatal(err)
			}
			external := filepath.Join(owned, "外部资料.txt")
			if err := os.WriteFile(external, []byte("保留外部资料"), 0600); err != nil {
				t.Fatal(err)
			}
			manifest.RequirementMaterials = domain.RequirementMaterialsDirectory{Status: domain.MaterialsReady, WriteRoot: &owned}
			if err := service.Manifests.Save(oldPath, manifest); err != nil {
				t.Fatal(err)
			}
			if archived {
				manifest, err = service.Archive(ctx, config, oldPath, false)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(oldPath, "本地记录.txt"), []byte("保留任务文件"), 0600); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(paths.Home, "moved-tasks")
			result, err := service.MigrateTaskRoot(ctx, config, target, nil)
			if err != nil {
				t.Fatal(err)
			}
			if result.MigratedTasks != 1 || len(result.CleanupFailures) != 0 || result.Config.TaskRoot == nil || *result.Config.TaskRoot != target {
				t.Fatalf("migration result: %+v", result)
			}
			next := filepath.Join(target, manifest.TaskDirectoryName)
			loaded, err := service.Manifests.Load(next)
			if err != nil {
				t.Fatal(err)
			}
			if loaded.CreatedAt != manifest.CreatedAt || loaded.LifecycleStatus != manifest.LifecycleStatus || loaded.RequirementMaterials.WriteRoot == nil || *loaded.RequirementMaterials.WriteRoot != owned {
				t.Fatalf("identity or material association changed: %+v", loaded)
			}
			if data, err := os.ReadFile(filepath.Join(next, "本地记录.txt")); err != nil || string(data) != "保留任务文件" {
				t.Fatalf("local file lost: %q %v", data, err)
			}
			if notes, err := service.Documents.ReadTaskNotes(next); err != nil || notes != "需求与验收范围" {
				t.Fatalf("notes lost: %q %v", notes, err)
			}
			if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
				t.Fatalf("old task retained unexpectedly: %v", err)
			}
			if err := service.Delete(ctx, result.Config, next, false); err != nil {
				t.Fatal(err)
			}
			if data, err := os.ReadFile(external); err != nil || string(data) != "保留外部资料" {
				t.Fatalf("external material deleted: %q %v", data, err)
			}
		})
	}
}
