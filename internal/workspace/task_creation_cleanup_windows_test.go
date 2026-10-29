//go:build windows

package workspace

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/documents"
)

func TestCreateTaskRollbackDoesNotFollowWorkspaceJunction(t *testing.T) {
	service, config, root := optionalServiceFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	taskPath := filepath.Join(root, "junction-task")
	external := filepath.Join(root, "external-task-documents")
	externalDocument := filepath.Join(external, AgentDirectoryName, AgentContextFileName)
	if err := os.MkdirAll(filepath.Dir(externalDocument), 0755); err != nil {
		t.Fatal(err)
	}
	original := []byte("external document must remain unchanged")
	if err := os.WriteFile(externalDocument, original, 0600); err != nil {
		t.Fatal(err)
	}
	var callbackErr error
	var fixtureErr error
	_, err := service.CreateTaskWithProgress(ctx, config, CreateTaskRequest{FolderName: "junction-task"}, func(event TaskCreationProgress) {
		if callbackErr != nil || event.Phase != TaskCreationPhaseSavingManifest || event.Status != TaskCreationProgressCompleted {
			return
		}
		junction := filepath.Join(taskPath, AgentWorkspaceDir)
		// Standalone creation now prepares local materials before saving the
		// manifest. Remove only those verified generated entries, then replace
		// the empty workspace directory with the malicious junction fixture.
		if callbackErr = documents.CleanupLocalTaskMaterials(taskPath); callbackErr != nil {
			return
		}
		if callbackErr = os.Remove(junction); callbackErr != nil {
			return
		}
		output, commandErr := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", junction, external).CombinedOutput()
		if commandErr != nil {
			fixtureErr = errors.New(string(output) + ": " + commandErr.Error())
			return
		}
		cancel()
	})
	if callbackErr != nil {
		t.Fatal(callbackErr)
	}
	if fixtureErr != nil {
		t.Skipf("junction fixture unavailable: %v", fixtureErr)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled task creation with retained junction: %v", err)
	}
	data, readErr := os.ReadFile(externalDocument)
	if readErr != nil || string(data) != string(original) {
		t.Fatalf("rollback followed junction into external task documents: %q %v", data, readErr)
	}
	if _, statErr := os.Lstat(filepath.Join(taskPath, AgentWorkspaceDir)); statErr != nil {
		t.Fatalf("rollback removed or redirected the task junction: %v", statErr)
	}
	if !strings.Contains(err.Error(), AgentWorkspaceDir) {
		t.Fatalf("cleanup error did not identify the protected junction: %v", err)
	}
}
