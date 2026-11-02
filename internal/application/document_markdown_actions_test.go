package application

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/documents"
)

func taskMaterialMarkdownPayload(t *testing.T, a *App, taskPath, root string) map[string]any {
	t.Helper()
	manifest, err := a.Manifests.Load(taskPath)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{
		"markdownEditContext": taskMaterialsMarkdownEditContext,
		"draftTaskPath":       taskPath, "draftMaterialsRoot": root,
		"desktopRenameTaskCreatedAt": manifest.CreatedAt,
	}
}

func TestTaskMaterialMarkdownCreateDraftSave(t *testing.T) {
	for _, area := range []string{"owned", "temporary", "local"} {
		t.Run(area, func(t *testing.T) {
			a, owned, taskPath := materialTextApplicationFixture(t)
			root, directory := visibleMaterialsRoot(owned), owned
			if area == "temporary" {
				directory = filepath.Join(root, documents.TemporaryMaterialsDirectory)
				if err := os.MkdirAll(directory, 0700); err != nil {
					t.Fatal(err)
				}
			} else if area == "local" {
				scope, err := documents.PrepareLocalTaskMaterials(taskPath)
				if err != nil {
					t.Fatal(err)
				}
				manifest, err := a.Manifests.Load(taskPath)
				if err != nil {
					t.Fatal(err)
				}
				manifest.RequirementMaterials.WriteRoot = &scope.Owned
				if err := a.Manifests.Save(taskPath, manifest); err != nil {
					t.Fatal(err)
				}
				root, directory = scope.Root, scope.Owned
			}
			ctx := context.Background()
			original := "\xef\xbb\xbf---\r\nraw: metadata\n---\r\n\n# 标题\r\n<!-- keep -->\n正文  \r\n"
			created, err := a.documentAction(ctx, Action{Kind: "document.create", TaskID: "task", Path: directory, Text: original, Payload: map[string]any{"name": "notes.MARKDOWN"}})
			if err != nil {
				t.Fatal(err)
			}
			action := Action{Kind: "document.read", TaskID: "task", Path: created.Path, Payload: taskMaterialMarkdownPayload(t, a, taskPath, root)}
			read, err := a.documentAction(ctx, action)
			if err != nil {
				t.Fatal(err)
			}
			content := read.Data.(documents.Content)
			if content.ReadOnly || content.MarkdownEditContext != taskMaterialsMarkdownEditContext || content.Text != original[3:] || !content.HasUTF8BOM || content.Draft != nil {
				t.Fatalf("task material did not expose the editable disk source: %#v", content)
			}
			action.Text = strings.Replace(content.Text, "# 标题", "# 修改后的标题", 1)
			action.Payload["expectedHash"], action.Payload["hasUtf8Bom"], action.Payload["newline"] = content.Hash, true, content.Newline
			action.Kind = "document.draft"
			if _, err := a.documentAction(ctx, action); err != nil {
				t.Fatal(err)
			}
			action.Kind = "document.read"
			reopened, err := a.documentAction(ctx, action)
			if err != nil {
				t.Fatal(err)
			}
			reopenedContent := reopened.Data.(documents.Content)
			if reopened.Text != original[3:] || reopenedContent.Draft == nil || reopenedContent.Draft.Content != action.Text {
				t.Fatalf("draft was not offered separately from the disk source: %#v", reopenedContent)
			}
			if disk, err := os.ReadFile(created.Path); err != nil || string(disk) != original {
				t.Fatalf("opening or caching a draft overwrote disk: %q %v", disk, err)
			}
			action.Kind = "document.save"
			saved, err := a.documentAction(ctx, action)
			if err != nil {
				t.Fatal(err)
			}
			disk, err := os.ReadFile(created.Path)
			if err != nil || string(disk) != "\xef\xbb\xbf"+action.Text || saved.Data.(documents.Content).Hash != documents.Hash(disk) || saved.Data.(documents.Content).MarkdownEditContext != taskMaterialsMarkdownEditContext {
				t.Fatalf("Markdown save changed untouched source bytes or lost its version: %q %#v %v", disk, saved.Data, err)
			}
			action.Kind = "document.read"
			clean, err := a.documentAction(ctx, action)
			if err != nil || clean.Data.(documents.Content).Draft != nil {
				t.Fatalf("saved draft remains: %#v %v", clean, err)
			}
		})
	}
}

func TestTaskMaterialMarkdownConflictPreservesDraftAndExplicitOverwrite(t *testing.T) {
	a, owned, taskPath := materialTextApplicationFixture(t)
	ctx, root := context.Background(), visibleMaterialsRoot(owned)
	path := filepath.Join(owned, "conflict.md")
	if err := os.WriteFile(path, []byte("# Original\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	action := Action{Kind: "document.read", TaskID: "task", Path: path, Payload: taskMaterialMarkdownPayload(t, a, taskPath, root)}
	read, err := a.documentAction(ctx, action)
	if err != nil {
		t.Fatal(err)
	}
	baseline := read.Data.(documents.Content).Hash
	action.Kind, action.Text, action.Payload["expectedHash"] = "document.draft", "# My unsaved source\r\n", baseline
	if _, err := a.documentAction(ctx, action); err != nil {
		t.Fatal(err)
	}
	external := "# External change\n"
	if err := os.WriteFile(path, []byte(external), 0600); err != nil {
		t.Fatal(err)
	}
	action.Kind = "document.save"
	conflict, err := a.documentAction(ctx, action)
	if !errors.Is(err, documents.ErrConflict) || conflict.Text != external || conflict.Data.(documents.Content).Hash != documents.Hash([]byte(external)) {
		t.Fatalf("external baseline was not returned as a conflict: %#v %v", conflict, err)
	}
	action.Kind = "document.read"
	reopened, err := a.documentAction(ctx, action)
	if err != nil {
		t.Fatal(err)
	}
	content := reopened.Data.(documents.Content)
	if reopened.Text != external || content.Draft == nil || content.Draft.Content != action.Text || content.Draft.BaselineHash != baseline {
		t.Fatalf("conflict or reopening lost the recoverable draft: %#v", content)
	}
	action.Kind, action.Payload["overwrite"] = "document.save", true
	if _, err := a.documentAction(ctx, action); err != nil {
		t.Fatal(err)
	}
	action.Kind = "document.read"
	saved, err := a.documentAction(ctx, action)
	if err != nil || saved.Text != action.Text || saved.Data.(documents.Content).Draft != nil {
		t.Fatalf("explicit conflict resolution did not finish the save: %#v %v", saved, err)
	}
}

func TestTaskMaterialMarkdownRequiresCapturedCurrentIdentity(t *testing.T) {
	for _, scenario := range []string{"missing-task", "missing-root", "missing-created", "wrong-task", "wrong-root", "recreated-task", "changed-materials"} {
		t.Run(scenario, func(t *testing.T) {
			a, owned, taskPath := materialTextApplicationFixture(t)
			root, path := visibleMaterialsRoot(owned), filepath.Join(owned, "identity.md")
			if err := os.WriteFile(path, []byte("# Source\n"), 0600); err != nil {
				t.Fatal(err)
			}
			payload := taskMaterialMarkdownPayload(t, a, taskPath, root)
			payload["expectedHash"] = documents.Hash([]byte("# Source\n"))
			switch scenario {
			case "missing-task":
				delete(payload, "draftTaskPath")
			case "missing-root":
				delete(payload, "draftMaterialsRoot")
			case "missing-created":
				delete(payload, "desktopRenameTaskCreatedAt")
			case "wrong-task":
				payload["draftTaskPath"] = filepath.Join(taskPath, "other")
			case "wrong-root":
				payload["draftMaterialsRoot"] = owned
			case "recreated-task", "changed-materials":
				manifest, err := a.Manifests.Load(taskPath)
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "recreated-task" {
					manifest.CreatedAt += "-recreated"
				} else {
					replacement := filepath.Join(filepath.Dir(root), "456-other", "owned")
					if err := os.MkdirAll(replacement, 0700); err != nil {
						t.Fatal(err)
					}
					manifest.RequirementMaterials.WriteRoot = &replacement
				}
				if err := a.Manifests.Save(taskPath, manifest); err != nil {
					t.Fatal(err)
				}
			}
			for _, kind := range []string{"document.save", "document.draft", "document.draft-discard"} {
				if _, err := a.documentAction(context.Background(), Action{Kind: kind, TaskID: "task", Path: path, Text: "must stay blocked", Payload: payload}); err == nil {
					t.Fatalf("%s accepted stale or incomplete identity", kind)
				}
			}
			if disk, err := os.ReadFile(path); err != nil || string(disk) != "# Source\n" {
				t.Fatalf("stale action changed source: %q %v", disk, err)
			}
			if _, err := os.Stat(filepath.Join(a.Paths.Cache, "materials-markdown-drafts.json")); !os.IsNotExist(err) {
				t.Fatalf("rejected actions touched the draft cache: %v", err)
			}
		})
	}
}

func TestTaskMaterialMarkdownKeepsOtherEntriesAndAreasReadOnly(t *testing.T) {
	for _, scenario := range []string{"legacy", "instructions", "skills", "git", "service", "module", "peer", "system"} {
		t.Run(scenario, func(t *testing.T) {
			a, owned, taskPath := materialTextApplicationFixture(t)
			root, path := visibleMaterialsRoot(owned), filepath.Join(owned, "readonly.md")
			payload := taskMaterialMarkdownPayload(t, a, taskPath, root)
			action := Action{TaskID: "task", Payload: payload}
			switch scenario {
			case "legacy":
				delete(payload, "markdownEditContext")
			case "instructions", "skills", "git":
				payload["markdownEditContext"] = scenario
			case "service":
				action.ServiceID = "service"
			case "module":
				action.ModuleID = "module"
			case "peer":
				path = filepath.Join(root, "peer", "readonly.md")
			case "system":
				path = filepath.Join(root, "AGENTS.md")
			}
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("# Read only\n"), 0600); err != nil {
				t.Fatal(err)
			}
			action.Kind, action.Path = "document.read", path
			read, err := a.documentAction(context.Background(), action)
			if err != nil {
				t.Fatal(err)
			}
			content := read.Data.(documents.Content)
			if !content.ReadOnly || content.MarkdownEditContext != "" || content.Draft != nil {
				t.Fatalf("unrelated entry or readonly area became editable: %#v", content)
			}
			action.Text, payload["expectedHash"] = "blocked mutation", content.Hash
			for _, kind := range []string{"document.save", "document.draft", "document.draft-discard"} {
				action.Kind = kind
				if _, err := a.documentAction(context.Background(), action); err == nil {
					t.Fatalf("%s changed a readonly Markdown entry", kind)
				}
			}
			if disk, err := os.ReadFile(path); err != nil || string(disk) != "# Read only\n" {
				t.Fatalf("readonly source changed: %q %v", disk, err)
			}
		})
	}
}

func TestTaskMaterialMarkdownDraftDiscardDoesNotWriteDisk(t *testing.T) {
	a, owned, taskPath := materialTextApplicationFixture(t)
	ctx, root := context.Background(), visibleMaterialsRoot(owned)
	path := filepath.Join(owned, "discard.md")
	if err := os.WriteFile(path, []byte("# Original\n"), 0600); err != nil {
		t.Fatal(err)
	}
	payload := taskMaterialMarkdownPayload(t, a, taskPath, root)
	payload["expectedHash"] = documents.Hash([]byte("# Original\n"))
	action := Action{Kind: "document.draft", TaskID: "task", Path: path, Text: "# Unsaved\n", Payload: payload}
	if _, err := a.documentAction(ctx, action); err != nil {
		t.Fatal(err)
	}
	action.Kind = "document.draft-discard"
	if _, err := a.documentAction(ctx, action); err != nil {
		t.Fatal(err)
	}
	action.Kind = "document.read"
	read, err := a.documentAction(ctx, action)
	if err != nil || read.Text != "# Original\n" || read.Data.(documents.Content).Draft != nil {
		t.Fatalf("discard changed disk source or retained the draft: %#v %v", read, err)
	}
}
