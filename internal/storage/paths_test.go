package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTaskWorktreeNamespace(t *testing.T) {
	root := t.TempDir()
	paths := NewPaths(filepath.Join(root, "application"))
	paths.ProfileID = "first"
	taskRoot := filepath.Join(root, "tasks")
	first := filepath.Join(taskRoot, "silverwing-first")
	if got := paths.TaskWorktreeNamespace(&taskRoot); got != first {
		t.Fatalf("first profile namespace = %q, want %q", got, first)
	}
	other := paths
	other.ProfileID = "second"
	second := filepath.Join(taskRoot, "silverwing-second")
	if got := other.TaskWorktreeNamespace(&taskRoot); got != second {
		t.Fatalf("second profile namespace = %q, want %q", got, second)
	}
	if _, err := os.Stat(taskRoot); !os.IsNotExist(err) {
		t.Fatalf("namespace lookup created directories: %v", err)
	}
	if got := paths.TaskWorktreeNamespace(nil); got != "" {
		t.Fatalf("unconfigured task root returned %q", got)
	}
	if got := paths.TaskWorktreeNamespace(&paths.Tasks); got != "" {
		t.Fatalf("task root within application data returned %q", got)
	}
	if err := os.MkdirAll(first, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(first, TaskManifestFileName), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := paths.TaskWorktreeNamespace(&taskRoot); got != "" {
		t.Fatalf("task occupying namespace returned %q", got)
	}
	if got := other.TaskWorktreeNamespace(&taskRoot); got != second {
		t.Fatalf("another profile's collision changed namespace to %q", got)
	}
}
