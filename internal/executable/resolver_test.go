package executable

import (
	"context"
	"testing"

	"silverwing-go/internal/process"
)

type loginPathRunner struct{ calls int }

func (r *loginPathRunner) Run(ctx context.Context, _ process.Spec) (process.Result, error) {
	r.calls++
	if err := ctx.Err(); err != nil {
		return process.Result{}, err
	}
	return process.Result{Stdout: "/opt/homebrew/bin:/usr/bin\n"}, nil
}

func TestCanceledLoginPathDiscoveryDoesNotPoisonCache(t *testing.T) {
	runner := &loginPathRunner{}
	resolver := NewResolver(runner)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if path := resolver.macLoginShellPath(ctx); path != "" {
		t.Fatalf("cancelled lookup returned a path: %q", path)
	}
	if path := resolver.macLoginShellPath(context.Background()); path != "/opt/homebrew/bin:/usr/bin" || runner.calls != 2 {
		t.Fatalf("fresh lookup reused cancelled discovery: %q (%d calls)", path, runner.calls)
	}
}

func TestUnsupportedToolIsRejectedWithoutDiscovery(t *testing.T) {
	runner := &loginPathRunner{}
	resolver := NewResolver(runner)
	if got := resolver.Resolve(context.Background(), Tool("genbu"), ""); got != (Resolution{}) {
		t.Fatalf("unsupported tool was resolved: %+v", got)
	}
	if _, _, err := resolver.Prepare(context.Background(), Tool("genbu"), ""); err == nil {
		t.Fatal("unsupported tool was prepared")
	}
	if runner.calls != 0 {
		t.Fatalf("unsupported tool started discovery processes: %d", runner.calls)
	}
}

func TestExplicitConfiguredToolPathRemainsAuthoritative(t *testing.T) {
	runner := &loginPathRunner{}
	resolver := NewResolver(runner)
	configured := `/opt/company tools/meegle`
	got, _, err := resolver.Prepare(context.Background(), ToolMeegle, configured)
	if err != nil {
		t.Fatal(err)
	}
	if got.Command != configured || got.Source != SourceConfigured {
		t.Fatalf("configured path was replaced: %+v", got)
	}
}
