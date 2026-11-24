package platform

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"silverwing-go/internal/storage"
)

func TestCLIBundleCLIRegistersDirectoryAfterSuccessfulInstallation(t *testing.T) {
	s := NewService(storage.NewPaths(t.TempDir()), nil, nil)
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "sw.exe")
	calls := 0
	content := "first build"
	register := func(_ context.Context, installedDir string) (string, error) {
		calls++
		data, err := os.ReadFile(path)
		if installedDir != dir || err != nil || string(data) != content {
			t.Fatalf("PATH registration preceded a successful install: %q %q %v", installedDir, data, err)
		}
		return "用户 PATH 已就绪", nil
	}
	install := func(expected string) (CLIBundleInstallResult, error) {
		return s.installCLIBundleCLI(ctx, path, []byte(content), 0o755, expected, register)
	}
	first, err := install("")
	if err != nil || first.PATHMessage != "用户 PATH 已就绪" || calls != 1 {
		t.Fatalf("first install: %+v calls=%d err=%v", first, calls, err)
	}
	same, err := install("")
	if err != nil || same.PATHMessage == "" || calls != 2 {
		t.Fatalf("reinstall did not repair the PATH entry: %+v calls=%d err=%v", same, calls, err)
	}
	content = "second build"
	pending, err := install("")
	if err != nil || pending.Replacement == "" || pending.PATHMessage != "" || calls != 2 {
		t.Fatalf("unconfirmed overwrite registered PATH: %+v calls=%d err=%v", pending, calls, err)
	}
	replaced, err := install(pending.Replacement)
	if err != nil || replaced.Replacement != "" || replaced.PATHMessage == "" || calls != 3 {
		t.Fatalf("confirmed overwrite did not register PATH: %+v calls=%d err=%v", replaced, calls, err)
	}
}

func TestCLIBundleCLIKeepsInstalledFileAndReportsPathFailure(t *testing.T) {
	s := NewService(storage.NewPaths(t.TempDir()), nil, nil)
	path := filepath.Join(t.TempDir(), "sw.exe")
	result, err := s.installCLIBundleCLI(context.Background(), path, []byte("payload"), 0o755, "",
		func(context.Context, string) (string, error) { return "", errors.New("注册表访问被拒绝") })
	if err != nil || result.Path != path || !strings.Contains(result.PATHMessage, "用户 PATH 未更新") {
		t.Fatalf("partial installation result: %+v err=%v", result, err)
	}
	record, err := os.ReadFile(s.cliBundlePathRecord())
	if err != nil || string(record) != path {
		t.Fatalf("installation path was not recorded: %q %v", record, err)
	}
}

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
