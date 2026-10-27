package integrations

import (
	"silverwing-go/internal/domain"
	"testing"
)

func TestCommentUpdateMatchesOnlyOriginalIDAndRelationship(t *testing.T) {
	parent := "parent"
	original := domain.RequirementComment{ID: "fixed", Content: "old", AuthorKey: "other-author", ParentID: &parent, CreatedAt: "1990-01-01"}
	d := domain.RequirementCommentDraft{Content: "new", Edit: &domain.CommentEditTarget{CommentID: "fixed", Original: &original}, Attempt: &domain.CommentPublishAttempt{CommentID: "fixed", Body: "new", AuthorKey: "me", StartedAt: "now"}}
	wanted := original
	wanted.Content = "new"
	other := wanted
	other.ID = "other"
	if c, _ := matchUpdatedComment(&d, []domain.RequirementComment{wanted, other}); c.ID != "fixed" {
		t.Fatal("edit used new-comment author/time rules")
	}
	for _, changes := range []string{"other-id", "old-content", "author", "parent", "extra-attachment", "wrong-response", "duplicate"} {
		t.Run(changes, func(t *testing.T) {
			x := d
			a := *d.Attempt
			x.Attempt = &a
			c := wanted
			rows := []domain.RequirementComment{c, other}
			switch changes {
			case "other-id":
				rows = rows[1:]
			case "old-content":
				rows[0].Content = "old"
			case "author":
				rows[0].AuthorKey = "me"
			case "parent":
				rows[0].ParentID = nil
			case "extra-attachment":
				u := "https://example.com/a"
				rows[0].AttachmentURL = &u
			case "wrong-response":
				x.Attempt.CommentID = "other"
			case "duplicate":
				rows = append(rows, c)
			}
			if found, reason := matchUpdatedComment(&x, rows); found.ID != "" || reason == "" {
				t.Fatalf("unsafe match %s: %s", changes, reason)
			}
		})
	}
}
