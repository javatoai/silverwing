package delivery

import (
	"context"
	"encoding/json"
	"testing"

	"silverwing-go/internal/platform"
	"silverwing-go/internal/storage"
)

func TestApplicationLogsClearKeepsActualWriterRunning(t *testing.T) {
	paths := storage.NewPaths(t.TempDir())
	writer := &Service{paths: paths}
	reader := platform.NewService(paths, nil, nil)
	writer.recordApplicationEvent("ERROR", "before", "before-clear fixture", nil)
	if _, err := reader.Execute(context.Background(), "logs.clear", map[string]any{"confirmed": true, "expectedHome": paths.Home}); err != nil {
		t.Fatal(err)
	}
	writer.recordApplicationEvent("ERROR", "after", "after-clear fixture", nil)
	data, err := reader.Execute(context.Background(), "logs.list", nil)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(encoded, &rows); err != nil || len(rows) != 1 || rows[0]["event"] != "after" {
		t.Fatalf("logger stopped or old logs returned: %s %v", encoded, err)
	}
}
