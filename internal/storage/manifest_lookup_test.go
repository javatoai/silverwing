package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
)

func writeLookupTask(tb testing.TB, store *ManifestStore, root, id string) domain.TaskManifest {
	tb.Helper()
	manifest := domain.DefaultTaskManifest()
	manifest.TaskDirectoryName, manifest.FolderName = id, id
	manifest.FeatureBranch, manifest.CreatedAt, manifest.UpdatedAt = "feature/fixture", "2026-10-29T00:00:00Z", "2026-10-29T00:00:00Z"
	if err := store.Save(filepath.Join(root, id), manifest); err != nil {
		tb.Fatal(err)
	}
	return manifest
}

func TestManifestLookupRereadsExternalEditsAndIgnoresNeighbours(t *testing.T) {
	root := t.TempDir()
	store := NewManifestStore()
	manifest := writeLookupTask(t, store, root, "chosen")
	if err := os.Mkdir(filepath.Join(root, "broken"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "broken", TaskManifestFileName), []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	entry, err := store.LoadTask(root, "chosen")
	if err != nil || entry.Path != filepath.Join(root, "chosen") || entry.Manifest.TaskDirectoryName != "chosen" {
		t.Fatalf("point read = %+v, %v", entry, err)
	}
	manifest.FolderName, manifest.CreatedAt = "external change", "2026-10-29 21:00:00"
	if err := store.Save(entry.Path, manifest); err != nil {
		t.Fatal(err)
	}
	updated, err := store.LoadTask(root, "chosen")
	if err != nil || updated.Manifest.CreatedAt != manifest.CreatedAt || updated.Manifest.FolderName != manifest.FolderName {
		t.Fatalf("point read hid external replacement: %+v, %v", updated, err)
	}
	manifest.TaskDirectoryName = "different"
	if err := store.Save(entry.Path, manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadTask(root, "chosen"); err == nil {
		t.Fatal("accepted a manifest whose directory identity differs")
	}
}

func captureManifestLookupGuard(t *testing.T, taskRoot, taskID string) manifestLookupGuard {
	t.Helper()
	root, err := os.OpenRoot(taskRoot)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	task, err := root.OpenRoot(taskID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = task.Close() })
	rootInfo, err := root.Stat(".")
	if err != nil {
		t.Fatal(err)
	}
	taskInfo, err := root.Lstat(taskID)
	if err != nil {
		t.Fatal(err)
	}
	manifestInfo, err := task.Lstat(TaskManifestFileName)
	if err != nil {
		t.Fatal(err)
	}
	guard := manifestLookupGuard{taskRoot, taskID, root, task, rootInfo, taskInfo, manifestInfo}
	if !guard.unchanged() {
		t.Fatal("freshly pinned fixture was already stale")
	}
	return guard
}

func TestManifestLookupGuardRejectsTaskAndManifestReplacement(t *testing.T) {
	for _, replaced := range []string{"task", "manifest"} {
		t.Run(replaced, func(t *testing.T) {
			root := t.TempDir()
			store := NewManifestStore()
			manifest := writeLookupTask(t, store, root, "chosen")
			guard := captureManifestLookupGuard(t, root, "chosen")
			if replaced == "task" {
				if err := os.Rename(filepath.Join(root, "chosen"), filepath.Join(root, "retired")); err != nil {
					t.Fatal(err)
				}
			}
			manifest.CreatedAt, manifest.FolderName = "2026-10-30T00:00:00Z", "replacement"
			if err := store.Save(filepath.Join(root, "chosen"), manifest); err != nil {
				t.Fatal(err)
			}
			if guard.unchanged() {
				t.Fatal("post-read guard accepted a replaced " + replaced)
			}
			entry, err := store.LoadTask(root, "chosen")
			if err != nil || entry.Manifest.CreatedAt != manifest.CreatedAt || entry.Manifest.FolderName != manifest.FolderName {
				t.Fatalf("next read did not adopt the current task identity: %+v, %v", entry, err)
			}
		})
	}
}

func TestManifestLookupRejectsTraversalAndUnsupportedSchema(t *testing.T) {
	root := t.TempDir()
	store := NewManifestStore()
	writeLookupTask(t, store, root, "chosen")
	for _, id := range []string{"", ".", "..", "../chosen", "nested/chosen", `nested\chosen`, "chosen\x00suffix"} {
		if _, err := store.LoadTask(root, id); err == nil {
			t.Fatalf("accepted task ID %q", id)
		}
	}
	path := filepath.Join(root, "chosen", TaskManifestFileName)
	if err := os.WriteFile(path, []byte(`{"schemaVersion":"999"}`), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := store.LoadTask(root, "chosen")
	var unsupported *UnsupportedManifestVersionError
	if !errors.As(err, &unsupported) {
		t.Fatalf("lost schema validation: %v", err)
	}
	if _, err := store.LoadTask(root, "missing"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing point lookup = %v", err)
	}
}

func TestManifestLookupAllowsRootAliasButRejectsChildAndManifestLinks(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "tasks")
	store := NewManifestStore()
	writeLookupTask(t, store, root, "chosen")
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Skipf("directory links unavailable: %v", err)
	}
	entry, err := store.LoadTask(alias, "chosen")
	if err != nil || entry.Path != filepath.Join(alias, "chosen") {
		t.Fatalf("configured root alias lost its lexical path: %+v, %v", entry, err)
	}
	if err := os.Symlink(filepath.Join(root, "chosen"), filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadTask(root, "linked"); err == nil {
		t.Fatal("accepted linked child task")
	}
	manifestPath := filepath.Join(root, "chosen", TaskManifestFileName)
	saved := filepath.Join(base, "saved.json")
	if err := os.Rename(manifestPath, saved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(saved, manifestPath); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadTask(root, "chosen"); err == nil {
		t.Fatal("accepted linked manifest")
	}
}

func BenchmarkManifestTaskLookup1000(b *testing.B) {
	root := b.TempDir()
	store := NewManifestStore()
	for index := 0; index < 1000; index++ {
		writeLookupTask(b, store, root, fmt.Sprintf("task-%04d", index))
	}
	b.Run("point", func(b *testing.B) {
		b.ReportAllocs()
		for index := 0; index < b.N; index++ {
			entry, err := store.LoadTask(root, "task-0500")
			if err != nil || !strings.HasSuffix(entry.Path, "task-0500") {
				b.Fatal(entry, err)
			}
		}
	})
	b.Run("scan", func(b *testing.B) {
		b.ReportAllocs()
		for index := 0; index < b.N; index++ {
			result, err := store.Scan(root)
			if err != nil || len(result.Current) != 1000 {
				b.Fatal(len(result.Current), err)
			}
		}
	})
}
