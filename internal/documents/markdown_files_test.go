package documents

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMarkdownSaveRejectsInvalidUTF8BeforeReplacingSource(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "source.md")
	original := "\xef\xbb\xbf# Safe source\r\n"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	service := NewService(filepath.Join(root, "cache"))
	snapshot, err := service.MarkdownSnapshot(root, path)
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{string([]byte{0xff}), strings.Repeat("x", MaxEditableMarkdown)} {
		if _, err := service.SaveMarkdown(context.Background(), root, path, invalid, snapshot.Hash); err == nil {
			t.Fatal("invalid UTF-8 or source exceeding the BOM-inclusive limit was saved")
		}
		if disk, err := os.ReadFile(path); err != nil || string(disk) != original {
			t.Fatalf("rejected Markdown save changed source bytes: %q %v", disk, err)
		}
	}
}
