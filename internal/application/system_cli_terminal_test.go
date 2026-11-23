package application

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
)

func TestSystemCLITerminalRejectsDisabledAndStaleOptionalCommands(t *testing.T) {
	a, err := newApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, tool := range []string{"lark", "genbu", "dragon"} {
		t.Run(tool, func(t *testing.T) {
			command := filepath.Join(a.Paths.Home, tool+".exe")
			cfg := domain.DefaultAppConfig()
			disabled := false
			switch tool {
			case "lark":
				cfg.LarkExecutablePath, cfg.OptionalCLIs.Lark = &command, &disabled
			case "genbu":
				cfg.GenbuExecutablePath, cfg.OptionalCLIs.Genbu = &command, &disabled
			case "dragon":
				cfg.DragonExecutablePath, cfg.OptionalCLIs.Dragon = &command, &disabled
			}
			if err := a.Config.Save(cfg); err != nil {
				t.Fatal(err)
			}
			a.optionalCLIRequire = nil
			action := Action{Kind: "system.cli-terminal", Path: command, Payload: map[string]any{"tool": tool}}
			if nativePlatform(tool) {
				for _, enabled := range []bool{false, true} {
					if _, err := a.Dispatch(ctx, Action{Kind: "platform.connection.set", ServiceID: tool, Payload: map[string]any{"enabled": enabled}}); err != nil {
						t.Fatal(err)
					}
					if _, err := a.Dispatch(ctx, action); err == nil || !strings.Contains(err.Error(), "请指定") {
						t.Fatalf("native integration accepted a local executable: %v", err)
					}
				}
				return
			}
			if _, err := a.Dispatch(ctx, action); err == nil || !strings.Contains(err.Error(), "未启用") {
				t.Fatalf("disabled terminal result: %v", err)
			}
			cfg.OptionalCLIs = domain.OptionalCLISettings{}
			if err := a.Config.Save(cfg); err != nil {
				t.Fatal(err)
			}
			a.optionalCLIRequire = func(_ context.Context, requested string) error {
				if requested != tool {
					t.Errorf("requested CLI = %q, want %q", requested, tool)
				}
				return nil
			}
			action.Path = filepath.Join(a.Paths.Home, "previous-"+tool+".exe")
			if _, err := a.Dispatch(ctx, action); err == nil || !strings.Contains(err.Error(), "命令路径已变化") {
				t.Fatalf("stale terminal result: %v", err)
			}
			delete(action.Payload, "tool")
			if _, err := a.Dispatch(ctx, action); err == nil || !strings.Contains(err.Error(), "请指定") {
				t.Fatalf("unlabeled terminal result: %v", err)
			}
		})
	}
}
