package storage

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func assertNoAtomicWriteTemps(t *testing.T, directory string) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(directory, ".silverwing-write-*.tmp"))
	if err != nil || len(files) != 0 {
		t.Fatalf("atomic temporary files remain: %v (%v)", files, err)
	}
}

func TestWriteFileAtomicReplacesBytesAndMode(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "example.txt")
	for _, tc := range []struct {
		data string
		mode os.FileMode
	}{{"原始内容\r\n", 0o600}, {"新内容 😀\r\n下一行\n", 0o640}, {"", 0o600}} {
		if err := WriteFileAtomic(path, []byte(tc.data), tc.mode); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != tc.data {
			t.Fatalf("atomic bytes changed: %q %v", data, err)
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || runtime.GOOS != "windows" && info.Mode().Perm() != tc.mode {
			t.Fatalf("requested file mode changed: %v %v", info, err)
		}
		assertNoAtomicWriteTemps(t, directory)
	}
}

func TestWriteFileAtomicFailurePreservesTargetAndCleansTemp(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "existing-directory")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(target, "original.txt")
	if err := os.WriteFile(marker, []byte("original target"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(target, []byte("replacement"), 0o600); err == nil {
		t.Fatal("replaced an existing directory with a file")
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "original target" {
		t.Fatalf("failed replacement changed original target: %q %v", data, err)
	}
	assertNoAtomicWriteTemps(t, directory)
	missingParent := filepath.Join(directory, "not-created")
	if err := WriteFileAtomic(filepath.Join(missingParent, "file.txt"), nil, 0o600); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing parent error changed: %v", err)
	}
	if _, err := os.Stat(missingParent); !os.IsNotExist(err) {
		t.Fatal("single-file primitive created a caller-owned directory")
	}
}
