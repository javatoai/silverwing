package application_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/application"
	"silverwing-go/internal/documents"
	"silverwing-go/internal/workspace"
)

const localCatalogStart = "<!-- silverwing:materials:start -->"
const localCatalogEnd = "<!-- silverwing:materials:end -->"

func localCatalogDocumentPaths(task string) []string {
	return []string{filepath.Join(task, workspace.AgentRouterFileName), filepath.Join(task, workspace.AgentWorkspaceDir, workspace.AgentDirectoryName, workspace.AgentContextFileName)}
}

func localCatalogRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func localCatalogOutsideBlock(t *testing.T, data []byte) ([]byte, []byte) {
	t.Helper()
	start, end := bytes.Index(data, []byte(localCatalogStart)), bytes.Index(data, []byte(localCatalogEnd))
	if start < 0 || end < start || bytes.Count(data, []byte(localCatalogStart)) != 1 || bytes.Count(data, []byte(localCatalogEnd)) != 1 {
		t.Fatalf("generated document has an invalid managed block: %s", data)
	}
	return data[:start], data[end+len(localCatalogEnd):]
}

func TestLocalMaterialsApplicationCatalogRefreshesAliasWithoutWarning(t *testing.T) {
	f := newLocalMaterialsOperationFixture(t)
	task := filepath.Join(f.alias, "task-a")
	scope := documents.LocalTaskMaterials(task)
	before := map[string][]byte{}
	for _, path := range localCatalogDocumentPaths(task) {
		data := append(localCatalogRead(t, f.physical(t, path)), []byte("\r\nUSER NOTE OUTSIDE THE MANAGED BLOCK\r\n")...)
		if err := os.WriteFile(f.physical(t, path), data, 0600); err != nil {
			t.Fatal(err)
		}
		before[path] = data
	}
	peer := filepath.Join(scope.Root, "new-reference-directory")
	if err := os.Mkdir(f.physical(t, peer), 0700); err != nil {
		t.Fatal(err)
	}
	result, err := f.app.Dispatch(context.Background(), application.Action{Kind: "document.list", TaskID: "task-a"})
	if err != nil || result.Path != scope.Root || result.Message != "资料已刷新" {
		t.Fatalf("real document.list still emitted a catalog warning under TaskRoot alias: %+v %v", result, err)
	}
	if _, err := application.ResultData[[]documents.Entry](result); err != nil {
		t.Fatal(err)
	}
	for path, original := range before {
		data := localCatalogRead(t, f.physical(t, path))
		prefix, suffix := localCatalogOutsideBlock(t, original)
		updatedPrefix, updatedSuffix := localCatalogOutsideBlock(t, data)
		if !bytes.Equal(prefix, updatedPrefix) || !bytes.Equal(suffix, updatedSuffix) || !bytes.Contains(data, []byte(peer)) || bytes.Contains(data, []byte(filepath.Join(f.actual, "task-a"))) {
			t.Fatalf("catalog refresh changed user bytes or exposed the physical namespace: %s", data)
		}
	}
	// Only the existing generated router is recoverable. No parent directory
	// or custom context document may be fabricated as part of a refresh.
	router := localCatalogDocumentPaths(task)[0]
	if err := os.Remove(f.physical(t, router)); err != nil {
		t.Fatal(err)
	}
	result, err = f.app.Dispatch(context.Background(), application.Action{Kind: "document.list", TaskID: "task-a"})
	if err != nil || result.Message != "资料已刷新" {
		t.Fatalf("missing generated router was not safely restored: %+v %v", result, err)
	}
	data := localCatalogRead(t, f.physical(t, router))
	localCatalogOutsideBlock(t, data)
	if !bytes.Contains(data, []byte(peer)) || bytes.Contains(data, []byte(filepath.Join(f.actual, "task-a"))) {
		t.Fatalf("restored router lost its public material paths: %s", data)
	}
}

func TestLocalMaterialsApplicationCatalogPreservesCustomRouter(t *testing.T) {
	for _, custom := range []string{"# custom user router\r\nkeep every byte\r\n", localCatalogStart + "\nmissing end"} {
		t.Run(map[bool]string{false: "custom", true: "malformed"}[strings.Contains(custom, localCatalogStart)], func(t *testing.T) {
			f := newLocalMaterialsOperationFixture(t)
			task := filepath.Join(f.alias, "task-a")
			paths := localCatalogDocumentPaths(task)
			if err := os.WriteFile(f.physical(t, paths[0]), []byte(custom), 0600); err != nil {
				t.Fatal(err)
			}
			peer := filepath.Join(documents.LocalTaskMaterials(task).Root, "new-peer")
			if err := os.Mkdir(f.physical(t, peer), 0700); err != nil {
				t.Fatal(err)
			}
			result, err := f.app.Dispatch(context.Background(), application.Action{Kind: "document.list", TaskID: "task-a"})
			if err != nil || !strings.Contains(result.Message, "本地说明更新失败") {
				t.Fatalf("custom catalog warning was lost or became a fatal list error: %+v %v", result, err)
			}
			localMaterialsOperationRequireBytes(t, f.physical(t, paths[0]), []byte(custom))
			if data := localCatalogRead(t, f.physical(t, paths[1])); !bytes.Contains(data, []byte(peer)) {
				t.Fatalf("custom router prevented the safe context from refreshing: %s", data)
			}
		})
	}
}

func TestLocalMaterialsApplicationCatalogRejectsInternalLinks(t *testing.T) {
	for _, target := range []string{"agent", "router", "context"} {
		t.Run(target, func(t *testing.T) {
			f := newLocalMaterialsOperationFixture(t)
			task := filepath.Join(f.alias, "task-a")
			paths := localCatalogDocumentPaths(task)
			peer := filepath.Join(documents.LocalTaskMaterials(task).Root, "new-peer")
			if err := os.Mkdir(f.physical(t, peer), 0700); err != nil {
				t.Fatal(err)
			}
			contextPath := f.physical(t, paths[1])
			original := localCatalogRead(t, contextPath)
			external := filepath.Join(f.base, "external-agent")
			var sentinel string
			if target == "agent" {
				agent := filepath.Dir(contextPath)
				if err := os.CopyFS(external, os.DirFS(agent)); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(agent, agent+"-original"); err != nil {
					t.Fatal(err)
				}
				localMaterialsOperationDirectoryLink(t, agent, external)
				sentinel = filepath.Join(external, workspace.AgentContextFileName)
			} else {
				path := contextPath
				if target == "router" {
					path = f.physical(t, paths[0])
					original = localCatalogRead(t, path)
				}
				if err := os.WriteFile(external, original, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				// A hard link exercises the existing single-link update guard
				// without relying on privileged Windows file symlink creation.
				if err := os.Link(external, path); err != nil {
					t.Fatal(err)
				}
				sentinel = external
			}
			result, err := f.app.Dispatch(context.Background(), application.Action{Kind: "document.list", TaskID: "task-a"})
			if err != nil || !strings.Contains(result.Message, "本地说明更新失败") {
				t.Fatalf("unsafe catalog target was accepted or made list fatal: %+v %v", result, err)
			}
			localMaterialsOperationRequireBytes(t, sentinel, original)
		})
	}
}
