package application

import (
	"context"
	"errors"
	"strconv"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/production"
)

type genbuBuildLogReader interface {
	BuildLog(context.Context, domain.AppConfig, string, string, int) (string, error)
}

type taskServiceGenbuClient interface {
	genbuConfigEditor
	genbuConfigSourceReader
	ApprovalList(context.Context, domain.AppConfig, production.GenbuApprovalListRequest) (production.GenbuApprovalPage, error)
	ApprovalDetail(context.Context, domain.AppConfig, string, string, int64) (production.GenbuApprovalDetail, error)
	ApproveUATConfigChange(context.Context, domain.AppConfig, string, string, int64, string) (production.GenbuApprovalDetail, error)
}

type GenbuBuildLogResult struct {
	Service string `json:"service"`
	Tag     string `json:"tag"`
	Text    string `json:"text"`
}

var errProdServiceRuntimeReadOnly = errors.New("PROD 服务运行操作仅支持查看 Pod 明细和脱敏配置")

func (a *App) taskServiceGenbuAction(ctx context.Context, action Action, client taskServiceGenbuClient) (ActionResult, error) {
	scope, err := serviceScopeForAction(action)
	if err != nil {
		return ActionResult{}, err
	}
	env := scope.Environment
	operation := taskServiceOperation(action.Kind)
	if env == "prod" && operation != "config-preview-discard" {
		return ActionResult{}, errProdServiceRuntimeReadOnly
	}
	switch operation {
	case "config-files", "config-source":
		return a.taskServiceConfigReadAction(ctx, action, scope, client)
	case "config-preview", "config-submit", "config-preview-discard":
		return a.taskServiceConfigEditAction(ctx, action, scope, client)
	case "approval-approve":
		return a.approveTaskServiceConfigChange(ctx, action, scope, client)
	case "approvals", "approval-detail":
	default:
		return ActionResult{}, errors.New("不支持的 Genbu 服务操作")
	}
	cfg, service, release, err := a.beginTaskServiceQuery(ctx, action, scope)
	if err != nil {
		return ActionResult{}, err
	}
	defer release()

	var result any
	switch operation {
	case "approvals":
		var query struct {
			OpenedAt time.Time `json:"openedAt"`
			Page     int       `json:"page"`
		}
		if err := decodePayload(action.Payload, &query); err != nil {
			return ActionResult{}, errors.New("审批查询参数无效")
		}
		if query.OpenedAt.IsZero() {
			query.OpenedAt = time.Now()
		}
		day := domain.SilverWingLocalDate(query.OpenedAt)
		result, err = client.ApprovalList(ctx, cfg, production.GenbuApprovalListRequest{
			Environment: env, Service: service.GenbuServiceName, Status: "all", Type: "7", Scope: "all", DateKind: "created",
			StartDate: day.AddDate(0, 0, -6).Format(time.DateOnly), EndDate: day.Format(time.DateOnly), Page: max(1, query.Page),
		})
	case "approval-detail":
		id, parseErr := strconv.ParseInt(textPayload(action, "approvalId"), 10, 64)
		if parseErr != nil || id <= 0 {
			return ActionResult{}, errors.New("审批单号无效")
		}
		var detail production.GenbuApprovalDetail
		detail, err = client.ApprovalDetail(ctx, cfg, env, service.GenbuServiceName, id)
		if err == nil && detail.CanApprove {
			if pending := checkGenbuConfigSubmission(a.genbuApprovalSubmissionPath(id)); pending != nil {
				detail.CanApprove, detail.ComparisonNote = false, production.ErrGenbuApprovalUnknown.Error()
			}
		}
		result = detail
	}
	if err != nil {
		return ActionResult{}, err
	}
	if _, _, err := a.taskServiceIdentity(ctx, action, scope); err != nil {
		return ActionResult{}, err
	}
	return ActionResult{Data: result}, nil
}

func readGenbuBuildLog(ctx context.Context, client genbuBuildLogReader, cfg domain.AppConfig, action Action, service, tag string) (GenbuBuildLogResult, error) {
	if service == "" || tag == "" {
		return GenbuBuildLogResult{}, errors.New("缺少构建日志对应的 Genbu 服务或 Tag")
	}
	var request struct {
		TailLines int `json:"tailLines"`
	}
	if err := decodePayload(action.Payload, &request); err != nil {
		return GenbuBuildLogResult{}, errors.New("构建日志行数无效")
	}
	if request.TailLines == 0 {
		request.TailLines = 100
	}
	if request.TailLines < 1 || request.TailLines > 1000 {
		return GenbuBuildLogResult{}, errors.New("构建日志行数必须在 1 到 1000 之间")
	}
	output, err := client.BuildLog(ctx, cfg, service, tag, request.TailLines)
	if err != nil {
		return GenbuBuildLogResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return GenbuBuildLogResult{}, err
	}
	return GenbuBuildLogResult{Service: service, Tag: tag, Text: output}, nil
}
