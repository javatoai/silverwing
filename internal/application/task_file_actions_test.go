package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/media"
	"silverwing-go/internal/storage"
)

func taskFilesActionFixture(t *testing.T) (*App, domain.TaskManifest, Action) {
	t.Helper()
	paths := storage.NewPaths(t.TempDir())
	a := &App{Paths: paths, Config: storage.NewConfigStore(paths), Manifests: storage.NewManifestStore()}
	cfg := domain.DefaultAppConfig()
	cfg.TaskRoot = &paths.Tasks
	if err := a.Config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	manifest := domain.DefaultTaskManifest()
	manifest.TaskDirectoryName = "task"
	manifest.FolderName = "Task"
	manifest.CreatedAt = "2026-11-01T10:00:00Z"
	manifest.FeatureBranch = "feature/task"
	root := filepath.Join(paths.Tasks, "task")
	if err := a.Manifests.Save(root, manifest); err != nil {
		t.Fatal(err)
	}
	for path, text := range map[string]string{"AGENTS.md": "# unchanged\r\n", ".workspace/agent/TASK-RULES.md": "untouched rules", "normal.txt": "readonly body", ".gitignore": "/ignored\n", "ignored/needle.txt": "ignored but visible", ".git/objects/secret": "git private"} {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	expected := TaskFilesContext{Home: paths.Home, TaskPath: root, TaskCreatedAt: manifest.CreatedAt, TaskRoot: paths.Tasks, Archived: false}
	return a, manifest, Action{TaskID: "task", Payload: map[string]any{"taskFilesContext": expected}}
}

func taskFilesBytes(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			result[rel] = fmt.Sprintf("%x", sha256.Sum256(data))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestTaskFilesActionsReadonlyIndependentOfMaterialsAndConfigChanges(t *testing.T) {
	a, _, action := taskFilesActionFixture(t)
	before := taskFilesBytes(t, a.Paths.Home)
	for _, kind := range []string{"task.files.list", "task.files.read", "task.files.search"} {
		action.Kind = kind
		action.Path = ""
		if kind == "task.files.read" {
			action.Path = ".workspace/agent/TASK-RULES.md"
		}
		if kind == "task.files.search" {
			action.Payload["query"] = "needle"
		}
		result, err := a.Dispatch(context.Background(), action)
		if err != nil {
			t.Fatal(kind, err)
		}
		if kind == "task.files.read" {
			content := result.Data.(TaskFilesReadResult).Content
			if !content.ReadOnly || content.Draft != nil || content.TextDraft != nil || content.MarkdownEditContext != "" || content.Text != "untouched rules" {
				t.Fatal("read entered materials/draft editor", content)
			}
		}
		if kind == "task.files.search" && len(result.Data.(TaskFilesSearchResult).Entries) != 1 {
			t.Fatal(result)
		}
	}
	after := taskFilesBytes(t, a.Paths.Home)
	if len(before) != len(after) {
		t.Fatal("readonly actions created files", before, after)
	}
	for path, hash := range before {
		if after[path] != hash {
			t.Fatal("readonly action wrote file", path)
		}
	}
	cfg, err := a.Config.LoadForPreview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cfg.Theme = domain.ThemeDark
	cfg.RequirementMaterialsRoot = stringPtrTaskFiles(t.TempDir())
	cfg.RequirementMaterialsSubdirectory = stringPtrTaskFiles("requirements")
	if err := a.Config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	action.Kind = "task.files.list"
	action.Path = ""
	if _, err := a.Dispatch(context.Background(), action); err != nil {
		t.Fatal("unrelated source/theme config invalidated task file scope", err)
	}
}
func stringPtrTaskFiles(s string) *string { return &s }

func TestTaskFilesGitSystemOpenRetainsOwnerAndContentBoundaries(t *testing.T) {
	a, _, action := taskFilesActionFixture(t)
	before := taskFilesBytes(t, a.Paths.Home)
	expected := action.Payload["taskFilesContext"].(TaskFilesContext)
	calls := 0
	launch := func(_ context.Context, kind, path string, directory bool) (ActionResult, error) {
		calls++
		if kind != "task.files.open" || !directory || path != filepath.Join(expected.TaskPath, ".git") {
			t.Fatal("wrong Git launch context", kind, path, directory)
		}
		return ActionResult{Path: path}, nil
	}
	action.Kind, action.Path = "task.files.open", ".git"
	if _, err := a.taskFilesActionWithLauncher(context.Background(), action, launch); err != nil || calls != 1 {
		t.Fatal("Git root system open failed", err, calls)
	}
	action.Path = ".git/objects/secret"
	if _, err := a.taskFilesActionWithLauncher(context.Background(), action, launch); err == nil || calls != 1 {
		t.Fatal("Git content system open escaped scope", err, calls)
	}
	action.Kind, action.Path = "task.files.read", ".git"
	if _, err := a.Dispatch(context.Background(), action); err == nil {
		t.Fatal("system-open exception enabled Git content reads")
	}
	action.Kind = "task.files.open"
	expected.TaskCreatedAt = "stale-owner"
	action.Payload["taskFilesContext"] = expected
	if _, err := a.taskFilesActionWithLauncher(context.Background(), action, launch); err == nil || calls != 1 {
		t.Fatal("stale task opened Git metadata", err, calls)
	}
	after := taskFilesBytes(t, a.Paths.Home)
	if len(before) != len(after) {
		t.Fatal("Git browse/launch changed file membership")
	}
	for p, hash := range before {
		if after[p] != hash {
			t.Fatal("Git browse/launch changed a file", p)
		}
	}
}

func TestTaskFilesReadMediaFlagSnapshotsAndNoWrites(t *testing.T) {
	a, _, action := taskFilesActionFixture(t)
	expected := action.Payload["taskFilesContext"].(TaskFilesContext)
	fixtures := map[string][]byte{"picture.png": {0x89, 'P', 'N', 'G', 0, 1}, "document.pdf": []byte("%PDF-snapshot\n")}
	for name, data := range fixtures {
		if err := os.WriteFile(filepath.Join(expected.TaskPath, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	before := taskFilesBytes(t, a.Paths.Home)
	action.Kind = "task.files.read"
	for path, data := range fixtures {
		action.Path = path
		delete(action.Payload, "media")
		metadataAction, err := a.Dispatch(context.Background(), action)
		if err != nil {
			t.Fatal(path, err)
		}
		metadata := metadataAction.Data.(TaskFilesReadResult)
		if len(metadata.MediaBytes) != 0 || metadata.MediaHash != "" || metadata.Content.Hash != "" {
			t.Fatal("omitted flag loaded media bytes", metadata)
		}
		action.Payload["media"] = false
		withoutMedia, err := a.Dispatch(context.Background(), action)
		if err != nil || len(withoutMedia.Data.(TaskFilesReadResult).MediaBytes) != 0 {
			t.Fatal("false flag changed metadata read", withoutMedia, err)
		}
		action.Payload["media"] = true
		result, err := a.Dispatch(context.Background(), action)
		if err != nil {
			t.Fatal(path, err)
		}
		snapshot := result.Data.(TaskFilesReadResult)
		if !bytes.Equal(snapshot.MediaBytes, data) || snapshot.MediaHash != documents.Hash(data) || snapshot.Content.Size != int64(len(snapshot.MediaBytes)) || snapshot.Content.Hash != metadata.Content.Hash || snapshot.Identity != metadata.Identity || snapshot.Content.Modified != metadata.Content.Modified || snapshot.FallbackReason != "" {
			t.Fatal("snapshot bytes/hash diverged from metadata", snapshot, metadata)
		}
		if !snapshot.Content.ReadOnly || snapshot.Content.Draft != nil || snapshot.Content.TextDraft != nil || snapshot.Content.MarkdownEditContext != "" {
			t.Fatal("media request acquired write state", snapshot)
		}
	}
	action.Path = "normal.txt"
	if result, err := a.Dispatch(context.Background(), action); err == nil || result.Data != nil {
		t.Fatal("media mode accepted a text document", result, err)
	}
	for _, invalid := range []any{nil, "true", 1, map[string]any{"enabled": true}} {
		action.Path = "picture.png"
		action.Payload["media"] = invalid
		if result, err := a.Dispatch(context.Background(), action); err == nil || result.Data != nil {
			t.Fatal("nonboolean media flag accepted", invalid, result, err)
		}
	}
	after := taskFilesBytes(t, a.Paths.Home)
	if len(before) != len(after) {
		t.Fatal("media reads created files", before, after)
	}
	for path, hash := range before {
		if after[path] != hash {
			t.Fatal("media reads changed task/config files", path)
		}
	}
}

func TestTaskFilesReadMediaUsesFixedServerBudgets(t *testing.T) {
	a, _, action := taskFilesActionFixture(t)
	expected := action.Payload["taskFilesContext"].(TaskFilesContext)
	action.Kind = "task.files.read"
	action.Payload["media"] = true
	// Unknown client limits cannot raise either server budget.
	action.Payload["maxBytes"] = int64(1 << 40)
	action.Payload["maxImageBytes"] = int64(1 << 40)
	action.Payload["maxPDFBytes"] = int64(1 << 40)
	for name, cap := range map[string]int64{"large.png": media.DefaultMaxImageBytes, "large.pdf": media.DefaultMaxPDFBytes} {
		file, err := os.Create(filepath.Join(expected.TaskPath, name))
		if err != nil {
			t.Fatal(err)
		}
		truncateErr := file.Truncate(cap + 1)
		closeErr := file.Close()
		if truncateErr != nil || closeErr != nil {
			t.Fatal(truncateErr, closeErr)
		}
		action.Path = name
		result, err := a.Dispatch(context.Background(), action)
		if err != nil {
			t.Fatal(name, err)
		}
		read := result.Data.(TaskFilesReadResult)
		if read.FallbackReason == "" || len(read.MediaBytes) != 0 || read.MediaHash != "" || read.Content.Size != cap+1 || read.Content.Hash != "" || !read.Content.ReadOnly {
			t.Fatal("client override widened media budget", name, read)
		}
	}
}

type taskFilesActionCheckpointContext struct {
	context.Context
	calls int
	hook  func(int)
}

func (c *taskFilesActionCheckpointContext) Err() error {
	c.calls++
	if c.hook != nil {
		c.hook(c.calls)
	}
	return c.Context.Err()
}

func TestTaskFilesReadMediaDiscardsBytesWhenOwnerChangesBeforeReturn(t *testing.T) {
	a, manifest, action := taskFilesActionFixture(t)
	expected := action.Payload["taskFilesContext"].(TaskFilesContext)
	if err := os.WriteFile(filepath.Join(expected.TaskPath, "picture.png"), []byte("immutable source bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	action.Kind, action.Path = "task.files.read", "picture.png"
	action.Payload["media"] = true
	// Calibrate the last checkpoint on this identical read rather than depend
	// on storage implementation counts. It belongs to the final owner reload,
	// after ReadMedia has closed the pinned file and produced its bytes.
	control := &taskFilesActionCheckpointContext{Context: context.Background()}
	if result, err := a.Dispatch(control, action); err != nil || len(result.Data.(TaskFilesReadResult).MediaBytes) == 0 {
		t.Fatal("media read control failed", result, err)
	}
	changed := false
	ctx := &taskFilesActionCheckpointContext{Context: context.Background()}
	ctx.hook = func(checkpoint int) {
		if checkpoint != control.calls {
			return
		}
		manifest.CreatedAt = "replacement-task-created-at"
		if err := a.Manifests.Save(expected.TaskPath, manifest); err != nil {
			t.Fatal(err)
		}
		changed = true
	}
	result, err := a.Dispatch(ctx, action)
	if !changed {
		t.Fatal("post-read owner replacement checkpoint was not reached", control.calls, ctx.calls)
	}
	if !errors.Is(err, errTaskFilesContext) || result.Data != nil || result.Text != "" || result.Path != "" {
		t.Fatal("obsolete owner published media snapshot", result, err)
	}
}

func TestTaskFilesActionsRequireExactOwnerAndReadonlyRoutes(t *testing.T) {
	for _, scenario := range []string{"missing", "home", "path", "created", "root", "archive", "task-id", "absolute", "traversal", "git", "mutation"} {
		t.Run(scenario, func(t *testing.T) {
			a, _, action := taskFilesActionFixture(t)
			action.Kind = "task.files.read"
			action.Path = "normal.txt"
			expected := action.Payload["taskFilesContext"].(TaskFilesContext)
			switch scenario {
			case "missing":
				delete(action.Payload, "taskFilesContext")
			case "home":
				expected.Home = t.TempDir()
			case "path":
				expected.TaskPath = t.TempDir()
			case "created":
				expected.TaskCreatedAt = "another identity"
			case "root":
				expected.TaskRoot = t.TempDir()
			case "archive":
				expected.Archived = true
			case "task-id":
				action.TaskID = "other"
			case "absolute":
				action.Path = filepath.Join(expected.TaskPath, "normal.txt")
			case "traversal":
				action.Path = "../normal.txt"
			case "git":
				action.Path = ".git/objects/secret"
			case "mutation":
				action.Kind = "task.files.save"
			}
			if scenario != "missing" {
				action.Payload["taskFilesContext"] = expected
			}
			result, err := a.Dispatch(context.Background(), action)
			if err == nil || result.Data != nil || result.Text != "" {
				t.Fatal("invalid ownership/range or write route accepted", scenario, result, err)
			}
		})
	}
}

func TestTaskFilesActionsArchiveAndPostOperationRevalidation(t *testing.T) {
	a, manifest, action := taskFilesActionFixture(t)
	expected := action.Payload["taskFilesContext"].(TaskFilesContext)
	manifest.LifecycleStatus = domain.TaskArchived
	if err := a.Manifests.Save(expected.TaskPath, manifest); err != nil {
		t.Fatal(err)
	}
	action.Kind = "task.files.list"
	if _, err := a.Dispatch(context.Background(), action); err == nil {
		t.Fatal("lifecycle stale context accepted")
	}
	expected.Archived = true
	action.Payload["taskFilesContext"] = expected
	if _, err := a.Dispatch(context.Background(), action); err != nil {
		t.Fatal("archive readonly browsing rejected", err)
	}
	action.Kind = "task.files.open"
	action.Path = "normal.txt"
	called := false
	result, err := a.taskFilesActionWithLauncher(context.Background(), action, func(_ context.Context, kind, path string, directory bool) (ActionResult, error) {
		called = true
		if directory || filepath.Base(path) != "normal.txt" || kind != "task.files.open" {
			t.Fatal("launcher received wrong scope", path)
		}
		manifest.CreatedAt = "replacement-created"
		if err := a.Manifests.Save(expected.TaskPath, manifest); err != nil {
			t.Fatal(err)
		}
		return ActionResult{Path: path}, nil
	})
	if !called || err == nil || result.Data != nil || result.Path != "" {
		t.Fatal("post-operation ownership was not checked", result, err)
	}
}

func TestTaskFilesActionsCancelMissingConfigAndPolicy(t *testing.T) {
	a, _, action := taskFilesActionFixture(t)
	action.Kind = "task.files.search"
	action.Payload["query"] = "normal"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.Dispatch(ctx, action); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, kind := range []string{"task.files.list", "task.files.read", "task.files.search", "task.files.open", "task.files.reveal"} {
		policy := DescribeAction(kind)
		if !policy.Known || !policy.Cancellable || !policy.QuietSuccess || !policy.DedicatedResult || policy.ReloadSnapshot || policy.TaskMenuScoped {
			t.Fatal("read-only task-file policy mismatch", kind, policy)
		}
	}
	paths := storage.NewPaths(filepath.Join(t.TempDir(), "absent-home"))
	empty := &App{Paths: paths, Config: storage.NewConfigStore(paths), Manifests: storage.NewManifestStore()}
	action.Kind = "task.files.list"
	if _, err := empty.Dispatch(context.Background(), action); err == nil {
		t.Fatal("missing config accepted")
	}
	if _, err := os.Lstat(paths.Home); !os.IsNotExist(err) {
		t.Fatal("read initialized home/config/task folders", err)
	}
}
