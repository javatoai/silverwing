package application

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"silverwing-go/internal/documents"
)

func TestDocumentLinkPreviewReadsExternalFileWithoutWriteCapability(t *testing.T) {
	a, root, _ := materialTextApplicationFixture(t)
	path := filepath.Join(t.TempDir(), "external.MD")
	bytes := []byte("\xef\xbb\xbf# 外部文件\r\n原始内容\r\n")
	if err := os.WriteFile(path, bytes, 0600); err != nil {
		t.Fatal(err)
	}
	result, err := a.Dispatch(context.Background(), Action{Kind: "document.link-preview", Path: path})
	if err != nil {
		t.Fatal(err)
	}
	content, ok := result.Data.(documents.Content)
	if !ok || !content.ReadOnly || content.MarkdownEditContext != "" || content.Draft != nil || content.TextDraft != nil || content.Path != path {
		t.Fatalf("preview gained write context: %#v", result)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(bytes) {
		t.Fatalf("source changed: %q %v", got, err)
	}
	if _, err := a.Documents.ReadMaterialsPreview(root, path); err == nil {
		t.Fatal("normal materials boundary was weakened")
	}
}

func TestDocumentLinkPreviewRejectsMissingDirectoryRelativeAndCancelled(t *testing.T) {
	a, _, _ := materialTextApplicationFixture(t)
	for _, path := range []string{"relative.md", t.TempDir(), filepath.Join(t.TempDir(), "missing.md")} {
		if _, err := a.Dispatch(context.Background(), Action{Kind: "document.link-preview", Path: path}); err == nil {
			t.Fatalf("accepted %s", path)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.Dispatch(ctx, Action{Kind: "document.link-preview", Path: filepath.Join(t.TempDir(), "a.md")}); err == nil {
		t.Fatal("accepted cancelled request")
	}
}
