package application

import (
	"context"
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
