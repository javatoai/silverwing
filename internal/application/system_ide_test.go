package application

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
)

func TestSystemIDEUnavailableNamesSelectedTool(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("native discovery could find an installed IDE")
	}
	target := t.TempDir()
	a, err := newApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Config.Update(func(cfg domain.AppConfig) (domain.AppConfig, error) {
		cfg.TaskRoot = &target
		cfg.DefaultDevelopmentTool = domain.DevelopmentToolVisualStudioCode
		cfg.DevelopmentTools = []domain.DevelopmentToolConfig{{Type: domain.DevelopmentToolWebStorm, Path: filepath.Join(target, "missing")}}
		return cfg, nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		requested string
		tool      domain.DevelopmentToolType
		label     string
	}{
		{"webstorm", domain.DevelopmentToolWebStorm, "WebStorm"},
		{"", domain.DevelopmentToolVisualStudioCode, "Visual Studio Code"},
	} {
		t.Run(tc.label, func(t *testing.T) {
			_, err := a.Dispatch(context.Background(), Action{Kind: "system.ide", Path: target, Payload: map[string]any{"tool": tc.requested}})
			var unavailable *DevelopmentToolUnavailableError
			if !errors.As(err, &unavailable) || unavailable.Tool != tc.tool || !strings.Contains(err.Error(), tc.label) || !strings.Contains(err.Error(), "编辑器与 IDE") {
				t.Fatalf("unavailable %s should link to IDE settings, got %v", tc.label, err)
			}
		})
	}
}

func TestPlanSystemToolLaunchMac(t *testing.T) {
	const target = "/tmp/项目 A"
	for _, tc := range []struct {
		tool                 domain.DevelopmentToolType
		path, wantExecutable string
		wantArguments        []string
	}{
		{domain.DevelopmentToolIntelliJIdea, "/Applications/IntelliJ IDEA.app", "open", []string{"-a", "/Applications/IntelliJ IDEA.app", target}},
		{domain.DevelopmentToolVisualStudioCode, "/Applications/Visual Studio Code.app", "open", []string{"-n", "-a", "/Applications/Visual Studio Code.app", "--args", "--new-window", target}},
		{domain.DevelopmentToolVisualStudioCode, "/opt/homebrew/bin/code", "/opt/homebrew/bin/code", []string{"--new-window", target}},
	} {
		t.Run(tc.path, func(t *testing.T) {
			got, err := PlanSystemToolLaunch("darwin", tc.tool, tc.path, target)
			if err != nil || got.Executable != tc.wantExecutable || !slices.Equal(got.Arguments, tc.wantArguments) {
				t.Fatalf("unexpected launch plan: %+v, %v", got, err)
			}
		})
	}
}
