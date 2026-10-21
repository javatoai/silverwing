package commandruntime

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/executable"
	"silverwing-go/internal/process"
)

type environmentRunner struct{ calls int }

func (r *environmentRunner) Run(ctx context.Context, spec process.Spec) (process.Result, error) {
	r.calls++
	if err := ctx.Err(); err != nil {
		return process.Result{}, err
	}
	return process.Result{Stdout: "/opt/homebrew/bin:/usr/bin:/bin\n"}, nil
}

func TestPrepareSharesToolDefaultsAndTargetProxy(t *testing.T) {
	config := domain.DefaultAppConfig()
	runner := &environmentRunner{}
	for _, tool := range []executable.Tool{executable.ToolMeegle, executable.ToolLark, executable.ToolCodex, executable.ToolGit} {
		target := domain.CommandProxyTarget(strings.ToUpper(string(tool)))
		spec, err := Prepare(context.Background(), runner, &config, target, tool, "/fixture/command")
		if err != nil {
			t.Fatal(err)
		}
		if tool == executable.ToolCodex && spec.Env["HTTP_PROXY"] != "http://127.0.0.1:7897" {
			t.Fatal("Codex proxy missing")
		}
		if tool != executable.ToolCodex && spec.Env["HTTP_PROXY"] != "" {
			t.Fatalf("proxy crossed target: %s", tool)
		}
		if !slices.Contains(spec.RemoveEnv, "HTTP_PROXY") || !slices.Contains(spec.RemoveEnv, "https_proxy") {
			t.Fatal("inherited proxy was retained")
		}
		if tool == executable.ToolLark && (spec.Env["LARKSUITE_CLI_NO_UPDATE_NOTIFIER"] != "1" || spec.Env["LARKSUITE_CLI_NO_SKILLS_NOTIFIER"] != "1") {
			t.Fatal("Lark notification policy missing")
		}
	}
	spec, err := Prepare(context.Background(), runner, nil, "", "", "/fixture/bootstrap")
	if err != nil || len(spec.RemoveEnv) != 0 {
		t.Fatalf("Bootstrap inheritance changed: %+v %v", spec, err)
	}
}

func TestPrepareCancellationDoesNotProbe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner := &environmentRunner{}
	_, err := Prepare(ctx, runner, nil, "", executable.ToolMeegle, "")
	if !errors.Is(err, context.Canceled) || runner.calls != 0 {
		t.Fatalf("cancelled command probed: calls=%d err=%v", runner.calls, err)
	}
}

func TestProxyCredentialsAndNoProxyRemainScopedAndRedacted(t *testing.T) {
	c := domain.DefaultAppConfig()
	endpoint, user, password, noProxy := "http://proxy.invalid:8080", "fixture-user", "fixture-secret", "localhost,127.0.0.1"
	c.CommandProxyURL, c.CommandProxyUsername, c.CommandProxyPassword, c.CommandProxyNoProxy = &endpoint, &user, &password, &noProxy
	c.CommandProxyTargets = []domain.CommandProxyTarget{domain.ProxyTargetCodex}
	env, _ := ProxyEnvironment(c, domain.ProxyTargetCodex)
	if !strings.Contains(env["HTTP_PROXY"], "fixture-user:fixture-secret@") || env["NO_PROXY"] != noProxy {
		t.Fatalf("proxy options lost: %+v", env)
	}
	text := Redact(c, env["HTTP_PROXY"]+" "+endpoint+" "+password+" "+user)
	for _, secret := range []string{password, user, "proxy.invalid"} {
		if strings.Contains(text, secret) {
			t.Fatalf("credential leaked: %s", text)
		}
	}
	other, _ := ProxyEnvironment(c, domain.ProxyTargetMeegle)
	if len(other) != 0 {
		t.Fatal("Codex proxy applied to Meegle")
	}
}

func TestPrepareRejectsRemovedToolBeforeDiscovery(t *testing.T) {
	runner := &environmentRunner{}
	if _, err := Prepare(context.Background(), runner, nil, "", executable.Tool("genbu"), ""); err == nil {
		t.Fatal("removed Genbu CLI was accepted")
	}
	if runner.calls != 0 {
		t.Fatalf("unsupported CLI started discovery processes: %d", runner.calls)
	}
}
