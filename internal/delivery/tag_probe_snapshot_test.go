package delivery

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

func TestTagProbeSnapshotReportsCannotMutateSharedCandidates(t *testing.T) {
	service, config, tasks, operations, runner := tagProbeProgressFixture(t)
	close(runner.slow)
	directory := filepath.Join(value(config.TaskRoot), tasks[0].TaskDirectoryName)
	operation := operations[0]
	checked := "2026-10-29 21:00:00"
	operation.GenbuStatus.CheckedAt, operation.ConflictFiles = &checked, []string{"owned.txt"}
	if err := service.store.save(context.Background(), directory, operation); err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.ReadTagProbeSnapshot(context.Background(), config, tasks)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range snapshot.Records() {
		if record.Report.Operation.OperationID == operation.OperationID {
			*record.Report.Operation.Tag = "changed by consumer"
			*record.Report.Operation.GenbuStatus.CheckedAt = "changed by consumer"
			record.Report.Operation.ConflictFiles[0] = "changed by consumer"
		}
	}
	for _, record := range snapshot.Records() {
		if record.Report.Operation.OperationID == operation.OperationID && (value(record.Report.Operation.Tag) != value(operation.Tag) || value(record.Report.Operation.GenbuStatus.CheckedAt) != checked || record.Report.Operation.ConflictFiles[0] != "owned.txt") {
			t.Fatal("consumer mutation changed another repository's round candidates")
		}
	}
}

func TestTagProbeSnapshotReusesCandidatesAndKeepsUnselectedNewerRelease(t *testing.T) {
	service, config, tasks, operations, runner := tagProbeProgressFixture(t)
	close(runner.slow)
	directory := filepath.Join(value(config.TaskRoot), tasks[0].TaskDirectoryName)
	newest, older := operations[2], operations[0]
	newest.GenbuStatus.UAT = domain.GenbuSuccess
	if err := service.store.save(context.Background(), directory, newest); err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.ReadTagProbeSnapshot(context.Background(), config, tasks)
	if err != nil || len(snapshot.Records()) != len(operations) {
		t.Fatalf("read candidates: %v", err)
	}
	// This would fail a second history scan. All lanes must reuse the accepted
	// round snapshot while retaining the store's point CAS re-read.
	if err := os.WriteFile(filepath.Join(directory, tagOperationDirectory, strings.Repeat("f", 32)+".json"), []byte("{not-json"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := WithTagProbeSnapshot(context.Background(), snapshot)
	_, err = service.ProbeWithProgress(ctx, config, tasks, false, []string{older.OperationID}, nil)
	if err != nil {
		t.Fatalf("a lane rescanned round history: %v", err)
	}
	stored, err := service.store.load(directory, older.OperationID)
	if err != nil || !stored.GenbuStatus.StoppedByNewerRelease {
		t.Fatalf("unselected newer release was omitted: %+v, %v", stored.GenbuStatus, err)
	}
	runner.mu.Lock()
	queries := len(runner.calls)
	runner.mu.Unlock()
	if queries != 0 {
		t.Fatalf("older operation queried despite newer released candidate: %d", queries)
	}
	changedConfig := config
	changedConfig.Theme = domain.ThemeLight
	if _, err := service.ProbeWithProgress(ctx, changedConfig, tasks, false, nil, nil); err == nil {
		t.Fatal("snapshot crossed configuration scope")
	}
}

func TestTagProbeSnapshotPreservesCASAndFinalPointRead(t *testing.T) {
	service, config, tasks, operations, runner := tagProbeProgressFixture(t)
	close(runner.slow)
	directory := filepath.Join(value(config.TaskRoot), tasks[0].TaskDirectoryName)
	service.manifests = storage.NewManifestStore()
	tasks[0].Services = nil // The narrow probe fixture has no persisted workspace definition.
	if err := service.manifests.Save(directory, tasks[0]); err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.ReadTagProbeSnapshot(context.Background(), config, tasks)
	if err != nil {
		t.Fatal(err)
	}
	external := operations[0]
	message := "external accepted change"
	external.Message = &message
	if err := service.store.save(context.Background(), directory, external); err != nil {
		t.Fatal(err)
	}
	_, err = service.ProbeWithProgress(WithTagProbeSnapshot(context.Background(), snapshot), config, tasks, true, []string{external.OperationID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := service.store.load(directory, external.OperationID)
	if err != nil || value(stored.Message) != value(external.Message) || stored.GenbuStatus.Build != external.GenbuStatus.Build {
		t.Fatalf("round snapshot bypassed the CAS reread: %+v, %v", stored, err)
	}
	if err := os.WriteFile(filepath.Join(directory, tagOperationDirectory, strings.Repeat("f", 32)+".json"), []byte("{not-json"), 0600); err != nil {
		t.Fatal(err)
	}
	records, err := service.ReadTagProbeRecords(context.Background(), config, tasks, []string{external.OperationID})
	if err != nil || len(records) != 1 || value(records[0].Report.Operation.Message) != value(external.Message) {
		t.Fatalf("final status was not a point reread: %+v, %v", records, err)
	}
	replacement := tasks[0]
	replacement.CreatedAt = "2026-10-29 23:59:59"
	if err := service.manifests.Save(directory, replacement); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReadTagProbeRecords(context.Background(), config, tasks, []string{external.OperationID}); err == nil {
		t.Fatal("point status accepted a recreated task")
	}
}
