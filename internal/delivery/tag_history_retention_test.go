package delivery

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
	"silverwing-go/internal/workspace"
)

func TestTagHistoryRetentionLimitIsPerTask(t *testing.T) {
	root := t.TempDir()
	config := domain.DefaultAppConfig()
	config.TaskRoot, config.TagHistoryMaxGroups = &root, 2
	s := &Service{locks: workspace.NewOperationLocks(filepath.Join(t.TempDir(), "locks"))}
	tasks := []domain.TaskManifest{{TaskDirectoryName: "a"}, {TaskDirectoryName: "b"}}
	for _, task := range tasks {
		directory := filepath.Join(root, task.TaskDirectoryName)
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 3; i++ {
			operation := domain.TagOperation{OperationID: NewTagJobID(), FolderName: task.TaskDirectoryName,
				ServiceName: "fixture", RepositoryID: "repository", SourceBranch: "feature/fixture", Remote: "origin",
				TagMode: domain.TagBuildCurrentBranch, State: domain.TagOperationFailed,
				CreatedAt: fmt.Sprintf("2026-10-20 12:0%d:00", i), UpdatedAt: fmt.Sprintf("2026-10-20 12:0%d:00", i), ConflictFiles: []string{},
				GenbuStatus: domain.GenbuTagProbeStatus{Build: domain.GenbuUnknown, UAT: domain.GenbuUnknown, Production: domain.GenbuUnknown}}
			if err := s.store.save(context.Background(), directory, operation); err != nil {
				t.Fatal(err)
			}
		}
	}
	deleted, err := s.EnforceHistoryRetention(context.Background(), config, tasks)
	if err != nil || deleted != 2 {
		t.Fatalf("want one deletion per task, got %d: %v", deleted, err)
	}
	for _, task := range tasks {
		records, err := s.store.list(filepath.Join(root, task.TaskDirectoryName))
		if err != nil || len(records) != 2 {
			t.Fatalf("task %s lost its own retention allowance: %d, %v", task.TaskDirectoryName, len(records), err)
		}
	}
}

func TestTagHistoryRetentionRemovesWholeOldBatchAndSummary(t *testing.T) {
	for _, change := range []string{"none", "recreated", "corrupt"} {
		t.Run(change, func(t *testing.T) {
			paths := storage.NewPaths(t.TempDir())
			s := NewService(paths, nil, nil, nil, nil)
			config := domain.DefaultAppConfig()
			config.TaskRoot, config.TagHistoryMaxGroups = &paths.Tasks, 2
			task := domain.DefaultTaskManifest()
			task.FolderName, task.TaskDirectoryName, task.FeatureBranch = "fixture", "fixture", "feature/fixture"
			task.CreatedAt, task.UpdatedAt = "2026-10-19 12:00:00", "2026-10-19 12:00:00"
			directory := filepath.Join(paths.Tasks, task.TaskDirectoryName)
			if err := s.manifests.Save(directory, task); err != nil {
				t.Fatal(err)
			}
			batch := "old-batch"
			kept := map[string]bool{}
			for i := 0; i < 4; i++ {
				operation := domain.TagOperation{OperationID: NewTagJobID(), FolderName: task.TaskDirectoryName,
					ServiceName: "fixture", RepositoryID: "repository", SourceBranch: task.FeatureBranch, Remote: "origin",
					TagMode: domain.TagBuildCurrentBranch, State: domain.TagOperationFailed,
					CreatedAt: task.CreatedAt, UpdatedAt: fmt.Sprintf("2026-10-20 12:0%d:00", i), ConflictFiles: []string{},
					GenbuStatus: domain.GenbuTagProbeStatus{Build: domain.GenbuUnknown, UAT: domain.GenbuUnknown, Production: domain.GenbuUnknown}}
				if i < 2 {
					operation.BatchID = &batch
				} else {
					kept[operation.OperationID] = true
				}
				if err := s.store.save(context.Background(), directory, operation); err != nil {
					t.Fatal(err)
				}
				if err := s.store.appendHistory(context.Background(), directory, operation); err != nil {
					t.Fatal(err)
				}
			}
			if change == "recreated" {
				replacement := task
				replacement.CreatedAt = "2026-10-20 13:00:00"
				if err := s.manifests.Save(directory, replacement); err != nil {
					t.Fatal(err)
				}
			} else if change == "corrupt" {
				if err := os.WriteFile(filepath.Join(directory, tagOperationDirectory, NewTagJobID()+".json"), []byte("not json"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			deleted, err := s.EnforceHistoryRetention(context.Background(), config, []domain.TaskManifest{task})
			if change == "none" && (err != nil || deleted != 2) {
				t.Fatalf("whole oldest batch was not removed: %d, %v", deleted, err)
			}
			if change != "none" && (err == nil || deleted != 0) {
				t.Fatalf("unsafe retention was accepted: %d, %v", deleted, err)
			}
			history, readErr := os.ReadFile(filepath.Join(directory, "tag-build-history.jsonl"))
			if readErr != nil {
				t.Fatal(readErr)
			}
			lines := strings.Split(strings.TrimSpace(string(history)), "\n")
			want := 2
			if change != "none" {
				want = 4
			}
			if len(lines) != want {
				t.Fatalf("summary has %d entries, want %d", len(lines), want)
			}
			for _, line := range lines {
				var entry domain.TagBuildHistoryEntry
				if err := json.Unmarshal([]byte(line), &entry); err != nil {
					t.Fatal(err)
				}
				if change == "none" && !kept[entry.OperationID] {
					t.Fatal("deleted batch remained in the persisted summary")
				}
			}
		})
	}
}
