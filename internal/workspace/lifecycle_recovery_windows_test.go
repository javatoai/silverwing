//go:build windows

package workspace

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// Build the reparse data independently of the recovery implementation. This is
// the WSL link format found in extracted Linux runtimes in task directories.
func makeRecoveryWSLLink(t *testing.T, path, target string) []byte {
	t.Helper()
	data := make([]byte, 12+len(target))
	binary.LittleEndian.PutUint32(data, 0xa000001d)
	binary.LittleEndian.PutUint16(data[4:], uint16(len(data)-8))
	binary.LittleEndian.PutUint32(data[8:], 2)
	copy(data[12:], target)
	makeRecoveryReparsePoint(t, path, data)
	return data
}

func makeRecoveryReparsePoint(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)
	var returned uint32
	if err := windows.DeviceIoControl(handle, windows.FSCTL_SET_REPARSE_POINT,
		&data[0], uint32(len(data)), nil, 0, &returned, nil); err != nil {
		if errors.Is(err, windows.ERROR_PRIVILEGE_NOT_HELD) || errors.Is(err, windows.ERROR_NOT_SUPPORTED) || errors.Is(err, windows.ERROR_INVALID_FUNCTION) {
			t.Skipf("filesystem cannot create the required reparse fixture: %v", err)
		}
		t.Fatal(err)
	}
}

func recoveryReparseBytes(t *testing.T, path string) []byte {
	t.Helper()
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)
	data := make([]byte, windows.MAXIMUM_REPARSE_DATA_BUFFER_SIZE)
	var returned uint32
	if err := windows.DeviceIoControl(handle, windows.FSCTL_GET_REPARSE_POINT,
		nil, 0, &data[0], uint32(len(data)), &returned, nil); err != nil {
		t.Fatal(err)
	}
	return data[:returned]
}

func TestCopyDirectoryPreservesWSLSymlinks(t *testing.T) {
	for _, target := range []string{"redis-check-rdb", "../outside.txt", "/不存在/中文 路径"} {
		t.Run(target, func(t *testing.T) {
			root := t.TempDir()
			source, backup := filepath.Join(root, "资料 根目录"), filepath.Join(root, "recovery")
			want := makeRecoveryWSLLink(t, filepath.Join(source, "redis-server"), target)
			original := []byte("\xef\xbb\xbf保留原文\r\n")
			for _, path := range []string{filepath.Join(root, "outside.txt"), filepath.Join(source, "redis-check-rdb")} {
				if err := os.WriteFile(path, original, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := copyDirectory(source, backup); err != nil {
				t.Fatal(err)
			}
			if got := recoveryReparseBytes(t, filepath.Join(backup, "redis-server")); !bytes.Equal(got, want) {
				t.Fatalf("link changed or was dereferenced: got %x, want %x", got, want)
			}
			for _, path := range []string{filepath.Join(root, "outside.txt"), filepath.Join(source, "redis-check-rdb"), filepath.Join(backup, "redis-check-rdb")} {
				if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, original) {
					t.Fatalf("regular data changed: %s, %q, %v", path, got, err)
				}
			}
		})
	}
}

func TestTaskDeleteWithWSLSymlink(t *testing.T) {
	service, config, root := optionalServiceFixture(t)
	if _, err := service.CreateTask(context.Background(), config, CreateTaskRequest{FolderName: "TNS 删除回归"}); err != nil {
		t.Fatal(err)
	}
	task := filepath.Join(root, "TNS 删除回归")
	makeRecoveryWSLLink(t, filepath.Join(task, "ai-data", "root", "usr", "bin", "redis-server"), "redis-check-rdb")
	if err := service.Delete(context.Background(), config, task, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(task); !os.IsNotExist(err) {
		t.Fatalf("task still exists: %v", err)
	}
	assertNoRecoveryTransaction(t, root)
}

func TestCopyDirectoryWSLSymlinkRefusesExistingDestination(t *testing.T) {
	root := t.TempDir()
	source, backup := filepath.Join(root, "source"), filepath.Join(root, "recovery")
	makeRecoveryWSLLink(t, filepath.Join(source, "redis-server"), "redis-check-rdb")
	if err := os.MkdirAll(backup, 0o700); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(backup, "redis-server")
	if err := os.WriteFile(destination, []byte("existing data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := copyDirectory(source, backup); err == nil {
		t.Fatal("overwrote an existing destination")
	}
	if got, err := os.ReadFile(destination); err != nil || string(got) != "existing data" {
		t.Fatalf("existing destination changed: %q, %v", got, err)
	}
}

func TestTaskDeleteBackupFailureRestoresWSLSymlink(t *testing.T) {
	service, config, root := optionalServiceFixture(t)
	if _, err := service.CreateTask(context.Background(), config, CreateTaskRequest{FolderName: "rollback"}); err != nil {
		t.Fatal(err)
	}
	task := filepath.Join(root, "rollback")
	manifest, err := os.ReadFile(filepath.Join(task, "silverwing.json"))
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(task, "ai-data", "a-redis-server")
	want := makeRecoveryWSLLink(t, link, "../../outside.txt")
	outside := filepath.Join(root, "outside.txt")
	if err := os.WriteFile(outside, []byte("do not touch"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A Unix socket is intentionally unsupported. Fail after the WSL link has
	// already been backed up, and verify rollback preserves the entire task.
	unsupported := filepath.Join(task, "ai-data", "z-unix-socket")
	socket := make([]byte, 8)
	binary.LittleEndian.PutUint32(socket, 0x80000023)
	makeRecoveryReparsePoint(t, unsupported, socket)
	saved, loadErr := service.Manifests.Load(task)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if _, err := service.Lifecycle.RemoveAll(context.Background(), config, task, saved, true); err == nil || !strings.Contains(err.Error(), "z-unix-socket") {
		t.Fatalf("unsupported file did not abort safely: %v", err)
	}
	if got := recoveryReparseBytes(t, link); !bytes.Equal(got, want) {
		t.Fatalf("rollback changed link: %x", got)
	}
	if got := recoveryReparseBytes(t, unsupported); !bytes.Equal(got, socket) {
		t.Fatalf("rollback lost unsupported file: %x", got)
	}
	if got, err := os.ReadFile(filepath.Join(task, "silverwing.json")); err != nil || !bytes.Equal(got, manifest) {
		t.Fatalf("rollback changed manifest: %v", err)
	}
	if got, err := os.ReadFile(outside); err != nil || string(got) != "do not touch" {
		t.Fatalf("rollback touched external link target: %q, %v", got, err)
	}
	assertNoRecoveryTransaction(t, root)
}

func assertNoRecoveryTransaction(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".silverwing-delete-") {
			t.Fatalf("deletion transaction leaked: %s", entry.Name())
		}
	}
}
