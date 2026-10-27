package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"strings"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/editor"
)

func commentDraftTarget(d domain.RequirementCommentDraft) string {
	if d.Edit != nil {
		return d.Edit.CommentID
	}
	return ""
}

func commentEditID(p map[string]any) (string, error) {
	id := stringValue(p, "editCommentID")
	if id == "" || id != strings.TrimSpace(id) || len(id) > 256 || strings.ContainsAny(id, "\x00\r\n\t") {
		return "", errors.New("编辑评论编号无效")
	}
	return id, nil
}

func sameCommentSnapshot(a, b domain.RequirementComment) bool {
	// Display names can be enriched independently of the remote record.
	a.AuthorName, b.AuthorName = "", ""
	return reflect.DeepEqual(a, b)
}

func findComment(comments []domain.RequirementComment, id string) (domain.RequirementComment, error) {
	var found domain.RequirementComment
	for _, c := range comments {
		if c.ID == id {
			if found.ID != "" {
				return found, errors.New("评论编号重复，无法确认编辑目标")
			}
			found = c
		}
	}
	if found.ID == "" {
		return found, errors.New("原评论已不存在或无权读取，请打开原需求核对")
	}
	return found, nil
}

func (s *Service) latestEditComment(ctx context.Context, config domain.AppConfig, d domain.RequirementCommentDraft) (domain.RequirementComment, error) {
	comments, err := s.fetchRequirementComments(ctx, config, d.Item)
	if err != nil {
		return domain.RequirementComment{}, err
	}
	return findComment(comments.Comments, commentDraftTarget(d))
}

func (s *Service) resetCommentEdit(ctx context.Context, config domain.AppConfig, d domain.RequirementCommentDraft, remote domain.RequirementComment) (domain.RequirementCommentDraft, error) {
	d.ID, d.Content, d.Attempt = commentRandomID(), remote.Content, nil
	d.Images, d.Attachment = []domain.CommentDraftAsset{}, nil
	d.Edit = &domain.CommentEditTarget{CommentID: remote.ID, Original: &remote}
	stage := func(rawURL, name string, image bool) (domain.CommentDraftAsset, error) {
		u, err := url.Parse(rawURL)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || strings.ContainsAny(rawURL, "\x00\r\n") {
			return domain.CommentDraftAsset{}, errors.New("原评论资源地址无效")
		}
		data, err := s.downloadMeegleAttachmentLimit(ctx, config, d.Item, rawURL, commentAssetLimit)
		if err != nil {
			return domain.CommentDraftAsset{}, redactAttachmentURL(err, rawURL)
		}
		a, err := s.stageCommentAssetBytes(d, domain.CommentDraftAsset{ID: commentRandomID(), Name: name, OriginalURL: rawURL}, data, image)
		return a, err
	}
	for i, rawURL := range editor.FeishuCommentImageURLs(remote.Content) {
		a, err := stage(rawURL, fmt.Sprintf("原图片 %d", i+1), true)
		if err != nil {
			s.removeCommentDraftAssets(d)
			return d, fmt.Errorf("读取原评论图片失败：%w", err)
		}
		d.Images = append(d.Images, a)
	}
	if remote.AttachmentURL != nil && *remote.AttachmentURL != "" {
		a, err := stage(*remote.AttachmentURL, "原附件", false)
		if err != nil {
			s.removeCommentDraftAssets(d)
			return d, fmt.Errorf("读取原评论附件失败：%w", err)
		}
		d.Attachment = &a
	}
	return d, nil
}

func (s *Service) requirementCommentEditDraft(ctx context.Context, config domain.AppConfig, p map[string]any) (any, error) {
	commentID, err := commentEditID(p)
	if err != nil {
		return nil, err
	}
	item, err := s.requirementTarget(ctx, config, p)
	if err != nil {
		return nil, err
	}
	expected, _, err := expectedRequirementIdentity(p)
	if err != nil {
		return nil, err
	}
	// Reuse the confirmed local login just as saving a new comment does.
	session, loggedOut, err := s.requirementIdentityFor(ctx, config, expected, false, s.requirementCacheIdentity)
	if err != nil {
		return nil, err
	}
	if loggedOut || session.token == "" {
		return nil, errors.New("请先登录 Meegle 账号")
	}
	release, err := s.acquireRequirementCommentLock(ctx, item)
	if err != nil {
		return nil, err
	}
	defer release()
	d, err := s.readCommentTargetDraft(session.token, item, commentID)
	if err != nil {
		return nil, err
	}
	original := d
	scope := s.commentScope(ctx, config)
	op := stringValue(p, "operation")
	if op == "" {
		op = "load"
	}
	if op != "load" {
		if err := commentExpectedDraft(d, p); err != nil {
			return nil, err
		}
		if d.Scope != scope || stringValue(p, "scope") != scope {
			return nil, errors.New("账号或配置已变化，请重新打开编辑稿")
		}
		if d.Pending() {
			return nil, errors.New("修改结果待核对，请先核对原评论；不会自动重新保存")
		}
	}
	switch op {
	case "load", "reload":
		if op == "reload" && !boolValue(p, "confirmed") {
			return nil, errors.New("重新载入原评论需要确认放弃本地修改")
		}
		remote, err := s.latestEditComment(ctx, config, d)
		if err != nil {
			return nil, err
		}
		if d.Edit.Original == nil || d.Attempt != nil && d.Attempt.State == "published" || op == "reload" {
			d, err = s.resetCommentEdit(ctx, config, d, remote)
			if err != nil {
				return nil, err
			}
		} else if !d.Pending() {
			edit := *d.Edit
			edit.Conflict = !sameCommentSnapshot(*edit.Original, remote)
			d.Edit = &edit
		}
		var identity []string
		_ = json.Unmarshal([]byte(session.raw), &identity)
		if len(identity) < 5 {
			return nil, errors.New("当前 Meegle 账号信息不完整")
		}
		d.Scope, d.Item, d.Account = scope, item, identity[4]
	case "save":
		body, _ := p["content"].(string)
		if len(body) > 2<<20 {
			return nil, errors.New("评论文字不能超过 2 MB")
		}
		d.Content, d.Attempt = body, nil
	case "add-image", "add-attachment", "paste":
		if op == "add-attachment" && d.Attachment != nil {
			return nil, errors.New("每条评论只能添加一个附件，请先移除原附件")
		}
		if op != "add-attachment" && len(d.Images) >= 20 {
			return nil, errors.New("每条评论最多添加 20 张图片")
		}
		png, _ := p["imagePNG"].([]byte)
		if op == "paste" && len(png) == 0 {
			return nil, errors.New("剪贴板中没有截图")
		}
		a, err := s.stageCommentAsset(d, stringValue(p, "sourcePath"), "", png, op != "add-attachment")
		if err != nil {
			return nil, err
		}
		if op == "add-attachment" {
			d.Attachment = &a
		} else {
			d.Images = append(append([]domain.CommentDraftAsset{}, d.Images...), a)
		}
		d.Attempt = nil
	case "remove":
		id := stringValue(p, "assetID")
		images := []domain.CommentDraftAsset{}
		for _, a := range d.Images {
			if a.ID == id {
				if a.OriginalURL != "" {
					d.Content = editor.RewriteCommentImages(d.Content, map[string]*string{a.OriginalURL: nil})
				}
			} else {
				images = append(images, a)
			}
		}
		d.Images = images
		if d.Attachment != nil && d.Attachment.ID == id {
			d.Attachment = nil
		}
		d.Attempt = nil
	case "discard":
		if !boolValue(p, "confirmed") {
			return nil, errors.New("放弃修改需要确认")
		}
		d.ID, d.Content, d.Attempt = commentRandomID(), "", nil
		d.Images, d.Attachment = []domain.CommentDraftAsset{}, nil
		d.Edit = &domain.CommentEditTarget{CommentID: commentID}
	default:
		return nil, errors.New("未知评论编辑稿操作")
	}
	// A slow read/download cannot persist a new target's state or discard the
	// original draft after configuration/session replacement.
	current, err := s.config.Load()
	if err != nil {
		return nil, err
	}
	if scope != s.commentScope(ctx, current) {
		return nil, errors.New("账号或配置已变化，编辑稿未覆盖")
	}
	if _, loggedOut, err = s.requirementIdentityFor(ctx, current, session.token, false, s.requirementCacheIdentity); err != nil || loggedOut {
		if err != nil {
			return nil, err
		}
		return nil, errors.New("Meegle 已退出登录，编辑稿未覆盖")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err = s.saveCommentDraft(&d); err != nil {
		return nil, err
	}
	keep := map[string]bool{}
	for _, a := range d.Images {
		keep[a.ID] = true
	}
	if d.Attachment != nil {
		keep[d.Attachment.ID] = true
	}
	obsolete := original
	obsolete.Images = nil
	obsolete.Attachment = nil
	for _, a := range original.Images {
		if !keep[a.ID] {
			obsolete.Images = append(obsolete.Images, a)
		}
	}
	if original.Attachment != nil && !keep[original.Attachment.ID] {
		obsolete.Attachment = original.Attachment
	}
	s.removeCommentDraftAssets(obsolete)
	return s.commentDraftView(d), nil
}
