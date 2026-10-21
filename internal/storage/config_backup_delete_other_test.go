//go:build !windows

package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigBackupDeleteUnsupportedPlatformKeepsFile(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "silverwing-config-fixture.zip")
	if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	info, err := root.Lstat(filepath.Base(path))
	if err != nil {
		t.Fatal(err)
	}
	if err := removeListedConfigBackup(root, filepath.Base(path), info); err == nil || !strings.Contains(err.Error(), "当前平台") {
		t.Fatal("unsupported platform did not refuse delete", err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "fixture" {
		t.Fatal("unsupported platform changed data")
	}
}
