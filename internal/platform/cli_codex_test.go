package platform

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/executable"
	"silverwing-go/internal/storage"
)

func TestCodexCLIStatusRecoversConfiguredPath(t *testing.T) {
	for _, tc := range []struct {
		name           string
		oldWorks       bool
		candidate      bool
		candidateWorks bool
		wantProbes     string
	}{
		{name: "working configured path", oldWorks: true, candidate: true, candidateWorks: true, wantProbes: "old"},
		{name: "working replacement", candidate: true, candidateWorks: true, wantProbes: "old replacement"},
		{name: "no replacement", wantProbes: "old"},
		{name: "failed replacement", candidate: true, wantProbes: "old replacement"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			probeLog := filepath.Join(t.TempDir(), "probes")
			t.Setenv("SILVERWING_CODEX_PROBE_LOG", probeLog)
			oldDir := t.TempDir()
			old := writeCodexVersionFixture(t, oldDir, "old", tc.oldWorks)
			directories := []string{oldDir}
			var candidate string
			if tc.candidate {
				candidate = writeCodexVersionFixture(t, t.TempDir(), "replacement", tc.candidateWorks)
				directories = append(directories, filepath.Dir(candidate))
				if !tc.candidateWorks {
					third := writeCodexVersionFixture(t, t.TempDir(), "third", true)
					directories = append(directories, filepath.Dir(third))
				}
			}
			setCodexFixtureSearchPath(t, directories)
			paths := storage.NewPaths(t.TempDir())
			store := storage.NewConfigStore(paths)
			cfg, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			oldSetting, otherPath := " "+old+" ", "keep-dragon"
			cfg.CodexExecutablePath, cfg.DragonExecutablePath = &oldSetting, &otherPath
			if err := store.Save(cfg); err != nil {
				t.Fatal(err)
			}

			service := NewService(paths, store, nil)
			data, err := service.Execute(context.Background(), "cli.status", map[string]any{"tool": "codex"})
			if err != nil {
				t.Fatal(err)
			}
			status := data.(map[string]any)
			wantInstalled := tc.oldWorks || tc.candidateWorks
			wantCommand := candidate
			if tc.oldWorks {
				wantCommand = old
			}
			if status["installed"] != wantInstalled || status["command"] != wantCommand {
				t.Errorf("unexpected Codex status: %#v", status)
			}
			errorText, _ := status["error"].(string)
			if wantInstalled && status["version"] != "codex-cli 1.2.3" || !wantInstalled && errorText == "" {
				t.Errorf("unexpected version result: %#v", status)
			}
			if tc.oldWorks && status["clearedConfiguredPath"] != nil || !tc.oldWorks && status["clearedConfiguredPath"] != old {
				t.Errorf("unexpected cleared path: %#v", status)
			}
			cfg, err = store.Load()
			if err != nil {
				t.Fatal(err)
			}
			if wantInstalled && (cfg.CodexExecutablePath == nil || strings.TrimSpace(*cfg.CodexExecutablePath) != wantCommand) || !wantInstalled && cfg.CodexExecutablePath != nil {
				t.Errorf("unexpected saved Codex path: %v", cfg.CodexExecutablePath)
			}
			if cfg.DragonExecutablePath == nil || *cfg.DragonExecutablePath != otherPath {
				t.Errorf("recovery changed an unrelated path: %v", cfg.DragonExecutablePath)
			}
			log, err := os.ReadFile(probeLog)
			if err != nil || strings.Join(strings.Fields(string(log)), " ") != tc.wantProbes {
				t.Errorf("version probes = %q, want %q (read error: %v)", log, tc.wantProbes, err)
			}
		})
	}
}

func TestCodexCLIStatusDoesNotClearCanceledProbe(t *testing.T) {
	paths := storage.NewPaths(t.TempDir())
	store := storage.NewConfigStore(paths)
	old := filepath.Join(t.TempDir(), "codex-missing")
	if _, err := store.Update(func(cfg domain.AppConfig) (domain.AppConfig, error) {
		cfg.CodexExecutablePath = &old
		return cfg, nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	service := NewService(paths, store, nil)
	data, err := service.Execute(ctx, "cli.status", map[string]any{"tool": "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if status := data.(map[string]any); status["installed"] != false || status["clearedConfiguredPath"] != nil {
		t.Fatalf("a canceled probe attempted recovery: %#v", status)
	}
	cfg, err := store.Load()
	if err != nil || cfg.CodexExecutablePath == nil || *cfg.CodexExecutablePath != old {
		t.Fatalf("a canceled probe changed the configured path: %v (%v)", cfg.CodexExecutablePath, err)
	}
}

func TestCodexCLIRecoveryPreservesConcurrentManualPath(t *testing.T) {
	paths := storage.NewPaths(t.TempDir())
	store := storage.NewConfigStore(paths)
	old, manual := "old-codex", "manual-codex"
	if _, err := store.Update(func(cfg domain.AppConfig) (domain.AppConfig, error) {
		cfg.CodexExecutablePath = &old
		return cfg, nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	service := NewService(paths, store, nil)
	cleared, err := service.clearFailedCodexExecutable(ctx, old)
	if err != nil || cleared.CodexExecutablePath != nil {
		t.Fatalf("clear failed path: %v (%v)", cleared.CodexExecutablePath, err)
	}
	if _, err := store.Update(func(cfg domain.AppConfig) (domain.AppConfig, error) {
		cfg.CodexExecutablePath = &manual
		return cfg, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.clearFailedCodexExecutable(ctx, old); err == nil {
		t.Fatal("a stale failure cleared a newer setting")
	}
	replacement := filepath.Join(t.TempDir(), "replacement")
	if err := os.WriteFile(replacement, []byte("fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	resolution := executable.Resolution{Command: replacement, Source: executable.SourcePathFallback}
	if _, err := service.persistDiscoveredCLIExecutable(ctx, cleared, executable.ToolCodex, resolution); err == nil {
		t.Fatal("recovery should report a changed setting")
	}
	cfg, err := store.Load()
	if err != nil || cfg.CodexExecutablePath == nil || *cfg.CodexExecutablePath != manual {
		t.Fatalf("recovery changed the manual path: %v (%v)", cfg.CodexExecutablePath, err)
	}
}

func writeCodexVersionFixture(t *testing.T, dir, label string, works bool) string {
	t.Helper()
	name := "codex"
	script := "#!/bin/sh\n[ \"$#\" -eq 1 ] && [ \"$1\" = --version ] || exit 2\nprintf '%s\\n' '" + label + "' >>\"$SILVERWING_CODEX_PROBE_LOG\"\n"
	if works {
		script += "printf 'codex-cli 1.2.3\\n'\n"
	} else {
		script += "printf 'version failed\\n' >&2\nexit 1\n"
	}
	if runtime.GOOS == "windows" {
		name += ".cmd"
		script = "@echo off\r\nif not \"%~1\"==\"--version\" exit /b 2\r\nif not \"%~2\"==\"\" exit /b 2\r\n>>\"%SILVERWING_CODEX_PROBE_LOG%\" echo " + label + "\r\n"
		if works {
			script += "echo codex-cli 1.2.3\r\n"
		} else {
			script += ">&2 echo version failed\r\nexit /b 1\r\n"
		}
	}
	command := filepath.Join(dir, name)
	if err := os.WriteFile(command, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return command
}

func setCodexFixtureSearchPath(t *testing.T, directories []string) {
	t.Helper()
	root := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", root)
		directories = append(directories, filepath.Join(os.Getenv("SystemRoot"), "System32"))
	}
	searchPath := strings.Join(directories, string(os.PathListSeparator))
	t.Setenv("PATH", searchPath)
	t.Setenv("SILVERWING_CODEX_PROBE_PATH", searchPath)
	switch runtime.GOOS {
	case "darwin":
		t.Setenv("ZDOTDIR", root)
		if err := os.WriteFile(filepath.Join(root, ".zprofile"), []byte("export PATH=\"$SILVERWING_CODEX_PROBE_PATH\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	case "linux":
		profile := filepath.Join(root, "bash_env")
		if err := os.WriteFile(profile, []byte("export PATH=\"$SILVERWING_CODEX_PROBE_PATH\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("BASH_ENV", profile)
	}
}
