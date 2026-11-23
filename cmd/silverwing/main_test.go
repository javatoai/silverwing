package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/storage"
)

func TestStartupRejectsMissingShortcutProfileBeforeCreatingDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Removed Profile")
	err := run([]string{"--conf-dir", dir, "--expected-profile-id", "former-profile"})
	if err == nil || !strings.Contains(err.Error(), "快捷方式已失效") {
		t.Fatalf("startup error = %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("stale shortcut created a profile directory: %v", err)
	}
}

func TestStartupRejectsShortcutForReplacedProfileBeforeLocking(t *testing.T) {
	dir := t.TempDir()
	profileID, err := storage.LoadOrCreateProfileID(dir)
	if err != nil {
		t.Fatal(err)
	}
	err = run([]string{"--conf-dir", dir, "--expected-profile-id", profileID + "-wrong"})
	if err == nil || !strings.Contains(err.Error(), "快捷方式已失效") {
		t.Fatalf("startup error = %v", err)
	}
	lock := filepath.Join(storage.NewPaths(dir).Locks, "desktop.lock")
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Fatalf("stale shortcut acquired a desktop lock: %v", err)
	}
}

func TestStartupLeavesIncompatibleProfileUntouched(t *testing.T) {
	dir := t.TempDir()
	paths := storage.NewPaths(dir)
	if err := os.Mkdir(paths.Config, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(paths.Config, "legacy.json")
	const content = `{"oldConfiguration":true}`
	if err := os.WriteFile(legacy, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	err := run([]string{"--conf-dir", dir})
	if err == nil || !strings.Contains(err.Error(), "原有配置已保留") {
		t.Fatalf("startup error = %v", err)
	}
	if data, err := os.ReadFile(legacy); err != nil || string(data) != content {
		t.Fatalf("legacy configuration changed: %q, %v", data, err)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 1 || entries[0].Name() != "config" {
		t.Fatalf("startup wrote in the incompatible profile: %v, %v", entries, err)
	}
}
