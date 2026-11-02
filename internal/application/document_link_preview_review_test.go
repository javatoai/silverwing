package application_test

import (
	"context"
	"os"
	"path/filepath"
	"silverwing-go/internal/application"
	"silverwing-go/internal/documents"
	"testing"
)

func TestReviewLinkedPreviewUsesConfiguredAliasMaterialsScope(t *testing.T) {
	fixture := newLocalMaterialsOperationFixture(t)
	root := documents.LocalTaskMaterials(filepath.Join(fixture.alias, "task-a"))
	path := filepath.Join(root.Owned, "linked.md")
	source := []byte("# linked\r\n中文😀\r\n")
	if err := os.WriteFile(fixture.physical(t, path), source, 0600); err != nil {
		t.Fatal(err)
	}
	result, err := fixture.app.Dispatch(context.Background(), application.Action{Kind: "document.link-preview", TaskID: "task-a", Path: path})
	if err != nil {
		t.Fatal(err)
	}
	content, err := application.ResultData[documents.Content](result)
	if err != nil || content.Path != path || content.Text != string(source) || !content.ReadOnly || content.Draft != nil || content.MarkdownEditContext != "" {
		t.Fatalf("alias preview lost readonly source/path: %+v %v", content, err)
	}
}
