package integrations

import (
	"silverwing-go/internal/domain"
	"slices"
)

func clearCommentUploads(d *domain.RequirementCommentDraft) {
	d.Images = append([]domain.CommentDraftAsset{}, d.Images...)
	for i := range d.Images {
		d.Images[i].Upload = nil
	}
	if d.Attachment != nil {
		a := *d.Attachment
		a.Upload = nil
		d.Attachment = &a
	}
}

func matchUpdatedComment(d *domain.RequirementCommentDraft, comments []domain.RequirementComment) (domain.RequirementComment, string) {
	if d == nil || !d.Editing() || d.Edit.Original == nil || d.Attempt == nil {
		return domain.RequirementComment{}, "缺少原评论及修改快照，请保留编辑稿并核对原需求。"
	}
	if d.Attempt.CommentID != "" && d.Attempt.CommentID != d.Edit.CommentID {
		return domain.RequirementComment{}, "更新响应编号与原评论不一致，请核对原需求。"
	}
	c, err := findComment(comments, d.Edit.CommentID)
	if err != nil {
		return domain.RequirementComment{}, err.Error()
	}
	original := d.Edit.Original
	if c.AuthorKey != original.AuthorKey || (c.ParentID == nil) != (original.ParentID == nil) || c.ParentID != nil && *c.ParentID != *original.ParentID {
		return domain.RequirementComment{}, "原评论的作者或回复关系已变化，请核对原需求。"
	}
	images := d.ActiveImages()
	for _, a := range images {
		if a.Upload == nil || a.Upload.Token == "" {
			return domain.RequirementComment{}, "修改图片的上传凭证不完整，请核对原需求。"
		}
	}
	expected, sequence, e1 := comparableCommentBody(d.Attempt.Body, images)
	actual, actualSequence, e2 := comparableCommentBody(c.Content, images)
	if e1 != nil || e2 != nil || expected != actual || !slices.Equal(sequence, actualSequence) {
		return domain.RequirementComment{}, "原评论的正文或图片尚未匹配本次修改，请稍后重新核对。"
	}
	if d.Attachment != nil {
		if d.Attachment.Upload == nil || c.AttachmentURL == nil || !commentUploadURLMatches(*c.AttachmentURL, *d.Attachment.Upload) {
			return domain.RequirementComment{}, "原评论的附件与本次修改不一致，请核对原需求。"
		}
	} else if c.AttachmentURL != nil && *c.AttachmentURL != "" {
		return domain.RequirementComment{}, "原评论的附件尚未移除，请核对原需求。"
	}
	return c, ""
}
