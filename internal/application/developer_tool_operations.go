package application

import (
	"context"
	"errors"

	"silverwing-go/internal/tools"
)

type JSONToolRequest struct {
	Input, Operation   string
	NodeStart, NodeEnd *int
}
type TimeToolRequest struct{ Input, Operation, Unit, Zone string }
type TextDiffRequest struct {
	Left, Right string
	Options     tools.DiffOptions
}
type PortQueryRequest struct{ Port int }
type PortStopRequest struct {
	Process   tools.PortProcess
	Confirmed bool
}

type DeveloperTools interface {
	ProcessJSON(context.Context, JSONToolRequest) (OperationResult[*tools.JSONDocument], error)
	ConvertTime(context.Context, TimeToolRequest) (OperationResult[tools.TimeConversion], error)
	CompareText(context.Context, TextDiffRequest) (OperationResult[tools.DiffResult], error)
	ListPorts(context.Context, PortQueryRequest) (OperationResult[[]tools.PortProcess], error)
	StopPort(context.Context, PortStopRequest) (OperationResult[tools.PortTerminationResult], error)
}

var _ DeveloperTools = (*App)(nil)

func JSONToolRequestFromAction(action Action) (JSONToolRequest, error) {
	input := actionText(action)
	if input == "" {
		input = textPayload(action, "left")
	}
	request := JSONToolRequest{Input: input, Operation: textPayload(action, "operation")}
	if request.Operation == "node-value" {
		var bounds struct {
			Start *int `json:"nodeStart"`
			End   *int `json:"nodeEnd"`
		}
		if action.Payload["nodeStart"] == nil || action.Payload["nodeEnd"] == nil {
			return request, errors.New("JSON 节点范围缺失")
		}
		if err := decodePayload(action.Payload, &bounds); err != nil {
			return request, err
		}
		request.NodeStart, request.NodeEnd = bounds.Start, bounds.End
	}
	return request, nil
}
func TimeToolRequestFromAction(action Action) TimeToolRequest {
	input := actionText(action)
	if input == "" {
		input = textPayload(action, "left")
	}
	return TimeToolRequest{Input: input, Operation: textPayload(action, "operation"), Unit: textPayload(action, "unit"), Zone: textPayload(action, "zone")}
}
func TextDiffRequestFromAction(action Action) (TextDiffRequest, error) {
	request := TextDiffRequest{Left: textPayload(action, "left"), Right: textPayload(action, "right"), Options: tools.DiffOptions{ContextLines: 3}}
	if action.Payload["options"] != nil {
		if err := decodeField(action, "options", &request.Options); err != nil {
			return request, err
		}
	}
	return request, nil
}
func PortQueryRequestFromAction(action Action) (PortQueryRequest, error) {
	if action.Payload["port"] == nil && actionText(action) != "" {
		action.Payload = map[string]any{"port": actionText(action)}
	}
	port, err := numberPayload(action, "port")
	return PortQueryRequest{Port: port}, err
}
func PortStopRequestFromAction(action Action) (PortStopRequest, error) {
	request := PortStopRequest{Confirmed: boolPayload(action, "confirmed")}
	err := decodePayload(action.Payload, &request.Process)
	return request, err
}

func (a *App) ProcessJSON(ctx context.Context, request JSONToolRequest) (OperationResult[*tools.JSONDocument], error) {
	if err := operationContextError(ctx); err != nil {
		return OperationResult[*tools.JSONDocument]{}, err
	}
	result := OperationResult[*tools.JSONDocument]{}
	var err error
	switch request.Operation {
	case "node-value":
		if request.NodeStart == nil || request.NodeEnd == nil {
			return result, errors.New("JSON 节点范围缺失")
		}
		result.Message = "节点值已复制"
		result.Text, err = tools.JSONNodeValue(request.Input, *request.NodeStart, *request.NodeEnd)
	case "escape":
		result.Message = "已转义"
		result.Text, err = tools.EscapeJSON(request.Input)
	case "unescape":
		result.Message = "已反转义"
		result.Text, err = tools.UnescapeJSON(request.Input)
	default:
		var document tools.JSONDocument
		document, err = tools.ParseJSON(request.Input)
		result.Message, result.Text, result.Data = "JSON 已处理", document.Formatted, &document
		if request.Operation == "compact" || request.Operation == "minify" {
			result.Text = document.Compact
		}
	}
	return computedOperationResult(ctx, result, err)
}
func (a *App) ConvertTime(ctx context.Context, request TimeToolRequest) (OperationResult[tools.TimeConversion], error) {
	if err := operationContextError(ctx); err != nil {
		return OperationResult[tools.TimeConversion]{}, err
	}
	var value tools.TimeConversion
	var err error
	if request.Operation == "now" {
		value, err = tools.CurrentTime(request.Zone)
	} else {
		value, err = tools.ConvertTime(request.Input, request.Unit, request.Zone)
	}
	return computedOperationResult(ctx, OperationResult[tools.TimeConversion]{Message: "时间已转换", Text: value.Formatted, Data: value}, err)
}
func (a *App) CompareText(ctx context.Context, request TextDiffRequest) (OperationResult[tools.DiffResult], error) {
	if err := operationContextError(ctx); err != nil {
		return OperationResult[tools.DiffResult]{}, err
	}
	value, err := tools.CompareText(ctx, request.Left, request.Right, request.Options)
	return OperationResult[tools.DiffResult]{Message: "差异已计算", Text: value.Unified, Data: value}, err
}
func (a *App) ListPorts(ctx context.Context, request PortQueryRequest) (OperationResult[[]tools.PortProcess], error) {
	if err := operationContextError(ctx); err != nil {
		return OperationResult[[]tools.PortProcess]{}, err
	}
	values, err := tools.NewPortService(a.Runner).Query(ctx, request.Port)
	return OperationResult[[]tools.PortProcess]{Message: "端口已刷新", Data: values}, err
}
func (a *App) StopPort(ctx context.Context, request PortStopRequest) (OperationResult[tools.PortTerminationResult], error) {
	if err := operationContextError(ctx); err != nil {
		return OperationResult[tools.PortTerminationResult]{}, err
	}
	// Termination keeps observing the result after submission, even when its
	// window closes. Its service owns that cancellation contract.
	result, err := tools.NewPortService(a.Runner).TerminateDetailed(ctx, request.Process, request.Confirmed)
	return OperationResult[tools.PortTerminationResult]{Message: result.Message, Data: result}, err
}
func operationContextError(ctx context.Context) error {
	if ctx == nil {
		return errors.New("请求上下文不能为空")
	}
	return ctx.Err()
}
func computedOperationResult[T any](ctx context.Context, result OperationResult[T], err error) (OperationResult[T], error) {
	if cancelled := ctx.Err(); cancelled != nil {
		return OperationResult[T]{}, errors.Join(cancelled, err)
	}
	return result, err
}
