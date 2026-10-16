package application

import (
	"context"
	"encoding/json"

	"silverwing-go/internal/delivery"
	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
)

func (a *App) delivery(config domain.AppConfig) *delivery.Service {
	service := delivery.NewService(a.Paths, a.Config, a.Manifests, a.git(config), a.Runner)
	preparation := documents.NewPreparationService(a.Paths, a.Runner)
	service.SetPreparationAPI(delivery.PreparationAPI{
		Plan: func(ctx context.Context, config domain.AppConfig, link, title, folder string) (json.RawMessage, error) {
			plan, err := preparation.Plan(ctx, config, link, title, folder)
			if err != nil {
				return nil, err
			}
			return json.Marshal(plan)
		},
		Materialize: func(ctx context.Context, config domain.AppConfig, input json.RawMessage) (json.RawMessage, error) {
			var plan documents.DocumentationPlan
			if err := json.Unmarshal(input, &plan); err != nil {
				return nil, err
			}
			result, err := preparation.Materialize(ctx, config, plan)
			if err != nil {
				return nil, err
			}
			return json.Marshal(result)
		},
	})
	return service
}

// ExecuteDelivery preserves the source CLI's report JSON. Desktop presentation
// adapts the same reports into compact rows after this shared operation returns.
func (a *App) ExecuteDelivery(ctx context.Context, kind string, payload map[string]any) (any, error) {
	config, err := a.Config.Load()
	if err != nil {
		return nil, err
	}
	return a.delivery(config).Execute(ctx, kind, payload)
}
func (a *App) deliveryAction(ctx context.Context, action Action) (ActionResult, error) {
	if tagHistoryAction(action.Kind) {
		return a.tagHistoryAction(ctx, action)
	}
	if action.Kind == "tag.genbu.refresh" {
		force := true
		if _, exists := action.Payload["force"]; exists {
			force = boolPayload(action, "force")
		}
		return a.refreshTagGenbuForAction(ctx, force, action)
	}
	if _, scopedRequest := action.Payload["tagHistoryTaskPath"]; scopedRequest {
		config, err := a.Config.Load()
		if err != nil {
			return ActionResult{}, err
		}
		if _, _, err := a.tagHistoryActionScope(config, action); err != nil {
			return ActionResult{}, err
		}
	}
	payload := map[string]any{}
	for k, v := range action.Payload {
		payload[k] = v
	}
	if action.TaskID != "" {
		payload["taskFolder"] = action.TaskID
	}
	if action.ServiceID != "" {
		key := action.ServiceID
		if action.ModuleID != "" {
			key += ":" + action.ModuleID
		}
		payload["selectionKeys"] = []string{key}
	}
	data, err := a.ExecuteDelivery(ctx, action.Kind, payload)
	if err != nil {
		return ActionResult{}, err
	}
	result := ActionResult{Message: "交付操作结果已返回，请查看状态", Data: data}
	if action.Kind == "tag.history" {
		if reports, ok := data.([]delivery.TagOperationReport); ok {
			result.Data = tagRecords(action.TaskID, reports)
		}
		result.Message = "Tag 历史已读取"
	} else if action.Kind == "tag.status" {
		if report, ok := data.(delivery.TagOperationReport); ok {
			result.Data = tagRecord(action.TaskID, report)
		}
		result.Message = "Genbu 状态已读取"
	} else if action.Kind == "tag.target.update" {
		result.Message = "测试目标分支已保存"
	}
	return result, nil
}

// Manual refresh forces a probe; the visible-page timer respects probe TTL.
func (a *App) refreshTagGenbu(ctx context.Context, force bool) (ActionResult, error) {
	return a.refreshTagGenbuForAction(ctx, force, Action{Kind: "tag.genbu.refresh"})
}

func (a *App) refreshTagGenbuForAction(ctx context.Context, force bool, action Action) (ActionResult, error) {
	config, err := a.Config.Load()
	if err != nil {
		return ActionResult{}, err
	}
	if config.TaskRoot == nil || *config.TaskRoot == "" {
		return ActionResult{Message: "Genbu 状态已刷新", Data: []TagRecord{}}, nil
	}
	_, scopedTasks, err := a.tagHistoryActionScope(config, action)
	if err != nil {
		return ActionResult{}, err
	}
	tasks := scopedTasks
	service := a.delivery(config)
	if _, err := service.Probe(ctx, config, tasks, force); err != nil {
		return ActionResult{}, err
	}
	probeEnabled := false
	for _, group := range config.Groups {
		for _, configuredService := range group.Services {
			probeEnabled = probeEnabled || configuredService.GenbuProbeEnabled
		}
	}
	items := make([]TagRecord, 0)
	for _, task := range tasks {
		taskID := task.TaskDirectoryName
		reports, err := service.History(ctx, taskID)
		if err != nil {
			return ActionResult{}, err
		}
		items = append(items, tagRecords(taskID, reports)...)
	}
	message := "Genbu 状态已刷新"
	if !probeEnabled {
		message = "尚未启用 Genbu 状态探测"
	}
	return ActionResult{Message: message, Data: items}, nil
}

func tagRecords(taskID string, reports []delivery.TagOperationReport) []TagRecord {
	items := make([]TagRecord, 0, len(reports))
	for _, report := range reports {
		items = append(items, tagRecord(taskID, report))
	}
	return items
}

func tagRecord(taskID string, report delivery.TagOperationReport) TagRecord {
	o := report.Operation
	return TagRecord{
		ID: o.OperationID, TaskID: taskID, ServiceID: o.GroupServiceID, ModuleID: o.ModuleID,
		FolderName: o.FolderName, ServiceName: o.ServiceName, BatchID: value(o.BatchID), CreatedAt: o.CreatedAt,
		SourceBranch: o.SourceBranch, TargetBranch: value(o.TargetBranch), Remote: o.Remote,
		Tag: value(o.Tag), State: string(o.State), RetryKind: string(report.RetryKind), Updated: o.UpdatedAt,
		Detail: value(o.Message), ConflictSummary: report.ConflictSummary(), ConflictFiles: append([]string{}, o.ConflictFiles...), Guidance: value(report.Guidance),
		GenbuStatus: o.GenbuStatus,
	}
}
