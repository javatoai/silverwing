//go:build windows

package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
)

func TestTaskForceRecycleRejectsJunctionWorkspace(t *testing.T) {
	f := newTaskRecycleFixture(t)
	external := filepath.Join(f.base, "external repository")
	if err := os.Mkdir(external, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(external, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("external content"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(f.task, "workspace")
	if out, err := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", target, external).CombinedOutput(); err != nil {
		t.Fatalf("create isolated junction: %s %v", out, err)
	}
	manifest, err := f.service.Manifests.Load(f.task)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Services = []domain.ServiceWorkspace{{RepositoryID: "removed-repo", ServiceName: "service", WorktreePath: target, Strategy: domain.WorkspaceStandardWorktree}}
	preview, _, _, err := f.service.inspectForceDeletion(t.Context(), f.config, f.task, manifest)
	if err != nil || preview.CanForce || preview.Guard != "" || !strings.Contains(preview.Reason, target) {
		t.Fatalf("unsafe workspace acquired force approval: %+v %v", preview, err)
	}
	if contents, err := os.ReadFile(sentinel); err != nil || string(contents) != "external content" {
		t.Fatalf("external contents changed: %q %v", contents, err)
	}
}
