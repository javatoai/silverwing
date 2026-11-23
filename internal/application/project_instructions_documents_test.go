package application

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/workspace"
)

func TestProjectInstructionsDocumentActionsFollowAppliedBranch(t *testing.T) {
	ctx := context.Background()
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const url = "https://example.invalid/team/instructions.git"
	const branch = "docs/team"
	const name = "说明.md"
	const original = "# Current disk source\n"
	const pending = "# Work in progress\n"
	baseline := documents.Hash([]byte(original))
	cfg, err := a.Config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.ProjectInstructionsGitURL = url
	autoSync := false
	cfg.ProjectInstructionsGitEnabled = &autoSync
	saveConfig := func(selected string) {
		t.Helper()
		cfg.ProjectInstructionsGitBranch = selected
		if err := a.Config.Save(cfg); err != nil {
			t.Fatal(err)
		}
	}
	saveConfig("")
	scope := ProjectInstructionsGitScope(a.Paths.Home, cfg)
	act := func(kind, selected, body string) (ActionResult, error) {
		t.Helper()
		captured := scope
		captured.Branch = selected
		return a.Dispatch(ctx, Action{Kind: "document." + kind, Path: name, Text: body, Payload: map[string]any{
			ProjectInstructionsScopePayloadKey: captured, "expectedHash": baseline, "markdownEditContext": taskMaterialsMarkdownEditContext,
		}})
	}
	if _, err := act("list", "", ""); err == nil || !strings.Contains(err.Error(), "尚未克隆") {
		t.Fatalf("missing clone did not retain its error: %v", err)
	}
	directory := filepath.Join(scope.Root, "project-materials")
	if _, err := os.Lstat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("listing an uncloned repository created the directory: %v", err)
	}
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	documentPath := filepath.Join(directory, name)
	headPath := filepath.Join(scope.Root, ".git", "HEAD")
	bind := func(actual, applied string) {
		t.Helper()
		state := map[string]string{"URL": url, "Branch": actual, "StatusURL": url}
		if applied != "" {
			state["CheckoutTargetBranch"] = applied
		}
		encoded, err := json.Marshal(state)
		if err != nil {
			t.Fatal(err)
		}
		write(scope.Root+".json", string(encoded))
		write(headPath, "ref: refs/heads/"+actual+"\n")
	}
	bind("main", "")
	saveConfig(branch)
	if _, err := act("list", branch, ""); err == nil || !strings.Contains(err.Error(), "尚未检出") {
		t.Fatalf("missing directory bypassed the checkout guard: %v", err)
	}
	if _, err := os.Lstat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("listing a pending branch created the directory: %v", err)
	}
	saveConfig("")
	write(directory, "existing file")
	if _, err := act("list", "", ""); err == nil {
		t.Fatal("a regular file was accepted as the project instructions directory")
	}
	if disk, err := os.ReadFile(directory); err != nil || string(disk) != "existing file" {
		t.Fatalf("the regular file was modified: %s %v", disk, err)
	}
	if err := os.Remove(directory); err != nil {
		t.Fatal(err)
	}
	result, err := act("list", "", "")
	entries, ok := result.Data.([]documents.Entry)
	if err != nil || result.Path != directory || !ok || len(entries) != 0 {
		t.Fatalf("list did not create an empty project directory with auto-sync disabled: %+v %v", result, err)
	}
	write(documentPath, original)
	if result, err := act("read", "", ""); err != nil || result.Text != original {
		t.Fatalf("existing default-branch checkout was not readable: %+v %v", result, err)
	}
	saveConfig(branch)
	if _, err := act("read", "", ""); !errors.Is(err, workspace.ErrMaterialsGitScopeChanged) {
		t.Fatalf("old branch view still opened project files: %v", err)
	}
	for _, kind := range []string{"list", "read", "save"} {
		if _, err := act(kind, branch, pending); err == nil || !strings.Contains(err.Error(), "尚未检出") {
			t.Errorf("%s bypassed the pending branch switch: %v", kind, err)
		}
	}
	if disk, err := os.ReadFile(documentPath); err != nil || string(disk) != original {
		t.Fatalf("pending branch save changed the previous checkout: %s %v", disk, err)
	}
	if _, err := act("draft", "", pending); err != nil {
		t.Fatalf("failed to preserve stale default draft: %v", err)
	}
	bind(branch, branch)
	result, err = act("read", branch, "")
	content, ok := result.Data.(documents.Content)
	if err != nil || !ok || content.Draft != nil {
		t.Fatalf("explicit branch inherited the default draft: %+v %v", result, err)
	}
	if _, err := act("draft", branch, "# Explicit branch draft\n"); err != nil {
		t.Fatal(err)
	}
	saveConfig("")
	if _, err := act("read", "", ""); err == nil || !strings.Contains(err.Error(), "尚未检出") {
		t.Fatalf("remote-default mode used the previous explicit checkout: %v", err)
	}
	bind("main", "")
	result, err = act("read", "", "")
	content, ok = result.Data.(documents.Content)
	if err != nil || !ok || content.Draft == nil || content.Draft.Content != pending {
		t.Fatalf("default draft was not restored after returning to default branch: %+v %v", result, err)
	}
	if content.ReadOnly || content.MarkdownEditContext != taskMaterialsMarkdownEditContext {
		t.Fatalf("Markdown did not open in the shared editor: %+v", content)
	}
	t.Run("shared file operations", func(t *testing.T) {
		dispatch := func(kind, path, text string, fields map[string]any) (ActionResult, error) {
			if fields == nil {
				fields = make(map[string]any)
			}
			fields[ProjectInstructionsScopePayloadKey] = scope
			return a.Dispatch(ctx, Action{Kind: "document." + kind, Path: path, Text: text, Payload: fields})
		}
		source := filepath.Join(t.TempDir(), "copied.txt")
		const text = "service instructions fixture\n"
		if err := os.WriteFile(source, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		pasted, err := dispatch("paste", directory, "", map[string]any{
			"capturedClipboard": true, "clipboard": documents.ClipboardPayload{Files: []string{source}},
		})
		batch, ok := pasted.Data.(documents.FileOperationBatchResult)
		if err != nil || !ok || len(batch.Completed) != 1 {
			t.Fatalf("clipboard copy did not use the project scope: %+v %v", pasted, err)
		}
		copied := filepath.Join(directory, "copied.txt")
		renamed, err := dispatch("rename", copied, "", map[string]any{"name": "notes.txt"})
		if err != nil || renamed.Path != filepath.Join(directory, "notes.txt") {
			t.Fatalf("rename failed: %+v %v", renamed, err)
		}
		const changed = "edited project text\n"
		if _, err := dispatch("save", renamed.Path, changed, map[string]any{"expectedHash": documents.Hash([]byte(text))}); err != nil {
			t.Fatalf("plain text edit failed: %v", err)
		}
		searched, err := dispatch("search", "", "", map[string]any{"query": "edited project text"})
		found, ok := searched.Data.(documents.SearchResult)
		if err != nil || !ok || len(found.Hits) != 1 || found.Hits[0].FileName != "notes.txt" {
			t.Fatalf("search missed the edited project document: %+v %v", searched, err)
		}
		linked, err := dispatch("link-preview", renamed.Path, "", nil)
		preview, ok := linked.Data.(documents.Content)
		if err != nil || !ok || !preview.ReadOnly || preview.Text != changed {
			t.Fatalf("local link preview did not use the project document: %+v %v", linked, err)
		}
		if _, err := dispatch("link-preview", source, "", nil); err == nil {
			t.Fatal("project link preview read outside the project directory")
		}
	})
	write(headPath, "ref: refs/heads/another\n")
	if _, err := act("read", "", ""); err == nil || !strings.Contains(err.Error(), "与同步记录不一致") {
		t.Fatalf("external HEAD change bypassed verification: %v", err)
	}
}
