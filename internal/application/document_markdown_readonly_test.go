package application

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"silverwing-go/internal/documents"
)

func TestMarkdownReadOnlyIgnoresAndPreservesDraftCache(t *testing.T) {
	for _, scenario := range []string{"missing", "malformed", "populated"} {
		t.Run(scenario, func(t *testing.T) {
			a, root, taskPath := materialTextApplicationFixture(t)
			ctx := context.Background()
			path := filepath.Join(root, "reading.MARKDOWN")
			original := "\xef\xbb\xbf# Disk source\r\n\nunchanged  \r\n"
			if err := os.WriteFile(path, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			cachePath := filepath.Join(a.Paths.Cache, "materials-markdown-drafts.json")
			var cacheBytes []byte
			if scenario == "malformed" {
				if err := os.MkdirAll(a.Paths.Cache, 0700); err != nil {
					t.Fatal(err)
				}
				cacheBytes = []byte("{unreadable cache must stay intact\n")
				if err := os.WriteFile(cachePath, cacheBytes, 0600); err != nil {
					t.Fatal(err)
				}
			}
			const cachedSource = "# Existing unsaved draft\n保留这份草稿"
			if scenario == "populated" {
				identity, err := documents.NewMarkdownDraftIdentity("task", taskPath, root, path)
				if err != nil {
					t.Fatal(err)
				}
				if err := a.Documents.WriteMarkdownDraft(ctx, documents.MarkdownDraft{Identity: identity, Content: cachedSource, BaselineHash: documents.Hash([]byte("old disk version"))}); err != nil {
					t.Fatal(err)
				}
				cacheBytes, err = os.ReadFile(cachePath)
				if err != nil {
					t.Fatal(err)
				}
			}

			action := Action{Kind: "document.read", TaskID: "task", Path: path}
			read, err := a.documentAction(ctx, action)
			if err != nil {
				t.Fatal(err)
			}
			content := read.Data.(documents.Content)
			if !content.ReadOnly || read.Text != original[3:] || content.Text != read.Text || content.Draft != nil || content.TextDraft != nil || content.DraftError != "" {
				t.Fatalf("readonly read consulted or restored the draft cache: %#v", content)
			}
			action.Text = "blocked mutation"
			action.Payload = map[string]any{"expectedHash": content.Hash, "overwrite": true, "draftTaskPath": taskPath, "draftMaterialsRoot": root}
			for _, kind := range []string{"document.save", "document.draft", "document.draft-discard"} {
				action.Kind = kind
				if _, err := a.documentAction(ctx, action); !errors.Is(err, errMarkdownMaterialReadOnly) {
					t.Fatalf("%s did not reject Markdown mutation before cache access: %v", kind, err)
				}
			}
			if source, err := os.ReadFile(path); err != nil || string(source) != original {
				t.Fatalf("readonly actions changed source bytes: %q %v", source, err)
			}
			if scenario == "missing" {
				if _, err := os.Stat(cachePath); !os.IsNotExist(err) {
					t.Fatalf("readonly actions created a draft cache: %v", err)
				}
			} else {
				if cache, err := os.ReadFile(cachePath); err != nil || string(cache) != string(cacheBytes) {
					t.Fatalf("readonly actions changed the preserved cache: %q %v", cache, err)
				}
			}
			if scenario == "populated" {
				action.Kind = "document.draft-read"
				draft, err := a.documentAction(ctx, action)
				if err != nil || draft.Text != cachedSource {
					t.Fatalf("explicit readonly draft access failed: %#v %v", draft, err)
				}
				if cache, err := os.ReadFile(cachePath); err != nil || string(cache) != string(cacheBytes) {
					t.Fatalf("explicit readonly draft access modified cache: %q %v", cache, err)
				}
			}
		})
	}
}
