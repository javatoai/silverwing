package application

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
)

func TestStandaloneTaskApplicationMaterialsStayLocal(t *testing.T) {
	a, _, _ := materialTextApplicationFixture(t)
	config, err := a.Config.Load()
	if err != nil {
		t.Fatal(err)
	}
	config.RequirementMaterialsRoot, config.RequirementMaterialsSubdirectory = nil, nil
	missingGit := filepath.Join(t.TempDir(), "git-must-not-run")
	config.GitExecutablePath = &missingGit
	if err := a.Config.Save(config); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	created, err := a.workspaceAction(ctx, Action{Kind: "task.create", Payload: map[string]any{"folderName": "local", "workspaceToolIds": []string{}}})
	if err != nil {
		t.Fatal(err)
	}
	manifest := created.Data.(domain.TaskManifest)
	scope, local := documents.LocalScope(value(manifest.RequirementMaterials.WriteRoot))
	if !local || visibleMaterialsRoot(scope.Owned) != scope.Root || validAnnualTaskMaterials(manifest, config) != nil {
		t.Fatalf("local task unavailable: %+v", manifest)
	}
	if _, annual := documents.AnnualScope(scope.Owned); annual {
		t.Fatal("local task was admitted into the annual Git synchronization scope")
	}
	file := filepath.Join(scope.Owned, "分析.txt")
	if err := os.WriteFile(file, []byte("原资料\n"), 0600); err != nil {
		t.Fatal(err)
	}
	read, err := a.documentAction(ctx, Action{Kind: "document.read", TaskID: "local", Path: file})
	if err != nil || read.Data.(documents.Content).ReadOnly {
		t.Fatalf("local material cannot be read/edited: %+v %v", read, err)
	}
	content := read.Data.(documents.Content)
	if _, err := a.documentAction(ctx, Action{Kind: "document.save", TaskID: "local", Path: file, Text: "修改后的资料\n", Payload: map[string]any{"expectedHash": content.Hash}}); err != nil {
		t.Fatal(err)
	}
	peer := filepath.Join(scope.Root, "只读参考")
	if err := os.Mkdir(peer, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := a.documentAction(ctx, Action{Kind: "document.create", TaskID: "local", Path: peer, Payload: map[string]any{"name": "bad.md"}}); err == nil {
		t.Fatal("local peer directory became writable")
	}
	if _, err := a.documentAction(ctx, Action{Kind: "document.create", TaskID: "local", Path: scope.Temporary, Payload: map[string]any{"name": "scratch.md"}}); err != nil {
		t.Fatalf("temp cannot be written: %v", err)
	}
	listed, err := a.documentAction(ctx, Action{Kind: "document.list", TaskID: "local"})
	if err != nil || strings.Contains(listed.Message, "失败") {
		t.Fatalf("local catalog failed: %+v %v", listed, err)
	}
	link := "https://project.feishu.cn/obt/userstory/detail/123"
	association := taskContextActionForTest(t, a, config, manifest, scope.Base, "task.requirement-link")
	association.Path = scope.Base
	association.Payload["requirementLink"] = link
	stale := taskContextActionForTest(t, a, config, manifest, scope.Base, "task.requirement-link")
	stale.Path = scope.Base
	stale.Payload["requirementLink"] = link
	stale.Payload[desktopTaskContextPayloadKey].(map[string]any)["taskCreatedAt"] = "stale"
	if _, err := a.workspaceAction(ctx, stale); err == nil {
		t.Fatal("stale task association accepted")
	}
	if _, err := a.workspaceAction(ctx, association); err != nil {
		t.Fatal(err)
	}
	root, err := a.materialRoot(Action{TaskID: "local"})
	if err != nil || root != scope.Root {
		t.Fatalf("association changed materials root: %s %v", root, err)
	}
	if data, _ := os.ReadFile(file); string(data) != "修改后的资料\n" {
		t.Fatal("association changed the saved file")
	}
	markdown := filepath.Join(scope.Owned, "reading.md")
	if err := os.WriteFile(markdown, []byte("# 保留 Markdown 只读行为\n"), 0600); err != nil {
		t.Fatal(err)
	}
	md, err := a.documentAction(ctx, Action{Kind: "document.read", TaskID: "local", Path: markdown})
	if err != nil || !md.Data.(documents.Content).ReadOnly {
		t.Fatalf("existing Markdown reading contract changed: %+v %v", md, err)
	}
	manifest, err = a.Manifests.Load(scope.Base)
	if err != nil {
		t.Fatal(err)
	}
	other := documents.LocalTaskMaterials(filepath.Join(*config.TaskRoot, "different-task"))
	manifest.RequirementMaterials.WriteRoot = &other.Owned
	if err := a.Manifests.Save(scope.Base, manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := a.materialRoot(Action{TaskID: "local"}); err == nil {
		t.Fatal("materials from another task identity were accepted")
	}
}
