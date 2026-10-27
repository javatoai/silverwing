package application

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
	"silverwing-go/internal/workspace"
)

func TestMaterialsGitScopeAndDocumentMutationGuards(t *testing.T) {
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	config, err := a.Config.Load()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "requirements")
	sub := "研发"
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	config.RequirementMaterialsRoot = &root
	config.RequirementMaterialsSubdirectory = &sub
	settings := domain.DefaultMaterialsGitSyncSettings()
	settings.RepositoryURL = "https://example.invalid/docs.git"
	config.MaterialsGitSync = &settings
	if err := a.Config.Save(config); err != nil {
		t.Fatal(err)
	}
	scope := MaterialsGitScope(a.Paths.Home, config)
	scope.Branch = "wrong"
	if _, err := a.SyncMaterialsGit(context.Background(), scope, true, nil); !errors.Is(err, workspace.ErrMaterialsGitScopeChanged) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".git")); !os.IsNotExist(err) {
		t.Fatal("stale scope initialized repository")
	}
	unlock, err := storage.AcquireLock(filepath.Join(root, ".silverwing-requirement-materials.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, err := a.lockMaterialsGitDocumentMutation("document.save", filepath.Join(root, "123-需求", sub)); !errors.Is(err, storage.ErrLockBusy) {
		t.Fatalf("document mutation ignored shared root lock: %v", err)
	}
	release, err := a.lockMaterialsGitDocumentMutation("document.read", root)
	if err != nil {
		t.Fatal(err)
	}
	release()
	a.materialsGitGate.Lock()
	if _, err := a.documentAction(context.Background(), Action{Kind: "document.save"}); err == nil {
		t.Fatal("document write ignored Git gate")
	}
	a.materialsGitGate.Unlock()
}

func TestMaterialsGitTargetEditDisablesAutoSync(t *testing.T) {
	config := domain.DefaultAppConfig()
	s := domain.DefaultMaterialsGitSyncSettings()
	s.RepositoryURL = "https://example.invalid/docs.git"
	s.Enabled = true
	config.MaterialsGitSync = &s
	next := s
	next.Branch = "docs/new"
	raw, _ := json.Marshal(next)
	updated, err := mergeConfigurationFields(config, ConfigFieldsUpdate{Fields: map[string]json.RawMessage{"materialsGitSync": raw}})
	if err != nil || updated.MaterialsGitSettings().Enabled || updated.MaterialsGitSettings().Branch != "docs/new" {
		t.Fatalf("target edit: %+v %v", updated.MaterialsGitSettings(), err)
	}
}
