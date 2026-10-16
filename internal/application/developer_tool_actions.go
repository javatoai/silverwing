package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"silverwing-go/internal/tools"
)

// developerToolAction is shared by the desktop and CLI application boundary.
// Its context belongs to the individual tool request.
func (a *App) developerToolAction(ctx context.Context, action Action) (ActionResult, error) {
	if err := ctx.Err(); err != nil {
		return ActionResult{}, err
	}
	switch action.Kind {
	case "tool.json":
		input := actionText(action)
		if input == "" {
			input = textPayload(action, "left")
		}
		switch textPayload(action, "operation") {
		case "node-value":
			var bounds struct {
				Start int `json:"nodeStart"`
				End   int `json:"nodeEnd"`
			}
			if action.Payload["nodeStart"] == nil || action.Payload["nodeEnd"] == nil {
				return ActionResult{}, errors.New("JSON 节点范围缺失")
			}
			if err := decodePayload(action.Payload, &bounds); err != nil {
				return ActionResult{}, err
			}
			text, err := tools.JSONNodeValue(input, bounds.Start, bounds.End)
			return developerToolComputedResult(ctx, ActionResult{Message: "节点值已复制", Text: text}, err)
		case "escape":
			text, err := tools.EscapeJSON(input)
			return developerToolComputedResult(ctx, ActionResult{Message: "已转义", Text: text}, err)
		case "unescape":
			text, err := tools.UnescapeJSON(input)
			return developerToolComputedResult(ctx, ActionResult{Message: "已反转义", Text: text}, err)
		}
		doc, err := tools.ParseJSON(input)
		text := doc.Formatted
		if operation := textPayload(action, "operation"); operation == "compact" || operation == "minify" {
			text = doc.Compact
		}
		return developerToolComputedResult(ctx, ActionResult{Message: "JSON 已处理", Text: text, Data: doc}, err)
	case "tool.time":
		input := actionText(action)
		if input == "" {
			input = textPayload(action, "left")
		}
		var result tools.TimeConversion
		var err error
		if textPayload(action, "operation") == "now" {
			result, err = tools.CurrentTime(textPayload(action, "zone"))
		} else {
			result, err = tools.ConvertTime(input, textPayload(action, "unit"), textPayload(action, "zone"))
		}
		return developerToolComputedResult(ctx, ActionResult{Message: "时间已转换", Text: result.Formatted, Data: result}, err)
	case "tool.diff":
		options := tools.DiffOptions{ContextLines: 3}
		if raw, ok := action.Payload["options"]; ok {
			data, err := json.Marshal(raw)
			if err != nil {
				return ActionResult{}, err
			}
			if err = json.Unmarshal(data, &options); err != nil {
				return ActionResult{}, err
			}
		}
		result, err := tools.CompareText(ctx, textPayload(action, "left"), textPayload(action, "right"), options)
		return ActionResult{Message: "差异已计算", Text: result.Unified, Data: result}, err
	case "tool.ports":
		if action.Payload["port"] == nil && actionText(action) != "" {
			action.Payload = map[string]any{"port": actionText(action)}
		}
		port, err := numberPayload(action, "port")
		if err != nil {
			return ActionResult{}, err
		}
		items, err := tools.NewPortService(a.Runner).Query(ctx, port)
		return ActionResult{Message: "端口已刷新", Data: items}, err
	case "tool.port-stop":
		var item tools.PortProcess
		if err := decodePayload(action.Payload, &item); err != nil {
			return ActionResult{}, err
		}
		result, err := tools.NewPortService(a.Runner).TerminateDetailed(ctx, item, boolPayload(action, "confirmed"))
		// Once submitted, termination observes the outcome even if the view
		// closes. Preserve the service's result and cancellation semantics.
		return ActionResult{Message: result.Message, Data: result}, err
	}
	return ActionResult{}, fmt.Errorf("功能 %s 正在迁移，当前版本尚未实现", action.Kind)
}

func developerToolComputedResult(ctx context.Context, result ActionResult, err error) (ActionResult, error) {
	if cancelled := ctx.Err(); cancelled != nil {
		return ActionResult{}, errors.Join(cancelled, err)
	}
	return result, err
}
