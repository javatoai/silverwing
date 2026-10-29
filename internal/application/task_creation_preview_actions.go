package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

type taskCreationWorkspaceToolOption struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
	Available   bool   `json:"available"`
	Reason      string `json:"reason"`
}

type taskAgentPreviewPayload struct {
	workspace.CreateTaskRequest
	RequirementMaterials       *domain.RequirementMaterialsDirectory `json:"requirementMaterials"`
	TaskCreateToken            string                                `json:"taskCreateToken"`
	TaskCreatePreviewSignature string                                `json:"taskCreatePreviewSignature"`
	WorkspaceToolIDs           []string                              `json:"workspaceToolIds"`
}

type taskMaterialsPreviewPayload struct {
	RequirementLink              string `json:"requirementLink"`
	FolderName                   string `json:"folderName"`
	TaskCreateToken              string `json:"taskCreateToken"`
	TaskCreateMaterialsSignature string `json:"taskCreateMaterialsSignature"`
}

func taskCreationPreviewAction(ctx context.Context, action Action, config domain.AppConfig, service *workspace.TaskApplicationService) (ActionResult, bool, error) {
	if action.Kind != "workspace.tools-options" && action.Kind != "task.agent-preview" && action.Kind != "task.materials-preview" {
		return ActionResult{}, false, nil
	}
	if ctx == nil {
		return ActionResult{}, true, errors.New("请求上下文不能为空")
	}
	if err := ctx.Err(); err != nil {
		return ActionResult{}, true, err
	}

	switch action.Kind {
	case "workspace.tools-options":
		if err := decodeStrictPayload(action.Payload, &struct {
			TaskCreateToken string `json:"taskCreateToken"`
		}{}); err != nil {
			return ActionResult{}, true, err
		}
		options := []taskCreationWorkspaceToolOption{}
		if service != nil && service.WorkspaceTools != nil {
			descriptors := service.WorkspaceTools.Descriptors()
			sort.SliceStable(descriptors, func(i, j int) bool { return descriptors[i].ID < descriptors[j].ID })
			options = make([]taskCreationWorkspaceToolOption, 0, len(descriptors))
			for _, descriptor := range descriptors {
				if err := ctx.Err(); err != nil {
					return ActionResult{}, true, err
				}
				availability := service.WorkspaceTools.Availability(ctx, descriptor.ID)
				options = append(options, taskCreationWorkspaceToolOption{
					ID: descriptor.ID, DisplayName: descriptor.DisplayName,
					Description: descriptor.Description, Available: availability.Available,
					Reason: availability.Reason,
				})
			}
		}
		return ActionResult{Message: "工作区工具选项已读取", Data: options}, true, nil
	case "task.agent-preview":
		var payload taskAgentPreviewPayload
		if err := decodeStrictPayload(action.Payload, &payload); err != nil {
			return ActionResult{}, true, err
		}
		materials := domain.RequirementMaterialsDirectory{Status: domain.MaterialsNotRequested}
		if payload.RequirementMaterials != nil {
			materials = *payload.RequirementMaterials
		}
		if service == nil {
			return ActionResult{}, true, errors.New("任务服务不可用")
		}
		preview, err := service.PreviewAgentInstructions(ctx, config, payload.CreateTaskRequest, materials)
		if err != nil {
			return ActionResult{}, true, err
		}
		return ActionResult{Message: "Agent 文档预览已生成", Data: preview}, true, nil
	case "task.materials-preview":
		var payload taskMaterialsPreviewPayload
		if err := decodeStrictPayload(action.Payload, &payload); err != nil {
			return ActionResult{}, true, err
		}
		if service == nil {
			return ActionResult{}, true, errors.New("任务资料预览服务不可用")
		}
		preparation := service.MaterialsPreparation
		if preparation == nil {
			return ActionResult{}, true, errors.New("任务资料预览服务不可用")
		}
		materials, err := preparation.Preview(ctx, config, payload.RequirementLink, payload.FolderName)
		if err != nil {
			return ActionResult{}, true, err
		}
		return ActionResult{Message: "任务资料目录预览已完成", Data: materials}, true, nil
	}
	return ActionResult{}, false, fmt.Errorf("未处理的任务预览操作：%s", action.Kind)
}

func decodeStrictPayload(payload map[string]any, target any) error {
	if payload == nil {
		payload = map[string]any{}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("任务预览参数不合法：%w", err)
	}
	return nil
}
