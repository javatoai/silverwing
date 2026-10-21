package documents

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDocumentsAtomicWriteKeepsDirectoryPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new-directory", "file.txt")
	if err := atomicWrite(path, []byte("中文\r\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o640 {
			t.Fatalf("file mode changed: %v %v", info, err)
		}
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "中文\r\n" {
		t.Fatalf("wrapper did not preserve raw bytes: %q %v", data, err)
	}
}

func TestDocumentsTextSaveAndDraftUseAtomicWriter(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "example.txt")
	service := NewService(t.TempDir())
	if err := os.WriteFile(path, []byte("original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := service.Read(root, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.CacheDraft(path, "draft 中文\r\n"); err != nil {
		t.Fatal(err)
	}
	if draft, err := service.Draft(path); err != nil || draft != "draft 中文\r\n" {
		t.Fatalf("draft changed: %q %v", draft, err)
	}
	if _, err := service.Save(root, path, "wrong hash", "not-current", false); !errors.Is(err, ErrConflict) {
		t.Fatalf("save bypassed caller's original conflict gate: %v", err)
	}
	want := "saved 中文 😀\r\n最后一行\n"
	if _, err := service.Save(root, path, want, before.Hash, false); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != want {
		t.Fatalf("text save changed original bytes: %q %v", data, err)
	}
	if _, err := os.Stat(service.draftPath(path)); !os.IsNotExist(err) {
		t.Fatal("successful save retained its caller-owned draft")
	}
}
