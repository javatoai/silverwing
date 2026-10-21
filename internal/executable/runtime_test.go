package executable

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"silverwing-go/internal/process"
)

func TestEffectiveSearchPathKeepsPriorityAndAvoidsCurrentDirectory(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	name := "fixture-command"
	for _, dir := range []string{first, second} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("fixture"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	path := mergedPath(filepath.Join(first, "cli"), strings.Join([]string{first, second}, string(os.PathListSeparator)), second)
	if want := strings.Join([]string{first, second}, string(os.PathListSeparator)); path != want {
		t.Fatalf("path=%q want=%q", path, want)
	}
	if got := executableOnPath(name, path); got != filepath.Join(first, name) {
		t.Fatal(got)
	}
	if got := executableOnPath(name, "."+string(os.PathListSeparator)+second); got != filepath.Join(second, name) {
		t.Fatal("relative PATH entry was used")
	}
}

type simultaneousPathRunner struct {
	calls   atomic.Int32
	started chan struct{}
	release chan struct{}
}

func (r *simultaneousPathRunner) Run(ctx context.Context, _ process.Spec) (process.Result, error) {
	if r.calls.Add(1) == 1 {
		close(r.started)
	}
	select {
	case <-r.release:
		return process.Result{Stdout: "/fixture/bin:/usr/bin\n"}, nil
	case <-ctx.Done():
		return process.Result{}, ctx.Err()
	}
}
func TestConcurrentLoginPathDiscoveryIsShared(t *testing.T) {
	r := &simultaneousPathRunner{started: make(chan struct{}), release: make(chan struct{})}
	resolver := NewResolver(r)
	var workers sync.WaitGroup
	for range 12 {
		workers.Go(func() {
			if got := resolver.macLoginShellPath(context.Background()); got != "/fixture/bin:/usr/bin" {
				t.Errorf("path=%q", got)
			}
		})
	}
	<-r.started
	close(r.release)
	workers.Wait()
	if r.calls.Load() != 1 {
		t.Fatalf("spawned %d login shells", r.calls.Load())
	}
}
