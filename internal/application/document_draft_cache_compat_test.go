package application

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"silverwing-go/internal/documents"
)

func TestMarkdownDraftCacheLegacyEmptyReadOnlyPreservesCache(t *testing.T) {
	a, root, taskPath := materialTextApplicationFixture(t)
	ctx := context.Background()
	if err := os.MkdirAll(a.Paths.Cache, 0700); err != nil {
		t.Fatal(err)
	}
	cachePath := filepath.Join(a.Paths.Cache, "materials-markdown-drafts.json")
	cacheBytes := []byte("{\"schema\":1,\"records\":[]}\n")
	if err := os.WriteFile(cachePath, cacheBytes, 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "reading.md")
	original := "\xef\xbb\xbf# Header\r\n\r\n正文 kept\n"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	action := Action{Kind: "document.read", TaskID: "task", Path: path}
	read, err := a.documentAction(ctx, action)
	if err != nil {
		t.Fatal(err)
	}
	content := read.Data.(documents.Content)
	if content.DraftError != "" || content.Draft != nil || !content.ReadOnly || content.Text != original[3:] || !content.HasUTF8BOM || content.Hash != documents.Hash([]byte(original)) {
		t.Fatalf("Markdown was not the readonly disk source: %#v", content)
	}
	action.Text = "must never replace the disk or the legacy cache"
	action.Payload = map[string]any{"expectedHash": content.Hash, "hasUtf8Bom": true, "newline": "\r\n", "draftTaskPath": taskPath, "draftMaterialsRoot": visibleMaterialsRoot(root)}
	for _, kind := range []string{"document.save", "document.draft", "document.draft-discard"} {
		action.Kind = kind
		action.Payload["overwrite"] = true
		if _, err := a.documentAction(ctx, action); !errors.Is(err, errMarkdownMaterialReadOnly) {
			t.Fatalf("%s did not explicitly reject Markdown mutation: %v", kind, err)
		}
		if source, err := os.ReadFile(path); err != nil || string(source) != original {
			t.Fatalf("%s changed disk bytes: %q %v", kind, source, err)
		}
		if cache, err := os.ReadFile(cachePath); err != nil || string(cache) != string(cacheBytes) {
			t.Fatalf("%s changed the legacy cache: %q %v", kind, cache, err)
		}
	}
	action.Kind = "document.draft-read"
	legacy, err := a.documentAction(ctx, action)
	if err != nil || legacy.Data.(*documents.MarkdownDraft) != nil {
		t.Fatalf("readonly legacy empty cache compatibility failed: %#v %v", legacy, err)
	}
	if cache, err := os.ReadFile(cachePath); err != nil || string(cache) != string(cacheBytes) {
		t.Fatalf("readonly cache read rewrote records:[]: %q %v", cache, err)
	}
}
