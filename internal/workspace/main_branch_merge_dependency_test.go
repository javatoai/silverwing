package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
)

func TestMainBranchMergeAndLifecycleShareInjectedManifestReader(t *testing.T) {
	root := t.TempDir()
	taskPath := filepath.Join(root, "task")
	if err := os.Mkdir(taskPath, 0755); err != nil {
		t.Fatal(err)
	}
	manifest := domain.DefaultTaskManifest()
	manifest.TaskDirectoryName, manifest.CreatedAt = "task", "current-task"
	reader := &taskDocumentReader{manifest: manifest}
	service := NewWorkspaceMainBranchMergeService(NewGitClient(nil, "git-must-not-run"), reader, NewOperationLocks(filepath.Join(root, "locks")), nil)
	if service.Manifests != reader || service.Lifecycle.Manifests != reader {
		t.Fatal("merge composition replaced the supplied manifest reader")
	}
	config := domain.DefaultAppConfig()
	config.TaskRoot = &root
	// There is deliberately no on-disk manifest. A lifecycle without workspace
	// entries can restore only by reading the same injected task repository.
	if restored, err := service.Lifecycle.RestoreAll(context.Background(), config, taskPath, manifest); err != nil || len(restored) != 0 || reader.loads != 1 {
		t.Fatalf("lifecycle reader bypassed: restored=%+v loads=%d err=%v", restored, reader.loads, err)
	}
	expected := manifest
	expected.CreatedAt = "old-task"
	_, err := service.Merge(context.Background(), config, taskPath, expected, domain.ServiceWorkspace{})
	if err == nil || !strings.Contains(err.Error(), "任务身份已变化") || reader.loads != 2 {
		t.Fatalf("merge did not revalidate injected task identity: loads=%d err=%v", reader.loads, err)
	}
}
