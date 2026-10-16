package documents

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestMaterialDraftCacheEmptyArrayCompatibilityAndRoundTrip(t *testing.T) {
	for _, text := range []bool{false, true} {
		name, extension := "markdown", ".md"
		if text {
			name, extension = "text", ".txt"
		}
		for _, records := range []string{"[]", "[ \n\t ]", "{}", "null"} {
			t.Run(name+"/"+records, func(t *testing.T) {
				ctx, root := context.Background(), t.TempDir()
				cacheRoot, materials := filepath.Join(root, "home", "cache"), filepath.Join(root, "materials")
				for _, directory := range []string{cacheRoot, materials} {
					if err := os.MkdirAll(directory, 0700); err != nil {
						t.Fatal(err)
					}
				}
				cachePath := filepath.Join(cacheRoot, "materials-"+name+"-drafts.json")
				original := []byte(`{"schema":1,"records":` + records + "}\n")
				if err := os.WriteFile(cachePath, original, 0600); err != nil {
					t.Fatal(err)
				}
				service := NewService(cacheRoot)
				read, write := service.ReadMarkdownDraft, service.WriteMarkdownDraft
				if text {
					read, write = service.ReadMaterialTextDraft, service.WriteMaterialTextDraft
				}
				var drafts []MarkdownDraft
				for _, filename := range []string{"first", "second"} {
					path := filepath.Join(materials, filename+extension)
					if err := os.WriteFile(path, []byte("disk\r\n"), 0600); err != nil {
						t.Fatal(err)
					}
					identity, err := NewMarkdownDraftIdentity("task", filepath.Join(root, "tasks", "task"), materials, path)
					if err != nil {
						t.Fatal(err)
					}
					drafts = append(drafts, MarkdownDraft{Identity: identity, Content: filename + " 中文🚀\r\n\tkept\n", BaselineHash: Hash([]byte("disk\r\n")), HasUTF8BOM: true, Newline: "\r\n"})
				}
				if restored, err := read(ctx, drafts[0].Identity); err != nil || restored != nil {
					t.Fatalf("empty cache cannot be read: draft=%v err=%v", restored, err)
				}
				if data, err := os.ReadFile(cachePath); err != nil || string(data) != string(original) {
					t.Fatal("reading rewrote the original cache", err)
				}
				for _, draft := range drafts {
					if err := write(ctx, draft); err != nil {
						t.Fatal(err)
					}
				}
				for _, draft := range drafts {
					if restored, err := read(ctx, draft.Identity); err != nil || restored == nil || *restored != draft {
						t.Fatalf("draft or existing sibling was lost: %v %v", restored, err)
					}
				}
			})
		}
	}
}

func TestMaterialDraftCacheUnsupportedRecordsPreserveOriginal(t *testing.T) {
	for _, original := range []string{
		`{"schema":1,"records":[{"content":"unsaved source"}]}`,
		`{"schema":1,"records":true}`,
		`{"schema":2,"records":[]}`,
		`{"schema":1,"records":[`,
	} {
		t.Run(original, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "materials-markdown-drafts.json")
			if err := os.WriteFile(path, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			identity, err := NewMarkdownDraftIdentity("task", filepath.Join(root, "tasks"), filepath.Join(root, "materials"), "source.md")
			if err != nil {
				t.Fatal(err)
			}
			service := NewService(root)
			if _, err := service.ReadMarkdownDraft(context.Background(), identity); err == nil {
				t.Fatal("unsupported cache was accepted as an empty cache")
			}
			if err := service.WriteMarkdownDraft(context.Background(), MarkdownDraft{Identity: identity, Content: "new", BaselineHash: Hash([]byte("disk"))}); err == nil {
				t.Fatal("unsupported cache was overwritten")
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != original {
				t.Fatal("unsupported cache content changed", err)
			}
		})
	}
}
