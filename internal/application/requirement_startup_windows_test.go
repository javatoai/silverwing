//go:build windows

package application

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

func TestRequirementMetadataStartupDispatchConfirmsIdentityAndCaches(t *testing.T) {
	home := t.TempDir()
	paths := storage.NewPaths(home)
	cli := filepath.Join(home, "fixture.ps1")
	const script = `$ErrorActionPreference='Stop'
if ($args[0] -eq 'config') { 'fixture-profile'; exit 0 }
if ($args[0] -eq 'auth') { '{"authenticated":true,"host":"project.feishu.cn"}'; exit 0 }
if ($args[0] -eq 'user') { '{"user_key":"fixture-account"}'; exit 0 }
if ($args[0] -eq 'workitem') { '{"work_item_attribute":{"work_item_name":"Startup requirement","work_item_status":{"name":"In Progress"},"role_members":[]}}'; exit 0 }
throw 'unexpected command'
`
	if err := os.WriteFile(cli, append([]byte{0xef, 0xbb, 0xbf}, []byte(script)...), 0600); err != nil {
		t.Fatal(err)
	}
	config := domain.DefaultAppConfig()
	config.TaskRoot = &paths.Tasks
	config.MeegleExecutablePath = &cli
	config.MeegleProjects = []domain.MeegleProjectConfig{{SimpleName: "fixture", ProjectKey: "project-a"}}
	if err := storage.NewConfigStore(paths).Save(config); err != nil {
		t.Fatal(err)
	}
	manifest := domain.DefaultTaskManifest()
	manifest.TaskDirectoryName, manifest.FolderName = "task", "task"

	manifest.CreatedAt, manifest.UpdatedAt = "2026-10-15", "2026-10-15"
	manifest.RequirementLink = "https://project.feishu.cn/fixture/technical/detail/101"
	if err := storage.NewManifestStore().Save(filepath.Join(paths.Tasks, "task"), manifest); err != nil {
		t.Fatal(err)
	}
	app, err := New(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Integrations.CloseRequirementDocuments(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	action := Action{Kind: "requirements.metadata", TaskID: "task", Path: manifest.RequirementLink, Payload: map[string]any{"force": false}}
	result, err := app.Dispatch(ctx, action)
	if err != nil {
		t.Fatalf("metadata startup without a preconfirmed identity: %v", err)
	}
	metadata, ok := result.Data.(RequirementMetadata)
	if !ok || metadata.Status == nil || *metadata.Status != "In Progress" {
		t.Fatalf("startup status was not preserved: %#v", result.Data)
	}
	encoded, err := json.Marshal(metadata)
	var decoded RequirementMetadata
	if err != nil || json.Unmarshal(encoded, &decoded) != nil || decoded.Status == nil || *decoded.Status != "In Progress" {
		t.Fatal("metadata could not cross the desktop JSON boundary")
	}
	// The persistent cache must serve the same request after the CLI disappears.
	if err := os.Rename(cli, cli+".disabled"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Dispatch(ctx, action); err != nil {
		t.Fatalf("warm metadata should not execute the unavailable CLI: %v", err)
	}
	for _, invalid := range []string{"", "raw-user-key"} {
		action.Payload["expectedIdentity"] = invalid
		if _, err := app.Dispatch(ctx, action); err == nil {
			t.Fatalf("explicit invalid identity %q was accepted", invalid)
		}
	}
}
