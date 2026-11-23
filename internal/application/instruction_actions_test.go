package application

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/storage"
	"silverwing-go/internal/workspace"
)

func TestRetiredInstructionTemplateActionsLeaveExistingFilesUntouched(t *testing.T) {
	paths := storage.NewPaths(t.TempDir())
	a := &App{Paths: paths, Config: storage.NewConfigStore(paths), Documents: documents.NewService(paths.Cache)}
	legacyPath := filepath.Join(paths.Agents, "task-templates.json")
	legacy := `[{"id":"saved","name":"Existing","content":"Task rules","updatedAt":"2026-11-19T00:00:00Z"}]`
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"instructions.templates", "instructions.templates.list", "instructions.templates.save", "instructions.templates.remove"} {
		if policy := DescribeAction(kind); policy.Known || policy.SettingsDedicatedResult {
			t.Errorf("retired operation %s still has a settings policy: %+v", kind, policy)
		}
		action := Action{Kind: kind, Text: "new text", Payload: map[string]any{"scope": "global", "id": "saved", "name": "New"}}
		if _, err := a.Dispatch(context.Background(), action); err == nil {
			t.Errorf("retired operation %s still succeeded", kind)
		}
	}
	if got, err := os.ReadFile(legacyPath); err != nil || string(got) != legacy {
		t.Fatalf("existing template data changed: %q, %v", got, err)
	}
	if _, err := os.Stat(paths.GlobalAgents); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("retired operation reached the shared instruction document: %v", err)
	}
}

func TestSharedInstructionActionsReadDraftAndSave(t *testing.T) {
	for _, scope := range []string{"global", "group"} {
		t.Run(scope, func(t *testing.T) {
			paths := storage.NewPaths(t.TempDir())
			wantPath := paths.GlobalAgents
			if scope == "group" {
				wantPath = filepath.Join(paths.Agents, "groups", "test-group", "AGENTS.md")
			}
			a := &App{Paths: paths, Config: storage.NewConfigStore(paths), Documents: documents.NewService(paths.Cache)}
			action := Action{Kind: "instructions.read", Payload: map[string]any{"scope": scope, "groupId": "test-group"}}
			ctx := context.Background()
			initial, err := a.Dispatch(ctx, action)
			if err != nil || initial.Path != wantPath || initial.Text != "" {
				t.Fatalf("could not open shared rules: %+v, %v", initial, err)
			}
			action.Kind = "instructions.draft"
			action.Text = "# Shared rules\n"
			if _, err := a.Dispatch(ctx, action); err != nil {
				t.Fatal(err)
			}
			if draft, err := a.Documents.Draft(initial.Path); err != nil || draft != action.Text {
				t.Fatalf("shared draft missing: %q, %v", draft, err)
			}
			action.Kind = "instructions.save"
			action.Payload["expectedHash"] = initial.Data.(documents.Content).Hash
			if _, err := a.Dispatch(ctx, action); err != nil {
				t.Fatal(err)
			}
			action.Kind = "instructions.read"
			if result, err := a.Dispatch(ctx, action); err != nil || result.Path != initial.Path || result.Text != action.Text {
				t.Fatalf("saved shared rules missing: %+v, %v", result, err)
			}
		})
	}
}

func TestTaskInstructionActionsDefaultToReadOnlyAGENTS(t *testing.T) {
	a, _, fileAction := taskFilesActionFixture(t)
	a.Documents = documents.NewService(a.Paths.Cache)
	root := filepath.Join(a.Paths.Tasks, fileAction.TaskID)
	path := filepath.Join(root, workspace.AgentRouterFileName)
	legacyRelative := filepath.Join(workspace.AgentWorkspaceDir, workspace.AgentDirectoryName, "TASK-CONTEXT.md")
	legacy := filepath.Join(root, legacyRelative)
	const original = "# unchanged\r\n"
	if err := os.WriteFile(legacy, []byte("existing context"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, requested := range []string{"", workspace.AgentRouterFileName, path} {
		action := Action{Kind: "instructions.read", TaskID: fileAction.TaskID, Path: requested}
		result, err := a.Dispatch(ctx, action)
		content, ok := result.Data.(documents.Content)
		if err != nil || !ok || !content.ReadOnly || result.Path != path || result.Text != original {
			t.Fatalf("invalid AGENTS read for %q: %+v %v", requested, result, err)
		}
		for _, kind := range []string{"instructions.draft", "instructions.save"} {
			action.Kind, action.Text = kind, "changed"
			if _, err := a.Dispatch(ctx, action); err == nil {
				t.Fatalf("%s accepted read-only AGENTS", kind)
			}
		}
	}
	for _, action := range []Action{
		{Path: "TASK-CONTEXT.md"},
		{Path: legacy},
		{Payload: map[string]any{"scope": "context"}},
	} {
		action.Kind, action.TaskID = "instructions.read", fileAction.TaskID
		if _, err := a.Dispatch(ctx, action); err == nil {
			t.Fatalf("retired instruction route accepted: %+v", action)
		}
	}
	fileAction.Kind, fileAction.Path = "task.files.read", legacyRelative
	result, err := a.Dispatch(ctx, fileAction)
	read, ok := result.Data.(TaskFilesReadResult)
	if err != nil || !ok || !read.Content.ReadOnly || read.Content.Text != "existing context" {
		t.Fatalf("ordinary task file read failed: %+v %v", result, err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != original {
		t.Fatalf("read-only AGENTS changed: %q %v", got, err)
	}
}
