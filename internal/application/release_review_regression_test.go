package application

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

func TestReleaseReviewWorkspaceOperationsRetainCatalogWarnings(t *testing.T) {
	for _, kind := range []string{"task.materials-retry", "task.archive", "task.restore"} {
		t.Run(kind, func(t *testing.T) {
			a, scope, taskPath := annualApplicationFixture(t)
			manifest, err := a.Manifests.Load(taskPath)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "task.restore" {
				manifest.LifecycleStatus = domain.TaskArchived
				if err := a.Manifests.Save(taskPath, manifest); err != nil {
					t.Fatal(err)
				}
			}
			router := filepath.Join(taskPath, workspace.AgentRouterFileName)
			rules := filepath.Join(taskPath, workspace.AgentWorkspaceDir, workspace.AgentDirectoryName, workspace.AgentRulesFileName)
			original := []byte("# user-owned instructions\r\nkeep original bytes  \n")
			for _, path := range []string{router, rules} {
				if err := os.WriteFile(path, original, 0644); err != nil {
					t.Fatal(err)
				}
			}
			peer := filepath.Join(scope.Root, "new-peer-materials")
			if err := os.Mkdir(peer, 0755); err != nil {
				t.Fatal(err)
			}
			result, err := a.Dispatch(context.Background(), Action{Kind: kind, TaskID: "task"})
			if err != nil {
				t.Fatalf("soft catalog warning made the operation fail: %v", err)
			}
			updated, err := a.Manifests.Load(taskPath)
			if err != nil || updated.RequirementMaterials.Status != domain.MaterialsReady {
				t.Fatalf("successful materials association was lost: %+v, %v", updated, err)
			}
			if kind == "task.archive" && updated.LifecycleStatus != domain.TaskArchived || kind == "task.restore" && updated.LifecycleStatus != domain.TaskActive {
				t.Fatalf("lifecycle operation did not commit: %+v", updated)
			}
			if strings.Count(result.Message, "警告：") != 1 || !strings.Contains(result.Message, "本地资料说明更新失败") || !strings.Contains(result.Message, "刷新资料可单独重试，不重复 Git 同步") {
				t.Fatalf("operation omitted or duplicated actionable catalog warning: %q", result.Message)
			}
			for _, path := range []string{router, rules} {
				got, err := os.ReadFile(path)
				if err != nil || string(got) != string(original) {
					t.Fatalf("user document was changed: %s, %q, %v", path, got, err)
				}
			}
			contextPath := filepath.Join(taskPath, workspace.AgentWorkspaceDir, workspace.AgentDirectoryName, workspace.AgentContextFileName)
			generated, err := os.ReadFile(contextPath)
			if err != nil || !strings.Contains(string(generated), peer) {
				t.Fatalf("safe generated context was not refreshed: %q, %v", generated, err)
			}
		})
	}
}
