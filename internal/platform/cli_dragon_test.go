package platform

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/executable"
	"silverwing-go/internal/storage"
)

func TestEmbeddedPlatformsRejectLegacyExecutablePathsAndDiscovery(t *testing.T) {
	command := buildOptionalCLIStatusFixture(t)
	dragonName := "dragon"
	if runtime.GOOS == "windows" {
		dragonName += ".exe"
	}
	dragonPath := filepath.Join(filepath.Dir(command), dragonName)
	content, err := os.ReadFile(command)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dragonPath, content, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(command))
	logPath := filepath.Join(t.TempDir(), "calls.log")
	t.Setenv("SILVERWING_OPTIONAL_CLI_TEST_LOG", logPath)
	paths := storage.NewPaths(t.TempDir())
	store := storage.NewConfigStore(paths)
	cfg, err := store.Update(func(cfg domain.AppConfig) (domain.AppConfig, error) {
		cfg.GenbuExecutablePath, cfg.DragonExecutablePath = &command, &dragonPath
		enabled := true
		cfg.OptionalCLIs.Genbu, cfg.OptionalCLIs.Dragon = &enabled, &enabled
		return cfg, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(paths, store, nil)
	ctx := context.Background()
	for tool, exePath := range map[executable.Tool]string{executable.ToolGenbu: command, executable.ToolDragon: dragonPath} {
		if _, err := service.Execute(ctx, "cli.status", map[string]any{"tool": string(tool), "force": true}); err == nil {
			t.Fatal("embedded platform accepted legacy CLI probe", tool)
		}
		if resolved, err := service.resolveCLIExecutableWithSourceForce(ctx, cfg, tool, true); err == nil || resolved.Command != "" {
			t.Fatal("embedded platform resolved a command", tool, resolved, err)
		}
		if _, err := service.SaveOptionalCLIDiscovery(ctx, cfg, string(tool), exePath, string(executable.SourceProbed)); err == nil {
			t.Fatal("embedded platform accepted local CLI discovery", tool)
		}
	}
	if _, err := os.Stat(logPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy CLI was invoked: %v", err)
	}
}
