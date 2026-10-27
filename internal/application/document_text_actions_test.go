package application

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

func materialTextApplicationFixture(t *testing.T) (*App, string, string) {
	t.Helper()
	paths := storage.NewPaths(t.TempDir())
	paths.Tasks = filepath.Join(paths.Home, "isolated-tasks")
	materialsBase := filepath.Join(paths.Home, "isolated-materials")
	materials := filepath.Join(materialsBase, "2026", "123-text", "owned")
	if err := os.MkdirAll(materials, 0700); err != nil {
		t.Fatal(err)
	}
	a := &App{Paths: paths, Config: storage.NewConfigStore(paths), Manifests: storage.NewManifestStore(), Documents: documents.NewService(paths.Cache)}
	config := domain.DefaultAppConfig()
	config.TaskRoot = &paths.Tasks
	config.RequirementMaterialsRoot = &materialsBase
	subdirectory := "owned"
	config.RequirementMaterialsSubdirectory = &subdirectory
	if err := a.Config.Save(config); err != nil {
		t.Fatal(err)
	}
	manifest := taskIdentityFixture(t, a, paths.Tasks, "task", "task")
	manifest.RequirementMaterials.WriteRoot = &materials
	manifest.RequirementMaterials.Status = domain.MaterialsReady
	if err := a.Manifests.Save(filepath.Join(paths.Tasks, "task"), manifest); err != nil {
		t.Fatal(err)
	}
	return a, materials, filepath.Join(paths.Tasks, "task")
}

func TestMaterialTextApplicationReadDraftSaveAndIdentity(t *testing.T) {
	for _, name := range []string{"notes.txt", "query.sql", "data.json", "data.csv", "main.go"} {
		t.Run(name, func(t *testing.T) {
			a, root, taskPath := materialTextApplicationFixture(t)
			ctx := context.Background()
			path := filepath.Join(root, name)
			original := "\xef\xbb\xbf{\r\n \"n\": 1,\n \"s\": \"中文\"\r\n}\n"
			if err := os.WriteFile(path, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			action := Action{Kind: "document.read", TaskID: "task", Path: path}
			read, err := a.documentAction(ctx, action)
			if err != nil {
				t.Fatal(err)
			}
			content := read.Data.(documents.Content)
			if content.ReadOnly || !content.HasUTF8BOM || content.Text != original[3:] {
				t.Fatalf("bad editable source: %#v", content)
			}
			action.Kind = "document.draft"
			action.Text = strings.Replace(content.Text, "1", "2", 1)
			action.Payload = map[string]any{"expectedHash": content.Hash, "hasUtf8Bom": true, "draftTaskPath": taskPath, "draftMaterialsRoot": visibleMaterialsRoot(root)}
			if _, err := a.documentAction(ctx, action); err != nil {
				t.Fatal(err)
			}
			action.Kind = "document.read"
			restored, err := a.documentAction(ctx, action)
			if err != nil || restored.Data.(documents.Content).TextDraft == nil {
				t.Fatalf("draft not restored: %#v %v", restored, err)
			}
			action.Kind = "document.save"
			saved, err := a.documentAction(ctx, action)
			if err != nil {
				t.Fatal(err)
			}
			data, _ := os.ReadFile(path)
			if string(data) != "\xef\xbb\xbf"+action.Text || saved.Data.(documents.Content).Hash != documents.Hash(data) {
				t.Fatalf("projection serialized: %q", data)
			}
			action.Kind = "document.read"
			clean, err := a.documentAction(ctx, action)
			if err != nil || clean.Data.(documents.Content).TextDraft != nil {
				t.Fatal("saved draft remains")
			}
			action.Kind = "document.save"
			action.Text = "must not overwrite"
			action.Payload["draftTaskPath"] = filepath.Join(taskPath, "changed")
			if _, err := a.documentAction(ctx, action); err == nil {
				t.Fatal("changed task identity accepted")
			}
			action.Payload["draftTaskPath"] = taskPath
			if _, err := a.documentAction(ctx, action); !errors.Is(err, documents.ErrConflict) {
				t.Fatalf("stale hash accepted: %v", err)
			}
		})
	}
}

func TestMaterialTextApplicationReadonlyAndThreeMiBMarkdown(t *testing.T) {
	a, root, _ := materialTextApplicationFixture(t)
	utf16 := filepath.Join(root, "readonly.txt")
	if err := os.WriteFile(utf16, []byte{0xff, 0xfe, 'A', 0, '\n', 0}, 0600); err != nil {
		t.Fatal(err)
	}
	result, err := a.documentAction(context.Background(), Action{Kind: "document.read", TaskID: "task", Path: utf16})
	if err != nil || !result.Data.(documents.Content).ReadOnly || result.Text != "A\n" {
		t.Fatalf("UTF16 preview lost: %#v %v", result, err)
	}
	path := filepath.Join(root, "large.md")
	source := "# Large\r\n" + strings.Repeat("x", documents.MaxMaterialsText-12) + "\n\r"
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	result, err = a.documentAction(context.Background(), Action{Kind: "document.read", TaskID: "task", Path: path})
	if err != nil || result.Text != source || !result.Data.(documents.Content).ReadOnly || result.Data.(documents.Content).Draft != nil {
		t.Fatalf("3MiB markdown read changed: %d %v", len(result.Text), err)
	}
}
