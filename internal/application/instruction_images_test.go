package application

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
	"silverwing-go/internal/workspace"
)

func instructionImagesAppFixture(t *testing.T) (*App, string, domain.TaskManifest, string) {
	t.Helper()
	paths := storage.NewPaths(t.TempDir())
	a := &App{Paths: paths, Config: storage.NewConfigStore(paths), Manifests: storage.NewManifestStore(), Documents: documents.NewService(paths.Cache)}
	config := domain.DefaultAppConfig()
	config.TaskRoot = &paths.Tasks
	if err := a.Config.Save(config); err != nil {
		t.Fatal(err)
	}
	manifest := taskIdentityFixture(t, a, paths.Tasks, "task", "task")
	source := filepath.Join(t.TempDir(), "图片 #100%.png")
	if err := os.WriteFile(source, []byte("source image bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	return a, filepath.Join(paths.Tasks, "task"), manifest, source
}

func instructionImagesTaskAction(root string, manifest domain.TaskManifest, source string) Action {
	return Action{Kind: "instructions.images.import", TaskID: "task", Path: workspace.AgentRulesFileName, Payload: map[string]any{
		"scope": "task", "localTaskRules": true, "taskPath": root, "taskCreatedAt": manifest.CreatedAt, "clipboard": documents.ClipboardPayload{Files: []string{source}},
	}}
}

func TestInstructionImagesDispatchResolvesFixedDocumentsAndPreservesMetadata(t *testing.T) {
	for _, scope := range []string{"global", "group", "task"} {
		t.Run(scope, func(t *testing.T) {
			a, root, manifest, source := instructionImagesAppFixture(t)
			action := instructionImagesTaskAction(root, manifest, source)
			target := filepath.Join(root, workspace.AgentWorkspaceDir, workspace.AgentDirectoryName, workspace.AgentRulesFileName)
			if scope != "task" {
				action.TaskID, action.Path = "", ""
				action.Payload = map[string]any{"scope": scope, "clipboard": documents.ClipboardPayload{Files: []string{source}}}
				target = a.Paths.GlobalAgents
				if scope == "group" {
					action.Payload["groupId"] = "group-a"
					var err error
					target, err = a.Paths.GroupAgents("group-a")
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			beforeManifest, err := a.Manifests.Load(root)
			if err != nil {
				t.Fatal(err)
			}
			beforeConfig, err := a.Config.Load()
			if err != nil {
				t.Fatal(err)
			}
			result, err := a.Dispatch(context.Background(), action)
			if err != nil || result.Path != target {
				t.Fatalf("instruction import dispatch failed: %+v %v", result, err)
			}
			copied, ok := result.Data.(documents.InstructionImageImportResult)
			if !ok || len(copied.Resources) != 1 {
				t.Fatalf("unexpected image contract: %#v", result.Data)
			}
			got, err := os.ReadFile(filepath.Join(filepath.Dir(target), filepath.FromSlash(copied.Resources[0].RelativePath)))
			if err != nil || string(got) != "source image bytes" {
				t.Fatalf("wrong image destination: %q %v", got, err)
			}
			if _, err := os.Stat(target); !os.IsNotExist(err) {
				t.Fatal("import created the instruction body")
			}
			afterManifest, err := a.Manifests.Load(root)
			if err != nil || !reflect.DeepEqual(beforeManifest, afterManifest) {
				t.Fatal("image import changed task metadata")
			}
			afterConfig, err := a.Config.Load()
			if err != nil || !reflect.DeepEqual(beforeConfig, afterConfig) {
				t.Fatal("image import changed configuration")
			}
		})
	}
	descriptor := DescribeAction("instructions.images.import")
	if !descriptor.Known || !descriptor.QuietSuccess || !descriptor.DedicatedResult || !descriptor.Cancellable || descriptor.ReloadSnapshot || descriptor.RefreshOnError {
		t.Fatalf("image import should use a quiet dedicated result: %+v", descriptor)
	}
}

func TestInstructionImageImportRejectsStaleTaskAndClientDestinations(t *testing.T) {
	for _, scenario := range []string{"missing-capability", "missing-task", "stale-created", "stale-path", "generated-document", "foreign-document", "client-root", "client-directory", "client-target-directory", "unknown-scope"} {
		t.Run(scenario, func(t *testing.T) {
			a, root, manifest, source := instructionImagesAppFixture(t)
			action := instructionImagesTaskAction(root, manifest, source)
			switch scenario {
			case "missing-capability":
				delete(action.Payload, "localTaskRules")
			case "missing-task":
				action.TaskID = ""
			case "stale-created":
				action.Payload["taskCreatedAt"] = "different incarnation"
			case "stale-path":
				action.Payload["taskPath"] = t.TempDir()
			case "generated-document":
				action.Path = workspace.AgentContextFileName
			case "foreign-document":
				action.Path = filepath.Join(t.TempDir(), workspace.AgentRulesFileName)
			case "client-root":
				action.Payload["root"] = t.TempDir()
			case "client-directory":
				action.Payload["directory"] = t.TempDir()
			case "client-target-directory":
				action.Payload["targetDirectory"] = t.TempDir()
			case "unknown-scope":
				action.Payload["scope"] = "handoff"
			}
			result, err := a.Dispatch(context.Background(), action)
			if err == nil || result.Data != nil {
				t.Fatalf("unsafe import accepted: %+v %v", result, err)
			}
			if _, err := os.Stat(filepath.Join(root, workspace.AgentWorkspaceDir, workspace.AgentDirectoryName, "TASK-RULES.assets")); !os.IsNotExist(err) {
				t.Fatal("rejected task import wrote assets")
			}
		})
	}
}

func TestInstructionImagesRejectMixedScopesAndUnsafeGroups(t *testing.T) {
	for _, action := range []Action{
		{Kind: "instructions.images.import", TaskID: "task", Payload: map[string]any{"scope": "global"}},
		{Kind: "instructions.images.import", Payload: map[string]any{"scope": "group", "localTaskRules": true}},
		{Kind: "instructions.images.import", Payload: map[string]any{"scope": "group", "groupId": "../escape"}},
		{Kind: "instructions.images.import", Path: "other.md", Payload: map[string]any{"scope": "global"}},
	} {
		a, _, _, _ := instructionImagesAppFixture(t)
		if _, err := a.Dispatch(context.Background(), action); err == nil {
			t.Fatalf("unsafe scope accepted: %+v", action)
		}
	}
}

type instructionImageTaskReplacementContext struct {
	context.Context
	check func()
}

func (c instructionImageTaskReplacementContext) Err() error {
	c.check()
	return c.Context.Err()
}

func TestInstructionImageImportRechecksTaskIncarnationAfterCopy(t *testing.T) {
	a, root, manifest, source := instructionImagesAppFixture(t)
	action := instructionImagesTaskAction(root, manifest, source)
	asset := filepath.Join(root, workspace.AgentWorkspaceDir, workspace.AgentDirectoryName, "TASK-RULES.assets", filepath.Base(source))
	replaced := false
	ctx := instructionImageTaskReplacementContext{Context: context.Background(), check: func() {
		if _, err := os.Stat(asset); err == nil && !replaced {
			replaced = true
			manifest.CreatedAt = "new incarnation"
			if err := a.Manifests.Save(root, manifest); err != nil {
				t.Fatal(err)
			}
		}
	}}
	result, err := a.Dispatch(ctx, action)
	if !replaced || err == nil || result.Data != nil {
		t.Fatalf("late task replacement returned insertable references: %+v %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(root, workspace.AgentWorkspaceDir, workspace.AgentDirectoryName, workspace.AgentRulesFileName)); !os.IsNotExist(err) {
		t.Fatal("late replacement changed the body")
	}
}
