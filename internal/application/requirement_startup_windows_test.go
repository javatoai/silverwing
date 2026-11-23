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
	"silverwing-go/internal/workspace"
)

func TestRequirementMetadataStartupDispatchConfirmsIdentityAndCaches(t *testing.T) {
	home := t.TempDir()
	taskRoot := t.TempDir()
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
	app, err := New(home)
	if err != nil {
		t.Fatal(err)
	}
	selectProfileTaskRoot(t, app, taskRoot, "")
	config, err := app.Config.Load()
	if err != nil {
		t.Fatal(err)
	}
	config.MeegleExecutablePath = &cli
	config.MeegleProjects = []domain.MeegleProjectConfig{{SimpleName: "fixture", ProjectKey: "project-a"}}
	if err := app.Config.Save(config); err != nil {
		t.Fatal(err)
	}
	manifest := domain.DefaultTaskManifest()
	manifest.FolderName, manifest.TaskDirectoryName, manifest.FeatureBranch = "task", "task", "feature/test"
	manifest.CreatedAt, manifest.UpdatedAt = "2026-10-15", "2026-10-15"
	manifest.RequirementLink = "https://project.feishu.cn/fixture/technical/detail/101"
	if err := app.Manifests.Save(filepath.Join(taskRoot, "task"), manifest); err != nil {
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

func TestDirectRequirementLinkCanCreateTaskWithExistingMaterials(t *testing.T) {
	const directLink = "https://project.feishu.cn/fixture/6785bc520a0621258bcf0583/detail/7143084336"
	const standardLink = "https://project.feishu.cn/fixture/technical/detail/7143084336"
	home, taskRoot, materialsRoot := t.TempDir(), t.TempDir(), t.TempDir()
	cli := filepath.Join(home, "meegle-fixture.ps1")
	const script = `$ErrorActionPreference='Stop'
if ($args[0] -eq 'config' -and $args[1] -eq 'profile') { 'fixture-profile'; exit 0 }
if ($args[0] -eq 'auth' -and $args[1] -eq 'status') { '{"authenticated":true,"host":"project.feishu.cn"}'; exit 0 }
if ($args[0] -eq 'user' -and $args[1] -eq 'me') { '{"user_key":"fixture-account"}'; exit 0 }
if (($args -join ' ') -eq 'workitem get --project-key project-a --work-item-id 7143084336 --format json') {
  '{"work_item_id":"7143084336","name":"Fixture outside candidate list","description":"Direct link body","work_item_attribute":{"work_item_type":{"key":"6785bc520a0621258bcf0583"}}}'
  exit 0
}
throw ('unexpected Meegle command: ' + ($args -join ' '))
`
	if err := os.WriteFile(cli, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	app, err := New(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Integrations.CloseRequirementDocuments(context.Background()) })
	selectProfileTaskRoot(t, app, taskRoot, "")
	config, err := app.Config.Load()
	if err != nil {
		t.Fatal(err)
	}
	roleName := "dev"
	config.MeegleExecutablePath = &cli
	config.MeegleProjects = []domain.MeegleProjectConfig{{SimpleName: "fixture", ProjectKey: "project-a"}}
	config.RequirementMaterialsRoot, config.RequirementMaterialsSubdirectory = &materialsRoot, &roleName
	if err := app.Config.Save(config); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	content, err := app.ReadRequirement(ctx, RequirementReadRequest{URL: directLink})
	if err != nil || content.Data.ID != "7143084336" || content.Data.ProjectKey != "project-a" ||
		content.Data.Title != "Fixture outside candidate list" || content.Data.Body != "Direct link body" {
		t.Fatalf("direct link read: %+v, %v", content.Data, err)
	}
	create := func(name, link string) domain.TaskManifest {
		t.Helper()
		request := TaskCreateRequest{CreateTaskRequest: workspace.CreateTaskRequest{FolderName: name, RequirementLink: link}}
		tools := []string{}
		request.WorkspaceToolIDs = &tools
		if _, err := app.CreateTask(ctx, request, nil); err != nil {
			t.Fatal(err)
		}
		manifest, err := app.Manifests.Load(filepath.Join(taskRoot, name))
		if err != nil || manifest.RequirementMaterials.Status != domain.MaterialsReady || manifest.RequirementMaterials.WriteRoot == nil {
			t.Fatalf("task materials: %+v, %v", manifest.RequirementMaterials, err)
		}
		return manifest
	}
	original := create("standard-task", standardLink)
	notes := filepath.Join(*original.RequirementMaterials.WriteRoot, "notes.md")
	if err := os.WriteFile(notes, []byte("existing notes"), 0600); err != nil {
		t.Fatal(err)
	}
	task := create("direct-task", directLink)
	if task.RequirementLink != directLink || task.RequirementID == nil || *task.RequirementID != "7143084336" ||
		*task.RequirementMaterials.WriteRoot != *original.RequirementMaterials.WriteRoot {
		t.Fatalf("direct link did not reuse the original materials: %+v", task)
	}
	if data, err := os.ReadFile(notes); err != nil || string(data) != "existing notes" {
		t.Fatalf("existing materials changed: %q, %v", data, err)
	}
	content, err = app.ReadRequirement(ctx, RequirementReadRequest{TaskID: task.TaskDirectoryName})
	if err != nil || content.Data.Body != "Direct link body" || content.Data.Source != "CACHE" {
		t.Fatalf("linked task detail could not use the cached body: %+v, %v", content.Data, err)
	}
}
