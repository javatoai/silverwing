package production

import (
	"context"
	"errors"
	"sort"
	"strings"

	"silverwing-go/internal/commandruntime"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/networkproxy"
	"silverwing-go/internal/snowball/genbuapi"
)

type Genbu interface {
	Versions(context.Context, domain.AppConfig, string) ([]string, error)
	Status(context.Context, domain.AppConfig, string, string) (domain.GenbuTagProbeStatus, error)
}

// NativeGenbu adapts the embedded Genbu client to Silverwing's domain types.
// Client and Identity resolve the account and current network policy for a call.
type NativeGenbu struct {
	Client   func(context.Context, domain.AppConfig) (*genbuapi.Client, error)
	Identity func(context.Context, domain.AppConfig) (string, error)
	Require  func(context.Context) error
}

func (g NativeGenbu) operationContext(ctx context.Context) context.Context {
	if g.Require == nil {
		return ctx
	}
	return commandruntime.WithAdmission(ctx, func(requestCtx context.Context) error {
		if err := commandruntime.CheckAdmission(ctx); err != nil {
			return err
		}
		return g.CheckScope(requestCtx)
	})
}

func (g NativeGenbu) client(ctx context.Context, cfg domain.AppConfig) (context.Context, *genbuapi.Client, error) {
	if err := g.CheckScope(ctx); err != nil {
		return ctx, nil, err
	}
	if g.Client == nil {
		return ctx, nil, errors.New("Genbu 客户端尚未配置")
	}
	ctx = g.operationContext(ctx)
	client, err := g.Client(ctx, cfg)
	if err == nil && client == nil {
		return ctx, nil, errors.New("Genbu 客户端尚未配置")
	}
	return ctx, client, err
}

// AccountIdentity binds a configuration preview to its shared account.
// The provider keeps the identity stable across platform token refreshes.
func (g NativeGenbu) AccountIdentity(ctx context.Context, cfg domain.AppConfig) (string, error) {
	if err := g.CheckScope(ctx); err != nil {
		return "", err
	}
	if g.Identity == nil {
		return "", errors.New("无法确认 Genbu 账号")
	}
	ctx = g.operationContext(ctx)
	identity, err := g.Identity(ctx, cfg)
	if err != nil {
		return "", err
	}
	if err := checkGenbuReadScope(ctx); err != nil {
		return "", err
	}
	return identity, nil
}

// CheckScope revalidates the operation scope before using an observed result.
func (g NativeGenbu) CheckScope(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if g.Require != nil {
		return g.Require(ctx)
	}
	return nil
}

func checkGenbuReadScope(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return commandruntime.CheckAdmission(ctx)
}

func unknownStatus() domain.GenbuTagProbeStatus { return normalizeStatus(domain.GenbuTagProbeStatus{}) }
func normalizeStatus(s domain.GenbuTagProbeStatus) domain.GenbuTagProbeStatus {
	if s.Build == "" {
		s.Build = domain.GenbuUnknown
	}
	if s.UAT == "" {
		s.UAT = domain.GenbuUnknown
	}
	if s.Production == "" {
		s.Production = domain.GenbuUnknown
	}
	return s
}

func (g NativeGenbu) Versions(ctx context.Context, cfg domain.AppConfig, service string) ([]string, error) {
	result, err := g.queryPods(ctx, cfg, "prod", service)
	if err != nil {
		return nil, err
	}
	// Runtime versions serve as a baseline. Pipeline steps supply deployment status.
	seen := make(map[string]bool, len(result.Pods))
	for _, pod := range result.Pods {
		version := strings.TrimSpace(pod.AppVersion)
		if version == "" {
			return nil, errors.New("生产 Pod 缺少运行版本")
		}
		seen[version] = true
	}
	versions := make([]string, 0, len(seen))
	for version := range seen {
		versions = append(versions, version)
	}
	sort.Strings(versions)
	return versions, nil
}

func (g NativeGenbu) Status(ctx context.Context, cfg domain.AppConfig, service, tag string) (domain.GenbuTagProbeStatus, error) {
	status := unknownStatus()
	checked := timestamp()
	status.CheckedAt = &checked
	service, tag = strings.TrimSpace(service), strings.TrimSpace(tag)
	if !genbuName(service) || !genbuName(tag) {
		return status, errors.New("请输入有效的 Genbu 服务名和完整 Tag")
	}
	ctx, client, err := g.client(ctx, cfg)
	if err != nil {
		return status, err
	}
	result, err := client.QueryTag(ctx, genbuapi.QueryTagRequest{Service: service, Tag: tag})
	if scopeErr := checkGenbuReadScope(ctx); scopeErr != nil {
		return status, scopeErr
	}
	if errors.Is(err, genbuapi.ErrServiceNotFound) || errors.Is(err, genbuapi.ErrTagNotFound) {
		status.NotFound = true
		return status, nil
	}
	if err != nil {
		return status, genbuReadError(ctx, cfg, client, err)
	}
	status.Build = genbuStage(result.BuildStatus)
	status.UAT = genbuStage(result.UATStatus)
	status.Production = genbuStage(result.ProductionStatus)
	status.BuiltCompletedAt = genbuCompletedAt(status.Build, result.BuildStep)
	status.ReleasedCompletedAt = genbuCompletedAt(status.UAT, result.UATStep)
	status.ProductionReleasedCompletedAt = genbuCompletedAt(status.Production, result.ProductionStep)
	return status, nil
}

func genbuStage(stage genbuapi.PipelineStatus) domain.GenbuStageStatus {
	switch stage {
	case genbuapi.PipelineSuccess:
		return domain.GenbuSuccess
	case genbuapi.PipelineFailed:
		return domain.GenbuFailed
	case genbuapi.PipelineBuilding:
		return domain.GenbuBuilding
	case genbuapi.PipelineInitial:
		return domain.GenbuInitial
	default:
		return domain.GenbuUnknown
	}
}

func genbuCompletedAt(status domain.GenbuStageStatus, step *genbuapi.PipelineStep) *string {
	if status != domain.GenbuSuccess || step == nil || step.CreatedTime == "" {
		return nil
	}
	return &step.CreatedTime
}

func genbuReadError(ctx context.Context, cfg domain.AppConfig, client *genbuapi.Client, err error) error {
	var rejected genbuapi.RequestNotSentError
	switch {
	case errors.As(err, &rejected):
		return rejected
	case ctx.Err() != nil:
		return ctx.Err()
	case errors.Is(err, context.DeadlineExceeded):
		return errors.New("Genbu 请求超时")
	case genbuapi.IsAuthenticationError(err):
		return errors.New("Genbu 账号认证失败，请检查共享账号配置")
	default:
		credentials := client.ConfigSnapshot()
		return errors.New(networkproxy.Redact(cfg, genbuapi.RedactValues(err.Error(), credentials.Password, credentials.Token)))
	}
}
