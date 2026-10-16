//go:build windows

package documents

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestMaterialsUnknownExtensionLockedReadReportsError(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "locked.log")
	if err := os.WriteFile(path, []byte("text"), 0600); err != nil {
		t.Fatal(err)
	}
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.CloseHandle(handle)
	if _, err := (&Service{}).ReadMaterialsPreview(root, path); err == nil {
		t.Fatal("locked read was presented as unsupported content")
	}
}
