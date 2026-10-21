package integrations

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

func TestRequirementRefreshReadOrderRejectsLateAndChangedConfig(t *testing.T) {
	paths := storage.NewPaths(t.TempDir())
	store := storage.NewConfigStore(paths)
	config := domain.DefaultAppConfig()
	if err := store.Save(config); err != nil {
		t.Fatal(err)
	}
	config, _ = store.Load()
	s := NewService(paths, store, nil)
	old, _ := s.beginRequirementRead("metadata:a", false)
	fresh, _ := s.beginRequirementRead("metadata:a", true)
	if _, err := s.beginRequirementRead("metadata:a", false); err == nil {
		t.Fatal("ordinary read superseded active refresh")
	}
	writes := 0
	save := func() error { writes++; return nil }
	if err := s.commitRequirementRead(context.Background(), config, "metadata:a", fresh, save); err != nil {
		t.Fatal(err)
	}
	if err := s.commitRequirementRead(context.Background(), config, "metadata:a", old, save); !errors.Is(err, errRequirementReadSuperseded) {
		t.Fatal(err)
	}
	changed := config
	cli := "different-meegle"
	changed.MeegleExecutablePath = &cli
	if err := store.Save(changed); err != nil {
		t.Fatal(err)
	}
	if err := s.commitRequirementRead(context.Background(), config, "metadata:a", fresh, save); !errors.Is(err, errRequirementReadSuperseded) {
		t.Fatal(err)
	}
	if writes != 1 {
		t.Fatalf("stale writes: %d", writes)
	}
}

func TestRequirementRefreshCacheOverlaySurvivesRestartAndIsolatesScopes(t *testing.T) {
	paths := storage.NewPaths(filepath.Join(t.TempDir(), "home"))
	s := NewService(paths, nil, nil)
	item := domain.ParticipatedWorkItem{ProjectKey: "project-a", ProjectName: "space", Type: "userstory", ID: "101", URL: "https://project.feishu.cn/space/userstory/detail/101", Title: "old", MySprintEstimateDays: refreshString("2")}
	roles := []any{
		map[string]any{"name": "Dev Owner", "members": []any{map[string]any{"name": "new dev"}}},
		map[string]any{"name": "QC Owner", "members": []any{map[string]any{"name": "new QC"}}},
		map[string]any{"name": "产品经理", "members": []any{map[string]any{"name": "new PM"}}},
	}
	metadata := RequirementMetadataReadResult{ID: item.ID, ProjectKey: item.ProjectKey, Type: item.Type, Title: refreshString("new title"), Status: refreshString("Done"), WorkItem: metadataWorkItem(item.ProjectKey, item.Type, item.ID, refreshString("new title"), refreshString("Done"), roles), SprintEstimates: map[string]RequirementSprintEstimate{"project-a:900": {Days: refreshString("5"), FetchedAt: 200}}}
	if _, err := s.writeRequirementMetadataCache(requirementCacheKey("metadata", "account-a", item), metadata, 200); err != nil {
		t.Fatal(err)
	}
	// Restart/offline overlay is disk-only; there is no CLI or network fixture.
	s = NewService(paths, nil, nil)
	old := participatedResult{Items: []domain.ParticipatedWorkItem{item}, FetchedAt: 100, SelectedSprintKey: refreshString("project-a:900")}
	fresh := s.overlayRequirementList("account-a", old)
	got := fresh.Items[0]
	if got.Title != "new title" || valuePointer(got.Status) != "Done" || len(got.Developers) != 1 || len(got.QCOwners) != 1 || len(got.ProductManagers) != 1 || valuePointer(got.MySprintEstimateDays) != "5" {
		t.Fatalf("old list won over refreshed item: %+v", got)
	}
	old.SelectedSprintKey = refreshString("project-a:901")
	if got := s.overlayRequirementList("account-a", old).Items[0]; valuePointer(got.MySprintEstimateDays) != "2" || got.Title != "new title" {
		t.Fatal("estimate leaked across Sprints")
	}
	if got := s.overlayRequirementList("account-b", old).Items[0]; got.Title != "old" {
		t.Fatal("account cache leaked")
	}
	old.Items[0].ProjectKey = "project-b"
	if got := s.overlayRequirementList("account-a", old).Items[0]; got.Title != "old" {
		t.Fatal("project cache leaked")
	}
	old.Items[0] = item
	old.FetchedAt = 300
	if got := s.overlayRequirementList("account-a", old).Items[0]; got.Title != "old" {
		t.Fatal("old item refresh overwrote newer list")
	}
	if old.Items[0].Title != "old" {
		t.Fatal("overlay mutated caller snapshot")
	}
}

func TestRequirementRefreshTaskRolesUseExistingMapping(t *testing.T) {
	roles := []any{map[string]any{"name": "服务端工程师", "members": []any{map[string]any{"name": "dev"}}}, map[string]any{"name": "测试工程师", "members": []any{map[string]any{"name": "test"}}}}
	got := metadataWorkItem("p", "othertask", "1", nil, nil, roles)
	if len(got.Developers) != 1 || len(got.QCOwners) != 1 || len(got.ProductManagers) != 0 {
		t.Fatalf("task roles: %+v", got)
	}
}

func refreshString(value string) *string { return &value }
