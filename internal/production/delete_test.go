package production

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

func localDeleteFixture(t *testing.T) (*Service, Batch, string) {
	t.Helper()
	paths := storage.NewPaths(t.TempDir())
	// Nil CLI/config dependencies make any accidental remote/config access fail.
	s := New(paths, nil, nil, nil)
	b := Batch{ID: newID(), Ended: true, Phase: "ENDED", Version: "1.0.2", ReleaseBranch: "release/1.0.2", CreatedAt: timestamp(),
		Target: Target{Selection: Selection{GroupID: "group", ServiceID: "service"}, ServiceName: "Synthetic service", Repository: filepath.Join(paths.Home, "missing repository"), CommonDirectory: filepath.Join(paths.Home, "missing repository", ".git"), Remote: "origin", MainBranch: "master"}}
	if err := s.Store.Save(&b); err != nil {
		t.Fatal(err)
	}
	token, err := BatchRecordToken(b)
	if err != nil {
		t.Fatal(err)
	}
	return s, b, token
}

func TestProductionDeleteOnlyRemovesEndedLocalRecord(t *testing.T) {
	s, b, _ := localDeleteFixture(t)
	worktree := filepath.Join(s.Paths.Home, "production-worktrees", "retained")
	files := map[string]string{
		filepath.Join(worktree, "conflict.txt"):                                        "uncommitted user resolution",
		filepath.Join(b.Target.Repository, "source.go"):                                "user repository content",
		filepath.Join(b.Target.CommonDirectory, "refs", "heads", "master"):             "retained master reference",
		filepath.Join(s.Paths.Home, "remote.git", "refs", "heads", "release", "1.0.2"): "remote release",
		filepath.Join(s.Paths.Home, "remote.git", "refs", "tags", "1.0.2"):             "remote tag",
		filepath.Join(s.Paths.Tasks, "task", "tag-history.json"):                       "independent task Tag history",
	}
	for path, content := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	b.Intent = &Intent{Kind: "feature", Preview: Preview{ProductionStatus: unknownStatus()}, Merge: &Merge{Worktree: worktree, Conflicts: []string{"conflict.txt"}}}
	b.RetainedWorktrees = []string{worktree}
	b.Tags = []Tag{{Name: "1.0.2", Commit: strings.Repeat("a", 40), Status: unknownStatus()}}
	if err := s.Store.Save(&b); err != nil {
		t.Fatal(err)
	}
	other := b
	other.ID = newID()
	if err := s.Store.Save(&other); err != nil {
		t.Fatal(err)
	}
	token, err := BatchRecordToken(b)
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := s.Delete(context.Background(), b.ID, token); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Lstat(filepath.Join(s.Store.Directory, b.ID+".json")); !os.IsNotExist(err) {
		t.Fatalf("deleted batch record remains: %v", err)
	}
	if _, err := s.Store.Load(other.ID); err != nil {
		t.Fatal("deleting one batch removed another record", err)
	}
	for path, content := range files {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != content {
			t.Fatalf("local-record deletion changed retained content %s: %q %v", path, data, err)
		}
	}
}

func TestProductionDeleteRejectsStaleAndActiveRecords(t *testing.T) {
	for _, change := range []string{"active", "saved-update", "raw-update", "recreated", "target", "empty-token", "wrong-token", "invalid-id", "cancelled"} {
		t.Run(change, func(t *testing.T) {
			s, b, token := localDeleteFixture(t)
			path := filepath.Join(s.Store.Directory, b.ID+".json")
			id := b.ID
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch change {
			case "active":
				b.Ended = false
				if err := s.Store.Save(&b); err != nil {
					t.Fatal(err)
				}
				token, _ = BatchRecordToken(b)
			case "saved-update":
				b.Message = "a newer probe result"
				if err := s.Store.Save(&b); err != nil {
					t.Fatal(err)
				}
			case "raw-update", "recreated", "target":
				if change == "recreated" {
					b.CreatedAt = "another creation identity"
				} else if change == "target" {
					b.Target.Repository = "a different repository"
				} else {
					b.Message = "changed without updating its timestamp"
				}
				data, err := json.Marshal(b)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			case "empty-token":
				token = ""
			case "wrong-token":
				token = strings.Repeat("0", 64)
			case "invalid-id":
				id = "../" + b.ID
			case "cancelled":
				cancel()
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Delete(ctx, id, token); err == nil {
				t.Fatal("unsafe/stale deletion was accepted")
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != string(before) {
				t.Fatalf("rejected deletion modified the record: %v", err)
			}
		})
	}
}

func TestProductionDeleteSharesRepositoryLockAndLateWritesCannotRecreate(t *testing.T) {
	s, b, token := localDeleteFixture(t)
	if err := s.locks.WithRepository(b.Target.CommonDirectory, func() error {
		if err := s.Delete(context.Background(), b.ID, token); !errors.Is(err, storage.ErrLockBusy) {
			t.Fatalf("deletion bypassed an existing repository operation: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(context.Background(), b.ID, token); err != nil {
		t.Fatal(err)
	}
	if _, err := s.withRecord(context.Background(), b.ID, true, func(*Batch, domain.AppConfig) error {
		t.Fatal("a deleted record reached a later write callback")
		return nil
	}); err == nil {
		t.Fatal("late record operation recreated a deleted batch")
	}
}

func TestProductionDeleteFailureLeavesTheRecord(t *testing.T) {
	s, b, token := localDeleteFixture(t)
	s.Store.removeFile = func(path string) error {
		if path != filepath.Join(s.Store.Directory, b.ID+".json") {
			t.Fatal("removal escaped the single confirmed JSON record", path)
		}
		return os.ErrPermission
	}
	if err := s.Delete(context.Background(), b.ID, token); !errors.Is(err, os.ErrPermission) {
		t.Fatal("deletion failure was hidden", err)
	}
	if _, err := s.Store.Load(b.ID); err != nil {
		t.Fatal("failed deletion discarded its record", err)
	}
}

func TestProductionDeleteRejectsNonRegularRecords(t *testing.T) {
	s, b, token := localDeleteFixture(t)
	path := filepath.Join(s.Store.Directory, b.ID+".json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(path, "retained.txt")
	if err := os.WriteFile(marker, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(context.Background(), b.ID, token); err == nil {
		t.Fatal("non-regular record was accepted for deletion")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("record deletion recursed into a directory", err)
	}
}
