package application

import (
	"context"
	"errors"
	"strings"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/production"
)

func (a *App) CLIResolveGenbuService(ctx context.Context, selector string) (domain.AppConfig, domain.ServiceConfig, error) {
	cfg, err := a.Config.LoadForPreview(ctx)
	if err != nil {
		return cfg, domain.ServiceConfig{}, err
	}
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return cfg, domain.ServiceConfig{}, errors.New("请选择服务 ID 或唯一的服务名称")
	}
	service, err := cfg.Service(selector)
	if err != nil {
		matches := 0
		for _, candidate := range cfg.ServiceCatalog() {
			if strings.EqualFold(candidate.DisplayName, selector) || strings.EqualFold(strings.TrimSpace(candidate.GenbuServiceName), selector) {
				service = candidate
				matches++
			}
		}
		switch {
		case matches == 0:
			return cfg, domain.ServiceConfig{}, err
		case matches > 1:
			return cfg, domain.ServiceConfig{}, errors.New("服务名称或 Genbu 映射不唯一，请指定服务 ID")
		}
	}
	if err := validateConfiguredGenbuService(service); err != nil {
		return cfg, domain.ServiceConfig{}, err
	}
	return cfg, service, nil
}

func validateConfiguredGenbuService(service domain.ServiceConfig) error {
	switch {
	case !service.Enabled:
		return errors.New("此服务已停用")
	case !service.GenbuProbeEnabled:
		return errors.New("此服务未开启 Genbu 探测")
	case strings.TrimSpace(service.GenbuServiceName) == "":
		return errors.New("此服务缺少 Genbu 服务映射")
	default:
		return nil
	}
}

type CLIRemoteTagRequest struct {
	Command, Service, Tag, Keyword string
	Page, TailLines                int
}

type CLIRemoteTagResult struct {
	Service      string                      `json:"service"`
	GenbuService string                      `json:"genbuService"`
	Tag          string                      `json:"tag,omitempty"`
	Page         *production.GenbuTagPage    `json:"page,omitempty"`
	Status       *domain.GenbuTagProbeStatus `json:"status,omitempty"`
	Log          string                      `json:"log,omitempty"`
}

func (a *App) CLIRemoteTag(ctx context.Context, request CLIRemoteTagRequest) (CLIRemoteTagResult, error) {
	cfg, service, err := a.CLIResolveGenbuService(ctx, request.Service)
	if err != nil {
		return CLIRemoteTagResult{}, err
	}
	release, err := acquireTaskServiceReadSlot(ctx)
	if err != nil {
		return CLIRemoteTagResult{}, err
	}
	defer release()
	client, err := a.genbuClient(ctx)
	if err != nil {
		return CLIRemoteTagResult{}, err
	}
	result := CLIRemoteTagResult{
		Service: service.ID, GenbuService: strings.TrimSpace(service.GenbuServiceName), Tag: strings.TrimSpace(request.Tag),
	}
	switch request.Command {
	case "remote-list":
		var page production.GenbuTagPage
		page, err = client.TagList(ctx, cfg, result.GenbuService, request.Keyword, request.Page)
		result.Page = &page
	case "query":
		var status domain.GenbuTagProbeStatus
		status, err = client.Status(ctx, cfg, result.GenbuService, result.Tag)
		result.Status = &status
	case "logs":
		result.Log, err = client.BuildLog(ctx, cfg, result.GenbuService, result.Tag, request.TailLines)
	default:
		return CLIRemoteTagResult{}, errors.New("不支持的远端 Tag 查询")
	}
	if err != nil {
		return CLIRemoteTagResult{}, err
	}
	if err := client.CheckScope(ctx); err != nil {
		return CLIRemoteTagResult{}, err
	}
	current, serviceNow, err := a.CLIResolveGenbuService(ctx, request.Service)
	if err != nil {
		return CLIRemoteTagResult{}, err
	}
	if serviceNow.ID != service.ID || serviceNow.RepositoryID != service.RepositoryID || serviceNow.GenbuServiceName != service.GenbuServiceName ||
		TaskServiceQueryIdentity(current) != TaskServiceQueryIdentity(cfg) {
		return CLIRemoteTagResult{}, errors.New("Genbu 配置已变化，请重新查询")
	}
	if result.Status != nil && result.Status.NotFound {
		return result, errors.New("Genbu 未找到该 Tag")
	}
	return result, ctx.Err()
}
