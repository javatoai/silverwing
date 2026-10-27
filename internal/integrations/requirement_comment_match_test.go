package integrations

import (
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/domain"
)

func commentMatchFixture() (domain.RequirementCommentDraft, domain.RequirementComment) {
	d := domain.RequirementCommentDraft{Content: "# 你好", Images: []domain.CommentDraftAsset{{Name: "截图.png", Upload: &domain.CommentUpload{Token: "image-token-1", URL: "https://project.feishu.cn/upload/image-token-1"}}}}
	d.Attempt = &domain.CommentPublishAttempt{State: "unknown", AuthorKey: "author-1", StartedAt: "2026-10-27T01:52:57.8768215Z", BeforeIDs: []string{"old"}}
	d.Attempt.Body = d.Markdown(func(a domain.CommentDraftAsset) (string, string) { return a.Upload.URL, a.Upload.Token })
	c := domain.RequirementComment{ID: "new", AuthorKey: "author-1", CreatedAt: "2026-10-27 09:53:00", Content: "# 你好\n\n![](https://project.feishu.cn/goapi/v5/platform/file/stream/download/image-token-1)"}
	return d, c
}

func TestCommentPublishMatchRewrittenImages(t *testing.T) {
	d, c := commentMatchFixture()
	before := d.Attempt.Body
	got, reason := matchPublishedComment(&d, []domain.RequirementComment{c})
	if got.ID != c.ID || reason != "" || d.Attempt.Body != before || !d.Pending() {
		t.Fatalf("rewritten image was not confirmed without mutating the draft: %q %q", got.ID, reason)
	}
}

func TestCommentPublishMatchExistingIdenticalCommentIsNotNew(t *testing.T) {
	d, c := commentMatchFixture()
	d.Attempt.BeforeIDs = append(d.Attempt.BeforeIDs, c.ID)
	d.Attempt.StartedAt = "2026-10-27T02:35:54Z"
	got, reason := matchPublishedComment(&d, []domain.RequirementComment{c})
	if got.ID != "" || !strings.Contains(reason, "发布前") || !d.Pending() {
		t.Fatalf("existing identical comment must explain, not confirm, the new attempt: %s %s", got.ID, reason)
	}
	other := c
	other.ID = "actually-new"
	other.CreatedAt = "2026-10-27 10:35:55"
	if got, reason = matchPublishedComment(&d, []domain.RequirementComment{c, other}); got.ID != other.ID {
		t.Fatal("old duplicate hid the actual new comment", reason)
	}
}

func TestCommentPublishMatchRejectsUnsafeCandidates(t *testing.T) {
	for _, name := range []string{"old", "other-author", "reply", "wrong-id", "missing-time", "early", "late", "invalid-time", "wrong-token", "substring-token", "wrong-host", "wrong-route", "extra-path", "image-removed", "extra-image", "text-changed", "ordinary-comment", "link-changed", "ambiguous", "missing-upload", "unexpected-attachment"} {
		t.Run(name, func(t *testing.T) {
			d, c := commentMatchFixture()
			comments := []domain.RequirementComment{c}
			switch name {
			case "old":
				c.ID = "old"
			case "other-author":
				c.AuthorKey = "author-2"
			case "reply":
				p := "parent"
				c.ParentID = &p
			case "wrong-id":
				d.Attempt.CommentID = "different-id"
			case "missing-time":
				c.CreatedAt = ""
			case "early":
				c.CreatedAt = "2026-10-27 09:52:56"
			case "late":
				c.CreatedAt = "2026-10-27 09:53:43"
			case "invalid-time":
				c.CreatedAt = "not a time"
			case "wrong-token":
				c.Content = strings.ReplaceAll(c.Content, "image-token-1", "image-token-2")
			case "substring-token":
				c.Content = strings.ReplaceAll(c.Content, "image-token-1", "prefix-image-token-1-suffix")
			case "wrong-host":
				c.Content = strings.ReplaceAll(c.Content, "project.feishu.cn", "other.invalid")
			case "wrong-route":
				c.Content = strings.ReplaceAll(c.Content, "/goapi/v5/platform/file/stream/download/", "/arbitrary/")
			case "extra-path":
				c.Content = strings.ReplaceAll(c.Content, "image-token-1)", "image-token-1/extra)")
			case "image-removed":
				c.Content = d.Content
			case "extra-image":
				c.Content += "\n\n![](https://other.invalid/image.png)"
			case "text-changed":
				c.Content = strings.ReplaceAll(c.Content, "你好", "您好")
			case "ordinary-comment":
				d.Attempt.Body += "\n<!-- keep this -->"
			case "link-changed":
				d.Attempt.Body += "\n[文档](https://example.com/a)"
				c.Content += "\n[文档](https://example.com/b)"
			case "ambiguous":
				other := c
				other.ID = "second"
				comments = append(comments, other)
			case "missing-upload":
				d.Images[0].Upload = nil
			case "unexpected-attachment":
				a := "https://project.feishu.cn/files/extra"
				c.AttachmentURL = &a
			}
			comments[0] = c
			if got, reason := matchPublishedComment(&d, comments); got.ID != "" || reason == "" {
				t.Fatalf("unsafe candidate accepted: id=%q reason=%q", got.ID, reason)
			}
		})
	}
}

func TestCommentPublishMatchCodeAndExactSource(t *testing.T) {
	for _, source := range []string{"`![代码](https://example.com/image)`", "```md\n![代码](https://example.com/image)\n```", "    ![代码](https://example.com/image)", "\\![转义](https://example.com/image)", "<!-- 普通注释 -->", "[链接](https://example.com/a)", "- 列表\n    - 子项"} {
		d, c := commentMatchFixture()
		d.Attempt.Body = source + "\n\n" + d.Attempt.Body
		c.Content = source + "\n\n" + c.Content
		if got, reason := matchPublishedComment(&d, []domain.RequirementComment{c}); got.ID == "" {
			t.Fatalf("unchanged source rejected: %q %s", source, reason)
		}
		c.Content = strings.Replace(c.Content, source, source+" changed", 1)
		if got, _ := matchPublishedComment(&d, []domain.RequirementComment{c}); got.ID != "" {
			t.Fatalf("source difference ignored: %q", source)
		}
	}
}

func TestCommentPublishMatchOrderAttachmentsAndIdentityID(t *testing.T) {
	d, c := commentMatchFixture()
	d.Images = append(d.Images, domain.CommentDraftAsset{Upload: &domain.CommentUpload{Token: "second", URL: "https://project.feishu.cn/upload/second"}})
	d.Attempt.Body += "\n\n![二](https://project.feishu.cn/upload/second)<!--second-->"
	c.Content += "\n\n![](https://project.feishu.cn/goapi/v5/platform/file/stream/download/second)"
	if got, reason := matchPublishedComment(&d, []domain.RequirementComment{c}); got.ID == "" {
		t.Fatal(reason)
	}
	c.Content = "# 你好\n\n![](https://project.feishu.cn/goapi/v5/platform/file/stream/download/second)\n\n![](https://project.feishu.cn/goapi/v5/platform/file/stream/download/image-token-1)"
	if got, _ := matchPublishedComment(&d, []domain.RequirementComment{c}); got.ID != "" {
		t.Fatal("image order was ignored")
	}
	d, c = commentMatchFixture()
	d.Attachment = &domain.CommentDraftAsset{Upload: &domain.CommentUpload{Token: "attachment-1", URL: "https://project.feishu.cn/files/attachment-1"}}
	file := d.Attachment.Upload.URL
	c.AttachmentURL = &file
	if got, reason := matchPublishedComment(&d, []domain.RequirementComment{c}); got.ID == "" {
		t.Fatal(reason)
	}
	file += "-suffix"
	if got, _ := matchPublishedComment(&d, []domain.RequirementComment{c}); got.ID != "" {
		t.Fatal("attachment substring accepted")
	}
	d, c = commentMatchFixture()
	d.Images = nil
	d.Attempt.Body = "普通文字\r\n\n`代码`"
	c.Content = "普通文字\n\n`代码`\n"
	if got, reason := matchPublishedComment(&d, []domain.RequirementComment{c}); got.ID == "" {
		t.Fatal(reason)
	}
	d.Attempt.CommentID = c.ID
	c.CreatedAt = ""
	other := c
	other.ID = "unrelated"
	if got, reason := matchPublishedComment(&d, []domain.RequirementComment{other, c}); got.ID != c.ID {
		t.Fatal(reason)
	}
	if got, _ := matchPublishedComment(&d, []domain.RequirementComment{other}); got.ID != "" {
		t.Fatal("ID mismatch fell back to content")
	}
}

func TestCommentPublishWindowSecondPrecision(t *testing.T) {
	d, c := commentMatchFixture()
	for _, created := range []string{"2026-10-27 09:52:57", "2026-10-27T01:52:57Z", "2026-10-27T09:53:42+08:00"} {
		c.CreatedAt = created
		if got, reason := matchPublishedComment(&d, []domain.RequirementComment{c}); got.ID == "" {
			t.Fatalf("%s: %s", created, reason)
		}
	}
	if commentPublishTimeout != 45*time.Second {
		t.Fatal("publish timeout drifted")
	}
}
