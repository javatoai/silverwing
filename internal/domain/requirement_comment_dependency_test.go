package domain_test

import (
	"go/build"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDomainDependenciesExcludeEditorAndProcessRuntime(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the module source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	visited := map[string]bool{}
	var visit func(string, []string)
	visit = func(relative string, chain []string) {
		if visited[relative] {
			return
		}
		visited[relative] = true
		pkg, err := build.Default.ImportDir(filepath.Join(root, filepath.FromSlash(relative)), 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, dependency := range pkg.Imports {
			if !strings.HasPrefix(dependency, "silverwing-go/internal/") {
				continue
			}
			next := strings.TrimPrefix(dependency, "silverwing-go/")
			path := append(append([]string(nil), chain...), next)
			switch next {
			case "internal/editor", "internal/process", "internal/workspace", "internal/desktop":
				t.Errorf("domain regained a runtime dependency: %s", strings.Join(path, " -> "))
			default:
				visit(next, path)
			}
		}
	}
	visit("internal/domain", []string{"internal/domain"})
}
