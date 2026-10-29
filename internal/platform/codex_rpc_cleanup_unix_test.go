//go:build !windows

package platform

import (
	"errors"
	"syscall"
	"testing"
)

func assertCodexRPCFixtureExited(t *testing.T, pid int) {
	t.Helper()
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("RPC helper still running after operation returned: pid=%d err=%v", pid, err)
	}
}
