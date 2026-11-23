package documents

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func localTaskDocumentFixture(t *testing.T) localOperationFixture {
	t.Helper()
	f := newLocalOperationFixture(t)
	localOperationFile(t, filepath.Join(f.physical.Base, "AGENTS.md"), []byte("router sentinel"))
	return f
}

func TestLocalTaskDocumentScopeUsesOnlyGeneratedPhysicalPaths(t *testing.T) {
	f := localTaskDocumentFixture(t)
	scope, err := OpenLocalTaskDocumentScope(f.public.Base, f.public.Owned)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Close()
	if err := scope.WithDocument("AGENTS.md", func(path string, check func() error) error {
		if path != filepath.Join(f.physical.Base, "AGENTS.md") {
			t.Fatalf("generated file did not use its physical path: %s", path)
		}
		return check()
	}); err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{"../AGENTS.md", ".workspace/agent/TASK-CONTEXT.md", ".workspace/agent/TASK-RULES.md", filepath.Join(f.public.Base, "AGENTS.md"), filepath.Join(f.physical.Base, "AGENTS.md")} {
		called := false
		if err := scope.WithDocument(relative, func(string, func() error) error { called = true; return nil }); err == nil || called {
			t.Fatalf("generated document scope accepted another namespace: %s %v", relative, err)
		}
	}
	if _, err := f.service.ReadMaterialsPreview(f.public.Root, "../../AGENTS.md"); err == nil {
		t.Fatal("generated document access expanded ordinary materials permissions")
	}
	scope.Close()
	if err := scope.Check(); err == nil {
		t.Fatal("closed document scope retained authority")
	}
}

func TestLocalTaskDocumentScopeRejectsMismatchedTask(t *testing.T) {
	f := localTaskDocumentFixture(t)
	for _, task := range []string{filepath.Join(f.aliasRoot, "other-task"), f.physical.Base} {
		if scope, err := OpenLocalTaskDocumentScope(task, f.public.Owned); err == nil {
			scope.Close()
			t.Fatalf("materials from another task namespace were accepted: %s", task)
		}
	}
}

func TestLocalTaskDocumentScopeRejectsTaskLinkAndReplacement(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		t.Run(map[bool]string{false: "link", true: "directory-replacement"}[replacement], func(t *testing.T) {
			f := localTaskDocumentFixture(t)
			scope, err := OpenLocalTaskDocumentScope(f.public.Base, f.public.Owned)
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Close()
			task := f.physical.Base
			moved := task + "-old"
			var renameErr error
			if !replacement {
				renameErr = os.Rename(task, moved)
				if runtime.GOOS == "windows" && os.IsPermission(renameErr) {
					localOperationBytes(t, filepath.Join(task, "AGENTS.md"), []byte("router sentinel"))
					return
				}
				if renameErr != nil {
					t.Fatal(renameErr)
				}
				localOperationLink(t, task, moved)
			}
			called := false
			err = scope.WithDocument("AGENTS.md", func(path string, check func() error) error {
				called = true
				if !replacement {
					t.Fatal("linked task reached the IO callback")
				}
				renameErr = os.Rename(task, moved)
				if renameErr != nil {
					return renameErr
				}
				if err := os.CopyFS(task, os.DirFS(moved)); err != nil {
					t.Fatal(err)
				}
				if err := check(); err == nil {
					t.Fatal("replaced task directory retained commit authority")
				}
				return check()
			})
			if err == nil || called != replacement {
				t.Fatalf("unsafe generated document parent was accepted: callback=%v error=%v", called, err)
			}
			localOperationBytes(t, filepath.Join(task, "AGENTS.md"), []byte("router sentinel"))
			if runtime.GOOS != "windows" || !os.IsPermission(renameErr) {
				localOperationBytes(t, filepath.Join(moved, "AGENTS.md"), []byte("router sentinel"))
			}
		})
	}
}
