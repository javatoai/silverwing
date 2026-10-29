package desktop

import (
	"context"
	"errors"
	"testing"

	"silverwing-go/internal/application"
)

type pageFullReader struct {
	calls int
	value application.Snapshot
	err   error
}

func (reader *pageFullReader) Snapshot(context.Context) (application.Snapshot, error) {
	reader.calls++
	return reader.value, reader.err
}

type pageScopedReader struct {
	pageFullReader
	callsScoped int
	request     application.SnapshotRequest
	valueScoped application.Snapshot
	errScoped   error
	afterRead   func()
}

func (reader *pageScopedReader) SnapshotFor(_ context.Context, request application.SnapshotRequest) (application.Snapshot, error) {
	reader.callsScoped++
	reader.request = request
	if reader.afterRead != nil {
		reader.afterRead()
	}
	return reader.valueScoped, reader.errScoped
}

func TestPageSnapshotSettingsReadsOnlyConfiguration(t *testing.T) {
	reader := &pageScopedReader{valueScoped: application.Snapshot{Home: "isolated", ConfigJSON: "config"}}
	read, err := readPageSnapshot(context.Background(), reader, "settings", "isolated", "config")
	if err != nil || !read.partial || reader.calls != 0 || reader.callsScoped != 1 || reader.request != (application.SnapshotRequest{}) {
		t.Fatalf("settings read = %+v, %v; calls full=%d scoped=%d request=%+v", read, err, reader.calls, reader.callsScoped, reader.request)
	}
}

func TestPageSnapshotFallsBackForLegacyBackendAndOtherPages(t *testing.T) {
	legacy := &pageFullReader{value: application.Snapshot{ConfigJSON: "full"}}
	read, err := readPageSnapshot(context.Background(), legacy, "settings", "isolated", "config")
	if err != nil || read.partial || legacy.calls != 1 || read.snapshot.ConfigJSON != "full" {
		t.Fatalf("legacy fallback = %+v, %v; calls=%d", read, err, legacy.calls)
	}
	reader := &pageScopedReader{pageFullReader: pageFullReader{value: legacy.value}}
	read, err = readPageSnapshot(context.Background(), reader, "tasks", "isolated", "config")
	if err != nil || read.partial || reader.calls != 1 || reader.callsScoped != 0 {
		t.Fatalf("task read = %+v, %v; full=%d scoped=%d", read, err, reader.calls, reader.callsScoped)
	}
}

func TestPageSnapshotConfigurationOrHomeChangeReloadsWholeGraph(t *testing.T) {
	for _, snapshot := range []application.Snapshot{
		{Home: "isolated", ConfigJSON: "new"},
		{Home: "other", ConfigJSON: "config"},
	} {
		reader := &pageScopedReader{pageFullReader: pageFullReader{value: application.Snapshot{Home: "fresh", ConfigJSON: "full"}}, valueScoped: snapshot}
		read, err := readPageSnapshot(context.Background(), reader, "settings", "isolated", "config")
		if err != nil || read.partial || reader.calls != 1 || read.snapshot.Home != "fresh" {
			t.Fatalf("changed scope %+v = %+v, %v; calls=%d", snapshot, read, err, reader.calls)
		}
	}
}

func TestPageSnapshotCancellationAndErrorsDoNotStartFallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	reader := &pageScopedReader{afterRead: cancel}
	if _, err := readPageSnapshot(ctx, reader, "settings", "isolated", "config"); !errors.Is(err, context.Canceled) || reader.calls != 0 {
		t.Fatalf("canceled read = %v, calls=%d", err, reader.calls)
	}
	sentinel := errors.New("read failed")
	reader = &pageScopedReader{errScoped: sentinel}
	if _, err := readPageSnapshot(context.Background(), reader, "settings", "isolated", "config"); !errors.Is(err, sentinel) || reader.calls != 0 {
		t.Fatalf("failed read = %v, calls=%d", err, reader.calls)
	}
}

func TestPageSnapshotMergeRetainsLatestUnreadCollections(t *testing.T) {
	current := application.Snapshot{Tasks: []application.Task{{ID: "latest"}}, TagHistory: []application.TagRecord{{ID: "tag"}}, Skills: []application.Skill{{Name: "skill"}}}
	read := pageSnapshotRead{partial: true, snapshot: application.Snapshot{ConfigJSON: "config"}}
	merged := read.merge(current)
	if len(merged.Tasks) != 1 || merged.Tasks[0].ID != "latest" || len(merged.TagHistory) != 1 || len(merged.Skills) != 1 || merged.ConfigJSON != "config" {
		t.Fatalf("partial merge lost unread collections: %+v", merged)
	}
	read.request.Tasks, read.request.Skills, read.request.TagHistory = true, true, true
	merged = read.merge(current)
	if len(merged.Tasks) != 0 || len(merged.Skills) != 0 || len(merged.TagHistory) != 0 {
		t.Fatalf("explicit empty collections were not published: %+v", merged)
	}
	read.partial = false
	if merged = read.merge(current); len(merged.Tasks) != 0 {
		t.Fatal("full snapshot must replace collections")
	}
}
