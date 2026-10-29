package application

import (
	"context"
	"errors"
	"fmt"
)

// This adapter owns the legacy JSON fields; all tool behavior is typed.
func (a *App) developerToolAction(ctx context.Context, action Action) (ActionResult, error) {
	if err := operationContextError(ctx); err != nil {
		return ActionResult{}, err
	}
	switch action.Kind {
	case "tool.json":
		request, err := JSONToolRequestFromAction(action)
		if err != nil {
			return ActionResult{}, err
		}
		result, err := a.ProcessJSON(ctx, request)
		return result.ToActionResult(), err
	case "tool.time":
		result, err := a.ConvertTime(ctx, TimeToolRequestFromAction(action))
		return result.ToActionResult(), err
	case "tool.diff":
		request, err := TextDiffRequestFromAction(action)
		if err != nil {
			return ActionResult{}, err
		}
		result, err := a.CompareText(ctx, request)
		return result.ToActionResult(), err
	case "tool.ports":
		request, err := PortQueryRequestFromAction(action)
		if err != nil {
			return ActionResult{}, err
		}
		result, err := a.ListPorts(ctx, request)
		return result.ToActionResult(), err
	case "tool.port-stop":
		request, err := PortStopRequestFromAction(action)
		if err != nil {
			return ActionResult{}, err
		}
		result, err := a.StopPort(ctx, request)
		return result.ToActionResult(), err
	}
	return ActionResult{}, fmt.Errorf("功能 %s 正在迁移，当前版本尚未实现", action.Kind)
}

func developerToolComputedResult(ctx context.Context, result ActionResult, err error) (ActionResult, error) {
	if cancelled := ctx.Err(); cancelled != nil {
		return ActionResult{}, errors.Join(cancelled, err)
	}
	return result, err
}
