package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"silverwing-go/internal/domain"
)

func TestSNBNativeTagShardRemainsRequired(t *testing.T) {
	store := NewConfigStore(NewPaths(t.TempDir()))
	if _, err := store.Load(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(store.Paths().Config, "tag.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err == nil {
		t.Fatal("missing native tag shard was accepted")
	}
}

func TestSNBNativeServiceRequiresTestTagBaselineRef(t *testing.T) {
	store := NewConfigStore(NewPaths(t.TempDir()))
	config, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	repositoryRoot := filepath.Join(store.Paths().Home, "repo")
	config.Repositories = append(config.Repositories, domain.RepositoryConfig{
		ID: "repo", Name: "repo", RootPath: repositoryRoot, GitCommonDirectory: filepath.Join(repositoryRoot, ".git"),
	})
	config.Groups[0].Services = append(config.Groups[0].Services, domain.GroupServiceConfig{
		ID: "service", RepositoryID: "repo", DisplayName: "service", Enabled: true,
		MasterBranch: "origin/master", TestTagBaselineRef: "origin/release/test",
		DevelopmentTool: domain.DevelopmentToolIntelliJIdea,
		Modules: []domain.ServiceModuleConfig{{
			ID: "default", Name: "default", Strategy: domain.WorkspaceStandardWorktree,
			TagEnabled: true, TagMode: domain.TagBuildMergeToTargetBranch,
			TagMessagePrefix: "Tag", CustomCommands: []domain.WorkspaceCommandConfig{},
		}},
		Bootstrap: domain.BootstrapConfig{CopyRules: []domain.BootstrapCopyRule{}, Commands: []domain.BootstrapCommand{}},
	})
	if err := store.Save(config); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(store.Paths().Config, "services.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var shard struct {
		Schema int `json:"schema"`
		Groups []struct {
			GroupID  string                       `json:"groupId"`
			Services []map[string]json.RawMessage `json:"services"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(data, &shard); err != nil {
		t.Fatal(err)
	}
	if len(shard.Groups) != 1 || len(shard.Groups[0].Services) != 1 {
		t.Fatalf("unexpected service fixture: %+v", shard.Groups)
	}
	delete(shard.Groups[0].Services[0], "testTagBaselineRef")
	data, err = json.Marshal(shard)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err == nil {
		t.Fatal("missing required service testTagBaselineRef was accepted")
	}
}
