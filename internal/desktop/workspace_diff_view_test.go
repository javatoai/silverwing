package desktop

import (
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

func TestWorkspaceNativeDiffSourcesRequiresRealAddedKind(t *testing.T) {
	content := &workspace.WorkspaceFilePreviewContent{Text: "new file\n"}
	comparison := &workspace.WorkspaceFileComparison{NewContent: content}

	oldText, newText, ok := workspaceNativeDiffSourcesForKind(comparison, domain.WorkspaceGitAdded)
	if !ok || oldText != "" || newText != content.Text {
		t.Fatalf("added source (%q, %q, %v), want empty old and complete new source", oldText, newText, ok)
	}
	if _, _, ok := workspaceNativeDiffSourcesForKind(comparison, ""); ok {
		t.Fatal("unknown file kind with no old content was treated as added")
	}
	if _, _, ok := workspaceNativeDiffSourcesForKind(comparison, domain.WorkspaceGitModified); ok {
		t.Fatal("modified file with unavailable old content was treated as added")
	}

	comparison.OldContent = &workspace.WorkspaceFilePreviewContent{Text: "old file\n"}
	oldText, newText, ok = workspaceNativeDiffSourcesForKind(comparison, domain.WorkspaceGitModified)
	if !ok || oldText != comparison.OldContent.Text || newText != content.Text {
		t.Fatalf("modified source (%q, %q, %v), want complete two-sided source", oldText, newText, ok)
	}

	comparison.Truncated = true
	if _, _, ok := workspaceNativeDiffSourcesForKind(comparison, domain.WorkspaceGitAdded); ok {
		t.Fatal("truncated comparison was treated as a complete native added-file source")
	}
}
