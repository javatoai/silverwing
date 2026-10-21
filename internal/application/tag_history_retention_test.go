package application

import (
	"context"
	"path/filepath"
	"testing"

	"silverwing-go/internal/delivery"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

func TestTagHistoryReadPortsApplyRetentionToCompleteTaskGroups(t *testing.T) {
	for _, entry := range []struct {
		name, kind string
		probe      bool
	}{
		{"dispatch-history", "tag.history", false},
		{"probe-history", "tag.history", true},
		{"probe-refresh", "tag.history.refresh", true},
	} {
		t.Run(entry.name, func(t *testing.T) {
			a, scopes, _ := tagJobManagerFixture(t, "synthetic", "other")
			scope := scopes["synthetic"]
			config, err := a.Config.Load()
			if err != nil {
				t.Fatal(err)
			}
			config.TagHistoryMaxGroups = 2
			if err := a.Config.Save(config); err != nil {
				t.Fatal(err)
			}
			batch := "newest-batch"
			kept := map[string]bool{}
			for i := 0; i < 5; i++ {
				updated := []string{"2026-10-20 12:04:00", "2026-10-20 12:04:00", "2026-10-20 12:03:00", "2026-10-20 12:02:00", "2026-10-20 12:01:00"}[i]
				operation := domain.TagOperation{OperationID: delivery.NewTagJobID(), FolderName: scope.TaskID,
					ServiceName: "fixture", RepositoryID: "repository", SourceBranch: "feature/fixture", Remote: "origin",
					TagMode: domain.TagBuildCurrentBranch, State: domain.TagOperationFailed,
					CreatedAt: "2026-10-19 12:00:00", UpdatedAt: updated, ConflictFiles: []string{},
					GenbuStatus: domain.GenbuTagProbeStatus{Build: domain.GenbuUnknown, UAT: domain.GenbuUnknown, Production: domain.GenbuUnknown}}
				if i < 2 {
					operation.BatchID = &batch
				}
				if i < 3 {
					kept[operation.OperationID] = true
				}
				saveTagProbeFixture(t, scope.TaskPath, operation)
				operation.OperationID = delivery.NewTagJobID()
				saveTagProbeFixture(t, scopes["other"].TaskPath, operation)
			}
			probeCalls := 0
			a.tagJobManager().probe = func(context.Context, domain.AppConfig, []domain.TaskManifest, bool, []string, func(string, domain.TagOperation)) (bool, error) {
				probeCalls++
				return false, nil
			}
			action := tagProbeFixtureAction(scope, entry.kind, "")
			var result ActionResult
			var streamed []TagRecord
			if entry.probe {
				result, err = a.DispatchTagProbe(context.Background(), action, func(record TagRecord) { streamed = append(streamed, record) })
			} else {
				result, err = a.Dispatch(context.Background(), action)
			}
			if err != nil {
				t.Fatal(err)
			}
			records, ok := result.Data.([]TagRecord)
			if refresh, refreshOK := result.Data.(TagHistoryRefreshResult); refreshOK {
				records, ok = refresh.Records, true
			}
			if !ok || len(records) != 3 {
				t.Fatalf("want two complete groups (three records), got %d records (%T)", len(records), result.Data)
			}
			for _, record := range append(records, streamed...) {
				if !kept[record.ID] || record.TaskID != scope.TaskID {
					t.Fatalf("deleted or foreign record escaped through read/stream: %+v", record)
				}
			}
			if probeCalls != 0 {
				t.Fatal("local read or unpublished history queried Genbu")
			}
			// A fresh service must observe the deletion on disk, not just a filtered UI.
			service := a.delivery(config)
			persisted, err := service.History(context.Background(), scope.TaskID)
			other, otherErr := service.History(context.Background(), filepath.Base(scopes["other"].TaskPath))
			if err != nil || otherErr != nil || len(persisted) != 3 || len(other) != 5 {
				t.Fatalf("retention persistence/task isolation: own=%d other=%d err=%v/%v", len(persisted), len(other), err, otherErr)
			}
		})
	}
}

func TestTagHistoryRetentionDefersWhileBuildHoldsTaskLease(t *testing.T) {
	a, scope, _ := tagJobProbeFixture(t)
	config, err := a.Config.Load()
	if err != nil {
		t.Fatal(err)
	}
	config.TagHistoryMaxGroups = 1
	if err := a.Config.Save(config); err != nil {
		t.Fatal(err)
	}
	locks := workspace.NewOperationLocks(a.Paths.Locks)
	if err := locks.WithTask(scope.TaskPath, func() error {
		result, err := a.DispatchTagProbe(context.Background(), tagProbeFixtureAction(scope, "tag.history", ""), nil)
		if err != nil {
			return err
		}
		if records := result.Data.([]TagRecord); len(records) != 2 {
			t.Fatalf("retention changed an active build's records: %d", len(records))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	result, err := a.DispatchTagProbe(context.Background(), tagProbeFixtureAction(scope, "tag.history", ""), nil)
	if err != nil {
		t.Fatal(err)
	}
	if records := result.Data.([]TagRecord); len(records) != 1 {
		t.Fatalf("retention did not resume after the build lease was released: %d", len(records))
	}
}

func TestTagHistoryRetentionRejectsCanceledOrRecreatedTaskRequest(t *testing.T) {
	for _, kind := range []string{"canceled", "recreated"} {
		t.Run(kind, func(t *testing.T) {
			a, scope, _ := tagJobProbeFixture(t)
			config, err := a.Config.Load()
			if err != nil {
				t.Fatal(err)
			}
			config.TagHistoryMaxGroups = 1
			if err := a.Config.Save(config); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if kind == "canceled" {
				cancel()
			} else {
				manifest, err := a.Manifests.Load(scope.TaskPath)
				if err != nil {
					t.Fatal(err)
				}
				manifest.CreatedAt = "2026-10-20 13:00:00"
				if err := a.Manifests.Save(scope.TaskPath, manifest); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := a.DispatchTagProbe(ctx, tagProbeFixtureAction(scope, "tag.history", ""), nil); err == nil {
				t.Fatal("stale request accepted")
			}
			items, err := a.delivery(config).History(context.Background(), scope.TaskID)
			if err != nil || len(items) != 2 {
				t.Fatalf("rejected request changed history: %d, %v", len(items), err)
			}
		})
	}
}
