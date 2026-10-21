package platform

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

func TestPlatformAtomicWriteRetainsPrivateModesAndGates(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "private", "settings.json")
	for _, value := range []string{"first\r\n", "replacement 中文\n"} {
		if err := atomicWrite(path, []byte(value), 0); err != nil {
			t.Fatal(err)
		}
		if data, err := os.ReadFile(path); err != nil || string(data) != value {
			t.Fatalf("private write bytes changed: %q %v", data, err)
		}
	}
	if runtime.GOOS != "windows" {
		file, _ := os.Stat(path)
		dir, _ := os.Stat(filepath.Dir(path))
		if file.Mode().Perm() != 0o600 || dir.Mode().Perm() != 0o700 {
			t.Fatalf("private permissions changed: file=%s directory=%s", file.Mode(), dir.Mode())
		}
	}
	if err := atomicWrite(filepath.Dir(path), []byte("not a file"), 0); err == nil || !strings.Contains(err.Error(), "写入目标必须是普通文件") {
		t.Fatalf("non-regular target gate/error changed: %v", err)
	}
	t.Run("symbolic-link parent", func(t *testing.T) {
		outside := t.TempDir()
		linked := filepath.Join(root, "linked-parent")
		if err := os.Symlink(outside, linked); err != nil {
			if runtime.GOOS == "windows" && errors.Is(err, syscall.Errno(1314)) {
				t.Skip("isolated symbolic-link fixture requires a Windows privilege")
			}
			t.Fatal(err)
		}
		defer os.Remove(linked)
		if err := atomicWrite(filepath.Join(linked, "blocked.json"), []byte("must not write"), 0); err == nil {
			t.Fatal("symbolic-link parent bypassed original path gate")
		}
		if _, err := os.Stat(filepath.Join(outside, "blocked.json")); !os.IsNotExist(err) {
			t.Fatal("symbolic-link target was written")
		}
	})
}
