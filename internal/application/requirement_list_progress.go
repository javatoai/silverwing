package application

import (
	"context"
	"errors"

	"silverwing-go/internal/integrations"
)

type RequirementListProgress = integrations.RequirementListProgress

// Optional in-process capability; ordinary Dispatch and its result stay intact.
type RequirementListProgressBackend interface {
	DispatchRequirementList(context.Context, Action, func(RequirementListProgress)) (ActionResult, error)
}

var _ RequirementListProgressBackend = (*App)(nil)

func (a *App) DispatchRequirementList(ctx context.Context, action Action, onProgress func(RequirementListProgress)) (ActionResult, error) {
	if action.Kind != "requirements.list" && action.Kind != "requirements.refresh" {
		return ActionResult{}, errors.New("需求列表进度入口仅支持读取或刷新需求列表")
	}
	if ctx == nil {
		return ActionResult{}, errors.New("需求列表读取上下文不能为空")
	}
	if err := ctx.Err(); err != nil {
		return ActionResult{}, err
	}
	return a.integrationActionWithRequirementProgress(ctx, action, onProgress)
}
