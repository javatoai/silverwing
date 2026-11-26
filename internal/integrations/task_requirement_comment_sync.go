package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"silverwing-go/internal/commandruntime"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
	"silverwing-go/internal/storage"
)

type TaskRequirementCommentScope struct {
	ProjectKey string `json:"projectKey"`
	WorkItemID string `json:"workItemId"`
	Account    string `json:"account"`
	AuthorKey  string `json:"authorKey"`
}

type TaskRequirementCommentWrite struct {
	CommentID    string
	PreviousBody string
	Body         string
}

// TaskRequirementCommentAttempt records the evidence needed to reconcile a
// submitted request without sending it again.
type TaskRequirementCommentAttempt struct {
	CommentID string   `json:"commentId,omitempty"`
	ReceiptID string   `json:"receiptId,omitempty"`
	Body      string   `json:"body"`
	StartedAt string   `json:"startedAt"`
	BeforeIDs []string `json:"beforeIds,omitempty"`
	SendToken string   `json:"sendToken,omitempty"`
}

type TaskRequirementCommentConflictError struct{ Message string }

func (e *TaskRequirementCommentConflictError) Error() string { return e.Message }

type TaskRequirementCommentUncertainError struct{ Cause error }

func (e *TaskRequirementCommentUncertainError) Error() string {
	if e.Cause == nil {
		return "任务评论同步结果待核对"
	}
	return "任务评论同步结果待核对：" + e.Cause.Error()
}

func (e *TaskRequirementCommentUncertainError) Unwrap() error { return e.Cause }

// CachedTaskRequirementCommentAccount reuses a confirmed login when its local
// Meegle configuration still matches. Scheduling a task never runs the CLI.
func (s *Service) CachedTaskRequirementCommentAccount() string {
	if s == nil {
		return ""
	}
	config, err := s.config.Load()
	if err != nil {
		return ""
	}
	localContext := s.requirementIdentityLocalContext(config)
	s.identityStateMu.Lock()
	defer s.identityStateMu.Unlock()
	if !s.identityConfirmed || s.identityState != requirementIdentityAuthenticated || s.identityLocalContext != localContext || !validOpaqueRequirementIdentity(s.confirmedIdentity) {
		return ""
	}
	return s.confirmedIdentity
}

func (s *Service) ResolveTaskRequirementCommentScope(ctx context.Context, requirementURL string) (TaskRequirementCommentScope, error) {
	config, item, err := s.taskRequirementCommentTarget(ctx, requirementURL)
	if err != nil {
		return TaskRequirementCommentScope{}, err
	}
	scope, _, err := s.taskRequirementCommentScope(ctx, config, item)
	return scope, err
}

func (s *Service) taskRequirementCommentTarget(ctx context.Context, requirementURL string) (domain.AppConfig, domain.ParticipatedWorkItem, error) {
	if ctx == nil {
		return domain.AppConfig{}, domain.ParticipatedWorkItem{}, errors.New("缺少任务评论同步上下文")
	}
	if err := ctx.Err(); err != nil {
		return domain.AppConfig{}, domain.ParticipatedWorkItem{}, err
	}
	config, err := s.config.Load()
	if err != nil {
		return config, domain.ParticipatedWorkItem{}, err
	}
	item, err := s.requirementTarget(ctx, config, map[string]any{"url": requirementURL})
	return config, item, err
}

func (s *Service) taskRequirementCommentScope(ctx context.Context, config domain.AppConfig, item domain.ParticipatedWorkItem) (TaskRequirementCommentScope, string, error) {
	raw, loggedOut, err := s.requirementCacheIdentity(ctx, config)
	if err != nil {
		return TaskRequirementCommentScope{}, "", err
	}
	if loggedOut {
		return TaskRequirementCommentScope{}, "", errors.New("Meegle 当前已退出登录，请登录后重试同步")
	}
	var identity []string
	if err := json.Unmarshal([]byte(raw), &identity); err != nil || len(identity) < 5 || identity[4] == "" {
		return TaskRequirementCommentScope{}, "", errors.New("无法确认同步任务评论的 Meegle 账号")
	}
	return TaskRequirementCommentScope{ProjectKey: item.ProjectKey, WorkItemID: item.ID, Account: opaqueRequirementIdentity(raw), AuthorKey: identity[4]}, raw, nil
}

func (s *Service) verifyTaskRequirementCommentScope(ctx context.Context, config domain.AppConfig, item domain.ParticipatedWorkItem, expected TaskRequirementCommentScope) (string, error) {
	if expected.ProjectKey != item.ProjectKey || expected.WorkItemID != item.ID || expected.Account == "" || expected.AuthorKey == "" {
		return "", &TaskRequirementCommentConflictError{Message: "任务关联的需求或账号范围已变化，请核对同步目标"}
	}
	if err := s.verifyRequirementReadConfig(config); err != nil {
		return "", err
	}
	localContext := s.requirementIdentityLocalContext(config)
	scope, raw, err := s.taskRequirementCommentScope(ctx, config, item)
	if err != nil {
		return "", err
	}
	if scope != expected || s.requirementIdentityLocalContext(config) != localContext {
		return "", &TaskRequirementCommentConflictError{Message: "Meegle 账号已变化，请恢复原账号后重试同步"}
	}
	return raw, ctx.Err()
}

// SyncTaskRequirementComment holds the comment lock across preflight, write
// and readback. The caller saves the attempt at command admission, before the
// only mutation, and saves the returned ID when one is available.
func (s *Service) SyncTaskRequirementComment(ctx context.Context, requirementURL string, scope TaskRequirementCommentScope, req TaskRequirementCommentWrite, persist func(TaskRequirementCommentAttempt) error, guard func() error) (_ string, operationErr error) {
	if strings.TrimSpace(req.Body) == "" || persist == nil || guard == nil {
		return "", errors.New("缺少任务评论正文或同步范围")
	}
	if (req.CommentID == "") != (req.PreviousBody == "") {
		return "", &TaskRequirementCommentConflictError{Message: "缺少上次同步的评论编号或正文，请核对原需求"}
	}
	config, item, err := s.taskRequirementCommentTarget(ctx, requirementURL)
	if err != nil {
		return "", err
	}
	release, err := s.acquireRequirementCommentLock(ctx, item)
	if err != nil {
		return "", err
	}
	defer func() {
		finishRequirementCommentLock("requirement-comment:"+requirementCommentLockIdentity(item), release, operationErr)
	}()
	if _, err = s.verifyTaskRequirementCommentScope(ctx, config, item, scope); err != nil {
		return "", err
	}
	if err = s.verifyRequirementCommentTarget(ctx, config, item); err != nil {
		return "", err
	}
	path, cleanup, err := s.writeJSONArgument(map[string]string{"content": req.Body})
	if err != nil {
		return "", err
	}
	defer cleanup()
	action := "create"
	var targetID *string
	if req.CommentID != "" {
		if _, err = commentEditID(map[string]any{"editCommentID": req.CommentID}); err != nil {
			return "", err
		}
		action, targetID = "update", &req.CommentID
	}
	args := []string{"comment", "add", "--project-key", item.ProjectKey, "--work-item-id", item.ID, "--action", action, "--params", "@" + path, "--format", "json"}
	if targetID != nil {
		args = append(args, "--comment-id", *targetID)
	}
	dry, err := s.runMeegle(ctx, config, append(append([]string(nil), args...), "--dry-run"), 30*time.Second, "校验任务同步评论")
	if err != nil {
		return "", err
	}
	if err = validateCommentDryRun(dry.Stdout, item, req.Body, action, targetID); err != nil {
		return "", fmt.Errorf("任务评论校验失败：%w", err)
	}
	before, err := s.fetchRequirementComments(ctx, config, item)
	if err != nil {
		return "", err
	}
	if req.CommentID != "" {
		comment, err := ownedTaskRequirementComment(before.Comments, req.CommentID, scope.AuthorKey)
		if err != nil {
			return "", err
		}
		if taskRequirementCommentText(comment.Content) != taskRequirementCommentText(req.PreviousBody) {
			return "", &TaskRequirementCommentConflictError{Message: "原任务评论已被修改，请核对原需求"}
		}
	}
	localContext := s.requirementIdentityLocalContext(config)
	if _, err = s.verifyTaskRequirementCommentScope(ctx, config, item, scope); err != nil {
		return "", err
	}
	if req.CommentID != "" && req.Body == req.PreviousBody {
		return req.CommentID, nil
	}
	attempt := TaskRequirementCommentAttempt{CommentID: req.CommentID, Body: req.Body}
	if req.CommentID == "" {
		attempt.SendToken = commentRandomID()
		attempt.BeforeIDs = make([]string, 0, len(before.Comments))
		for _, comment := range before.Comments {
			attempt.BeforeIDs = append(attempt.BeforeIDs, comment.ID)
		}
	}
	admitted := false
	writeContext := commandruntime.WithAdmission(ctx, func(ctxForWrite context.Context) error {
		if err := ctxForWrite.Err(); err != nil {
			return err
		}
		if err := commandruntime.CheckAdmission(ctx); err != nil {
			return err
		}
		if err := s.verifyRequirementReadConfig(config); err != nil {
			return err
		}
		if s.requirementIdentityLocalContext(config) != localContext {
			return &TaskRequirementCommentConflictError{Message: "Meegle 配置已变化，请核对原账号后重试同步"}
		}
		if err := guard(); err != nil {
			return err
		}
		attempt.StartedAt = time.Now().UTC().Format(time.RFC3339Nano)
		if err := persist(attempt); err != nil {
			return err
		}
		admitted = true
		if err := guard(); err != nil {
			return err
		}
		if err := s.verifyRequirementReadConfig(config); err != nil {
			return err
		}
		if s.requirementIdentityLocalContext(config) != localContext {
			return &TaskRequirementCommentConflictError{Message: "Meegle 配置已变化，请核对原账号后重试同步"}
		}
		if attempt.SendToken != "" {
			stampPath, err := s.taskRequirementCommentCreateStampPath(item)
			if err != nil {
				return err
			}
			if err := storage.WriteFileAtomic(stampPath, []byte(attempt.SendToken), 0o600); err != nil {
				return fmt.Errorf("记录本次任务评论写入顺序：%w", err)
			}
		}
		return nil
	})
	written, writeErr := s.runMeegle(writeContext, config, args, commentPublishTimeout, "同步任务评论")
	var notSent interface{ RequestNotSent() }
	if !admitted || errors.As(writeErr, &notSent) {
		return "", writeErr
	}
	if process.IsCleanupFailure(writeErr) {
		return "", &TaskRequirementCommentUncertainError{Cause: writeErr}
	}
	response, decodeErr := decodeJSON(written.Stdout)
	if id := commentReturnedID(response); id != "" {
		attempt.ReceiptID = id
		if err := persist(attempt); err != nil {
			return "", &TaskRequirementCommentUncertainError{Cause: fmt.Errorf("未能保存评论写入回执：%w", err)}
		}
	}
	if writeErr == nil {
		if decodeErr != nil {
			writeErr = errors.New("Meegle 评论写入响应无法解析")
		} else if feedback := commentCreateResponseFailure(response); feedback != "" {
			writeErr = errors.New(truncateRunes(s.safeDiagnostic(config, feedback), 500))
		}
	}
	id, checkErr := s.checkTaskRequirementComment(ctx, config, item, scope, attempt)
	if checkErr != nil && writeErr != nil {
		checkErr = fmt.Errorf("%w（%w）", checkErr, writeErr)
	}
	if checkErr == nil && attempt.CommentID == "" && attempt.ReceiptID == "" {
		attempt.ReceiptID = id
		if err := persist(attempt); err != nil {
			return "", &TaskRequirementCommentUncertainError{Cause: fmt.Errorf("未能保存已回读的任务评论编号：%w", err)}
		}
	}
	return id, checkErr
}

// CheckTaskRequirementComment only reads the work item and its comments. It
// never resubmits a request that may have reached the server.
func (s *Service) CheckTaskRequirementComment(ctx context.Context, requirementURL string, scope TaskRequirementCommentScope, attempt TaskRequirementCommentAttempt) (_ string, operationErr error) {
	config, item, err := s.taskRequirementCommentTarget(ctx, requirementURL)
	if err != nil {
		return "", &TaskRequirementCommentUncertainError{Cause: err}
	}
	release, err := s.acquireRequirementCommentLock(ctx, item)
	if err != nil {
		return "", &TaskRequirementCommentUncertainError{Cause: err}
	}
	defer func() {
		finishRequirementCommentLock("requirement-comment:"+requirementCommentLockIdentity(item), release, operationErr)
	}()
	if _, err := s.verifyTaskRequirementCommentScope(ctx, config, item, scope); err != nil {
		return "", &TaskRequirementCommentUncertainError{Cause: err}
	}
	if err := s.verifyRequirementCommentTarget(ctx, config, item); err != nil {
		return "", &TaskRequirementCommentUncertainError{Cause: err}
	}
	return s.checkTaskRequirementComment(ctx, config, item, scope, attempt)
}

func (s *Service) checkTaskRequirementComment(ctx context.Context, config domain.AppConfig, item domain.ParticipatedWorkItem, scope TaskRequirementCommentScope, attempt TaskRequirementCommentAttempt) (string, error) {
	if attempt.CommentID == "" && attempt.ReceiptID == "" {
		// The shared lock serializes writes across profiles. Another create
		// after this attempt could give us the same author and body to match.
		stampPath, err := s.taskRequirementCommentCreateStampPath(item)
		if err != nil {
			return "", &TaskRequirementCommentUncertainError{Cause: err}
		}
		stamp, err := os.ReadFile(stampPath)
		if !commentValidID(attempt.SendToken) || err != nil || string(stamp) != attempt.SendToken {
			return "", &TaskRequirementCommentUncertainError{Cause: errors.New("无法确认本次评论的写入顺序，请在原需求中核对")}
		}
	}
	localContext := s.requirementIdentityLocalContext(config)
	comments, err := s.fetchRequirementComments(ctx, config, item)
	if err != nil {
		return "", &TaskRequirementCommentUncertainError{Cause: err}
	}
	if s.requirementIdentityLocalContext(config) != localContext {
		return "", &TaskRequirementCommentUncertainError{Cause: &TaskRequirementCommentConflictError{Message: "回读期间 Meegle 配置已变化，请使用原账号核对同步评论"}}
	}
	identity, err := s.verifyTaskRequirementCommentScope(ctx, config, item, scope)
	if err != nil {
		return "", &TaskRequirementCommentUncertainError{Cause: err}
	}
	id, err := matchTaskRequirementComment(comments.Comments, scope.AuthorKey, attempt)
	if err != nil {
		return "", &TaskRequirementCommentUncertainError{Cause: err}
	}
	s.cacheVerifiedRequirementComments(ctx, config, item, identity, comments)
	return id, nil
}

func (s *Service) taskRequirementCommentCreateStampPath(item domain.ParticipatedWorkItem) (string, error) {
	lockPath, err := s.requirementCommentLockPath(item)
	return lockPath + ".task-create", err
}

func matchTaskRequirementComment(comments []domain.RequirementComment, authorKey string, attempt TaskRequirementCommentAttempt) (string, error) {
	if authorKey == "" || strings.TrimSpace(attempt.Body) == "" || attempt.StartedAt == "" {
		return "", errors.New("缺少任务评论同步记录")
	}
	if attempt.CommentID != "" {
		if attempt.ReceiptID != "" && attempt.ReceiptID != attempt.CommentID {
			return "", &TaskRequirementCommentConflictError{Message: "更新回执与原任务评论编号不一致，请核对原需求"}
		}
		comment, err := ownedTaskRequirementComment(comments, attempt.CommentID, authorKey)
		if err != nil {
			return "", err
		}
		if taskRequirementCommentText(comment.Content) != taskRequirementCommentText(attempt.Body) {
			return "", errors.New("原任务评论尚未匹配本次修改，请稍后核对")
		}
		return comment.ID, nil
	}
	draft := &domain.RequirementCommentDraft{Attempt: &domain.CommentPublishAttempt{
		CommentID: attempt.ReceiptID, Body: taskRequirementCommentText(attempt.Body), StartedAt: attempt.StartedAt, BeforeIDs: attempt.BeforeIDs, AuthorKey: authorKey,
	}}
	comparable := make([]domain.RequirementComment, len(comments))
	for i, comment := range comments {
		comment.Content = taskRequirementCommentText(comment.Content)
		comparable[i] = comment
	}
	comment, reason := matchPublishedComment(draft, comparable)
	if comment.ID == "" {
		return "", errors.New(reason)
	}
	return comment.ID, nil
}

func ownedTaskRequirementComment(comments []domain.RequirementComment, id, authorKey string) (domain.RequirementComment, error) {
	comment, err := findComment(comments, id)
	if err != nil {
		return comment, &TaskRequirementCommentConflictError{Message: err.Error()}
	}
	if comment.AuthorKey != authorKey || comment.ParentID != nil || comment.AttachmentURL != nil {
		return comment, &TaskRequirementCommentConflictError{Message: "原任务评论的作者、回复关系或附件已变化，请核对原需求"}
	}
	return comment, nil
}

func taskRequirementCommentText(body string) string {
	body = strings.TrimSpace(strings.ReplaceAll(body, "\r\n", "\n"))
	// Feishu may adjust blank lines outside the generated sections and add
	// a text language tag to their fences. The enclosed lines are still exact.
	var sections []string
	for rest := body; rest != ""; {
		if len(sections) > 0 && rest == "-来自Silverwing" {
			sections = append(sections, rest)
			break
		}
		heading, afterHeading, ok := strings.Cut(rest, "\n")
		if !ok || (heading != "**服务与分支**" && heading != "**本地任务资料目录**") {
			return body
		}
		opening, afterOpening, ok := strings.Cut(strings.TrimLeft(afterHeading, "\n"), "\n")
		fence := strings.TrimSuffix(opening, "text")
		if !ok || len(fence) < 3 || strings.Trim(fence, "`") != "" {
			return body
		}
		content, afterClosing, ok := strings.Cut(afterOpening, "\n"+fence+"\n")
		if !ok {
			content, ok = strings.CutSuffix(afterOpening, "\n"+fence)
			if !ok {
				return body
			}
		}
		sections = append(sections, heading+"\n"+fence+"\n"+content+"\n"+fence)
		rest = strings.TrimLeft(afterClosing, "\n")
	}
	return strings.Join(sections, "\n")
}
