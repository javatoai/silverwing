package documents

import (
	"os"
	"path/filepath"
	"testing"
)

func localTaskDocumentFixture(t *testing.T) localOperationFixture {
	t.Helper()
	f := newLocalOperationFixture(t)
	localOperationFile(t, filepath.Join(f.physical.Base, "AGENTS.md"), []byte("router sentinel"))
	localOperationFile(t, filepath.Join(f.physical.Base, ".workspace", "agent", "TASK-CONTEXT.md"), []byte("context sentinel"))
	return f
}

func TestLocalTaskDocumentScopeUsesOnlyGeneratedPhysicalPaths(t *testing.T) {
	f := localTaskDocumentFixture(t)
	scope, err := OpenLocalTaskDocumentScope(f.public.Base, f.public.Owned)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Close()
	for _, relative := range []string{"AGENTS.md", ".workspace/agent/TASK-CONTEXT.md"} {
		if err := scope.WithDocument(relative, func(path string, check func() error) error {
			if path != filepath.Join(f.physical.Base, filepath.FromSlash(relative)) {
				t.Fatalf("generated file did not use its physical path: %s", path)
			}
			return check()
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, relative := range []string{"../AGENTS.md", ".workspace/agent/../agent/TASK-CONTEXT.md", ".workspace/agent/TASK-RULES.md", filepath.Join(f.public.Base, "AGENTS.md"), filepath.Join(f.physical.Base, "AGENTS.md")} {
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

func TestLocalTaskDocumentScopeRejectsAgentLinkAndReplacement(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		t.Run(map[bool]string{false: "link", true: "directory-replacement"}[replacement], func(t *testing.T) {
			f := localTaskDocumentFixture(t)
			scope, err := OpenLocalTaskDocumentScope(f.public.Base, f.public.Owned)
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Close()
			agent := filepath.Join(f.physical.Base, ".workspace", "agent")
			moved := agent + "-old"
			if !replacement {
				if err := os.Rename(agent, moved); err != nil {
					t.Fatal(err)
				}
				localOperationLink(t, agent, moved)
			}
			called := false
			err = scope.WithDocument(".workspace/agent/TASK-CONTEXT.md", func(path string, check func() error) error {
				called = true
				if !replacement {
					t.Fatal("linked agent parent reached the IO callback")
				}
				if err := os.Rename(agent, moved); err != nil {
					t.Fatal(err)
				}
				if err := os.CopyFS(agent, os.DirFS(moved)); err != nil {
					t.Fatal(err)
				}
				if err := check(); err == nil {
					t.Fatal("replaced real agent directory retained commit authority")
				}
				return check()
			})
			if err == nil || called != replacement {
				t.Fatalf("unsafe generated document parent was accepted: callback=%v error=%v", called, err)
			}
			localOperationBytes(t, filepath.Join(moved, "TASK-CONTEXT.md"), []byte("context sentinel"))
			localOperationBytes(t, filepath.Join(agent, "TASK-CONTEXT.md"), []byte("context sentinel"))
			localOperationBytes(t, filepath.Join(f.physical.Base, "AGENTS.md"), []byte("router sentinel"))
		})
	}
}
