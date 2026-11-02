package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

func TestAnnualGitFullRepositoryIncludesTempAndNonAnnual(t *testing.T) {
	f := newMaterialsGitFixture(t, true)
	scope, _ := documents.AnnualScope(filepath.Join(f.root, "2026", "123-需求", "研发"))
	if err := scope.EnsureLocal(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"2026/123-需求/temp/log.txt", "123-平铺/研发/old.md", "outside.txt", ".silverwing-persistent.json"} {
		materialsGitTestFile(t, f.root, path, "native content")
	}
	f.sync()
	for _, path := range []string{"2026/123-需求/temp/log.txt", "123-平铺/研发/old.md", "outside.txt", ".silverwing-persistent.json"} {
		if content := materialsGitTestRun(t, f.remote, "show", "master:"+path); content != "native content" {
			t.Fatal(path, content)
		}
	}
	if _, err := os.Stat(filepath.Join(scope.Temporary, ".gitignore")); !os.IsNotExist(err) {
		t.Fatal("generated temp ignore", err)
	}
}

func TestAnnualGitTempNativeDirtyCollisionStops(t *testing.T) {
	f := newMaterialsGitFixture(t, true)
	path := "2026/123-需求/temp/log.txt"
	materialsGitTestFile(t, f.root, path, "initial temp")
	f.sync()
	materialsGitTestRun(t, f.seed, "pull", "--ff-only", "origin", "master")
	materialsGitTestFile(t, f.seed, path, "remote temp edit")
	materialsGitTestRun(t, f.seed, "add", ".")
	materialsGitTestRun(t, f.seed, "commit", "-m", "remote temp")
	materialsGitTestRun(t, f.seed, "push", "origin", "master")
	materialsGitTestFile(t, f.root, path, "local unsaved temp")
	head := materialsGitTestRun(t, f.root, "rev-parse", "HEAD")
	status, err := f.service.Sync(context.Background(), f.scope, true, nil)
	if err == nil || !status.Paused {
		t.Fatal("native dirty collision did not pause", status, err)
	}
	if data, _ := os.ReadFile(filepath.Join(f.root, path)); string(data) != "local unsaved temp" {
		t.Fatal("local temp overwritten", string(data))
	}
	if materialsGitTestRun(t, f.root, "rev-parse", "HEAD") != head {
		t.Fatal("committed before remote merge succeeded")
	}
}

func TestAnnualGitManualPeerCommitUsesNativeHistory(t *testing.T) {
	f := newMaterialsGitFixture(t, true)
	f.sync()
	materialsGitTestFile(t, f.root, "2026/123-需求/产品/design.md", "manual peer edit")
	materialsGitTestRun(t, f.root, "add", "--", "2026/123-需求/产品/design.md")
	materialsGitTestRun(t, f.root, "commit", "-m", "manual peer")
	materialsGitTestFile(t, f.root, "2026/123-需求/研发/new.txt", "owned")
	f.sync()
	if content := materialsGitTestRun(t, f.remote, "show", "master:2026/123-需求/产品/design.md"); content != "manual peer edit" {
		t.Fatal(content)
	}
}

func TestAnnualGitPeerRenameDeleteAndDirtyCollision(t *testing.T) {
	f := newMaterialsGitFixture(t, true)
	f.sync()
	materialsGitTestFile(t, f.seed, "2026/123-需求/测试/case.md", "case")
	materialsGitTestRun(t, f.seed, "mv", "2026/123-需求/产品", "2026/123-需求/产品新版")
	materialsGitTestRun(t, f.seed, "add", ".")
	materialsGitTestRun(t, f.seed, "commit", "-m", "peer rename")
	materialsGitTestRun(t, f.seed, "push", "origin", "master")
	f.sync()
	if _, err := os.Stat(filepath.Join(f.root, "2026/123-需求/产品新版/design.md")); err != nil {
		t.Fatal(err)
	}
	materialsGitTestFile(t, f.root, "2026/123-需求/测试/case.md", "unsaved externally")
	materialsGitTestRun(t, f.seed, "rm", "2026/123-需求/测试/case.md")
	materialsGitTestRun(t, f.seed, "commit", "-m", "peer delete")
	materialsGitTestRun(t, f.seed, "push", "origin", "master")
	status, err := f.service.Sync(context.Background(), f.scope, true, nil)
	if err == nil || !status.Paused {
		t.Fatal("dirty peer did not pause", status, err)
	}
	if data, _ := os.ReadFile(filepath.Join(f.root, "2026/123-需求/测试/case.md")); string(data) != "unsaved externally" {
		t.Fatal("lost peer changes")
	}
}

func annualAgentFixture(t *testing.T) (*AgentDocumentService, domain.TaskManifest, domain.AppConfig, string, documents.AnnualMaterialsScope) {
	t.Helper()
	paths := storage.NewPaths(t.TempDir())
	task := filepath.Join(paths.Home, "tasks", "one")
	owned := filepath.Join(paths.Home, "materials", "requirements", "2026", "123-需求", "文档")
	scope, _ := documents.AnnualScope(owned)
	if err := os.MkdirAll(scope.Base, 0755); err != nil {
		t.Fatal(err)
	}
	if err := scope.EnsureLocal(); err != nil {
		t.Fatal(err)
	}
	m := domain.DefaultTaskManifest()
	m.TaskDirectoryName = "one"
	m.FolderName = "one"
	m.GroupID = "g"
	m.CreatedAt = "2026-10-27T10:00:00Z"
	m.RequirementLink = "https://project.feishu.cn/obt/userstory/detail/123"
	m.RequirementMaterials = domain.RequirementMaterialsDirectory{Status: domain.MaterialsReady, WriteRoot: &owned}
	config := domain.DefaultAppConfig()
	config.RequirementMaterialsRoot = &scope.Base
	sub := "文档"
	config.RequirementMaterialsSubdirectory = &sub
	manifests := storage.NewManifestStore()
	service := NewAgentDocumentService(paths, manifests)
	if err := manifests.Save(task, m); err != nil {
		t.Fatal(err)
	}
	if _, err := service.EnsureTaskDocuments(task, m, config); err != nil {
		t.Fatal(err)
	}
	return service, m, config, task, scope
}

// Model an existing task carrying an older managed-block version. A peer
// directory change alone no longer requires rewriting generic instructions.
func markMaterialsCatalogStale(t *testing.T, task string) {
	t.Helper()
	for _, relative := range []string{AgentRouterFileName, filepath.Join(AgentWorkspaceDir, AgentDirectoryName, AgentContextFileName)} {
		path := filepath.Join(task, relative)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(string(data), materialsCatalogEnd) != 1 {
			t.Fatal("missing managed block", path)
		}
		next := strings.Replace(string(data), materialsCatalogEnd, "旧版本资料目录说明，等待迁移\n"+materialsCatalogEnd, 1)
		if err := os.WriteFile(path, []byte(next), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAnnualAgentCatalogPreservesNotesAndNoOp(t *testing.T) {
	service, m, c, task, scope := annualAgentFixture(t)
	root := filepath.Join(task, AgentRouterFileName)
	data, err := os.ReadFile(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), scope.Owned) || !strings.Contains(string(data), scope.Temporary) {
		t.Fatal(string(data))
	}
	data = append(data, []byte("\n用户追加内容\n")...)
	if err := os.WriteFile(root, data, 0644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(root)
	if err := service.RefreshMaterialsDirectories(task, m, c, nil); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(root)
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("rewrote unchanged catalog")
	}
	peer := filepath.Join(scope.Root, "测试很长的资料名称")
	if err := os.Mkdir(peer, 0755); err != nil {
		t.Fatal(err)
	}
	if err := service.RefreshMaterialsDirectories(task, m, c, nil); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{root, filepath.Join(task, AgentWorkspaceDir, AgentDirectoryName, AgentContextFileName)} {
		data, _ := os.ReadFile(p)
		if strings.Contains(string(data), peer) || !strings.Contains(string(data), scope.Owned) || !strings.Contains(string(data), "主动阅读") || !strings.Contains(string(data), "只读") {
			t.Fatal("catalog missing", p, string(data))
		}
	}
	if data, _ := os.ReadFile(root); !strings.Contains(string(data), "用户追加内容") {
		t.Fatal("user content lost")
	}
	if err := os.Rename(peer, filepath.Join(scope.Root, "验证")); err != nil {
		t.Fatal(err)
	}
	if err := service.RefreshMaterialsDirectories(task, m, c, nil); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(root); strings.Contains(string(data), peer) {
		t.Fatal("stale peer")
	}
	if err := os.Remove(filepath.Join(scope.Root, "验证")); err != nil {
		t.Fatal(err)
	}
	if err := service.RefreshMaterialsDirectories(task, m, c, nil); err != nil {
		t.Fatal(err)
	}
}

func TestAnnualAgentCustomMalformedAndDeletedTaskProtected(t *testing.T) {
	for _, content := range []string{"# 我的自定义说明\n", materialsCatalogStart + "\nmissing end"} {
		service, m, c, task, _ := annualAgentFixture(t)
		path := filepath.Join(task, AgentRouterFileName)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		if err := service.RefreshMaterialsDirectories(task, m, c, nil); err == nil {
			t.Fatal("unsafe update accepted")
		}
		if data, _ := os.ReadFile(path); string(data) != content {
			t.Fatal("custom file changed")
		}
	}
	service, m, c, task, _ := annualAgentFixture(t)
	if err := os.RemoveAll(task); err != nil {
		t.Fatal(err)
	}
	if err := service.RefreshMaterialsDirectories(task, m, c, func() error { return os.ErrNotExist }); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if _, err := os.Stat(task); !os.IsNotExist(err) {
		t.Fatal("deleted task recreated")
	}
}
