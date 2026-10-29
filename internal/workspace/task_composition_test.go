package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

type taskCompositionManifestStore struct {
	*storage.ManifestStore
	saves int
}

func (s *taskCompositionManifestStore) Save(path string, manifest domain.TaskManifest) error {
	s.saves++
	return s.ManifestStore.Save(path, manifest)
}

func TestTaskCompositionUsesInjectedMaterialsAndStorage(t *testing.T) {
	defaults, _, _, config := newTaskCreationLockTestServices(t)
	store := &taskCompositionManifestStore{ManifestStore: storage.NewManifestStore()}
	dependencies := TaskApplicationDependencies(*defaults)
	dependencies.Manifests = store
	calls := 0
	dependencies.RequirementMaterials = func(context.Context, domain.AppConfig, string, string) (domain.RequirementMaterialsDirectory, *string, error) {
		calls++
		id, reason := "123", "injected source unavailable"
		return domain.RequirementMaterialsDirectory{Status: domain.MaterialsFailed, FailureReason: &reason}, &id, nil
	}
	service := NewTaskApplicationServiceWithDependencies(dependencies)
	manifest, err := service.CreateTask(context.Background(), config, CreateTaskRequest{FolderName: "injected", RequirementLink: "https://project.feishu.cn/obt/bug/detail/123"})
	if err != nil || calls != 1 || store.saves == 0 || manifest.RequirementMaterials.Status != domain.MaterialsFailed {
		t.Fatalf("explicit dependencies ignored: calls=%d saves=%d manifest=%+v err=%v", calls, store.saves, manifest, err)
	}
	if manifest.RequirementID == nil || *manifest.RequirementID != "123" || service.Manifests != store {
		t.Fatalf("injected source identity or storage lost: %+v", manifest)
	}
}

type taskDocumentReader struct {
	manifest domain.TaskManifest
	loads    int
}

func (s *taskDocumentReader) Load(string) (domain.TaskManifest, error) {
	s.loads++
	return s.manifest, nil
}

func TestAgentDocumentsAcceptReadOnlyManifestDependency(t *testing.T) {
	paths := storage.NewPaths(t.TempDir())
	reader := &taskDocumentReader{manifest: domain.DefaultTaskManifest()}
	documents := NewAgentDocumentService(paths, reader)
	task := filepath.Join(paths.Home, "task")
	notes := documents.TaskNotesPath(task, reader.manifest)
	if err := os.MkdirAll(filepath.Dir(notes), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(notes, []byte("injected reader rules"), 0600); err != nil {
		t.Fatal(err)
	}
	text, err := documents.ReadTaskNotes(task)
	if err != nil || text != "injected reader rules" || reader.loads != 1 {
		t.Fatalf("document reader ignored: text=%q loads=%d err=%v", text, reader.loads, err)
	}
}
