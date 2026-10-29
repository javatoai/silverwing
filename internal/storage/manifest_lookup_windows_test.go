//go:build windows

package storage

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func manifestLookupJunction(t *testing.T, path, target string) {
	t.Helper()
	if output, err := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", path, target).CombinedOutput(); err != nil {
		t.Fatalf("isolated junction fixture: %v: %s", err, output)
	}
}

func TestManifestLookupRetainsActualDirectoryNameBoundary(t *testing.T) {
	root := t.TempDir()
	store := NewManifestStore()
	manifest := writeLookupTask(t, store, root, "ActualTask")
	if _, err := store.LoadTask(root, "ActualTask"); err != nil {
		t.Fatalf("actual directory name rejected: %v", err)
	}
	manifest.TaskDirectoryName = "actualtask"
	if err := store.Save(filepath.Join(root, "ActualTask"), manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadTask(root, "actualtask"); err == nil {
		t.Fatal("case alias bypassed the actual-directory-name contract")
	}
	manifest.TaskDirectoryName = "ActualTask."
	if err := store.Save(filepath.Join(root, "ActualTask"), manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadTask(root, "ActualTask."); err == nil {
		t.Fatal("trailing-dot alias bypassed the actual-directory-name contract")
	}
}

func TestManifestLookupAllowsConfiguredJunctionButRejectsTaskJunction(t *testing.T) {
	base := t.TempDir()
	root, alias := filepath.Join(base, "real tasks"), filepath.Join(base, "root alias")
	store := NewManifestStore()
	writeLookupTask(t, store, root, "chosen")
	manifestLookupJunction(t, alias, root)
	entry, err := store.LoadTask(alias, "chosen")
	if err != nil || entry.Path != filepath.Join(alias, "chosen") {
		t.Fatalf("configured junction rejected: %+v, %v", entry, err)
	}
	manifestLookupJunction(t, filepath.Join(root, "linked-task"), filepath.Join(root, "chosen"))
	if _, err := store.LoadTask(root, "linked-task"); err == nil {
		t.Fatal("task junction was accepted as an independent task directory")
	}
}

func TestManifestLookupAllowsAncestorJunctionAndRejectsManifestReparse(t *testing.T) {
	base := t.TempDir()
	physical, ancestor := filepath.Join(base, "physical"), filepath.Join(base, "ancestor")
	root := filepath.Join(physical, "tasks")
	store := NewManifestStore()
	writeLookupTask(t, store, root, "ActualTask")
	manifestLookupJunction(t, ancestor, physical)
	configured := filepath.Join(ancestor, "tasks")
	entry, err := store.LoadTask(configured, "ActualTask")
	if err != nil || entry.Path != filepath.Join(configured, "ActualTask") {
		t.Fatalf("ancestor junction or lexical path rejected: %+v, %v", entry, err)
	}
	manifestPath := filepath.Join(root, "ActualTask", TaskManifestFileName)
	if err := os.Rename(manifestPath, filepath.Join(base, "saved.json")); err != nil {
		t.Fatal(err)
	}
	// Junction creation needs no symbolic-link privilege. A manifest reparse
	// entry must be rejected even when all configured ancestors are permitted.
	manifestLookupJunction(t, manifestPath, physical)
	info, err := os.Lstat(manifestPath)
	if err != nil || !manifestPathReparse(info) {
		t.Fatalf("fixture is not an actual manifest reparse entry: %v", err)
	}
	if _, err := store.LoadTask(configured, "ActualTask"); err == nil {
		t.Fatal("accepted a reparse entry as the task manifest")
	}
}

func TestManifestLookupRejectsRetargetedConfiguredJunction(t *testing.T) {
	base := t.TempDir()
	first, second, alias := filepath.Join(base, "first"), filepath.Join(base, "second"), filepath.Join(base, "alias")
	store := NewManifestStore()
	writeLookupTask(t, store, first, "chosen")
	manifest := writeLookupTask(t, store, second, "chosen")
	manifest.CreatedAt = "2026-10-30T00:00:00Z"
	if err := store.Save(filepath.Join(second, "chosen"), manifest); err != nil {
		t.Fatal(err)
	}
	manifestLookupJunction(t, alias, first)
	guard := captureManifestLookupGuard(t, alias, "chosen")
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	manifestLookupJunction(t, alias, second)
	if guard.unchanged() {
		t.Fatal("post-read guard accepted a retargeted configured root")
	}
	entry, err := store.LoadTask(alias, "chosen")
	if err != nil || entry.Path != filepath.Join(alias, "chosen") || entry.Manifest.CreatedAt != manifest.CreatedAt {
		t.Fatalf("next read did not use the new configured root identity: %+v, %v", entry, err)
	}
}

func TestManifestLookupLongPathAndUNCQueryForms(t *testing.T) {
	root := t.TempDir()
	for len(root) < 300 {
		root = filepath.Join(root, "long-root-component")
	}
	store := NewManifestStore()
	writeLookupTask(t, store, root, "ActualTask")
	if entry, err := store.LoadTask(root, "ActualTask"); err != nil || entry.Path != filepath.Join(root, "ActualTask") {
		t.Fatalf("long-path point lookup = %+v, %v", entry, err)
	}
	unc := `\\server\share\` + strings.Repeat(`component\`, 30) + "task"
	for _, test := range []struct{ path, want string }{
		{`C:\short\task`, `C:\short\task`},
		{unc, `\\?\UNC\` + unc[2:]},
		{`\\?\C:\already\task`, `\\?\C:\already\task`},
		{`\\?\UNC\server\share\task`, `\\?\UNC\server\share\task`},
	} {
		got, err := manifestLookupWindowsPath(test.path)
		if err != nil || got != test.want {
			t.Fatalf("query form %q = %q, %v; want %q", test.path, got, err, test.want)
		}
	}
	// A regular-looking entry still cannot bypass the native attribute guard.
	info, err := os.Stat(filepath.Join(root, "ActualTask", TaskManifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !manifestPathReparse(manifestLookupReparseInfo{info}) {
		t.Fatal("regular-looking manifest reparse attributes were ignored")
	}
}

type manifestLookupReparseInfo struct{ os.FileInfo }

func (manifestLookupReparseInfo) Sys() any {
	return &syscall.Win32FileAttributeData{FileAttributes: syscall.FILE_ATTRIBUTE_REPARSE_POINT}
}
