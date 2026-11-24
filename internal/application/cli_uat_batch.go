package application

import (
	"context"
	"encoding/base64"
	"errors"
	"slices"
	"strings"

	"silverwing-go/internal/domain"
)

type CLIUATBatchRequest struct {
	Batch, Task, Preflight string
}

type CLIUATBatchResult struct {
	Task      string                `json:"task"`
	BatchID   string                `json:"batchId"`
	Items     []UATBatchReleaseRow  `json:"items"`
	Skipped   []UATBatchReleaseItem `json:"skipped,omitempty"`
	Preflight string                `json:"-"`
}

func (a *App) CLIUATBatchRelease(ctx context.Context, request CLIUATBatchRequest) (CLIUATBatchResult, error) {
	task, history, err := a.findCLITagHistory(ctx, CLITagHistoryQuery{Task: request.Task, Batch: request.Batch})
	if err != nil {
		return CLIUATBatchResult{}, err
	}
	cfg, err := a.cliTagConfig(ctx)
	if err != nil {
		return CLIUATBatchResult{}, err
	}
	entry, err := a.Manifests.LoadTask(*cfg.TaskRoot, task)
	if err != nil {
		return CLIUATBatchResult{}, err
	}
	if entry.Manifest.LifecycleStatus == domain.TaskArchived {
		return CLIUATBatchResult{}, errors.New("已归档的任务不能批量发布到 UAT")
	}
	result := CLIUATBatchResult{Task: task, BatchID: request.Batch, Items: []UATBatchReleaseRow{}}
	items := make([]UATBatchReleaseItem, 0, len(history))
	for _, record := range history {
		op := record.Operation
		item := UATBatchReleaseItem{OperationID: op.OperationID, ServiceID: op.GroupServiceID, ModuleID: op.ModuleID, Tag: value(op.Tag)}
		if uatBatchProbeDisabled(cfg, item.ServiceID) {
			result.Skipped = append(result.Skipped, item)
			continue
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		return result, errors.New("当前 Tag 批次没有开启 Genbu 探测的记录")
	}
	// History can be reordered by status updates between preview and submit.
	slices.SortFunc(items, func(a, b UATBatchReleaseItem) int { return strings.Compare(a.OperationID, b.OperationID) })
	submit := request.Preflight != ""
	result.Items = initialUATBatchResult(items, submit).Items
	action := Action{Kind: "tag.history.uat-batch-preview", TaskID: task, Payload: map[string]any{
		"groupId": request.Batch, "tagHistoryHome": a.Paths.Home, "tagHistoryTaskRoot": *cfg.TaskRoot,
		"tagHistoryTaskPath": entry.Path, "tagHistoryTaskCreatedAt": entry.Manifest.CreatedAt,
	}}
	if submit {
		if err := applyCLIBatchPreflight(items, request.Preflight); err != nil {
			return result, err
		}
		action.Kind = "tag.history.uat-batch-submit"
	}
	client, err := a.genbuClient(ctx)
	if err != nil {
		return result, err
	}
	batch, err := a.runUATBatchRelease(ctx, action, items, submit, client)
	result.Items = batch.Items
	if submit {
		if err == nil && slices.ContainsFunc(batch.Items, func(row UATBatchReleaseRow) bool {
			return row.Error != "" || row.Result.Status != "submitted"
		}) {
			err = errors.New("批量发布未全部提交，请核对各项回执和发布状态")
		}
		return result, err
	}
	confirmations := make([]string, len(result.Items))
	for i := range result.Items {
		confirmations[i] = result.Items[i].Result.Preflight
		result.Items[i].Result.Preflight = ""
	}
	if err == nil {
		// The underlying signatures bind each item and the complete ordered batch.
		result.Preflight = base64.RawURLEncoding.EncodeToString([]byte(strings.Join(confirmations, ",")))
	}
	return result, err
}

func applyCLIBatchPreflight(items []UATBatchReleaseItem, preflight string) error {
	decoded, err := base64.RawURLEncoding.DecodeString(preflight)
	confirmations := strings.Split(string(decoded), ",")
	if err != nil || len(confirmations) != len(items) {
		return errors.New("批量发布预检编号无效或批次已变化，请重新预检")
	}
	for i, confirmation := range confirmations {
		items[i].Preflight = confirmation
	}
	return nil
}
