//go:build windows

package install

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestPortableLockedFileStopsAndCanRestoreAfterRelease(t *testing.T) {
	p := portablePlan(t)
	name, _ := windows.UTF16PtrFromString(filepath.Join(p.Installation.Root, "helper.exe"))
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)
	applyErr := (Portable{}).Apply(context.Background(), p)
	if applyErr == nil {
		t.Fatal("locked installed file was replaced")
	}
	if err := (Portable{}).Rollback(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if content, _ := os.ReadFile(filepath.Join(p.Installation.Root, "app.exe")); string(content) != "1.0.0" {
		t.Fatal("partial update not rolled back")
	}
}
