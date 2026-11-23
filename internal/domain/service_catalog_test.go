package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFlatServiceCatalogUsesGlobalIDs(t *testing.T) {
	config := DefaultAppConfig()
	if config.ServiceCatalog() == nil || len(config.ServiceCatalog()) != 0 {
		t.Fatal("default service catalog must be an empty list")
	}
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
	config.Repositories = []RepositoryConfig{{ID: "repo", Name: "Repository", RootPath: "C:/repo", GitCommonDirectory: "C:/repo/.git"}}
	service := ServiceConfig{
		ID: "api", RepositoryID: "repo", DisplayName: "API", MasterBranch: "origin/main", TestTagBaselineRef: "origin/test",
		Modules: []ServiceModuleConfig{{ID: "default", Name: "default"}},
	}
	config.Services = []ServiceConfig{service}
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
	if found, err := config.Service("api"); err != nil || found.RepositoryID != "repo" {
		t.Fatalf("lookup returned %+v, %v", found, err)
	}
	if _, err := config.Service("absent"); err == nil {
		t.Fatal("lookup accepted a missing service ID")
	}
	service.ID = "API"
	config.Services = append(config.Services, service)
	if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "服务 ID") {
		t.Fatalf("global ID collision was accepted: %v", err)
	}
}

func TestTaskManifestSerializesFlatServiceIdentity(t *testing.T) {
	manifest := DefaultTaskManifest()
	manifest.FolderName = "task"
	manifest.TaskDirectoryName = "task"
	manifest.WorkspaceDirectory = "C:/tasks/task"
	manifest.FeatureBranch = "feature/task"
	manifest.CreatedAt = "2026-11-20T01:02:03Z"
	manifest.UpdatedAt = manifest.CreatedAt
	manifest.Services = []ServiceWorkspace{{
		RepositoryID: "repo", ServiceName: "API", RepositoryPath: "C:/repo", WorktreePath: "C:/tasks/task/api",
		DevelopmentTool: DevelopmentToolIntelliJIdea, Branch: "feature/task", Health: WorkspaceReady,
		GroupServiceID: "api", ModuleID: "default", Strategy: WorkspaceStandardWorktree,
		ModuleSource: TaskModuleConfigured, TagMode: TagBuildMergeToTargetBranch,
	}}
	encoded, err := json.Marshal(NormalizeTaskManifest(manifest))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"groupId"`, `"groupServiceId"`} {
		if strings.Contains(string(encoded), key) {
			t.Fatalf("serialized retired key %s", key)
		}
	}
	if !strings.Contains(string(encoded), `"serviceId":"api"`) {
		t.Fatal("serialized manifest lost the service ID")
	}
	var decoded TaskManifest
	if err := DecodeStrict(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Services[0].GroupServiceID != "api" || decoded.WorkspaceDirectory != manifest.WorkspaceDirectory {
		t.Fatalf("manifest round trip lost service or workspace identity: %+v", decoded)
	}
}
