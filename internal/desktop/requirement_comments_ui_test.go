package desktop

import (
	"strings"
	"testing"

	"silverwing-go/internal/domain"
)

func TestRequirementCommentsMarkdownKeepsSourceCompact(t *testing.T) {
	parentID := "comment-1"
	attachment := "https://example.test/files/screenshot.png"
	comments := []domain.RequirementComment{
		{ID: parentID, AuthorName: "王震", CreatedAt: "2026-10-10 10:32:21", Content: "保留 **Markdown** 正文"},
		{ID: "comment-2", AuthorName: "李四", CreatedAt: "2026-10-10 10:34:00", ParentID: &parentID,
			Content: "![截图](https://example.test/image.png)", AttachmentURL: &attachment},
	}

	got := requirementCommentsMarkdown(comments)
	for _, want := range []string{"**王震** · 2026-10-10 10:32:21", "保留 **Markdown** 正文", "回复：王震", "![截图](https://example.test/image.png)", "https://example.test/files/screenshot.png"} {
		if !strings.Contains(got, want) {
			t.Errorf("comments source should contain %q: %s", want, got)
		}
	}
	if strings.Contains(got, "###") {
		t.Fatalf("comment metadata must not become a large Markdown heading: %s", got)
	}
}

func TestRequirementCommentStateKeyPrefersStableID(t *testing.T) {
	comment := domain.RequirementComment{ID: "comment-1"}
	if first, second := requirementCommentStateKey(comment, 0), requirementCommentStateKey(comment, 9); first != second {
		t.Fatalf("comment state key should remain stable across reorder: %q != %q", first, second)
	}
	if got := requirementCommentStateKey(domain.RequirementComment{}, 3); got != "index:3" {
		t.Fatalf("empty comment ID should use its position as a fallback key, got %q", got)
	}
}

func TestRequirementMetadataHasValuesOnlyForVisibleData(t *testing.T) {
	if requirementMetadataHasValues(requirementMetadata{}) {
		t.Fatal("empty metadata should not produce a visible summary")
	}
	title := "需求标题"
	if !requirementMetadataHasValues(requirementMetadata{Title: &title}) {
		t.Fatal("a cached title should count as retained metadata")
	}
	if !requirementMetadataHasValues(requirementMetadata{Participants: domain.RequirementParticipants{QCOwners: []domain.RequirementPerson{{Name: "测试"}}}}) {
		t.Fatal("retained participant roles should count as metadata")
	}
}
