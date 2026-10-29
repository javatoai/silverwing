//go:build windows

package platform

import (
	"errors"
	"testing"

	"golang.org/x/sys/windows"
)

func assertCodexRPCFixtureExited(t *testing.T, pid int) {
	t.Helper()
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)
	status, err := windows.WaitForSingleObject(handle, 0)
	if err != nil || status != windows.WAIT_OBJECT_0 {
		t.Fatalf("RPC helper still running after operation returned: pid=%d status=%d err=%v", pid, status, err)
	}
}
