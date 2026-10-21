//go:build darwin

package executable

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/process"
)

type macScriptRunner struct {
	login  string
	actual *process.Runner
}

func (r *macScriptRunner) Run(ctx context.Context, spec process.Spec) (process.Result, error) {
	if spec.Executable == "/bin/zsh" {
		return process.Result{Stdout: r.login + "\n"}, nil
	}
	return r.actual.Run(ctx, spec)
}

func TestMacDesktopFindsCommandAndShebangInterpreter(t *testing.T) {
	root := t.TempDir()
	cliDir, nodeDir := filepath.Join(root, "CLI with spaces"), filepath.Join(root, "node-bin")
	for _, dir := range []string{cliDir, nodeDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	cli := filepath.Join(cliDir, "fixture-meegle")
	if err := os.WriteFile(cli, []byte("#!/usr/bin/env node\nsynthetic JS\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nodeDir, "node"), []byte("#!/bin/sh\nprintf 'fixture-node-started'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "/usr/bin:/bin")
	r := &macScriptRunner{login: cliDir + ":" + nodeDir + ":/usr/bin:/bin", actual: process.NewRunner()}
	resolver := NewResolver(r)
	for _, configured := range []string{cli, "fixture-meegle"} {
		resolution, env, err := resolver.Prepare(context.Background(), ToolMeegle, configured)
		if err != nil {
			t.Fatal(err)
		}
		if resolution.Command != cli || !strings.HasPrefix(env["PATH"], cliDir+":") {
			t.Fatalf("desktop path not repaired: %+v %v", resolution, env)
		}
		result, err := r.Run(context.Background(), process.Spec{Executable: resolution.Command, Env: env, Timeout: time.Second})
		if err != nil || !result.Successful() || result.Stdout != "fixture-node-started" {
			t.Fatalf("shebang failed: %+v %v", result, err)
		}
	}
	if os.Getenv("PATH") != "/usr/bin:/bin" {
		t.Fatal("desktop environment was mutated")
	}
}
