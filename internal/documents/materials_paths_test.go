package documents

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

func TestMaterialsCollectionCreateReuseAndReadOnly(t *testing.T) {
	root := t.TempDir()
	planned, err := ValidateMaterialsPaths(root)
	if err != nil || planned.Requirements != filepath.Join(root, "requirements") {
		t.Fatalf("preview: %+v %v", planned, err)
	}
	if _, err := os.Stat(planned.Requirements); !os.IsNotExist(err) {
		t.Fatal("preview created collection")
	}
	paths, err := EnsureMaterialsPaths(root)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(paths.Requirements, "keep.md")
	if err := os.WriteFile(file, []byte("\xef\xbb\xbf原文\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := EnsureMaterialsPaths(root + string(filepath.Separator)); err != nil || got != paths {
		t.Fatalf("reuse: %+v %v", got, err)
	}
	if text, err := os.ReadFile(file); err != nil || string(text) != "\xef\xbb\xbf原文\r\n" {
		t.Fatalf("data changed: %q %v", text, err)
	}
	if _, err := os.Stat(filepath.Join(paths.Requirements, "requirements")); !os.IsNotExist(err) {
		t.Fatal("duplicated requirements")
	}
}

func TestMaterialsCollectionRejectsFileAndUnwrappedYears(t *testing.T) {
	root := t.TempDir()
	collection := filepath.Join(root, "requirements")
	if err := os.WriteFile(collection, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureMaterialsPaths(root); err == nil || !strings.Contains(err.Error(), collection) {
		t.Fatalf("collection conflict: %v", err)
	}
	if content, err := os.ReadFile(collection); string(content) != "keep" || err != nil {
		t.Fatalf("collection file changed: %q %v", content, err)
	}
	old := t.TempDir()
	unwrapped := filepath.Join(old, "2026", "7132215081-需求")
	if err := os.MkdirAll(unwrapped, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureMaterialsPaths(old); err == nil || !strings.Contains(err.Error(), unwrapped) {
		t.Fatalf("unwrapped annual directory conflict: %v", err)
	}
	if _, err := os.Stat(filepath.Join(old, "requirements")); !os.IsNotExist(err) {
		t.Fatal("created collection beside unresolved annual data")
	}
}

func TestMaterialsCollectionRejectsGitMarkersOnlyInCollection(t *testing.T) {
	for _, markerType := range []string{"directory", "file", "dangling-link"} {
		t.Run(markerType, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "requirements")
			collection := filepath.Join(root, "requirements")
			if err := os.MkdirAll(collection, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(root, ".git"), 0700); err != nil {
				t.Fatal(err)
			}
			if _, err := ValidateMaterialsPaths(root); err != nil {
				t.Fatalf("Git marker at configured root: %v", err)
			}
			marker := filepath.Join(collection, ".git")
			switch markerType {
			case "directory":
				if err := os.Mkdir(marker, 0700); err != nil {
					t.Fatal(err)
				}
			case "file":
				if err := os.WriteFile(marker, []byte("gitdir: ../metadata"), 0600); err != nil {
					t.Fatal(err)
				}
			case "dangling-link":
				localMaterialsDirectoryLink(t, marker, filepath.Join(collection, "missing"))
			}
			before, err := os.Lstat(marker)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ValidateMaterialsPaths(root); err == nil || !strings.Contains(err.Error(), marker) {
				t.Fatalf("validation missed nested Git marker: %v", err)
			}
			if _, err := EnsureMaterialsPaths(root); err == nil || !strings.Contains(err.Error(), marker) {
				t.Fatalf("preparation missed nested Git marker: %v", err)
			}
			if after, err := os.Lstat(marker); err != nil || !os.SameFile(before, after) {
				t.Fatalf("nested Git marker changed: %v", err)
			}
		})
	}
}

func TestMaterialsPreparationSharesExactRepositoryLock(t *testing.T) {
	parent := t.TempDir()
	root, subdirectory := filepath.Join(parent, "requirements"), "研发"
	collection := filepath.Join(root, "requirements")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	config := domain.AppConfig{RequirementMaterialsRoot: &root, RequirementMaterialsSubdirectory: &subdirectory}
	service := NewPreparationService(storage.Paths{}, nil)
	ctx := WithMaterialsCreationTime(context.Background(), time.Date(2026, 1, 1, 12, 0, 0, 0, time.Local))
	link := "https://project.feishu.cn/obt/bug/detail/123"
	parentRelease, err := storage.AcquireMaterialsRepositoryLock(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer parentRelease()
	plan, err := service.Plan(ctx, config, link, "需求", "名称")
	want := filepath.Join(collection, "2026", "123-名称", subdirectory)
	if err != nil || plan.DocumentationDirectory != want {
		t.Fatalf("plan: %+v %v", plan, err)
	}
	release, err := storage.AcquireMaterialsRepositoryLock(root)
	if err != nil {
		t.Fatal(err)
	}
	func() {
		defer release()
		if _, err := service.Plan(ctx, config, link, "需求", "名称"); !errors.Is(err, storage.ErrLockBusy) {
			t.Fatalf("plan bypassed configured root lock: %v", err)
		}
		if _, err := service.Materialize(ctx, config, plan); !errors.Is(err, storage.ErrLockBusy) {
			t.Fatalf("materialization bypassed configured root lock: %v", err)
		}
		result, err := service.Ensure(ctx, config, link, "名称")
		if err != nil || result.Status != domain.MaterialsFailed || result.FailureReason == nil || !strings.Contains(*result.FailureReason, root) {
			t.Fatalf("ensure bypassed configured root lock: %+v %v", result, err)
		}
		if _, err := os.Lstat(collection); !os.IsNotExist(err) {
			t.Fatalf("planning or blocked preparation changed collection: %v", err)
		}
	}()
	materialized, err := service.Materialize(ctx, config, plan)
	if err != nil || materialized.Plan.DocumentationDirectory != want {
		t.Fatalf("materialize with parent lock held: %+v %v", materialized, err)
	}
	ready, err := service.Ensure(ctx, config, link, "其他名称")
	if err != nil || ready.Status != domain.MaterialsReady || ready.WriteRoot == nil || *ready.WriteRoot != want {
		t.Fatalf("ensure with parent lock held: %+v %v", ready, err)
	}
}
