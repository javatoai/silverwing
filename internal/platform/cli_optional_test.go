package platform

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/executable"
	"silverwing-go/internal/storage"
)

func TestOptionalCLIStatusSkipsDisabledExecutablesAndOnlyChecksVersionOnRequest(t *testing.T) {
	ctx := context.Background()
	paths := storage.NewPaths(t.TempDir())
	store := storage.NewConfigStore(paths)
	command := buildOptionalCLIStatusFixture(t)
	logPath := filepath.Join(t.TempDir(), "calls.log")
	t.Setenv("SILVERWING_OPTIONAL_CLI_TEST_LOG", logPath)
	disabled, enabled := false, true
	_, err := store.Update(func(cfg domain.AppConfig) (domain.AppConfig, error) {
		cfg.LarkExecutablePath, cfg.GenbuExecutablePath, cfg.DragonExecutablePath = &command, &command, &command
		cfg.OptionalCLIs = domain.OptionalCLISettings{Lark: &disabled, Genbu: &disabled, Dragon: &disabled}
		return cfg, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(paths, store, nil)
	statusFor := func(tool string, versionOnly bool) map[string]any {
		t.Helper()
		data, e := service.Execute(ctx, "cli.status", map[string]any{"tool": tool, "force": true, "versionOnly": versionOnly})
		if e != nil {
			t.Fatal(e)
		}
		return data.(map[string]any)
	}
	for _, tool := range []string{"lark"} {
		for _, versionOnly := range []bool{false, true} {
			status := statusFor(tool, versionOnly)
			if status["enabled"] != false || status["installed"] != false || status["command"] != command {
				t.Fatalf("unexpected disabled %s status: %#v", tool, status)
			}
		}
	}
	if _, err := os.Stat(logPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("disabled tool touched the process log: %v", err)
	}
	_, err = store.Update(func(cfg domain.AppConfig) (domain.AppConfig, error) {
		cfg.OptionalCLIs = domain.OptionalCLISettings{Lark: &enabled, Genbu: &enabled, Dragon: &enabled}
		return cfg, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"lark"} {
		status := statusFor(tool, true)
		if status["enabled"] != true || status["installed"] != true || status["version"] != "fixture 1.0.0" || status["authenticationState"] != "NOT_CHECKED" {
			t.Fatalf("unexpected version-only %s status: %#v", tool, status)
		}
	}
	data, err := os.ReadFile(logPath)
	if err != nil || string(data) != "--version\n" {
		t.Fatalf("version-only probes ran unexpected arguments: %q (%v)", data, err)
	}
	if status := statusFor("lark", false); status["installed"] != true || status["authenticationState"] != "LOCAL_IDENTITY" {
		t.Fatalf("full Lark status failed to read local identity: %#v", status)
	}
	data, err = os.ReadFile(logPath)
	if err != nil || !strings.HasPrefix(string(data), "--version\n--version\n"+"auth status --profile ") {
		t.Fatalf("full Lark status ran unexpected arguments: %q (%v)", data, err)
	}
}

func TestOptionalCLIDiscoveryRespectsConcurrentDisable(t *testing.T) {
	paths := storage.NewPaths(t.TempDir())
	store := storage.NewConfigStore(paths)
	enabled, disabled := true, false
	before, err := store.Update(func(cfg domain.AppConfig) (domain.AppConfig, error) {
		cfg.OptionalCLIs = domain.OptionalCLISettings{Lark: &enabled, Genbu: &enabled, Dragon: &enabled}
		return cfg, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Update(func(cfg domain.AppConfig) (domain.AppConfig, error) {
		cfg.OptionalCLIs = domain.OptionalCLISettings{Lark: &disabled, Genbu: &disabled, Dragon: &disabled}
		return cfg, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := filepath.Join(t.TempDir(), "discovered")
	if err := os.WriteFile(candidate, []byte("fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	service := NewService(paths, store, nil)
	for _, tool := range []executable.Tool{executable.ToolLark} {
		resolution := executable.Resolution{Command: candidate, Source: executable.SourceProbed}
		if _, err := service.persistDiscoveredCLIExecutable(context.Background(), before, tool, resolution); err == nil {
			t.Fatalf("discovery saved a path after %s was disabled", tool)
		}
	}
	after, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"lark"} {
		if after.OptionalCLIEnabled(tool) || after.OptionalCLIPath(tool) != "" {
			t.Fatalf("discovery changed disabled %s settings", tool)
		}
	}
}

func buildOptionalCLIStatusFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	command := filepath.Join(dir, "genbu")
	if runtime.GOOS == "windows" {
		command += ".exe"
	}
	source := filepath.Join(dir, "main.go")
	const fixture = `package main
import (
	"fmt"
	"os"
	"strings"
)
func main() {
	args := strings.Join(os.Args[1:], " ")
	log, err := os.OpenFile(os.Getenv("SILVERWING_OPTIONAL_CLI_TEST_LOG"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		os.Exit(1)
	}
	fmt.Fprintln(log, args)
	log.Close()
	if args == "--version" {
		fmt.Println("fixture 1.0.0")
		return
	}
	if strings.HasPrefix(args, "auth status --profile ") {
		fmt.Printf("{\"appId\":%q}\n", os.Getenv("SILVERWING_OPTIONAL_CLI_TEST_APP"))
		return
	}
	os.Exit(2)
}`
	if err := os.WriteFile(source, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("go", "build", "-o", command, source).CombinedOutput(); err != nil {
		t.Fatalf("build optional CLI fixture: %v\n%s", err, output)
	}
	t.Setenv("SILVERWING_OPTIONAL_CLI_TEST_APP", domain.DefaultLarkNoticeConfig().AppID)
	return command
}
