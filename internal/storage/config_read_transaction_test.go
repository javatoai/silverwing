package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/domain"
)

func TestConfigReadersWaitForWholeShardTransaction(t *testing.T) {
	for _, reader := range []string{"load", "preview", "raw"} {
		t.Run(reader, func(t *testing.T) {
			store := NewConfigStore(NewPaths(t.TempDir()))
			config, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			config.Repositories = []domain.RepositoryConfig{{ID: "repo", Name: "repo", RootPath: "C:/repo", GitCommonDirectory: "C:/repo/.git"}}
			config.Services = []domain.ServiceConfig{{ID: "service", RepositoryID: "repo", DisplayName: "Service", MasterBranch: "origin/main", TestTagBaselineRef: "origin/test", DevelopmentTool: domain.DevelopmentToolIntelliJIdea,
				Modules: []domain.ServiceModuleConfig{{ID: "default", Name: "default", Strategy: domain.WorkspaceStandardWorktree, TagMode: domain.TagBuildMergeToTargetBranch}}}}
			config = normalizeConfigCollections(config)
			release, err := store.acquireMutationLock()
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			// Hold the writer lock while the service still points to a repository
			// that will only appear in the second shard replacement.
			if err := store.writeShards(shardsFor(config), []string{"services.json"}); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				other := NewConfigStore(store.Paths())
				if reader == "raw" {
					snapshot := other.FileSnapshot()
					if snapshot.ReadError != nil {
						done <- errors.New(*snapshot.ReadError)
						return
					}
					if snapshot.Content == nil || strings.Count(*snapshot.Content, `"id": "repo"`) != 1 || strings.Count(*snapshot.Content, `"id": "service"`) != 1 {
						done <- errors.New("raw snapshot missed committed repository or service")
						return
					}
					done <- nil
					return
				}
				var observed domain.AppConfig
				var readErr error
				if reader == "preview" {
					observed, readErr = other.LoadForPreview(context.Background())
				} else {
					observed, readErr = other.Load()
				}
				if readErr == nil && (len(observed.Services) != 1 || len(observed.Repositories) != 1) {
					readErr = errors.New("reader missed committed repository or service")
				}
				done <- readErr
			}()
			select {
			case err := <-done:
				t.Fatalf("reader escaped the active transaction: %v", err)
			case <-time.After(40 * time.Millisecond):
			}
			if err := store.writeShards(shardsFor(config), []string{"workspace.json"}); err != nil {
				t.Fatal(err)
			}
			release()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("reader did not resume after commit")
			}
		})
	}
}

func TestConfigPreviewLockCancellationAndEmptyHome(t *testing.T) {
	paths := NewPaths(filepath.Join(t.TempDir(), "absent"))
	store := NewConfigStore(paths)
	if _, err := store.LoadForPreview(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(paths.Home); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("preview initialized home: %v", err)
	}
	release, err := store.acquireMutationLock()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if _, err := store.LoadForPreview(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting preview did not honor cancellation: %v", err)
	}
}

func TestConfigReadersRejectExternalWriterLock(t *testing.T) {
	store := NewConfigStore(NewPaths(t.TempDir()))
	if _, err := store.Load(); err != nil {
		t.Fatal(err)
	}
	release, err := AcquireLock(filepath.Join(store.Paths().Locks, configLockFile))
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := store.Load(); !errors.Is(err, ErrLockBusy) {
		t.Fatalf("load ignored external writer: %v", err)
	}
	if _, err := store.LoadForPreview(context.Background()); !errors.Is(err, ErrLockBusy) {
		t.Fatalf("preview ignored external writer: %v", err)
	}
}
