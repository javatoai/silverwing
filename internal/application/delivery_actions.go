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
	payload := map[string]any{}
	for key, value := range action.Payload {
		payload[key] = value
	}
	if action.TaskID != "" {
		payload["taskFolder"] = action.TaskID
	}
	data, err := a.ExecuteDelivery(ctx, action.Kind, payload)
	return ActionResult{Message: "任务操作结果已返回，请查看状态", Data: data}, err
}
