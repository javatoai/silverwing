package integrations

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	goldhtml "github.com/yuin/goldmark/renderer/html"
	"golang.org/x/net/html"
	"silverwing-go/internal/commandruntime"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
)

const (
	dragonCommentApprovalURLPrefix = "https://dragon.snowballtech.com/#/workorder/detail/"
	dragonCommentNotice            = "需求已提测，辛苦进入测试环节。"
)

// DragonRequirementCommentUncertainError leaves a possibly submitted comment
// eligible for read-only reconciliation.
type DragonRequirementCommentUncertainError struct {
	Cause error
}

func (e *DragonRequirementCommentUncertainError) Error() string {
	if e.Cause != nil {
		return "青龙提测评论写入结果待核对：" + e.Cause.Error()
	}
	return "青龙提测评论写入结果待核对"
}

func (e *DragonRequirementCommentUncertainError) Unwrap() error { return e.Cause }

// ResolveDragonRequirementCommentScope binds future comment work to the
// requirement's project and the active Meegle account without changing either.
func (s *Service) ResolveDragonRequirementCommentScope(ctx context.Context, requirementURL string) (string, error) {
	config, item, err := s.dragonRequirementCommentTarget(ctx, requirementURL)
	if err != nil {
		return "", err
	}
	_, scope, err := s.dragonRequirementCommentIdentity(ctx, config, item)
	return scope, err
}

// SyncDragonRequirementComment creates at most one comment per invocation.
// A fresh full read detects an existing reference before any create.
func (s *Service) SyncDragonRequirementComment(ctx context.Context, requirementURL, approvalID, approvalURL, expectedScope string) (string, error) {
	return s.dragonRequirementComment(ctx, requirementURL, approvalID, approvalURL, expectedScope, true)
}

// CheckDragonRequirementComment reconciles an earlier attempt and returns an
// empty ID when a full fresh read finds no matching comment.
func (s *Service) CheckDragonRequirementComment(ctx context.Context, requirementURL, approvalID, approvalURL, expectedScope string) (string, error) {
	return s.dragonRequirementComment(ctx, requirementURL, approvalID, approvalURL, expectedScope, false)
}

func (s *Service) dragonRequirementCommentTarget(ctx context.Context, requirementURL string) (domain.AppConfig, domain.ParticipatedWorkItem, error) {
	if ctx == nil {
		return domain.AppConfig{}, domain.ParticipatedWorkItem{}, errors.New("青龙提测评论上下文不能为空")
	}
	config, err := s.config.Load()
	if err != nil {
		return config, domain.ParticipatedWorkItem{}, err
	}
	item, err := s.requirementTarget(ctx, config, map[string]any{"url": requirementURL})
	return config, item, err
}

func (s *Service) dragonRequirementCommentIdentity(ctx context.Context, config domain.AppConfig, item domain.ParticipatedWorkItem) (string, string, error) {
	// The account probe belongs to the Dragon workflow's admission scope.
	identity, loggedOut, err := s.requirementCacheIdentity(ctx, config)
	if err != nil {
		return "", "", err
	}
	if loggedOut {
		return "", "", errors.New("Meegle 当前已退出登录，请登录后同步青龙提测评论")
	}
	if identity == "" {
		return "", "", errors.New("无法确认同步青龙提测评论的 Meegle 账号")
	}
	target := item.ProjectKey + "\x00" + item.ProjectName + "\x00" + item.Type + "\x00" + item.ID
	return identity, requirementCacheDigest(opaqueRequirementIdentity(identity) + "\x00" + target), nil
}

func (s *Service) verifyDragonRequirementCommentIdentity(ctx context.Context, config domain.AppConfig, item domain.ParticipatedWorkItem, expected string) error {
	if err := s.verifyRequirementReadConfig(config); err != nil {
		return err
	}
	current, _, err := s.dragonRequirementCommentIdentity(ctx, config, item)
	if err != nil {
		return err
	}
	if current != expected {
		return &RequirementIdentityChangedError{State: requirementIdentityAuthenticated, Identity: opaqueRequirementIdentity(current)}
	}
	return ctx.Err()
}

func (s *Service) dragonRequirementComment(ctx context.Context, requirementURL, approvalID, approvalURL, expectedScope string, create bool) (_ string, operationErr error) {
	id, err := strconv.ParseInt(approvalID, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != approvalID || approvalURL != dragonCommentApprovalURLPrefix+approvalID {
		return "", errors.New("青龙审批编号与详情链接不一致")
	}
	if expectedScope == "" {
		return "", errors.New("缺少已确认的青龙提测评论目标及 Meegle 账号")
	}
	config, item, err := s.dragonRequirementCommentTarget(ctx, requirementURL)
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
	identity, scope, err := s.dragonRequirementCommentIdentity(ctx, config, item)
	if err != nil {
		return "", err
	}
	if scope != expectedScope {
		return "", errors.New("Meegle 账号或需求项目已变化，请恢复原配置后同步青龙提测评论")
	}
	if err := s.verifyRequirementCommentTarget(ctx, config, item); err != nil {
		return "", err
	}
	comments, err := s.fetchRequirementComments(ctx, config, item)
	if err != nil {
		return "", fmt.Errorf("无法完整读取青龙提测审批对应的需求评论：%w", err)
	}
	existing := findDragonRequirementComment(comments.Comments, approvalID, approvalURL, "", false)
	if existing != "" || !create {
		if err := s.verifyDragonRequirementCommentIdentity(ctx, config, item, identity); err != nil {
			return "", err
		}
		return existing, nil
	}

	content := "提测审批链接：" + approvalURL + "\n" + dragonCommentNotice
	params, cleanup, err := s.writeJSONArgument(map[string]string{"content": content})
	if err != nil {
		return "", err
	}
	defer cleanup()
	args := []string{"comment", "add", "--project-key", item.ProjectKey, "--work-item-id", item.ID, "--action", "create", "--params", "@" + params, "--format", "json"}
	dry, err := s.runMeegle(ctx, config, append(append([]string(nil), args...), "--dry-run"), 30*time.Second, "校验青龙提测评论")
	if err != nil {
		return "", err
	}
	if err := validateCommentDryRun(dry.Stdout, item, content, "create", nil); err != nil {
		return "", fmt.Errorf("青龙提测评论校验失败：%w", err)
	}
	if err := s.verifyDragonRequirementCommentIdentity(ctx, config, item, identity); err != nil {
		return "", err
	}

	write, writeErr := s.runMeegle(ctx, config, args, 30*time.Second, "添加青龙提测评论")
	var blocked *commandruntime.AdmissionError
	if errors.As(writeErr, &blocked) {
		return "", writeErr
	}
	if process.IsCleanupFailure(writeErr) {
		return "", &DragonRequirementCommentUncertainError{Cause: writeErr}
	}
	receipt, decodeErr := decodeJSON(write.Stdout)
	if writeErr == nil {
		switch {
		case decodeErr != nil:
			writeErr = errors.New("Meegle 评论写入响应无法解析")
		default:
			if feedback := commentCreateResponseFailure(receipt); feedback != "" {
				writeErr = errors.New(truncateRunes(s.safeDiagnostic(config, feedback), 500))
			}
		}
	}
	verified, verifyErr := s.fetchRequirementComments(ctx, config, item)
	if verifyErr == nil {
		verifyErr = s.verifyDragonRequirementCommentIdentity(ctx, config, item, identity)
	}
	if verifyErr == nil {
		if id := findDragonRequirementComment(verified.Comments, approvalID, approvalURL, commentReturnedID(receipt), true); id != "" {
			return id, nil
		}
		verifyErr = errors.New("回读未确认同一审批链接和提测正文")
	}
	if writeErr != nil {
		verifyErr = fmt.Errorf("%w（%w）", verifyErr, writeErr)
	}
	return "", &DragonRequirementCommentUncertainError{Cause: verifyErr}
}

var (
	dragonCommentMarkdown = goldmark.New(goldmark.WithExtensions(extension.GFM), goldmark.WithRendererOptions(goldhtml.WithUnsafe()))
	dragonCommentURLToken = regexp.MustCompile(`(?i)https?://[^\s\p{Z}<>"'\\\[\]{}()，。；、！？：（）【】《》“”‘’]+`)
	dragonCommentIDToken  = regexp.MustCompile(`(?i)(?:\bapproval[_ -]+id\b|\bdragon\s+approval(?:\s+id)?\b|(?:青龙\s*)?(?:提测\s*)?审批(?:单)?\s*(?:ID|编号|单号)|(?:青龙\s*(?:提测\s*)?|提测\s*)审批(?:单)?)\s*[:：=]?\s*#?\s*([0-9]+)(?:$|[^a-z0-9_./-])`)
)

func findDragonRequirementComment(comments []domain.RequirementComment, approvalID, approvalURL, receiptID string, requireBody bool) string {
	for _, comment := range comments {
		if receiptID != "" && receiptID != comment.ID || requireBody && comment.ParentID != nil {
			continue
		}
		hasLink, visible := dragonRequirementCommentContent(comment.Content, approvalURL)
		if requireBody {
			if hasLink && strings.Contains(visible, dragonCommentNotice) {
				return comment.ID
			}
			continue
		}
		if hasLink {
			return comment.ID
		}
		for _, ref := range dragonCommentIDToken.FindAllStringSubmatch(visible, -1) {
			if ref[1] == approvalID {
				return comment.ID
			}
		}
	}
	return ""
}

// Render Markdown to a local buffer only. Tokenization also handles any HTML
// link markup in rich comments without fetching links or running the HTML.
func dragonRequirementCommentContent(content, approvalURL string) (bool, string) {
	var rendered bytes.Buffer
	if err := dragonCommentMarkdown.Convert([]byte(content), &rendered); err != nil {
		return false, ""
	}
	tokens := html.NewTokenizer(&rendered)
	var visible strings.Builder
	var hasLink bool
tokensLoop:
	for {
		switch tokens.Next() {
		case html.ErrorToken:
			break tokensLoop
		case html.TextToken:
			visible.Write(tokens.Text())
		case html.StartTagToken, html.SelfClosingTagToken, html.EndTagToken:
			token := tokens.Token()
			switch token.Data {
			case "a":
				for _, attribute := range token.Attr {
					if attribute.Key == "href" && attribute.Val == approvalURL {
						hasLink = true
					}
				}
			case "br", "div", "li", "p", "tr":
				visible.WriteByte('\n')
			}
		}
	}
	text := visible.String()
	if !hasLink {
		for _, candidate := range dragonCommentURLToken.FindAllString(text, -1) {
			if strings.TrimRight(candidate, ".,;!") == approvalURL {
				hasLink = true
				break
			}
		}
	}
	return hasLink, text
}
