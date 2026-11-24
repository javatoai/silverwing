package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/production"
	"silverwing-go/internal/tools"
)

type CLIServiceReadRequest struct {
	Command, Service, Environment string
	Namespace, File               string
	ApprovalID                    int64
	Page, PageSize                int
	Status, StartDate, EndDate    string
}

func (r CLIServiceReadRequest) Validate() error {
	if r.Service == "" {
		return errors.New("请指定 --service")
	}
	if r.Environment != "uat" && r.Environment != "prod" {
		return errors.New("请用 --env 指定 uat 或 prod")
	}
	switch r.Command {
	case "pods", "config":
		return nil
	case "approval", "approval-diff":
		if r.ApprovalID < 1 {
			return errors.New("请用 --id 指定有效的审批编号")
		}
	case "approvals":
		if r.Page < 0 || r.PageSize < 0 || r.PageSize > 100 {
			return errors.New("页码必须大于 0，每页条数必须在 1 到 100 之间")
		}
		switch r.Status {
		case "", "all", "pending", "approved", "rejected", "revoked":
		default:
			return errors.New("审批状态必须为 all、pending、approved、rejected 或 revoked")
		}
		if r.StartDate != "" || r.EndDate != "" {
			start, startErr := time.Parse(time.DateOnly, r.StartDate)
			end, endErr := time.Parse(time.DateOnly, r.EndDate)
			if startErr != nil || endErr != nil || end.Before(start) {
				return errors.New("请同时指定有效的 --start-date 和 --end-date，开始日期不得晚于结束日期")
			}
		}
	default:
		return errors.New("服务命令仅支持 Pod、配置和配置审批查询")
	}
	return nil
}

type CLIConfigApprovalResult struct {
	production.GenbuApprovalItem
	Details    map[string]string    `json:"details"`
	FileNames  []string             `json:"file_names"`
	Comparison *CLIConfigComparison `json:"comparison,omitempty"`
}

type CLIConfigComparison struct {
	Baseline string                  `json:"baseline"`
	Compared bool                    `json:"compared"`
	Note     string                  `json:"note,omitempty"`
	Changes  []CLIConfigApprovalDiff `json:"changes"`
}

type CLIConfigApprovalDiff struct {
	File         string `json:"file"`
	BeforeExists bool   `json:"before_exists"`
	AfterExists  bool   `json:"after_exists"`
	Unified      string `json:"unified"`
	Note         string `json:"note,omitempty"`
}

// CLIServiceRead limits both environments to explicitly supported queries.
func (a *App) CLIServiceRead(ctx context.Context, request CLIServiceReadRequest) (any, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	cfg, service, err := a.CLIResolveGenbuService(ctx, request.Service)
	if err != nil {
		return nil, err
	}
	release, err := acquireTaskServiceReadSlot(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	client, err := a.genbuClient(ctx)
	if err != nil {
		return nil, err
	}
	data, readErr := readCLIService(ctx, client, cfg, service.GenbuServiceName, request)
	if err := client.CheckScope(ctx); err != nil {
		return nil, err
	}
	currentCfg, current, err := a.CLIResolveGenbuService(ctx, request.Service)
	if err != nil || current.ID != service.ID || current.GenbuServiceName != service.GenbuServiceName ||
		current.RepositoryID != service.RepositoryID || TaskServiceQueryIdentity(currentCfg) != TaskServiceQueryIdentity(cfg) {
		return nil, errors.New("服务或 Genbu 配置已变化，请重新查询")
	}
	var ambiguity *production.ServiceConfigNamespaceError
	if errors.As(readErr, &ambiguity) {
		return nil, fmt.Errorf("请用 --namespace 指定配置所在命名空间（%s）", strings.Join(ambiguity.Candidates, "、"))
	}
	return data, readErr
}

func readCLIService(ctx context.Context, client production.NativeGenbu, cfg domain.AppConfig, service string, request CLIServiceReadRequest) (any, error) {
	env := request.Environment
	switch request.Command {
	case "pods":
		result, err := client.Pods(ctx, cfg, env, service)
		if err == nil {
			return result, nil
		}
		return nil, err
	case "config":
		result, err := client.ReadServiceConfig(ctx, cfg, env, service, request.Namespace, request.File)
		if err == nil {
			return result, nil
		}
		return nil, err
	case "approvals":
		result, err := client.ApprovalList(ctx, cfg, production.GenbuApprovalListRequest{
			Environment: env, Service: service, Status: request.Status, Type: "7",
			StartDate: request.StartDate, EndDate: request.EndDate, Page: request.Page, PageSize: request.PageSize,
		})
		if err == nil {
			return result, nil
		}
		return nil, err
	case "approval", "approval-diff":
		includeDiff := request.Command == "approval-diff"
		detail, err := client.ReadConfigApproval(ctx, cfg, env, service, request.ApprovalID, includeDiff)
		if err != nil {
			return nil, err
		}
		return formatCLIConfigApproval(ctx, detail, includeDiff)
	default:
		return nil, errors.New("服务命令仅支持 Pod、配置和配置审批查询")
	}
}

func formatCLIConfigApproval(ctx context.Context, detail production.GenbuApprovalDetail, includeDiff bool) (CLIConfigApprovalResult, error) {
	result := CLIConfigApprovalResult{GenbuApprovalItem: detail.GenbuApprovalItem, Details: detail.Details, FileNames: detail.FileNames}
	if !includeDiff {
		return result, nil
	}
	comparison := &CLIConfigComparison{
		Baseline: "current", Compared: detail.HasComparison, Note: detail.ComparisonNote, Changes: []CLIConfigApprovalDiff{},
	}
	result.Comparison = comparison
	if !detail.HasComparison {
		if comparison.Note == "" {
			comparison.Note = "无法核对当前配置"
		}
		return result, errors.New(comparison.Note)
	}
	for _, change := range detail.Changes {
		diff, err := tools.CompareText(ctx, change.Before, change.After, tools.DiffOptions{ContextLines: 3})
		if err != nil {
			comparison.Compared = false
			comparison.Note = "无法生成完整的配置差异"
			return result, err
		}
		item := CLIConfigApprovalDiff{File: change.File, BeforeExists: change.BeforeExists, AfterExists: change.AfterExists, Unified: diff.Unified}
		if diff.Unified == "" && item.BeforeExists == item.AfterExists {
			item.Note = "文件内容有变化，脱敏后无可显示的文本差异"
		}
		comparison.Changes = append(comparison.Changes, item)
	}
	return result, nil
}
