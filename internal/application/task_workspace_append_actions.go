package application

import (
	"context"
	"errors"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

func taskWorkspaceAppendAction(
	ctx context.Context,
	action Action,
	config domain.AppConfig,
	service *workspace.TaskApplicationService,
	taskPath string,
) (ActionResult, bool, error) {
	switch action.Kind {
	case "task.services-add-preview":
		request, err := addWorkspaceRequest(action)
		if err != nil {
			return ActionResult{}, true, err
		}
		conflicts, err := service.InspectAddServicesBranchReuse(ctx, config, taskPath, request)
		if err != nil {
			return ActionResult{}, true, err
		}
		if conflicts == nil {
			conflicts = []workspace.BranchReuseConflict{}
		}
		return ActionResult{Message: "服务追加预检已完成", Data: conflicts}, true, nil
	case "task.modules-add-preview":
		request, err := addWorkspaceRequest(action)
		if err != nil {
			return ActionResult{}, true, err
		}
		conflicts, err := service.InspectAddModulesBranchReuse(ctx, config, taskPath, request)
		if err != nil {
			return ActionResult{}, true, err
		}
		if conflicts == nil {
			conflicts = []workspace.BranchReuseConflict{}
		}
		return ActionResult{Message: "模块追加预检已完成", Data: conflicts}, true, nil
	case "task.services-add":
		request, err := addWorkspaceRequest(action)
		if err != nil {
			return ActionResult{}, true, err
		}
		manifest, err := service.AddServices(ctx, config, taskPath, request)
		if err != nil {
			return ActionResult{}, true, err
		}
		return ActionResult{Message: "服务已追加", Data: manifest}, true, nil
	case "task.modules-add":
		request, err := addWorkspaceRequest(action)
		if err != nil {
			return ActionResult{}, true, err
		}
		manifest, err := service.AddModules(ctx, config, taskPath, request)
		if err != nil {
			return ActionResult{}, true, err
		}
		return ActionResult{Message: "模块已追加", Data: manifest}, true, nil
	case "task.services-retry":
		var payload struct {
			ServiceIDs []string `json:"serviceIds"`
		}
		if err := decodePayload(action.Payload, &payload); err != nil {
			return ActionResult{}, true, err
		}
		serviceIDs := payload.ServiceIDs
		if len(serviceIDs) == 0 {
			serviceIDs = nil
		}
		manifest, err := service.RetryFailedServices(ctx, config, taskPath, serviceIDs)
		if err != nil {
			return ActionResult{}, true, err
		}
		return ActionResult{Message: "失败服务已重试", Data: manifest}, true, nil
	default:
		return ActionResult{}, false, nil
	}
}

func addWorkspaceRequest(action Action) (workspace.AddWorkspaceRequest, error) {
	var request workspace.AddWorkspaceRequest
	if err := decodePayload(action.Payload, &request); err != nil {
		return workspace.AddWorkspaceRequest{}, err
	}
	if action.ServiceID != "" {
		request.ServiceID = action.ServiceID
	}
	if request.ServiceID == "" && action.Payload != nil {
		if value, ok := action.Payload["serviceId"].(string); ok {
			request.ServiceID = value
		}
	}
	if request.ServiceID == "" && len(request.ServiceSelections) == 0 && len(request.ServiceIDs) == 0 {
		return workspace.AddWorkspaceRequest{}, errors.New("请选择要追加的服务")
	}
	return request, nil
}
