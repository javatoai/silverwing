package storage

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"silverwing-go/internal/domain"
)

func TestProfileManifestSeparatesLocalAndManagedMaterials(t *testing.T) {
	base := t.TempDir()
	paths := NewPaths(filepath.Join(base, "conf"))
	if err := os.Mkdir(paths.Home, 0o755); err != nil {
		t.Fatal(err)
	}
	store := NewProfileManifestStore(paths)
	tasks := filepath.Join(base, "external-tasks")
	localTask := filepath.Join(tasks, "local")
	local := filepath.Join(localTask, ".workspace", "materials", "docs")
	managedTask := filepath.Join(tasks, "managed")
	managedRelative := "materials/requirements/2026/7000000001/docs"
	managed := filepath.Join(paths.Home, filepath.FromSlash(managedRelative))
	for _, fixture := range []struct{ task, root, stored string }{
		{localTask, local, "@task/.workspace/materials/docs"},
		{managedTask, managed, managedRelative},
	} {
		manifest := writeLookupTask(t, store, tasks, filepath.Base(fixture.task))
		manifest.RequirementMaterials = domain.RequirementMaterialsDirectory{Status: domain.MaterialsReady, WriteRoot: &fixture.root}
		if err := store.Save(fixture.task, manifest); err != nil {
			t.Fatal(err)
		}
		if got := storedManifestMaterialsRoot(t, fixture.task); got != fixture.stored {
			t.Fatalf("persisted materials path = %q, want %q", got, fixture.stored)
		}
	}
	assertMaterialsAcrossManifestReaders(t, store, localTask, local)
	assertMaterialsAcrossManifestReaders(t, store, managedTask, managed)
	movedPaths := NewPaths(filepath.Join(base, "moved-conf"))
	movedTasks := filepath.Join(base, "moved-external-tasks")
	if err := os.Rename(paths.Home, movedPaths.Home); err != nil {
		t.Fatal(err)
	}
	movedStore := NewProfileManifestStore(movedPaths)
	assertMaterialsAcrossManifestReaders(t, movedStore, localTask, local)
	assertMaterialsAcrossManifestReaders(t, movedStore, managedTask, filepath.Join(movedPaths.Home, filepath.FromSlash(managedRelative)))
	if err := os.Rename(tasks, movedTasks); err != nil {
		t.Fatal(err)
	}
	movedLocalTask := filepath.Join(movedTasks, "local")
	assertMaterialsAcrossManifestReaders(t, movedStore, movedLocalTask, filepath.Join(movedLocalTask, ".workspace", "materials", "docs"))
	assertMaterialsAcrossManifestReaders(t, movedStore, filepath.Join(movedTasks, "managed"), filepath.Join(movedPaths.Home, filepath.FromSlash(managedRelative)))
}

func TestProfileManifestRejectsInvalidMaterialsPathsAndOldSchema(t *testing.T) {
	base := t.TempDir()
	store := NewProfileManifestStore(NewPaths(filepath.Join(base, "conf")))
	tasks := filepath.Join(base, "external-tasks")
	task := filepath.Join(tasks, "chosen")
	manifest := writeLookupTask(t, store, tasks, "chosen")
	manifest.RequirementMaterials.Status = domain.MaterialsReady
	for _, invalid := range []string{
		"@task", "@TASK/.workspace/materials/docs", `@task\.workspace\materials\docs`,
		"@task/.workspace/materials/docs/../temp", "@task/../materials", "./@task/.workspace/materials/docs",
		"../external-tasks/chosen/.workspace/materials/docs", filepath.Join(base, "outside-task"),
	} {
		t.Run(invalid, func(t *testing.T) {
			manifest.RequirementMaterials.WriteRoot = &invalid
			if err := store.Save(task, manifest); err == nil {
				t.Fatal("saved invalid materials path")
			}
			writeRawManifestForMaterialsTest(t, task, manifest)
			if _, err := store.Load(task); err == nil {
				t.Fatal("loaded invalid materials path")
			}
			if _, err := store.LoadTask(tasks, "chosen"); err == nil {
				t.Fatal("point lookup accepted invalid materials path")
			}
			scan, err := store.Scan(tasks)
			if err != nil || len(scan.Current) != 0 || scan.Failures[task] == "" {
				t.Fatalf("scan accepted invalid materials path: %+v, %v", scan, err)
			}
		})
	}
	manifest.SchemaVersion = "2.0.0"
	writeRawManifestForMaterialsTest(t, task, manifest)
	var unsupported *UnsupportedManifestVersionError
	if _, err := store.LoadTask(tasks, "chosen"); !errors.As(err, &unsupported) {
		t.Fatalf("point lookup did not reject v2: %v", err)
	}
	scan, err := store.Scan(tasks)
	if err != nil || len(scan.UnsupportedPaths) != 1 || scan.UnsupportedPaths[0] != task {
		t.Fatalf("scan did not reject v2: %+v, %v", scan, err)
	}
}

func storedManifestMaterialsRoot(t *testing.T, task string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(task, TaskManifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	var manifest domain.TaskManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.RequirementMaterials.WriteRoot == nil {
		t.Fatal("persisted manifest has no materials path")
	}
	return *manifest.RequirementMaterials.WriteRoot
}

func writeRawManifestForMaterialsTest(t *testing.T, task string, manifest domain.TaskManifest) {
	t.Helper()
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(task, TaskManifestFileName), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertMaterialsAcrossManifestReaders(t *testing.T, store *ManifestStore, task, want string) {
	t.Helper()
	manifest, err := store.Load(task)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := store.LoadTask(filepath.Dir(task), filepath.Base(task))
	if err != nil {
		t.Fatal(err)
	}
	scan, err := store.Scan(filepath.Dir(task))
	if err != nil {
		t.Fatal(err)
	}
	for _, scanned := range scan.Current {
		if scanned.Path != task {
			continue
		}
		for _, observed := range []domain.TaskManifest{manifest, entry.Manifest, scanned.Manifest} {
			assertProfileConfigPath(t, observed.RequirementMaterials.WriteRoot, want)
		}
		return
	}
	t.Fatalf("scan omitted task %s: %+v", task, scan)
}
