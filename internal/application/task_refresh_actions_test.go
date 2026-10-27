package application

import (
	"context"

	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

// Root runs this filesystem case in an isolated temp/home. The child agent's
// focused run selects only the pure metadata and race checks below.
func TestTaskRefreshDispatchDocuments(t *testing.T) {
	paths := storage.NewPaths(filepath.Join(t.TempDir(), "home"))
	isolated := domain.DefaultAppConfig()
	isolated.TaskRoot = &paths.Tasks
	if err := storage.NewConfigStore(paths).Save(isolated); err != nil {
		t.Fatal(err)
	}
	app, err := New(paths.Home)
	if err != nil {
		t.Fatal(err)
	}
	config, err := app.Config.Load()
	if err != nil {
		t.Fatal(err)
	}
	taskPath := filepath.Join(*config.TaskRoot, "refresh-proof")
	if err = os.MkdirAll(filepath.Join(taskPath, ".workspace", "agent"), 0700); err != nil {
		t.Fatal(err)
	}
	manifest := domain.DefaultTaskManifest()
	manifest.TaskDirectoryName, manifest.FolderName = "refresh-proof", "refresh-proof"

	manifest.CreatedAt, manifest.UpdatedAt = "2026-10-13T00:00:00Z", "2026-10-13T00:00:00Z"
	manifest.AgentDocumentLayout = domain.AgentDocumentReferencedV2
	if err = app.Manifests.Save(taskPath, manifest); err != nil {
		t.Fatal(err)
	}
	router := []byte("# 用户根路由\n保持原文\n")
	notes := []byte("# 用户备注\n保留手工内容\n")
	if err = os.WriteFile(filepath.Join(taskPath, "AGENTS.md"), router, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(taskPath, ".workspace", "agent", "TASK-RULES.md"), notes, 0600); err != nil {
		t.Fatal(err)
	}
	oldContext := []byte("# 旧生成上下文\nrefresh must replace this generated content\n")
	if err = os.WriteFile(filepath.Join(taskPath, ".workspace", "agent", "TASK-CONTEXT.md"), oldContext, 0600); err != nil {
		t.Fatal(err)
	}
	result, err := app.Dispatch(context.Background(), Action{Kind: "task.refresh"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, ok := result.Data.(Snapshot)
	if !ok || len(snapshot.Tasks) != 1 || snapshot.Tasks[0].ID != "refresh-proof" {
		t.Fatalf("refresh result %+v", result)
	}
	contextBytes, err := os.ReadFile(filepath.Join(taskPath, ".workspace", "agent", "TASK-CONTEXT.md"))
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(contextBytes, oldContext) || !strings.HasPrefix(string(contextBytes), "# 任务上下文\n\n") ||
		!strings.Contains(string(contextBytes), "## 工作范围\n") {
		t.Fatalf("generated context %q", contextBytes)
	}
	routerAfter, err := os.ReadFile(filepath.Join(taskPath, "AGENTS.md"))
	if err != nil || !reflect.DeepEqual(routerAfter, router) {
		t.Fatalf("root router changed: %q, err %v", routerAfter, err)
	}
	notesAfter, err := os.ReadFile(filepath.Join(taskPath, ".workspace", "agent", "TASK-RULES.md"))
	if err != nil || !reflect.DeepEqual(notesAfter, notes) {
		t.Fatalf("manual notes changed: %q, err %v", notesAfter, err)
	}
}
