//go:build !windows

package auth

import (
	"os"
	"testing"
)

func assertPrivateCredentialFile(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("credential file permissions %o, want 600", got)
	}
}
