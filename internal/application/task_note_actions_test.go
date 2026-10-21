package application

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
	"silverwing-go/internal/workspace"
)

func TestLocalTaskRulesActionsAreRawAndConfined(t *testing.T) {
	paths := storage.NewPaths(t.TempDir())
	a := &App{Paths: paths, Config: storage.NewConfigStore(paths), Manifests: storage.NewManifestStore(), Documents: documents.NewService(paths.Cache)}
	config := domain.DefaultAppConfig()
	config.TaskRoot = &paths.Tasks
	if err := a.Config.Save(config); err != nil {
		t.Fatal(err)
	}
	manifest := taskIdentityFixture(t, a, paths.Tasks, "task", "task")
	root := filepath.Join(paths.Tasks, "task")
	path := workspace.NewAgentDocumentService(paths, a.Manifests).TaskNotesPath(root, manifest)
	action := Action{Kind: "instructions.read", TaskID: "task", Path: workspace.AgentRulesFileName, Payload: map[string]any{"localTaskRules": true, "taskPath": root, "taskCreatedAt": manifest.CreatedAt}}
	missing, err := a.instructionAction(context.Background(), action)
	if err != nil || missing.Text != "" || missing.Path != path || missing.Data.(documents.TaskRulesSnapshot).Exists {
		t.Fatalf("missing task file did not load empty: %+v %v", missing, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("opening created the file")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(filepath.Dir(path), workspace.AgentContextFileName)
	if err := os.WriteFile(sentinel, []byte("不要重写生成文档\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	action.Kind = "instructions.save"
	action.Text = "完整中文\r\n尾部空格  \r\n\r\n"
	action.Payload["expectedExists"] = false
	saved, err := a.instructionAction(context.Background(), action)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != action.Text {
		t.Fatalf("raw task save trimmed source: %q", data)
	}
	unchanged, _ := os.ReadFile(sentinel)
	if string(unchanged) != "不要重写生成文档\r\n" {
		t.Fatal("saving notes regenerated other files")
	}
	action.Kind = "instructions.read"
	read, err := a.instructionAction(context.Background(), action)
	if err != nil || read.Text != action.Text {
		t.Fatalf("raw instruction read trimmed newlines: %q %v", read.Text, err)
	}
	action.Kind = "instructions.save"
	action.Text = ""
	action.Payload["expectedExists"] = true
	action.Payload["expectedHash"] = saved.Data.(documents.TaskRulesSnapshot).Hash
	if _, err := a.instructionAction(context.Background(), action); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	if len(data) != 0 {
		t.Fatal("explicit empty instruction save was not empty")
	}
	action.Path = sentinel
	if _, err := a.instructionAction(context.Background(), action); err == nil {
		t.Fatal("UI-selected arbitrary instruction file was accepted")
	}
	action.Path = workspace.AgentRulesFileName
	action.Payload["taskCreatedAt"] = "replaced-task"
	if _, err := a.instructionAction(context.Background(), action); err == nil {
		t.Fatal("a stale task identity saved the current task")
	}
}
