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

func localCatalogMarkStale(t *testing.T, path string) {
	t.Helper()
	data := localCatalogRead(t, path)
	if bytes.Count(data, []byte(localCatalogEnd)) != 1 {
		t.Fatal("missing managed block", path)
	}
	next := bytes.Replace(data, []byte(localCatalogEnd), []byte("旧版本资料目录说明，等待迁移\n"+localCatalogEnd), 1)
	if err := os.WriteFile(path, next, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestLocalMaterialsApplicationCatalogRefreshesWithoutWarning(t *testing.T) {
	f := newLocalMaterialsOperationFixture(t)
	task := filepath.Join(f.taskRoot, "task-a")
	scope := documents.LocalTaskMaterials(task)
	agents := filepath.Join(task, workspace.AgentRouterFileName)
	localCatalogMarkStale(t, agents)
	before := append(localCatalogRead(t, agents), []byte("\r\nUSER NOTE OUTSIDE THE MANAGED BLOCK\r\n")...)
	if err := os.WriteFile(agents, before, 0600); err != nil {
		t.Fatal(err)
	}
	peer := filepath.Join(scope.Root, "new-reference-directory")
	if err := os.Mkdir(peer, 0700); err != nil {
		t.Fatal(err)
	}
	result, err := f.app.Dispatch(context.Background(), application.Action{Kind: "document.list", TaskID: "task-a"})
	if err != nil || result.Path != scope.Root || result.Message != "资料已刷新" {
		t.Fatalf("document.list emitted a catalog warning: %+v %v", result, err)
	}
	if _, err := application.ResultData[[]documents.Entry](result); err != nil {
		t.Fatal(err)
	}
	data := localCatalogRead(t, agents)
	prefix, suffix := localCatalogOutsideBlock(t, before)
	updatedPrefix, updatedSuffix := localCatalogOutsideBlock(t, data)
	if !bytes.Equal(prefix, updatedPrefix) || !bytes.Equal(suffix, updatedSuffix) || bytes.Contains(data, []byte(peer)) || !bytes.Contains(data, []byte(scope.Owned)) || !bytes.Contains(data, []byte("主动阅读")) {
		t.Fatalf("catalog refresh changed user bytes or lost the materials guidance: %s", data)
	}
	if err := os.Remove(agents); err != nil {
		t.Fatal(err)
	}
	result, err = f.app.Dispatch(context.Background(), application.Action{Kind: "document.list", TaskID: "task-a"})
	if err != nil || result.Message != "资料已刷新" {
		t.Fatalf("missing generated router was not safely restored: %+v %v", result, err)
	}
	data = localCatalogRead(t, agents)
	localCatalogOutsideBlock(t, data)
	if bytes.Contains(data, []byte(peer)) || !bytes.Contains(data, []byte(scope.Owned)) || !bytes.Contains(data, []byte("主动阅读")) {
		t.Fatalf("restored router lost its public material paths: %s", data)
	}
}

func TestLocalMaterialsApplicationCatalogPreservesCustomRouter(t *testing.T) {
	for _, custom := range []string{"# custom user router\r\nkeep every byte\r\n", localCatalogStart + "\nmissing end"} {
		t.Run(map[bool]string{false: "custom", true: "malformed"}[strings.Contains(custom, localCatalogStart)], func(t *testing.T) {
			f := newLocalMaterialsOperationFixture(t)
			task := filepath.Join(f.taskRoot, "task-a")
			agents := filepath.Join(task, workspace.AgentRouterFileName)
			if err := os.WriteFile(agents, []byte(custom), 0600); err != nil {
				t.Fatal(err)
			}
			peer := filepath.Join(documents.LocalTaskMaterials(task).Root, "new-peer")
			if err := os.Mkdir(peer, 0700); err != nil {
				t.Fatal(err)
			}
			result, err := f.app.Dispatch(context.Background(), application.Action{Kind: "document.list", TaskID: "task-a"})
			if err != nil || !strings.Contains(result.Message, "本地说明更新失败") {
				t.Fatalf("custom catalog warning was lost or became a fatal list error: %+v %v", result, err)
			}
			localMaterialsOperationRequireBytes(t, agents, []byte(custom))
		})
	}
}

func TestLocalMaterialsApplicationCatalogRejectsAGENTSHardLink(t *testing.T) {
	f := newLocalMaterialsOperationFixture(t)
	agents := filepath.Join(f.taskRoot, "task-a", workspace.AgentRouterFileName)
	localCatalogMarkStale(t, agents)
	original := localCatalogRead(t, agents)
	external := filepath.Join(f.base, "external-agent")
	if err := os.WriteFile(external, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(agents); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(external, agents); err != nil {
		t.Fatal(err)
	}
	result, err := f.app.Dispatch(context.Background(), application.Action{Kind: "document.list", TaskID: "task-a"})
	if err != nil || !strings.Contains(result.Message, "本地说明更新失败") {
		t.Fatalf("unsafe catalog target was accepted or made list fatal: %+v %v", result, err)
	}
	localMaterialsOperationRequireBytes(t, external, original)
}
