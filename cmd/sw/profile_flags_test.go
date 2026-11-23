package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

func TestCLIRejectsConflictingDirectoryAliasesAcrossSubcommand(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "First Profile")
	second := filepath.Join(root, "Second Profile")
	var out, stderr bytes.Buffer
	code := run(context.Background(), []string{
		"--home", first, "tag", "list", "--conf-dir", second, "--task", "test-task",
	}, &out, &stderr)
	if code != exitUsage || !strings.Contains(stderr.String(), "--conf-dir 和 --home") {
		t.Fatalf("CLI exit = %d, stdout = %q, stderr = %q", code, out.String(), stderr.String())
	}
	if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
		t.Fatalf("conflicting aliases initialized a profile: %v, %v", entries, err)
	}
}

func TestCLITaskDirectoryFollowsProfileConfiguration(t *testing.T) {
	base := t.TempDir()
	type fixture struct{ home, taskDir, taskID string }
	makeProfile := func(name string) fixture {
		t.Helper()
		paths := storage.NewPaths(filepath.Join(base, name+" profile"))
		var err error
		paths.ProfileID, err = storage.LoadOrCreateProfileID(paths.Home)
		if err != nil {
			t.Fatal(err)
		}
		config := domain.DefaultAppConfig()
		root := filepath.Join(base, name+" tasks")
		config.TaskRoot = &root
		if err := storage.NewConfigStore(paths).Save(config); err != nil {
			t.Fatal(err)
		}
		manifest := domain.DefaultTaskManifest()
		manifest.FolderName, manifest.TaskDirectoryName = name, name
		manifest.CreatedAt, manifest.UpdatedAt = "2026-11-20T00:00:00Z", "2026-11-20T00:00:00Z"
		taskDir := filepath.Join(root, name)
		if err := storage.NewProfileManifestStore(paths).Save(taskDir, manifest); err != nil {
			t.Fatal(err)
		}
		return fixture{paths.Home, taskDir, name}
	}
	first, second := makeProfile("first"), makeProfile("second")
	query := func(home, id string, want int) {
		t.Helper()
		var out, stderr bytes.Buffer
		if got := run(context.Background(), []string{"--conf-dir", home, "tag", "list", "--task", id}, &out, &stderr); got != want {
			t.Fatalf("tag list for %q at %q returned %d, expected %d: %q %q", id, home, got, want, out.String(), stderr.String())
		}
	}
	query(first.home, first.taskID, 0)
	query(second.home, second.taskID, 0)
	query(first.home, second.taskID, exitError)
	query(second.home, first.taskID, exitError)

	moved := filepath.Join(base, "moved first profile")
	if err := os.Rename(first.home, moved); err != nil {
		t.Fatal(err)
	}
	query(moved, first.taskID, 0)
	if _, err := os.Stat(filepath.Join(first.taskDir, storage.TaskManifestFileName)); err != nil {
		t.Fatalf("task left its configured location: %v", err)
	}
}
