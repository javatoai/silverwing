package platform

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"silverwing-go/internal/storage"
)

func TestCLIBundleInstallRequiresCurrentOverwriteConfirmation(t *testing.T) {
	s := NewService(storage.NewPaths(t.TempDir()), nil, nil)
	ctx := context.Background()
	if _, err := s.Execute(ctx, "cli.bundle.install", map[string]any{"artifact": "skill"}); err == nil {
		t.Fatal("installation started without an explicit click")
	}
	path := filepath.Join(t.TempDir(), ".agents", "skills", "silverwing-tag", "SKILL.md")
	install := func(content, expected string) (CLIBundleInstallResult, error) {
		return s.installCLIBundleFile(ctx, path, []byte(content), 0o644, expected)
	}
	requireContent := func(expected string) {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil || string(data) != expected {
			t.Fatalf("installed content %q, wanted %q: %v", data, expected, err)
		}
	}
	first, err := install("initial", "")
	if err != nil || first.Path != path || first.Replacement != "" {
		t.Fatal(first, err)
	}
	same, err := install("initial", "")
	if err != nil || same.Replacement != "" {
		t.Fatal(same, err)
	}
	replace, err := install("updated", "")
	if err != nil || replace.Replacement == "" {
		t.Fatal(replace, err)
	}
	requireContent("initial")
	if err := os.WriteFile(path, []byte("user edit"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := install("updated", replace.Replacement); err == nil {
		t.Fatal("stale confirmation replaced newer content")
	}
	requireContent("user edit")
	replace, err = install("updated", "")
	if err != nil || replace.Replacement == "" {
		t.Fatal(replace, err)
	}
	if _, err := install("updated", replace.Replacement); err != nil {
		t.Fatal(err)
	}
	requireContent("updated")
}

func TestCLIBundleStatusRejectsAnUnrelatedSW(t *testing.T) {
	s := NewService(storage.NewPaths(t.TempDir()), nil, nil)
	dir := t.TempDir()
	name := "sw"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("unrelated command"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.cliBundlePathRecord(), []byte(path), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	result, err := s.Execute(context.Background(), "cli.bundle.status", nil)
	if err != nil || result.(CLIBundleStatus).CLIPath != "" {
		t.Fatal("an unrelated command was reported installed", result, err)
	}
}
