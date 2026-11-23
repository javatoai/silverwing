package application

import (
	"context"
	"errors"
	"path/filepath"

	"silverwing-go/internal/delivery"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/production"
)

type genbuHistoryLogTarget struct {
	Service, Tag, RecordIdentity string
}

type genbuHistoryLogTargetReader func(context.Context, Action) (domain.AppConfig, genbuHistoryLogTarget, error)

func (a *App) tagHistoryBuildLogAction(ctx context.Context, action Action, client genbuBuildLogReader) (ActionResult, error) {
	return a.genbuHistoryBuildLog(ctx, action, client, a.tagHistoryBuildLogTarget)
}

func (a *App) productionBuildLogAction(ctx context.Context, action Action, client genbuBuildLogReader) (ActionResult, error) {
	return a.genbuHistoryBuildLog(ctx, action, client, a.productionBuildLogTarget)
}

func (a *App) genbuHistoryBuildLog(ctx context.Context, action Action, client genbuBuildLogReader, resolve genbuHistoryLogTargetReader) (ActionResult, error) {
	release, err := acquireTaskServiceReadSlot(ctx)
	if err != nil {
		return ActionResult{}, err
	}
	defer release()
	if err := a.RequireOptionalCLI(ctx, "genbu"); err != nil {
		return ActionResult{}, err
	}
	cfg, target, err := resolve(ctx, action)
	if err != nil {
		return ActionResult{}, err
	}
	result, err := readGenbuBuildLog(ctx, client, cfg, action, target.Service, target.Tag)
	if err != nil {
		return ActionResult{}, err
	}
	current, currentTarget, err := resolve(ctx, action)
	if err != nil {
		return ActionResult{}, err
	}
	if currentTarget != target || TaskServiceQueryIdentity(current) != TaskServiceQueryIdentity(cfg) {
		return ActionResult{}, errors.New("Tag 记录或 Genbu 配置已变化，请重新打开构建日志")
	}
	return ActionResult{Data: result}, nil
}

func (a *App) tagHistoryBuildLogTarget(ctx context.Context, action Action) (domain.AppConfig, genbuHistoryLogTarget, error) {
	cfg, err := a.Config.LoadForPreview(ctx)
	if err != nil {
		return cfg, genbuHistoryLogTarget{}, err
	}
	_, tasks, err := a.tagHistoryActionScope(cfg, action)
	if err != nil {
		return cfg, genbuHistoryLogTarget{}, err
	}
	task := tasks[0]
	operation, err := delivery.ReadTagHistoryOperation(filepath.Join(*cfg.TaskRoot, task.TaskDirectoryName), textPayload(action, "operationId"))
	if err != nil {
		return cfg, genbuHistoryLogTarget{}, err
	}
	if (action.ServiceID != "" && action.ServiceID != operation.GroupServiceID) || (action.ModuleID != "" && action.ModuleID != operation.ModuleID) ||
		!domain.TagGenbuProbeEligible(operation.State, value(operation.Tag)) {
		return cfg, genbuHistoryLogTarget{}, errors.New("当前 Tag 构建记录缺少已推送的 Tag 或记录身份不匹配")
	}
	service, err := cfg.Service(operation.GroupServiceID)
	if err != nil {
		return cfg, genbuHistoryLogTarget{}, err
	}
	if !service.GenbuProbeEnabled || service.GenbuServiceName == "" || (operation.RepositoryID != "" && operation.RepositoryID != service.RepositoryID) {
		return cfg, genbuHistoryLogTarget{}, errors.New("Tag 记录的 Genbu 服务未配置或已变化")
	}
	return cfg, genbuHistoryLogTarget{Service: service.GenbuServiceName, Tag: value(operation.Tag), RecordIdentity: operation.CreatedAt}, nil
}

func (a *App) productionBuildLogTarget(ctx context.Context, action Action) (domain.AppConfig, genbuHistoryLogTarget, error) {
	cfg, err := a.Config.LoadForPreview(ctx)
	if err != nil {
		return cfg, genbuHistoryLogTarget{}, err
	}
	service := production.New(a.Paths, nil, nil, nil)
	batch, err := service.Store.Load(textPayload(action, "batchId"))
	if err != nil {
		return cfg, genbuHistoryLogTarget{}, err
	}
	tagName := textPayload(action, "tag")
	if batch.Target.GenbuService != "" && tagName != "" {
		for _, tag := range batch.Tags {
			if tag.Name == tagName {
				return cfg, genbuHistoryLogTarget{Service: batch.Target.GenbuService, Tag: tag.Name, RecordIdentity: tag.Object}, nil
			}
		}
	}
	return cfg, genbuHistoryLogTarget{}, errors.New("当前生产批次中未找到所选 Genbu Tag")
}
