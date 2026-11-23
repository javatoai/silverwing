package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"time"
	"unicode/utf8"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/production"
)

const (
	genbuConfigSourceTTL   = 30 * time.Minute
	genbuConfigSourceLimit = 16
)

type genbuConfigAccount interface {
	AccountIdentity(context.Context, domain.AppConfig) (string, error)
}

func genbuConfigCLICheck(client genbuConfigAccount) func(context.Context) error {
	bound, _ := client.(production.NativeGenbu)
	return bound.Require
}

type genbuConfigSourceReader interface {
	genbuConfigAccount
	ListConfigFiles(context.Context, domain.AppConfig, string, string) (production.GenbuConfigFiles, error)
	ReadConfigFile(context.Context, domain.AppConfig, string, string, string) (production.GenbuConfigFile, error)
}

type TaskServiceConfigSourceResult struct {
	production.GenbuConfigFile
	SourceToken string `json:"sourceToken"`
}

type genbuConfigSource struct {
	scope    genbuConfigPreviewScope
	checkCLI func(context.Context) error
	identity string
	target   genbuConfigTarget
	expires  time.Time
}

// Source grants keep only target metadata. A digest alone cannot distinguish
// a prior account's read from a new account viewing the same target.
type genbuConfigSourceStore struct {
	mu      sync.Mutex
	entries map[string]genbuConfigSource
}

func (s *genbuConfigSourceStore) save(ctx context.Context, source genbuConfigSource) (string, error) {
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	token := hex.EncodeToString(nonce[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	if source.checkCLI != nil {
		if err := source.checkCLI(ctx); err != nil {
			return "", err
		}
	}
	if s.entries == nil {
		s.entries = make(map[string]genbuConfigSource)
	}
	now := time.Now()
	var oldestToken string
	var oldest time.Time
	for currentToken, entry := range s.entries {
		if !now.Before(entry.expires) {
			delete(s.entries, currentToken)
			continue
		}
		if oldest.IsZero() || entry.expires.Before(oldest) {
			oldestToken, oldest = currentToken, entry.expires
		}
	}
	if len(s.entries) >= genbuConfigSourceLimit {
		delete(s.entries, oldestToken)
	}
	source.expires = now.Add(genbuConfigSourceTTL)
	s.entries[token] = source
	return token, nil
}

func (s *genbuConfigSourceStore) target(ctx context.Context, token string, scope genbuConfigPreviewScope, identity, file, snapshotID string) (genbuConfigTarget, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	source, ok := s.entries[token]
	if ok && (!time.Now().Before(source.expires) || (source.checkCLI != nil && source.checkCLI(ctx) != nil)) {
		delete(s.entries, token)
		ok = false
	}
	if !ok || source.scope != scope || source.identity != identity || source.target.file != file || source.target.snapshotID != snapshotID {
		return genbuConfigTarget{}, production.ErrGenbuConfigPreviewStale
	}
	return source.target, nil
}

func (a *App) taskServiceConfigReadAction(ctx context.Context, action Action, scope genbuConfigPreviewScope, client genbuConfigSourceReader) (ActionResult, error) {
	isSource := taskServiceOperation(action.Kind) == "config-source"
	contextKey := scope.contextKey()
	for key := range action.Payload {
		if key != contextKey && key != "environment" && (key != "file" || !isSource) {
			return ActionResult{}, errors.New("配置读取请求包含无效参数")
		}
	}
	file := textPayload(action, "file")
	if isSource {
		if scope.Context.Archived {
			return ActionResult{}, errors.New("已归档任务不能读取服务配置原文")
		}
		if file == "" {
			return ActionResult{}, errors.New("请选择配置文件")
		}
	}
	cfg, service, release, err := a.beginTaskServiceQuery(ctx, action, scope)
	if err != nil {
		return ActionResult{}, err
	}
	defer release()
	if !isSource {
		result, err := client.ListConfigFiles(ctx, cfg, scope.Environment, service.GenbuServiceName)
		if err != nil {
			return ActionResult{}, err
		}
		if _, _, err := a.taskServiceIdentity(ctx, action, scope); err != nil {
			return ActionResult{}, err
		}
		if err := ctx.Err(); err != nil {
			return ActionResult{}, err
		}
		return ActionResult{Data: result}, nil
	}
	identity, err := client.AccountIdentity(ctx, cfg)
	if err != nil {
		return ActionResult{}, err
	}
	if identity == "" {
		return ActionResult{}, errors.New("无法确认 Genbu 程序或账号")
	}
	result, err := client.ReadConfigFile(ctx, cfg, scope.Environment, service.GenbuServiceName, file)
	if err != nil {
		return ActionResult{}, err
	}
	if result.Service == "" || result.Environment != scope.Environment || result.Namespace == "" || result.Name == "" ||
		result.UpdateTime == "" || result.File != file || !validGenbuConfigDigest(result.SnapshotID) || !utf8.ValidString(result.Content) {
		return ActionResult{}, errors.New("Genbu 返回的配置原文缺少目标信息或目标不匹配")
	}
	currentIdentity, err := a.currentGenbuConfigIdentity(ctx, action, scope, client)
	if err != nil {
		return ActionResult{}, err
	}
	if currentIdentity != identity {
		return ActionResult{}, errors.New("读取配置原文期间 Genbu 程序或账号已变化，请重新读取")
	}
	target := genbuConfigTarget{
		service: result.Service, environment: result.Environment, namespace: result.Namespace, name: result.Name,
		file: file, updateTime: result.UpdateTime, snapshotID: result.SnapshotID,
	}
	token, err := a.genbuConfigSources.save(ctx, genbuConfigSource{scope: scope, checkCLI: genbuConfigCLICheck(client), identity: identity, target: target})
	if err != nil {
		return ActionResult{}, err
	}
	return ActionResult{Data: TaskServiceConfigSourceResult{GenbuConfigFile: result, SourceToken: token}}, nil
}

func (a *App) currentGenbuConfigIdentity(ctx context.Context, action Action, scope genbuConfigPreviewScope, client genbuConfigAccount) (string, error) {
	cfg, _, err := a.taskServiceIdentity(ctx, action, scope)
	if err != nil {
		return "", err
	}
	identity, err := client.AccountIdentity(ctx, cfg)
	if err != nil {
		return "", err
	}
	return identity, ctx.Err()
}

func validGenbuConfigDigest(digest string) bool {
	if len(digest) != hex.EncodedLen(sha256.Size) {
		return false
	}
	_, err := hex.DecodeString(digest)
	return err == nil
}
