package documents

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMaterialsCollectionCreateReuseAndReadOnly(t *testing.T) {
	root := t.TempDir()
	planned, err := configuredRequirementsRoot(&root, false)
	if err != nil || planned != filepath.Join(root, "requirements") {
		t.Fatalf("preview: %s %v", planned, err)
	}
	if _, err := os.Stat(planned); !os.IsNotExist(err) {
		t.Fatal("preview created collection")
	}
	paths, err := EnsureMaterialsPaths(root)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(paths.Requirements, "keep.md")
	if err := os.WriteFile(file, []byte("\xef\xbb\xbf原文\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, selected := range []string{root, paths.Requirements, root} {
		got, err := EnsureMaterialsPaths(selected)
		if err != nil || got.Root != root {
			t.Fatalf("reuse: %+v %v", got, err)
		}
	}
	if text, err := os.ReadFile(file); err != nil || string(text) != "\xef\xbb\xbf原文\r\n" {
		t.Fatalf("data changed: %q %v", text, err)
	}
	if _, err := os.Stat(filepath.Join(paths.Requirements, "requirements")); !os.IsNotExist(err) {
		t.Fatal("duplicated requirements")
	}
}

func TestMaterialsCollectionRejectsFileAndUnwrappedYears(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "requirements"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureMaterialsPaths(root); err == nil {
		t.Fatal("collection file overwritten")
	}
	old := t.TempDir()
	if err := os.MkdirAll(filepath.Join(old, "2026", "7132215081-需求"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureMaterialsPaths(old); err == nil {
		t.Fatal("silently redirected unwrapped annual data")
	}
	if _, err := os.Stat(filepath.Join(old, "requirements")); !os.IsNotExist(err) {
		t.Fatal("created collection beside unresolved annual data")
	}
}
