package application

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/platform"
	"silverwing-go/internal/storage"
	"silverwing-go/internal/workspace"
)

func TestAnnualApplicationArchiveCustomRouterSucceedsWithVisibleWarningAndLocalRetry(t *testing.T) {
	t.Setenv("USERPROFILE", t.TempDir())
	a, scope, taskPath := annualApplicationFixture(t)
	router := filepath.Join(taskPath, workspace.AgentRouterFileName)
	original, err := os.ReadFile(router)
	if err != nil {
		t.Fatal(err)
	}
	custom := "# user-owned instructions\nkeep every byte\n"
	if err := os.WriteFile(router, []byte(custom), 0644); err != nil {
		t.Fatal(err)
	}
	peer := filepath.Join(scope.Root, "new-peer")
	if err := os.Mkdir(peer, 0755); err != nil {
		t.Fatal(err)
	}
	result, err := a.Dispatch(context.Background(), Action{Kind: "task.archive", TaskID: "task"})
	if err != nil || !strings.Contains(result.Message, "警告") || !strings.Contains(result.Message, "刷新资料可单独重试") {
		t.Fatal("custom catalogue turned archive into a fatal or silent failure", result.Message, err)
	}
	archived, err := a.Manifests.Load(taskPath)
	if err != nil || archived.LifecycleStatus != domain.TaskArchived {
		t.Fatal("archive did not persist normally", archived, err)
	}
	data, _ := os.ReadFile(router)
	contextData, _ := os.ReadFile(filepath.Join(taskPath, ".workspace", "agent", "TASK-CONTEXT.md"))
	if string(data) != custom || !strings.Contains(string(contextData), peer) {
		t.Fatal("archive overwrote custom AGENTS or skipped safe context")
	}
	a.Platform = platform.NewService(a.Paths, a.Config, nil)
	refreshed, err := a.Dispatch(context.Background(), Action{Kind: "task.refresh"})
	if err != nil || !strings.Contains(refreshed.Message, "警告") {
		t.Fatal("ordinary task refresh hid the nonfatal catalogue warning", refreshed.Message, err)
	}
	// Restore a manageable document as the user would, then use the existing
	// local refresh. No Git runner is present, so it cannot repeat sync writes.
	if err := os.WriteFile(router, original, 0644); err != nil {
		t.Fatal(err)
	}
	result, err = a.Dispatch(context.Background(), Action{Kind: "document.list", TaskID: "task"})
	if err != nil || strings.Contains(result.Message, "说明更新失败") || a.Runner != nil {
		t.Fatal("standalone local retry did not settle the warning", result.Message, err)
	}
	data, _ = os.ReadFile(router)
	if !strings.Contains(string(data), peer) {
		t.Fatal("local retry did not refresh the archived task's managed block")
	}
}

func TestAnnualApplicationInstructionsSaveCustomRouterWarnsAndUpdatesSafeContext(t *testing.T) {
	a, scope, taskPath := annualApplicationFixture(t)
	router := filepath.Join(taskPath, workspace.AgentRouterFileName)
	custom := []byte("# USER CUSTOM INSTRUCTIONS\nkeep all user bytes\n")
	if err := os.WriteFile(router, custom, 0644); err != nil {
		t.Fatal(err)
	}
	peer := filepath.Join(scope.Root, "new-peer")
	if err := os.Mkdir(peer, 0755); err != nil {
		t.Fatal(err)
	}
	manifest, err := a.Manifests.Load(taskPath)
	if err != nil {
		t.Fatal(err)
	}
	docs := workspace.NewAgentDocumentService(a.Paths, a.Manifests)
	notes := docs.TaskNotesPath(taskPath, manifest)
	current, err := a.Documents.Read(taskPath, notes)
	if err != nil {
		t.Fatal(err)
	}
	result, err := a.Dispatch(context.Background(), Action{Kind: "instructions.save", TaskID: "task", Text: "saved task rules", Payload: map[string]any{"expectedHash": current.Hash}})
	if err != nil || !strings.Contains(result.Message, "说明已保存") || !strings.Contains(result.Message, "警告") ||
		!strings.Contains(result.Message, "刷新资料可单独重试") {
		t.Fatal("instructions.save lost either the successful save or its catalogue warning", result.Message, err)
	}
	data, readErr := os.ReadFile(router)
	contextData, contextErr := os.ReadFile(filepath.Join(taskPath, workspace.AgentWorkspaceDir, workspace.AgentDirectoryName, workspace.AgentContextFileName))
	if readErr != nil || string(data) != string(custom) || contextErr != nil || !strings.Contains(string(contextData), peer) {
		t.Fatal("instructions.save overwrote the custom router or skipped safe context", string(data), readErr, contextErr)
	}
	notesData, readErr := os.ReadFile(notes)
	content, ok := result.Data.(documents.Content)
	if readErr != nil || string(notesData) != "saved task rules" || !ok || content.Hash != documents.Hash(notesData) {
		t.Fatal("warning changed saved rules or the returned editor conflict hash", string(notesData), content, readErr)
	}
}

func TestAnnualApplicationInstructionsSaveRequiredContextFailureRemainsFatal(t *testing.T) {
	a, _, taskPath := annualApplicationFixture(t)
	contextPath := filepath.Join(taskPath, workspace.AgentWorkspaceDir, workspace.AgentDirectoryName, workspace.AgentContextFileName)
	if err := os.Remove(contextPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(contextPath, 0700); err != nil {
		t.Fatal(err)
	}
	manifest, err := a.Manifests.Load(taskPath)
	if err != nil {
		t.Fatal(err)
	}
	notes := workspace.NewAgentDocumentService(a.Paths, a.Manifests).TaskNotesPath(taskPath, manifest)
	current, err := a.Documents.Read(taskPath, notes)
	if err != nil {
		t.Fatal(err)
	}
	result, err := a.Dispatch(context.Background(), Action{Kind: "instructions.save", TaskID: "task", Text: "save must report context I/O failure", Payload: map[string]any{"expectedHash": current.Hash}})
	if err == nil || strings.Contains(result.Message, "说明已保存") {
		t.Fatal("required TASK-CONTEXT I/O failure became a successful optional warning", result.Message, err)
	}
}

func TestAnnualApplicationScanDiagnosticsDoNotSilentlySkipArchives(t *testing.T) {
	a, scope, taskPath := annualApplicationFixture(t)
	config, err := a.Config.Load()
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := a.Manifests.Load(taskPath)
	if err != nil {
		t.Fatal(err)
	}
	service := workspace.NewAgentDocumentService(a.Paths, a.Manifests)
	savedBytes := map[string][]byte{}
	var goodArchive string
	for _, name := range []string{"good-archive", "broken-archive", "future-archive"} {
		archived := manifest
		archived.TaskDirectoryName, archived.FolderName, archived.LifecycleStatus = name, name, domain.TaskArchived
		path := filepath.Join(*config.TaskRoot, name)
		if err := a.Manifests.Save(path, archived); err != nil {
			t.Fatal(err)
		}
		if _, err := service.EnsureTaskDocuments(path, archived, config); err != nil {
			t.Fatal(err)
		}
		if name == "good-archive" {
			goodArchive = path
			continue
		}
		manifestPath := filepath.Join(path, storage.TaskManifestFileName)
		data := []byte("{corrupt archive must remain unchanged")
		if name == "future-archive" {
			data, err = os.ReadFile(manifestPath)
			if err != nil {
				t.Fatal(err)
			}
			var raw map[string]any
			if err := json.Unmarshal(data, &raw); err != nil {
				t.Fatal(err)
			}
			raw["schemaVersion"] = "999.0"
			data, err = json.Marshal(raw)
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(manifestPath, data, 0644); err != nil {
			t.Fatal(err)
		}
		savedBytes[manifestPath] = data
	}
	peer := filepath.Join(scope.Root, "peer-after-scan")
	if err := os.Mkdir(peer, 0755); err != nil {
		t.Fatal(err)
	}
	result, err := a.Dispatch(context.Background(), Action{Kind: "document.list", TaskID: "task"})
	if err != nil || !strings.Contains(result.Message, "无法确认资料说明覆盖") ||
		!strings.Contains(result.Message, "broken-archive") || !strings.Contains(result.Message, "future-archive") {
		t.Fatal("incomplete scan was presented as full coverage", result.Message, err)
	}
	for _, path := range []string{taskPath, goodArchive} {
		for _, relative := range []string{"AGENTS.md", filepath.Join(".workspace", "agent", "TASK-CONTEXT.md")} {
			data, err := os.ReadFile(filepath.Join(path, relative))
			if err != nil || !strings.Contains(string(data), peer) {
				t.Fatal("scan diagnostics prevented safe task fanout", path, relative, err)
			}
		}
	}
	for path, original := range savedBytes {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != string(original) {
			t.Fatal("scan warning repaired or rewrote an unreadable/unsupported manifest", path, err)
		}
	}
}

func TestAnnualApplicationClearedConfigurationNeverReenablesFlatMaterials(t *testing.T) {
	for _, layout := range []string{"flat", "annual"} {
		t.Run(layout, func(t *testing.T) {
			t.Setenv("USERPROFILE", t.TempDir())
			a, scope, taskPath := annualApplicationFixture(t)
			config, err := a.Config.Load()
			if err != nil {
				t.Fatal(err)
			}
			manifest, err := a.Manifests.Load(taskPath)
			if err != nil {
				t.Fatal(err)
			}
			owned := scope.Owned
			if layout == "flat" {
				owned = filepath.Join(a.Paths.Home, "old-flat-materials")
				if err := os.MkdirAll(owned, 0755); err != nil {
					t.Fatal(err)
				}
				manifest.RequirementMaterials.WriteRoot = &owned
				if err := a.Manifests.Save(taskPath, manifest); err != nil {
					t.Fatal(err)
				}
			}
			retained := filepath.Join(owned, "keep.txt")
			if err := os.WriteFile(retained, []byte("original file"), 0644); err != nil {
				t.Fatal(err)
			}
			config.RequirementMaterialsRoot, config.RequirementMaterialsSubdirectory = nil, nil
			if err := a.Config.Save(config); err != nil {
				t.Fatal(err)
			}
			a.Platform = platform.NewService(a.Paths, a.Config, nil)
			snapshot, err := a.Snapshot(context.Background())
			if err != nil || len(snapshot.Tasks) != 1 || snapshot.Tasks[0].MaterialsStatus != string(domain.MaterialsFailed) ||
				snapshot.Tasks[0].MaterialsPath != "" || snapshot.Tasks[0].MaterialsFailureReason == "" {
				t.Fatal("cleared configuration left materials available in Snapshot", snapshot.Tasks, err)
			}
			for _, action := range []Action{
				{Kind: "document.create", Path: owned, Payload: map[string]any{"name": "must-not-exist.txt"}},
				{Kind: "document.save", Path: retained, Text: "must not replace"},
			} {
				action.TaskID = "task"
				if _, err := a.Dispatch(context.Background(), action); err == nil {
					t.Fatal("cleared configuration allowed an old materials mutation", action.Kind)
				}
			}
			if _, err := os.Stat(filepath.Join(owned, "must-not-exist.txt")); !os.IsNotExist(err) {
				t.Fatal("unsupported old materials were written", err)
			}
			data, err := os.ReadFile(retained)
			current, loadErr := a.Manifests.Load(taskPath)
			if err != nil || string(data) != "original file" || loadErr != nil ||
				value(current.RequirementMaterials.WriteRoot) != owned || current.RequirementMaterials.Status != domain.MaterialsReady {
				t.Fatal("unavailable materials warning changed original files/manifest", err, loadErr)
			}
		})
	}
}
