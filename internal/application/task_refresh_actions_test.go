package application

import (
	"context"
	"errors"
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
	manifest.FolderName, manifest.TaskDirectoryName, manifest.FeatureBranch = "refresh-proof", "refresh-proof", "feature/refresh-proof"
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
		!strings.Contains(string(contextBytes), "### 本任务可改动的 Worktree\n\n| 服务名 | 创建基线 | 策略 | Worktree 路径 |\n|--------|----------|------|---------------|\n| （无） |  |  |  |\n") {
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

func taskRefreshTestRepository(id string) domain.RepositoryConfig {
	root := filepath.Join("fixture", id)
	return domain.RepositoryConfig{ID: id, Name: id, RootPath: root, GitCommonDirectory: filepath.Join(root, ".git")}
}

func TestRefreshTaskRepositories(t *testing.T) {
	before := []domain.RepositoryConfig{taskRefreshTestRepository("a"), taskRefreshTestRepository("b")}
	t.Run("metadata refresh preserves repository IDs and input", func(t *testing.T) {
		original := append([]domain.RepositoryConfig(nil), before...)
		branch := "feature/current"
		got, err := refreshTaskRepositories(context.Background(), before, func(path string) (domain.RepositoryConfig, error) {
			return domain.RepositoryConfig{ID: "inspected-ID", RootPath: path, GitCommonDirectory: filepath.Join(path, ".git"), CurrentBranch: &branch}, nil
		})
		if err != nil || len(got) != 2 || got[0].ID != "a" || got[1].ID != "b" || got[0].CurrentBranch == nil || *got[0].CurrentBranch != branch || !reflect.DeepEqual(before, original) {
			t.Fatalf("got %+v, err %v", got, err)
		}
	})
	t.Run("changed common directory is rejected", func(t *testing.T) {
		got, err := refreshTaskRepositories(context.Background(), before, func(path string) (domain.RepositoryConfig, error) {
			return domain.RepositoryConfig{RootPath: path, GitCommonDirectory: "other-repository"}, nil
		})
		if err == nil || got != nil {
			t.Fatalf("got %+v, err %v", got, err)
		}
	})
	t.Run("pre-cancel performs no inspection", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		calls := 0
		got, err := refreshTaskRepositories(ctx, before, func(string) (domain.RepositoryConfig, error) { calls++; return domain.RepositoryConfig{}, nil })
		if !errors.Is(err, context.Canceled) || calls != 0 || got != nil {
			t.Fatalf("calls %d, err %v", calls, err)
		}
	})
	t.Run("cancellation does not publish partial metadata", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		calls := 0
		got, err := refreshTaskRepositories(ctx, before, func(path string) (domain.RepositoryConfig, error) {
			calls++
			cancel()
			return domain.RepositoryConfig{RootPath: path, GitCommonDirectory: filepath.Join(path, ".git")}, nil
		})
		if !errors.Is(err, context.Canceled) || calls != 1 || got != nil {
			t.Fatalf("calls %d, err %v", calls, err)
		}
	})
	t.Run("repository inspection error remains available", func(t *testing.T) {
		cause := errors.New("repository unavailable")
		_, err := refreshTaskRepositories(context.Background(), before, func(string) (domain.RepositoryConfig, error) { return domain.RepositoryConfig{}, cause })
		if !errors.Is(err, cause) {
			t.Fatalf("err %v", err)
		}
	})
}

func TestApplyRefreshedTaskRepositories(t *testing.T) {
	a, b := taskRefreshTestRepository("a"), taskRefreshTestRepository("b")
	branch := "updated-branch"
	refreshed := []domain.RepositoryConfig{a, b}
	refreshed[0].CurrentBranch = &branch
	t.Run("current order and unrelated settings survive", func(t *testing.T) {
		current := domain.AppConfig{Repositories: []domain.RepositoryConfig{b, a}, TagHistoryMaxGroups: 7}
		got, err := applyRefreshedTaskRepositories(current, []domain.RepositoryConfig{a, b}, refreshed)
		if err != nil || got.Repositories[0].ID != "b" || got.Repositories[1].CurrentBranch == nil || *got.Repositories[1].CurrentBranch != branch || got.TagHistoryMaxGroups != 7 || current.Repositories[1].CurrentBranch != nil {
			t.Fatalf("got %+v, err %v", got, err)
		}
	})
	for _, check := range []struct {
		name         string
		repositories []domain.RepositoryConfig
	}{
		{"repository removed", []domain.RepositoryConfig{a}},
		{"repository added", []domain.RepositoryConfig{a, b, taskRefreshTestRepository("c")}},
		{"repository root changed", []domain.RepositoryConfig{{ID: a.ID, RootPath: "changed-root", GitCommonDirectory: a.GitCommonDirectory}, b}},
		{"repository identity changed", []domain.RepositoryConfig{{ID: a.ID, RootPath: a.RootPath, GitCommonDirectory: "changed-common"}, b}},
	} {
		t.Run(check.name, func(t *testing.T) {
			current := domain.AppConfig{Repositories: check.repositories}
			got, err := applyRefreshedTaskRepositories(current, []domain.RepositoryConfig{a, b}, refreshed)
			if err == nil || !reflect.DeepEqual(got, current) {
				t.Fatalf("got %+v, err %v", got, err)
			}
		})
	}
}
