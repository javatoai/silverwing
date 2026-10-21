package application

import (
	"context"
	"errors"

	"silverwing-go/internal/domain"
)

// DispatchMaterialsSync runs the same approved sync implementation used by
// Dispatch while forwarding progress to the native desktop's private session.
func (a *App) DispatchMaterialsSync(ctx context.Context, action Action, onProgress func(domain.MaterialsSyncProgress)) (ActionResult, error) {
	if action.Kind != "materials.sync" {
		return ActionResult{}, errors.New("materials progress dispatch only accepts materials.sync")
	}
	if err := ctx.Err(); err != nil {
		return ActionResult{}, err
	}
	payload, err := a.integrationActionPayload(action)
	if err != nil {
		return ActionResult{}, err
	}
	execution, err := a.Integrations.SyncMaterialsWithProgress(ctx, payload, onProgress)
	return ActionResult{Message: "资料同步执行已结束", Data: MaterialsSyncExecution{
		Result: execution.Result, RequirementDirectoryURL: execution.RequirementDirectoryURL,
	}}, err
}
