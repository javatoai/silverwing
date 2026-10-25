package application

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/production"
)

func TestProductionActionsRequireConfirmationAndKeepTaskTagHistorySeparate(t *testing.T) {
	a, _, _ := tagJobManagerFixture(t, "synthetic")
	result, err := a.Dispatch(context.Background(), Action{Kind: "production.list"})
	if err != nil {
		t.Fatal(err)
	}
	report, ok := result.Data.(production.Report)
	if !ok || len(report.Batches) != 0 {
		t.Fatalf("unexpected independent history: %#v", result.Data)
	}
	for _, kind := range []string{"production.create", "production.merge", "production.merge-main", "production.merge-release", "production.publish", "production.resume", "production.abort-merge", "production.end", "production.delete", "production.accept-remote", "production.rebase-pending", "production.reprepare-tag"} {
		_, err := a.Dispatch(context.Background(), Action{Kind: kind})
		if err == nil || !strings.Contains(err.Error(), "确认") {
			t.Fatalf("%s bypassed confirmation: %v", kind, err)
		}
	}
}

func TestProductionPreparationDecodesPartialPreviewAndRejectsRemovedService(t *testing.T) {
	a, _, _ := tagJobManagerFixture(t, "synthetic")
	b := production.Batch{ID: strings.Repeat("a", 32), Version: "1.0.2", ReleaseBranch: "release/1.0.2",
		Target: production.Target{Selection: production.Selection{GroupID: "default", ServiceID: "removed-service"},
			CommonDirectory: filepath.Join(a.Paths.Home, "synthetic.git")}}
	store := production.Store{Directory: filepath.Join(a.Paths.Home, "production-releases")}
	if err := store.Save(&b); err != nil {
		t.Fatal(err)
	}
	for _, wire := range []bool{false, true} {
		action := Action{Kind: "production.prepare-tag", Payload: map[string]any{"batchId": b.ID, "preview": production.Preview{Target: b.Target}}}
		if wire {
			data, err := json.Marshal(action)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(data, &action); err != nil {
				t.Fatal(err)
			}
		}
		_, err := a.Dispatch(context.Background(), action)
		if err == nil || !strings.Contains(err.Error(), "服务已移除或停用") {
			t.Fatalf("preparation rejected the preview shape or bypassed its target guard: %v", err)
		}
		unchanged, err := store.Load(b.ID)
		if err != nil || unchanged.Intent != nil || unchanged.TagPlan != nil || len(unchanged.Tags) != 0 {
			t.Fatalf("rejected read changed the batch: %v %+v", err, unchanged)
		}
	}
}
