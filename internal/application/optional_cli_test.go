package application

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
)

func newOptionalCLIApp(t *testing.T) *App {
	t.Helper()
	app, err := newApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func setOptionalCLISwitch(t *testing.T, app *App, tool string, enabled bool) {
	t.Helper()
	_, err := app.Dispatch(context.Background(), Action{Kind: "config.fields.save", Payload: map[string]any{
		"update": map[string]any{"objectFields": map[string]any{"optionalCLIs": map[string]bool{tool: enabled}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOptionalCLILarkIdentityUsesVerifiedCommand(t *testing.T) {
	ctx := context.Background()
	a := newOptionalCLIApp(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "lark-fixture")
	marker := filepath.Join(dir, "invocations")
	if runtime.GOOS == "windows" {
		path += ".exe"
	}
	const fixture = `package main
import (
	"fmt"
	"os"
)
func main() {
	log, err := os.OpenFile(os.Getenv("SW_LARK_PROBE_LOG"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil { os.Exit(1) }
	fmt.Fprintln(log, os.Args[1])
	log.Close()
	if os.Args[1] == "--version" {
		fmt.Println("1.0.0")
	} else {
		fmt.Printf("{\"appId\":%q}\n", os.Getenv("SW_LARK_PROBE_APP"))
	}
}`
	source := filepath.Join(dir, "main.go")
	if err := os.WriteFile(source, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("go", "build", "-o", path, source).CombinedOutput(); err != nil {
		t.Fatalf("build Lark fixture: %v\n%s", err, out)
	}
	cfg := domain.DefaultAppConfig()
	cfg.LarkExecutablePath = &path
	t.Setenv("SW_LARK_PROBE_APP", cfg.EffectiveLarkNotice().AppID)
	t.Setenv("SW_LARK_PROBE_LOG", marker)
	if err := a.Config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	if err := a.RequireOptionalCLI(ctx, "lark"); err != nil {
		t.Fatal(err)
	}
	result, err := a.Dispatch(ctx, Action{Kind: "cli.status", Payload: map[string]any{"tool": "lark"}})
	if err != nil || result.Data.(map[string]any)["authenticationState"] != "LOCAL_IDENTITY" {
		t.Fatalf("Lark local identity: %+v, %v", result, err)
	}
	contents, err := os.ReadFile(marker)
	if err != nil || strings.Join(strings.Fields(string(contents)), " ") != "--version auth" {
		t.Fatalf("unexpected CLI commands: %q, %v", contents, err)
	}
}
