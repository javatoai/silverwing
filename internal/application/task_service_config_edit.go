package application

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
	"unicode/utf8"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/production"
	"silverwing-go/internal/storage"
	"silverwing-go/internal/workspace"
)

const (
	genbuConfigPreviewTTL   = 5 * time.Minute
	genbuConfigPreviewLimit = 16
)

var errGenbuConfigPreviewGone = errors.New("配置预览已过期、已使用或不属于当前任务和服务，请重新预览")

type genbuConfigEditor interface {
	genbuConfigAccount
	PreviewConfigEdit(context.Context, domain.AppConfig, production.GenbuConfigEditRequest) (production.GenbuConfigEditResult, error)
	SubmitConfigEdit(context.Context, domain.AppConfig, production.GenbuConfigEditRequest, string) (production.GenbuConfigEditResult, error)
}

type TaskServiceConfigPreviewResult struct {
	production.GenbuConfigEditResult
	PreviewToken string `json:"previewToken,omitempty"`
	ExpiresAt    string `json:"expiresAt,omitempty"`
}

type genbuConfigPreviewScope struct {
	TaskID, ServiceID, Environment string
	Context                        TaskServicesContext
	Runtime                        ServiceRuntimeContext
}

func (scope genbuConfigPreviewScope) contextKey() string {
	if scope.Runtime.Home != "" {
		return serviceRuntimeContextKey
	}
	return "taskServicesContext"
}

type genbuConfigPreview struct {
	scope     genbuConfigPreviewScope
	checkCLI  func(context.Context) error
	identity  string
	request   production.GenbuConfigEditRequest
	previewID string
	changeID  string
	target    genbuConfigTarget
	expires   time.Time
	timer     *time.Timer
}

type genbuConfigTarget struct {
	service, environment, namespace, name, file, updateTime, snapshotID string
}

func genbuConfigResultTarget(result production.GenbuConfigEditResult, bindSnapshot bool) genbuConfigTarget {
	target := genbuConfigTarget{
		service: result.Service, environment: result.Environment, namespace: result.Namespace,
		name: result.Name, file: result.File, updateTime: result.UpdateTime,
	}
	if bindSnapshot {
		target.snapshotID = result.SnapshotID
	}
	return target
}

// A preview keeps the exact edit in this process until confirmation or expiry.
// The remote preview ID binds that edit to the observed ConfigMap selection.
type genbuConfigPreviewStore struct {
	mu      sync.Mutex
	entries map[string]genbuConfigPreview
}

func (s *genbuConfigPreviewStore) save(ctx context.Context, preview genbuConfigPreview) (string, time.Time, error) {
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", time.Time{}, err
	}
	token := hex.EncodeToString(nonce[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	if preview.checkCLI != nil {
		if err := preview.checkCLI(ctx); err != nil {
			return "", time.Time{}, err
		}
	}
	if s.entries == nil {
		s.entries = make(map[string]genbuConfigPreview)
	}
	var oldestToken string
	var oldest time.Time
	for currentToken, current := range s.entries {
		if current.scope == preview.scope {
			s.removeLocked(currentToken)
			continue
		}
		if oldest.IsZero() || current.expires.Before(oldest) {
			oldestToken, oldest = currentToken, current.expires
		}
	}
	if len(s.entries) >= genbuConfigPreviewLimit {
		s.removeLocked(oldestToken)
	}
	preview.expires = time.Now().Add(genbuConfigPreviewTTL)
	preview.timer = time.AfterFunc(genbuConfigPreviewTTL, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.entries, token)
	})
	s.entries[token] = preview
	return token, preview.expires, nil
}

func (s *genbuConfigPreviewStore) removeLocked(token string) {
	if preview, exists := s.entries[token]; exists {
		preview.timer.Stop()
		delete(s.entries, token)
	}
}

func (s *genbuConfigPreviewStore) discard(token string, scope genbuConfigPreviewScope) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if preview, ok := s.entries[token]; ok && preview.scope == scope {
		s.removeLocked(token)
	}
}

func (s *genbuConfigPreviewStore) take(ctx context.Context, token string, scope genbuConfigPreviewScope, identity string) (genbuConfigPreview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	preview, exists := s.entries[token]
	if !exists || preview.scope != scope {
		return genbuConfigPreview{}, errGenbuConfigPreviewGone
	}
	s.removeLocked(token)
	if preview.identity != identity || !time.Now().Before(preview.expires) ||
		(preview.checkCLI != nil && preview.checkCLI(ctx) != nil) {
		return genbuConfigPreview{}, errGenbuConfigPreviewGone
	}
	return preview, nil
}

func (a *App) taskServiceConfigEditAction(ctx context.Context, action Action, scope genbuConfigPreviewScope, client genbuConfigEditor) (ActionResult, error) {
	operation := taskServiceOperation(action.Kind)
	if err := validateGenbuConfigEditFields(action, operation, scope.contextKey()); err != nil {
		return ActionResult{}, err
	}
	token := textPayload(action, "previewToken")
	if operation == "config-preview-discard" {
		a.genbuConfigPreviews.discard(token, scope)
		return ActionResult{}, nil
	}
	if scope.Context.Archived {
		return ActionResult{}, errors.New("已归档任务不能编辑服务配置")
	}
	if operation == "config-submit" && (!boolPayload(action, "confirmed") || token == "") {
		return ActionResult{}, errors.New("请先确认当前配置预览")
	}
	if operation == "config-submit" {
		defer a.genbuConfigPreviews.discard(token, scope)
	}
	cfg, service, release, err := a.beginTaskServiceQuery(ctx, action, scope)
	if err != nil {
		return ActionResult{}, err
	}
	defer release()
	if operation == "config-submit" {
		return a.submitGenbuConfigEdit(ctx, action, scope, client, token)
	}
	request, err := genbuConfigEditRequest(action, scope.Environment, service.GenbuServiceName)
	if err != nil {
		return ActionResult{}, err
	}
	return a.previewGenbuConfigEdit(ctx, action, scope, client, cfg, request)
}

func validateGenbuConfigEditFields(action Action, operation, contextKey string) error {
	for key, raw := range action.Payload {
		switch key {
		case contextKey, "environment":
			continue
		case "namespace":
			if text, ok := raw.(string); ok && text == "" {
				continue
			}
		case "file", "content", "operations", "snapshotId", "sourceToken":
			if operation == "config-preview" {
				continue
			}
		case "previewToken":
			if operation != "config-preview" {
				continue
			}
		case "confirmed":
			if operation == "config-submit" {
				continue
			}
		}
		return errors.New("配置编辑请求包含无效参数，请重新预览")
	}
	return nil
}

func genbuConfigEditRequest(action Action, env, service string) (production.GenbuConfigEditRequest, error) {
	request := production.GenbuConfigEditRequest{Environment: env, Service: service, File: textPayload(action, "file")}
	if request.File == "" {
		return request, errors.New("请输入准确的配置文件名")
	}
	content, hasContent := action.Payload["content"]
	_, hasOperations := action.Payload["operations"]
	_, hasSnapshot := action.Payload["snapshotId"]
	_, hasSource := action.Payload["sourceToken"]
	if hasSnapshot || hasSource {
		if !hasContent || hasOperations || !validGenbuConfigDigest(textPayload(action, "snapshotId")) || !validGenbuConfigDigest(textPayload(action, "sourceToken")) {
			return request, production.ErrGenbuConfigPreviewStale
		}
		request.StdinReplacement = true
	}
	if hasContent == hasOperations {
		return request, errors.New("请选择整文件替换或属性修改")
	}
	if hasContent {
		text, ok := content.(string)
		if !ok || !utf8.ValidString(text) {
			return request, errors.New("配置文件内容必须为 UTF-8")
		}
		request.Replacement = []byte(text)
		return request, nil
	}
	if err := decodeField(action, "operations", &request.Edits); err != nil || len(request.Edits) == 0 {
		return request, errors.New("请填写至少一项有效的属性操作")
	}
	return request, nil
}

func (a *App) previewGenbuConfigEdit(ctx context.Context, action Action, scope genbuConfigPreviewScope, client genbuConfigEditor, cfg domain.AppConfig, request production.GenbuConfigEditRequest) (ActionResult, error) {
	identity, err := client.AccountIdentity(ctx, cfg)
	if err != nil {
		return ActionResult{}, err
	}
	if identity == "" {
		return ActionResult{}, errors.New("无法确认 Genbu 程序或账号")
	}
	var sourceTarget genbuConfigTarget
	if request.StdinReplacement {
		sourceTarget, err = a.genbuConfigSources.target(ctx, textPayload(action, "sourceToken"), scope, identity, request.File, textPayload(action, "snapshotId"))
		if err != nil {
			return ActionResult{}, err
		}
	}
	result, err := client.PreviewConfigEdit(ctx, cfg, request)
	if err != nil {
		return ActionResult{}, err
	}
	target := genbuConfigResultTarget(result, request.StdinReplacement)
	validStatus := (result.Status == "preview" && result.PreviewID != "" && validGenbuConfigDigest(result.ChangeID)) ||
		(result.Status == "no-change" && result.PreviewID == "")
	if !validStatus || target.service == "" || target.namespace == "" || target.name == "" || target.updateTime == "" || target.file != request.File || target.environment != request.Environment {
		return ActionResult{}, errors.New("Genbu 返回的配置预览缺少目标信息或目标不匹配")
	}
	if request.StdinReplacement && sourceTarget != target {
		return ActionResult{}, production.ErrGenbuConfigPreviewStale
	}
	currentIdentity, err := a.currentGenbuConfigIdentity(ctx, action, scope, client)
	if err != nil {
		return ActionResult{}, err
	}
	if currentIdentity != identity {
		if request.StdinReplacement {
			return ActionResult{}, production.ErrGenbuConfigPreviewStale
		}
		return ActionResult{}, errors.New("配置预览期间 Genbu 程序或账号已变化，请重新预览")
	}
	if result.Status == "no-change" {
		return ActionResult{Data: TaskServiceConfigPreviewResult{GenbuConfigEditResult: result}}, nil
	}
	if err := checkGenbuConfigSubmission(a.genbuConfigSubmissionPath(result.ChangeID)); err != nil {
		return ActionResult{}, err
	}
	token, expires, err := a.genbuConfigPreviews.save(ctx, genbuConfigPreview{
		scope: scope, checkCLI: genbuConfigCLICheck(client), identity: identity, request: request,
		previewID: result.PreviewID, target: target, changeID: result.ChangeID,
	})
	if err != nil {
		return ActionResult{}, err
	}
	return ActionResult{Data: TaskServiceConfigPreviewResult{GenbuConfigEditResult: result, PreviewToken: token, ExpiresAt: expires.UTC().Format(time.RFC3339Nano)}}, nil
}

func (a *App) submitGenbuConfigEdit(ctx context.Context, action Action, scope genbuConfigPreviewScope, client genbuConfigEditor, token string) (ActionResult, error) {
	var result production.GenbuConfigEditResult
	var warning string
	submit := func() error {
		cfg, _, err := a.taskServiceIdentity(ctx, action, scope)
		if err != nil {
			return err
		}
		identity, err := client.AccountIdentity(ctx, cfg)
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		preview, err := a.genbuConfigPreviews.take(ctx, token, scope, identity)
		if err != nil {
			return err
		}
		// Confirm that the saved catalog target still matches after account
		// verification and before reserving this approval submission.
		if scope.Runtime.Home != "" {
			cfg, _, err = a.taskServiceIdentity(ctx, action, scope)
			if err != nil {
				return err
			}
		}
		path := a.genbuConfigSubmissionPath(preview.changeID)
		pending, err := a.recordGenbuConfigSubmission(path)
		if err != nil {
			return err
		}
		// Once recorded, an interruption before a definite result must leave the
		// change blocked, including when this process never receives the receipt.
		result, err = client.SubmitConfigEdit(ctx, cfg, preview.request, preview.previewID)
		if errors.Is(err, production.ErrGenbuConfigSubmitUnknown) {
			return pending
		}
		if err == nil && (result.Status != "submitted" || result.ApprovalID < 0 || result.PreviewID != preview.previewID ||
			result.ChangeID != preview.changeID || genbuConfigResultTarget(result, preview.request.StdinReplacement) != preview.target) {
			result = production.GenbuConfigEditResult{}
			return pending
		}
		if removeErr := storage.RemoveFileIfPresent(path); removeErr != nil {
			warning = "未能清理本地配置审批提交记录：" + removeErr.Error()
			if err != nil {
				return errors.Join(err, errors.New(warning))
			}
		}
		return err
	}
	var err error
	if scope.Runtime.Home != "" {
		err = submit()
	} else {
		// Archive, restore, and task removal share this lock. Revalidate under
		// the lock before consuming the preview and starting the submission.
		err = workspace.NewOperationLocks(a.Paths.Locks).WithTask(scope.Context.TaskPath, submit)
	}
	return ActionResult{Data: result, Message: warning}, err
}
