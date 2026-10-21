package application

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/production"
	"silverwing-go/internal/storage"
)

func TestProductionDeleteActionOnlyRemovesConfirmedLocalHistory(t *testing.T) {
	for _, scenario := range []string{"unconfirmed", "active", "stale", "missing-token", "delete", "repeat"} {
		t.Run(scenario, func(t *testing.T) {
			// No config, CLI runner or network dependencies are supplied.
			a := &App{Paths: storage.NewPaths(t.TempDir())}
			s := production.New(a.Paths, nil, nil, nil)
			b := production.Batch{ID: strings.Repeat("a", 32), Ended: true, Version: "1.0.2", ReleaseBranch: "release/1.0.2", CreatedAt: "created", Phase: "ENDED",
				Target: production.Target{Selection: production.Selection{GroupID: "removed-group", ServiceID: "removed-service"}, CommonDirectory: filepath.Join(a.Paths.Home, "missing-repo", ".git")}}
			if scenario == "active" {
				b.Ended = false
			}
			if err := s.Store.Save(&b); err != nil {
				t.Fatal(err)
			}
			token, err := production.BatchRecordToken(b)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "stale" {
				b.Message = "updated after confirmation"
				if err := s.Store.Save(&b); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "missing-token" {
				token = ""
			}
			// Deletion must not depend on successfully listing unrelated history.
			other := filepath.Join(s.Store.Directory, strings.Repeat("b", 32)+".json")
			if err := os.WriteFile(other, []byte("unrelated malformed record"), 0600); err != nil {
				t.Fatal(err)
			}
			action := Action{Kind: "production.delete", Payload: map[string]any{"batchId": b.ID, "recordToken": token, "confirmed": scenario != "unconfirmed"}}
			raw, err := json.Marshal(action)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(raw, &action); err != nil {
				t.Fatal(err)
			}
			result, err := a.Dispatch(context.Background(), action)
			wantSuccess := scenario == "delete" || scenario == "repeat"
			if (err == nil) != wantSuccess {
				t.Fatalf("unexpected deletion result: %+v %v", result, err)
			}
			_, recordErr := os.Stat(filepath.Join(s.Store.Directory, b.ID+".json"))
			if os.IsNotExist(recordErr) != wantSuccess {
				t.Fatal("confirmation/current-record checks did not protect deletion")
			}
			if data, err := os.ReadFile(other); err != nil || string(data) != "unrelated malformed record" {
				t.Fatal("deleting one record touched unrelated history", err)
			}
			if wantSuccess && (result.Data != nil || !strings.Contains(result.Message, "已删除")) {
				t.Fatal("deletion returned an old batch or hid a completed removal")
			}
			if scenario == "repeat" {
				if _, err := a.Dispatch(context.Background(), action); err != nil {
					t.Fatal("retrying a completed local deletion failed", err)
				}
			}
		})
	}
}
