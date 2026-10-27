package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
)

const (
	commentRequestFeedbackPrefix = "发布请求反馈："
	commentCheckFeedbackPrefix   = "\n核对结果："
)

// Independent from the legacy materials-comment upsert. This path always
// creates one top-level comment, with no synchronization marker.
func (s *Service) PublishRequirementComment(ctx context.Context, p map[string]any, progress func(domain.CommentPublishProgress)) (out any, operationErr error) {
	if stringValue(p, "editCommentID") != "" {
		return nil, errors.New("新增评论不能绑定编辑目标")
	}
	return s.writeUserRequirementComment(ctx, p, progress, false)
}

func (s *Service) UpdateRequirementComment(ctx context.Context, p map[string]any, progress func(domain.CommentPublishProgress)) (any, error) {
	if _, err := commentEditID(p); err != nil {
		return nil, err
	}
	return s.writeUserRequirementComment(ctx, p, progress, true)
}

func (s *Service) writeUserRequirementComment(ctx context.Context, p map[string]any, progress func(domain.CommentPublishProgress), editing bool) (out any, operationErr error) {
	if ctx == nil {
		return nil, errors.New("评论发布上下文不能为空")
	}
	config, err := s.config.Load()
	if err != nil {
		return nil, err
	}
	item, err := s.requirementTarget(ctx, config, p)
	if err != nil {
		return nil, err
	}
	expected, _, err := expectedRequirementIdentity(p)
	if err != nil || expected == "" {
		return nil, errors.New("请先打开评论草稿以确认账号")
	}
	release, err := s.acquireRequirementCommentLock(ctx, item)
	if err != nil {
		return nil, err
	}
	var cleanupErr error
	defer func() {
		if cleanupErr == nil {
			cleanupErr = operationErr
		}
		finishRequirementCommentLock("requirement-comment:"+requirementCommentLockIdentity(item), release, cleanupErr)
	}()
	commentID := ""
	if editing {
		commentID = stringValue(p, "editCommentID")
	}
	d, err := s.readCommentTargetDraft(expected, item, commentID)
	if err != nil {
		return nil, err
	}
	requestID := stringValue(p, "requestID")
	if !commentValidID(requestID) {
		return nil, errors.New("评论发布请求标识无效")
	}
	result := func(status, message string) (any, error) {
		id := ""
		if d.Attempt != nil {
			id = d.Attempt.CommentID
		}
		return domain.CommentPublishResult{Status: status, Message: message, Draft: s.commentDraftView(d), CommentID: id}, nil
	}
	if d.Attempt != nil && d.Attempt.ID == requestID && d.Attempt.State == "published" {
		if editing {
			return result("updated", "评论已修改")
		}
		return result("published", "评论已发布")
	}
	if err = commentExpectedDraft(d, p); err != nil {
		return nil, err
	}
	defer func() {
		if operationErr != nil && !d.Pending() {
			cleanupErr = operationErr
			out = domain.CommentPublishResult{Status: "failed", Message: operationErr.Error(), Draft: s.commentDraftView(d)}
			operationErr = nil
		}
	}()
	if d.Pending() {
		if editing {
			return result("unknown", "修改结果待核对，请勿重复保存")
		}
		return result("unknown", "发布结果待核对，请勿重复发布")
	}
	if !d.HasContent() {
		return nil, errors.New("请输入评论、添加图片或附件")
	}
	if editing && !d.HasChanges() {
		return nil, errors.New("评论内容未变化，无需保存")
	}
	if editing && (d.Edit.Original == nil || d.Edit.Conflict) {
		return nil, errors.New("请先重新载入原评论，再保存修改；本地修改已保留")
	}
	if progress != nil {
		progress(domain.CommentPublishProgress{Stage: "核对目标"})
	}
	// Opening the draft already confirmed this login. Reuse that session for
	// the publication; a new service without one probes once on first use.
	session, loggedOut, err := s.requirementIdentityFor(ctx, config, expected, false, s.requirementCacheIdentity)
	if err != nil {
		return nil, err
	}
	if loggedOut {
		return nil, errors.New("Meegle 当前已退出登录")
	}
	scope := s.commentScope(ctx, config)
	if d.Scope != scope || stringValue(p, "scope") != scope {
		return nil, errors.New("评论配置已变化，请重新打开草稿")
	}
	// Check local configuration and the app's current login session before each
	// mutation, without querying profile/auth/user again at every checkpoint.
	guard := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		current, err := s.config.Load()
		if err != nil {
			return err
		}
		if s.commentScope(ctx, current) != scope {
			return errors.New("账号、目录或配置已变化，已停止后续发布")
		}
		active, loggedOut, err := s.requirementIdentityFor(ctx, current, expected, false, s.requirementCacheIdentity)
		if err != nil {
			return err
		}
		if loggedOut || active.raw != session.raw {
			return errors.New("Meegle 账号已变化，已停止后续发布")
		}
		return nil
	}
	if err = s.verifyRequirementCommentTarget(ctx, config, item); err != nil {
		return nil, err
	}
	conflict := func() error {
		edit := *d.Edit
		edit.Conflict = true
		d.Edit = &edit
		if e := guard(); e != nil {
			return e
		}
		if e := s.saveCommentDraft(&d); e != nil {
			return e
		}
		return errors.New("原评论已在远端修改，已停止保存；本地修改已保留，可重新载入原评论")
	}
	checkOriginal := func() error {
		if !editing {
			return nil
		}
		if d.Edit.Original == nil {
			return errors.New("缺少原评论快照，请重新打开编辑")
		}
		remote, err := s.latestEditComment(ctx, config, d)
		if err != nil {
			return err
		}
		if !sameCommentSnapshot(*d.Edit.Original, remote) {
			return conflict()
		}
		return guard()
	}
	// Text-only edits need one pre-write snapshot read. Resource edits also
	// check before uploading, so an existing conflict uploads nothing.
	if editing && (len(d.ActiveImages()) > 0 || d.Attachment != nil) {
		if err = checkOriginal(); err != nil {
			return nil, err
		}
	}
	assets := make([]*domain.CommentDraftAsset, 0, len(d.Images)+1)
	activeImages := map[string]bool{}
	for _, a := range d.ActiveImages() {
		activeImages[a.ID] = true
	}
	for i := range d.Images {
		if activeImages[d.Images[i].ID] {
			assets = append(assets, &d.Images[i])
		}
	}
	imageCount := len(assets)
	if d.Attachment != nil {
		assets = append(assets, d.Attachment)
	}
	for i, a := range assets {
		path, e := s.commentAssetPath(d, *a)
		if e != nil {
			return nil, e
		}
		if a.Upload != nil && a.Upload.Scope == scope {
			continue
		}
		resource, stage := "14", "上传图片"
		if i >= imageCount {
			resource, stage = "13", "上传附件"
		}
		if progress != nil {
			progress(domain.CommentPublishProgress{Stage: stage, Completed: i, Total: len(assets)})
		}
		if e = guard(); e != nil {
			return nil, e
		}
		upload, e := s.runMeegle(ctx, config, []string{"attachment", "+upload", path, "--project-key", item.ProjectKey, "--work-item-id", item.ID, "--resource-type", resource, "--filename", a.Name, "--content-type", a.MIME, "--format", "json"}, 2*time.Minute, stage)
		if e != nil {
			return nil, e
		}
		receipt, e := parseCommentUpload(upload.Stdout, resource == "14")
		if e != nil {
			return nil, e
		}
		if _, e = s.commentAssetPath(d, *a); e != nil {
			return nil, e
		}
		receipt.Scope = scope
		a.Upload = &receipt
		if e = s.saveCommentDraft(&d); e != nil {
			return nil, e
		}
		if progress != nil {
			progress(domain.CommentPublishProgress{Stage: stage, Completed: i + 1, Total: len(assets)})
		}
	}
	body := d.Markdown(func(a domain.CommentDraftAsset) (string, string) { return a.Upload.URL, a.Upload.Token })
	params := map[string]string{"content": body}
	// An explicit empty attachment clears it on update. Never omit this field
	// and accidentally preserve an attachment the user removed.
	if editing {
		params["file_token"] = ""
	}
	if d.Attachment != nil {
		params["file_token"] = d.Attachment.Upload.Token
	}
	path, remove, err := s.writeJSONArgument(params)
	if err != nil {
		return nil, err
	}
	defer remove()
	action, operation := "create", "发布需求评论"
	var targetID *string
	if editing {
		action, operation, targetID = "update", "修改需求评论", &commentID
	}
	args := []string{"comment", "add", "--project-key", item.ProjectKey, "--work-item-id", item.ID, "--action", action, "--params", "@" + path, "--format", "json"}
	if editing {
		args = append(args, "--comment-id", commentID)
	}
	if err = guard(); err != nil {
		return nil, err
	}
	dry, err := s.runMeegle(ctx, config, append(append([]string{}, args...), "--dry-run"), 30*time.Second, "核对评论内容")
	if err != nil {
		return nil, err
	}
	if err = validateCommentDryRun(dry.Stdout, item, body, action, targetID); err != nil {
		return nil, err
	}
	value, _ := decodeJSON(dry.Stdout)
	fileToken, hasFileToken := object(object(value)["params"])["file_token"]
	if text(fileToken) != params["file_token"] || editing && !hasFileToken {
		return nil, errors.New("评论附件校验失败")
	}
	before, err := s.fetchRequirementComments(ctx, config, item)
	if err != nil {
		return nil, err
	}
	if err = guard(); err != nil {
		return nil, err
	}
	if err = s.verifyRequirementCommentTarget(ctx, config, item); err != nil {
		return nil, err
	}
	if editing {
		// Use the last read immediately before the durable intent, including
		// attachment, author, reply relationship and server update timestamp.
		remote, e := findComment(before.Comments, commentID)
		if e != nil {
			return nil, e
		}
		if !sameCommentSnapshot(*d.Edit.Original, remote) {
			return nil, conflict()
		}
	}
	var identity []string
	_ = json.Unmarshal([]byte(session.raw), &identity)
	if len(identity) < 5 {
		return nil, errors.New("账号身份信息不完整")
	}
	d.Attempt = &domain.CommentPublishAttempt{ID: requestID, State: "publishing", Body: body, StartedAt: time.Now().UTC().Format(time.RFC3339Nano), AuthorKey: identity[4]}
	if editing {
		d.Attempt.CommentID = commentID
	}
	for _, c := range before.Comments {
		d.Attempt.BeforeIDs = append(d.Attempt.BeforeIDs, c.ID)
	}
	// Durable intent BEFORE the sole create; failed storage means no create.
	if err = s.saveCommentDraft(&d); err != nil {
		d.Attempt = nil // No request was issued; preserve an editable snapshot.
		return nil, err
	}
	if err = guard(); err != nil {
		d.Attempt = nil
		_ = s.saveCommentDraft(&d)
		return nil, err
	}
	if progress != nil {
		stage := "发布评论"
		if editing {
			stage = "保存修改"
		}
		progress(domain.CommentPublishProgress{Stage: stage})
	}
	if err = guard(); err != nil {
		d.Attempt = nil
		_ = s.saveCommentDraft(&d)
		return nil, err
	}
	write, writeErr := s.runMeegle(ctx, config, args, commentPublishTimeout, operation)
	if process.IsCleanupFailure(writeErr) {
		cleanupErr = writeErr
	}
	d.Attempt.State = "unknown"
	if writeErr != nil {
		d.Attempt.Error = commentRequestFeedbackPrefix + writeErr.Error()
	}
	if v, e := decodeJSON(write.Stdout); e == nil {
		if returned := commentReturnedID(v); returned != "" {
			d.Attempt.CommentID = returned
		}
		if editing && commentCreateResponseFailure(v) != "" {
			// An explicit server rejection is editable; a transport failure with
			// no conclusive response remains read-only and must be reconciled.
			failed := d
			attempt := *d.Attempt
			failed.Attempt = &attempt
			failed.Attempt.State = "failed"
			failed.Attempt.Error = "保存修改失败：" + truncateRunes(s.safeDiagnostic(config, commentCreateResponseFailure(v)), 500)
			clearCommentUploads(&failed)
			if e := s.saveCommentDraft(&failed); e != nil {
				return result("unknown", "保存失败状态无法写入，请先核对原评论。"+e.Error())
			}
			d = failed
			return result("failed", d.Attempt.Error)
		}
		if feedback := commentCreateResponseFailure(v); writeErr == nil && feedback != "" {
			d.Attempt.Error = commentRequestFeedbackPrefix + truncateRunes(s.safeDiagnostic(config, feedback), 500)
		}
	} else if writeErr == nil {
		d.Attempt.Error = commentRequestFeedbackPrefix + "响应无法解析，无法确认远端是否已受理。"
	}
	if err = s.saveCommentDraft(&d); err != nil {
		return result("unknown", "请求已执行，但发布状态保存失败，请核对发布结果。"+err.Error())
	}
	if cleanupErr != nil {
		return result("unknown", "发布进程状态待核对，重启后核对发布结果")
	}
	if err = guard(); err != nil {
		return result("unknown", "账号或配置已变化，请回到原账号核对发布结果")
	}
	return s.reconcileCommentPublishWithIdentity(ctx, config, &d, false)
}

func parseCommentUpload(raw string, image bool) (domain.CommentUpload, error) {
	v, err := decodeJSON(raw)
	if err != nil {
		return domain.CommentUpload{}, errors.New("上传响应格式无效")
	}
	var receipt domain.CommentUpload
	var visit func(any)
	visit = func(value any) {
		if m := object(value); m != nil {
			for k, v := range m {
				switch k {
				case "file_token":
					if token := text(v); token != "" {
						receipt.Token = token
					}
				case "file_url":
					if u := text(v); u != "" {
						receipt.URL = u
					}
				case "data", "file", "file_info", "file_metadata", "metadata":
					visit(v)
				}
			}
		}
	}
	visit(v)
	if receipt.Token == "" || strings.ContainsAny(receipt.Token, "<>\r\n") {
		return receipt, errors.New("上传响应缺少有效文件凭证")
	}
	if image {
		u, e := url.Parse(receipt.URL)
		if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || strings.ContainsAny(receipt.URL, "()<>\r\n") {
			return receipt, errors.New("图片上传响应缺少有效 file_url，未发布评论")
		}
	}
	return receipt, nil
}

func commentReturnedID(value any) string {
	m := object(value)
	if id := text(m["comment_id"]); id != "" {
		return id
	}
	if data := m["data"]; data != nil {
		return commentReturnedID(data)
	}
	return ""
}

// CLI exit success does not make an error envelope a successful publication.
// Preserve the feedback, but still reconcile read-only: even a failed response
// can follow a server-side write, so it must never enable an automatic retry.
func commentCreateResponseFailure(value any) string {
	root := object(value)
	if root == nil {
		return "响应格式无效，无法确认远端是否已受理。"
	}
	if meegleResponseFailed(root) {
		for _, candidate := range []any{at(root, "error", "message"), root["message"], root["msg"], root["error"]} {
			if message := text(candidate); message != "" {
				return message
			}
		}
		return "Meegle 返回失败状态，发布结果仍需核对。"
	}
	if data := object(root["data"]); data != nil {
		return commentCreateResponseFailure(data)
	}
	return ""
}

func (s *Service) reconcileCommentPublish(ctx context.Context, config domain.AppConfig, d *domain.RequirementCommentDraft) (any, error) {
	// An explicit recovery/check can run long after the original publication.
	// Preserve its fresh identity verification; the immediate publish readback
	// uses the session already confirmed for that operation instead.
	return s.reconcileCommentPublishWithIdentity(ctx, config, d, true)
}

func (s *Service) reconcileCommentPublishWithIdentity(ctx context.Context, config domain.AppConfig, d *domain.RequirementCommentDraft, refreshIdentity bool) (any, error) {
	if err := ctx.Err(); err != nil {
		return domain.CommentPublishResult{Status: "unknown", Message: "操作已停止，请回到原目标核对结果。", Draft: s.commentDraftView(*d)}, nil
	}
	comments, err := s.fetchRequirementComments(ctx, config, d.Item)
	if ctx.Err() != nil {
		return domain.CommentPublishResult{Status: "unknown", Message: "操作已停止，请回到原目标核对结果。", Draft: s.commentDraftView(*d)}, nil
	}
	reason := ""
	if err == nil {
		// Reading a potentially slow list must not confirm or clear a draft after
		// its account/configuration has been replaced in the meantime.
		current, scopeErr := s.config.Load()
		if scopeErr == nil && s.commentScope(ctx, current) != d.Scope {
			scopeErr = errors.New("账号、目录或配置已变化，请回到原目标核对发布结果")
		}
		if scopeErr == nil {
			_, loggedOut, identityErr := s.requirementIdentityFor(ctx, current, d.Identity, refreshIdentity, s.requirementCacheIdentity)
			scopeErr = identityErr
			if scopeErr == nil && loggedOut {
				scopeErr = errors.New("Meegle 当前已退出登录，请回到原账号核对发布结果")
			}
		}
		if scopeErr != nil {
			return domain.CommentPublishResult{Status: "unknown", Message: scopeErr.Error(), Draft: s.commentDraftView(*d)}, nil
		}
		var matched domain.RequirementComment
		if d.Editing() {
			matched, reason = matchUpdatedComment(d, comments.Comments)
		} else {
			matched, reason = matchPublishedComment(d, comments.Comments)
		}
		if matched.ID != "" {
			completed := *d
			attempt := *d.Attempt
			completed.Attempt = &attempt
			completed.Attempt.CommentID = matched.ID
			completed.Attempt.State = "published"
			completed.Attempt.Error = ""
			completed.Content = ""
			completed.Images = []domain.CommentDraftAsset{}
			completed.Attachment = nil
			if err = s.saveCommentDraft(&completed); err == nil {
				// Replace the read cache under a fresh generation so a read
				// started before publishing cannot restore the previous list.
				if identity, loggedOut, e := s.requirementIdentityFor(ctx, config, d.Identity, false, s.requirementCacheIdentity); e == nil && !loggedOut {
					key := requirementCacheKey("comments", identity.raw, d.Item)
					if generation, e := s.beginRequirementRead(key, true); e == nil {
						_ = s.commitRequirementRead(ctx, config, key, generation, func() error {
							_, e := s.writeRequirementCommentsCache(key, comments, requirementUnixMillis())
							return e
						})
						s.finishRequirementRead(key, generation)
					}
				}
				s.removeCommentDraftAssets(*d)
				*d = completed
				status, message := "published", "评论已发布"
				if d.Editing() {
					status, message = "updated", "评论已修改"
				}
				return domain.CommentPublishResult{Status: status, Message: message, Draft: *d, CommentID: matched.ID}, nil
			}
			reason = "评论已匹配，但本地成功状态保存失败：" + err.Error()
		}
	} else {
		reason = "回读评论失败：" + err.Error()
	}
	message := "发布结果待核对；未自动重新发布。"
	if d.Editing() {
		message = "修改结果待核对；未自动重新保存。"
	}
	cause := d.Attempt.Error
	if reason != "" {
		if strings.HasPrefix(cause, commentRequestFeedbackPrefix) {
			// Keep the original create feedback without accumulating repeated
			// check errors in this existing journal field.
			cause = strings.SplitN(cause, commentCheckFeedbackPrefix, 2)[0] + commentCheckFeedbackPrefix + reason
		} else {
			cause = reason
		}
	}
	if cause != "" {
		message += " " + cause
	}
	d.Attempt.State = "unknown"
	d.Attempt.Error = cause
	if e := s.saveCommentDraft(d); e != nil {
		message += e.Error()
	}
	return domain.CommentPublishResult{Status: "unknown", Message: message, Draft: s.commentDraftView(*d)}, nil
}

func (s *Service) checkRequirementCommentPublish(ctx context.Context, config domain.AppConfig, p map[string]any) (any, error) {
	item, err := s.requirementTarget(ctx, config, p)
	if err != nil {
		return nil, err
	}
	expected, _, err := expectedRequirementIdentity(p)
	if err != nil {
		return nil, err
	}
	session, loggedOut, err := s.requirementIdentityFor(ctx, config, expected, true, s.requirementCacheIdentity)
	if err != nil {
		return nil, err
	}
	if loggedOut {
		return nil, errors.New("请先登录原 Meegle 账号")
	}
	release, err := s.acquireRequirementCommentLock(ctx, item)
	if err != nil {
		return nil, err
	}
	defer release()
	commentID := stringValue(p, "editCommentID")
	if commentID != "" {
		if _, err := commentEditID(p); err != nil {
			return nil, err
		}
	}
	d, err := s.readCommentTargetDraft(session.token, item, commentID)
	if err != nil {
		return nil, err
	}
	if err = commentExpectedDraft(d, p); err != nil {
		return nil, err
	}
	if d.Scope != s.commentScope(ctx, config) {
		return nil, fmt.Errorf("配置已变化，请重新打开原需求草稿核对")
	}
	if !d.Pending() {
		return nil, errors.New("没有待核对的发布请求")
	}
	return s.reconcileCommentPublish(ctx, config, &d)
}
