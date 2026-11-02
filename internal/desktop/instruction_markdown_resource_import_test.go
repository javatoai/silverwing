package desktop

import (
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/application"
	"silverwing-go/internal/documents"
	"silverwing-go/internal/editor"
	markdownui "silverwing-go/internal/ui/markdown"
	"silverwing-go/internal/workspace"
)

func instructionMarkdownImportFixture(t *testing.T, scope string) (*app, *markdownEditorState, markdownui.ImportRequest) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "AGENTS.md")
	ref := documentSaveRef{kind: "instruction", scope: scope, path: path}
	s := &app{}
	if scope == "task" {
		path = filepath.Join(root, workspace.AgentWorkspaceDir, workspace.AgentDirectoryName, workspace.AgentRulesFileName)
		ref.path, ref.taskID, ref.taskPath, ref.createdAt, ref.localTaskRules = path, "task-a", root, "created-a", true
		s.taskUnit.state.tasks = []task{{id: ref.taskID, path: root, createdAt: ref.createdAt}}
	}
	key := "instruction-test-" + scope
	s.materialsUnit.state.documentSaveRefs = map[string]documentSaveRef{key: ref}
	component := markdownui.NewEditorState(markdownui.Document{ID: key, Path: path, Source: "# 原文\r\n"})
	t.Cleanup(component.Close)
	state := &markdownEditorState{path: path, taskID: ref.taskID, liveIdentity: key, componentEditor: component, doc: editor.NewDocument("# 原文\r\n", key), binding: &markdownEditorBinding{id: key, isCurrent: func() bool { return true }}}
	request := markdownui.ImportRequest{DocumentID: key, DocumentPath: path, Version: component.Snapshot().Version, Files: []string{"captured.png"}, ImagePNG: []byte{1, 2, 3}}
	return s, state, request
}

func TestInstructionMarkdownImportCapturesOnlyItsOwnBinding(t *testing.T) {
	for _, scope := range []string{"global", "group", "task"} {
		t.Run(scope, func(t *testing.T) {
			s, state, request := instructionMarkdownImportFixture(t, scope)
			s.materialsUnit.state.documentPath = "unrelated-material.md"
			owner, err := s.captureInstructionMarkdownImport(state, request)
			if err != nil || !s.instructionMarkdownImportCurrent(state, owner) {
				t.Fatalf("instruction owner was not captured: %+v %v", owner, err)
			}
			request.Files[0], request.ImagePNG[0] = "changed.png", 9
			if owner.request.Files[0] != "captured.png" || owner.request.ImagePNG[0] != 1 {
				t.Fatal("import retained mutable caller-owned clipboard slices")
			}
			action := instructionMarkdownImportAction(owner.ref, documents.ClipboardPayload{Files: owner.request.Files, ImagePNG: owner.request.ImagePNG})
			if action.Kind != "instructions.images.import" || action.Path != state.path || action.TaskID != owner.ref.taskID || action.Payload["scope"] != scope {
				t.Fatalf("wrong instruction dispatch: %+v", action)
			}
			for _, field := range []string{"root", "directory", "targetDirectory", "draftMaterialsRoot", "draftTaskPath"} {
				if _, exists := action.Payload[field]; exists {
					t.Fatalf("instruction import supplied an unrelated write root: %s", field)
				}
			}
			if scope == "task" && (action.Payload["localTaskRules"] != true || action.Payload["taskPath"] != owner.ref.taskPath || action.Payload["taskCreatedAt"] != owner.ref.createdAt) {
				t.Fatal("task import lost its local identity")
			}
		})
	}
}

func TestInstructionMarkdownImportLateResultsRequireExactOwnerAndVersion(t *testing.T) {
	for _, scenario := range []string{"version", "component", "document", "binding", "path", "identity", "read-only", "closed", "detached", "inactive", "save-ref", "task-incarnation", "task-path", "source-mode"} {
		t.Run(scenario, func(t *testing.T) {
			s, state, request := instructionMarkdownImportFixture(t, "task")
			owner, err := s.captureInstructionMarkdownImport(state, request)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "version":
				state.componentEditor.NativeState().Buffer().Set("new content")
			case "component":
				state.componentEditor = markdownui.NewEditorState(markdownui.Document{ID: request.DocumentID, Path: state.path, Source: "replacement"})
				t.Cleanup(state.componentEditor.Close)
			case "document":
				state.doc = editor.NewDocument(state.doc.Source(), owner.key)
			case "binding":
				state.binding = &markdownEditorBinding{id: request.DocumentID, isCurrent: func() bool { return true }}
			case "path":
				state.path = "another.md"
			case "identity":
				state.liveIdentity = "replacement"
			case "read-only":
				state.readOnly = true
			case "closed":
				state.closed = true
			case "detached":
				state.detached = true
			case "inactive":
				state.binding.isCurrent = func() bool { return false }
			case "save-ref":
				ref := s.materialsUnit.state.documentSaveRefs[owner.key]
				ref.localTaskRules = false
				s.materialsUnit.state.documentSaveRefs[owner.key] = ref
			case "task-incarnation":
				s.taskUnit.state.tasks[0].createdAt = "replacement"
			case "task-path":
				s.taskUnit.state.tasks[0].path = t.TempDir()
			case "source-mode":
				if err := state.componentEditor.SetMode(markdownui.Source); err != nil {
					t.Fatal(err)
				}
			}
			before := state.componentEditor.Snapshot()
			if s.instructionMarkdownImportCurrent(state, owner) {
				t.Fatal("a stale instruction import still owns the editor")
			}
			if state.componentEditor.Snapshot() != before {
				t.Fatal("checking a late import changed the current body")
			}
		})
	}
}

func TestInstructionMarkdownImportRejectsGeneratedAndLegacyTaskEditors(t *testing.T) {
	for _, scenario := range []string{"local-capability", "generated", "other-kind", "read-only", "wrong-request-document", "wrong-request-path"} {
		t.Run(scenario, func(t *testing.T) {
			s, state, request := instructionMarkdownImportFixture(t, "task")
			ref := s.materialsUnit.state.documentSaveRefs[state.binding.id]
			switch scenario {
			case "local-capability":
				ref.localTaskRules = false
			case "generated":
				ref.path = filepath.Join(ref.taskPath, workspace.AgentWorkspaceDir, workspace.AgentDirectoryName, workspace.AgentContextFileName)
				state.path, request.DocumentPath = ref.path, ref.path
			case "other-kind":
				ref.kind = "material-markdown"
			case "read-only":
				ref.readOnly = true
			case "wrong-request-document":
				request.DocumentID = "other"
			case "wrong-request-path":
				request.DocumentPath = "other.md"
			}
			s.materialsUnit.state.documentSaveRefs[state.binding.id] = ref
			if _, err := s.captureInstructionMarkdownImport(state, request); err == nil {
				t.Fatal("an unrelated/generated instruction editor imported images")
			}
		})
	}
}

func TestInstructionMarkdownImportAllowsAutosaveWithoutRetargeting(t *testing.T) {
	s, state, request := instructionMarkdownImportFixture(t, "global")
	owner, err := s.captureInstructionMarkdownImport(state, request)
	if err != nil {
		t.Fatal(err)
	}
	ref := s.materialsUnit.state.documentSaveRefs[owner.key]
	ref.hash, ref.saveVersion = "saved-current-buffer", 17
	s.materialsUnit.state.documentSaveRefs[owner.key] = ref
	if !s.instructionMarkdownImportCurrent(state, owner) {
		t.Fatal("an ordinary autosave invalidated the same document/version")
	}
}

func TestInstructionMarkdownImportMapsSpecialFileNamesToLocalImages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	var resources []documents.InstructionImageResource
	for _, name := range []string{"中文 #100%.png", "a&copy;.jpg", "a?question.gif", "animation.webp"} {
		resources = append(resources, documents.InstructionImageResource{RelativePath: "AGENTS.assets/" + name, Text: strings.TrimSuffix(name, filepath.Ext(name))})
	}
	result, err := instructionMarkdownImportResult(application.ActionResult{Path: path, Data: documents.InstructionImageImportResult{Resources: resources}}, path)
	if err != nil || len(result.Resources) != len(resources) {
		t.Fatalf("could not map image contract: %+v %v", result, err)
	}
	for index, resource := range result.Resources {
		target, err := url.Parse(resource.Target)
		if err != nil || target.RawQuery != "" || target.Fragment != "" || !resource.Image {
			t.Fatalf("filename became URI syntax: %+v %v", resource, err)
		}
		resolved, err := markdownui.ResolveLink(path, resource.Target)
		if err != nil || resolved.Path != filepath.Join(filepath.Dir(path), filepath.FromSlash(resources[index].RelativePath)) {
			t.Fatalf("copied image reference does not round-trip: %+v %v", resolved, err)
		}
	}
}

func TestInstructionMarkdownImportRejectsPartialOrEscapedContracts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	for _, relative := range []string{"../outside.png", "AGENTS.assets/../outside.png", "other.assets/a.png", "AGENTS.assets/script.svg", "AGENTS.assets/nested/a.png", filepath.Join(t.TempDir(), "external.png")} {
		imported := application.ActionResult{Path: path, Data: documents.InstructionImageImportResult{Resources: []documents.InstructionImageResource{{RelativePath: "AGENTS.assets/valid.png"}, {RelativePath: relative}}}}
		result, err := instructionMarkdownImportResult(imported, path)
		if err == nil || len(result.Resources) != 0 {
			t.Fatalf("partial or unsafe import contract accepted: %q %+v %v", relative, result, err)
		}
	}
	if result, err := instructionMarkdownImportResult(application.ActionResult{Path: "other.md"}, path); err == nil || len(result.Resources) != 0 {
		t.Fatal("import for another instruction file was accepted")
	}
	s, state, request := instructionMarkdownImportFixture(t, "global")
	state.closed = true
	s.importInstructionMarkdownResources(state, nil, request, func(result markdownui.ImportResult, err error) {
		if err == nil || len(result.Resources) != 0 {
			t.Fatal("closed instruction editor accepted a direct import")
		}
	})
}
