package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"silverwing-go/internal/delivery"
	"silverwing-go/internal/domain"
)

// NewTagCLI uses the desktop's stores and services without running startup
// recovery or creating a desktop session.
func NewTagCLI(home string) (*App, error) { return newApp(home) }

type CLITagTarget struct {
	SelectionKey string              `json:"selectionKey"`
	ServiceID    string              `json:"serviceId"`
	Service      string              `json:"service"`
	RepositoryID string              `json:"repositoryId"`
	ModuleID     string              `json:"moduleId"`
	Module       string              `json:"module"`
	SourceBranch string              `json:"sourceBranch"`
	TargetBranch string              `json:"targetBranch,omitempty"`
	Remote       string              `json:"remote"`
	Mode         domain.TagBuildMode `json:"mode"`
	GenbuService string              `json:"genbuService,omitempty"`
	Selectable   bool                `json:"selectable"`
	Error        string              `json:"error,omitempty"`
}

type CLITagPreflightItem struct {
	Target      CLITagTarget          `json:"target"`
	Preview     delivery.TagPreflight `json:"preview"`
	Error       string                `json:"error,omitempty"`
	OperationID string                `json:"operationId,omitempty"`
	Worktree    string                `json:"worktree,omitempty"`
	BlockedBy   string                `json:"blockedBy,omitempty"`
}

type CLITagPreflightResult struct {
	ID       string
	Items    []CLITagPreflightItem
	Warnings []string
}

type CLITagJobResult struct {
	Task    string          `json:"task"`
	BatchID string          `json:"batchId"`
	Items   []CLITagJobItem `json:"items"`
}

type CLITagJobItem struct {
	OperationID  string      `json:"operationId"`
	SelectionKey string      `json:"selectionKey"`
	Phase        TagJobPhase `json:"phase"`
	Record       *TagRecord  `json:"record"`
	Error        string      `json:"error,omitempty"`
}

type CLITagHistoryQuery struct {
	Task, Batch, Operation, UATNamespace string
}

func (a *App) cliTagConfig(ctx context.Context) (domain.AppConfig, error) {
	pending, err := a.TaskRootMigrationPending(ctx)
	if err != nil {
		return domain.AppConfig{}, err
	}
	if pending != nil && *pending {
		return domain.AppConfig{}, errors.New("任务根目录迁移尚未结束，请先在桌面处理迁移")
	}
	config, err := a.Config.LoadForPreview(ctx)
	if err == nil && strings.TrimSpace(value(config.TaskRoot)) == "" {
		err = errors.New("请先在 Silverwing 中配置任务根目录，或用 --home 指定已有数据目录")
	}
	return config, err
}

func (a *App) cliTagTask(ctx context.Context, taskID string) (*delivery.TagTask, TagJobScope, error) {
	config, err := a.cliTagConfig(ctx)
	if err != nil {
		return nil, TagJobScope{}, err
	}
	entry, err := a.Manifests.LoadTask(value(config.TaskRoot), taskID)
	if err != nil {
		return nil, TagJobScope{}, err
	}
	task, err := a.delivery(config).OpenTagTask(taskID)
	if err != nil {
		return nil, TagJobScope{}, err
	}
	scope := TagJobScope{Home: a.Paths.Home, TaskRoot: value(config.TaskRoot), TaskID: taskID,
		TaskPath: entry.Path, TaskCreatedAt: entry.Manifest.CreatedAt, ConfigIdentity: configTagIdentity(config)}
	if configTagIdentity(task.Config()) != scope.ConfigIdentity || task.Manifest().CreatedAt != scope.TaskCreatedAt ||
		!sameTagHistoryTaskPath(task.Directory(), scope.TaskPath) {
		return nil, TagJobScope{}, errors.New("任务或配置已变化，请重试")
	}
	return task, scope, nil
}

func cliTagTargets(task *delivery.TagTask) []CLITagTarget {
	manifest, config := task.Manifest(), task.Config()
	out := make([]CLITagTarget, 0, len(manifest.Services))
	for _, w := range manifest.Services {
		row := CLITagTarget{SelectionKey: w.SelectionKey(), ServiceID: w.GroupServiceID, Service: w.ServiceName,
			RepositoryID: w.RepositoryID, ModuleID: w.ModuleID, Module: w.ModuleName, SourceBranch: w.Branch,
			TargetBranch: value(w.TagTargetRef), Remote: w.PushRemote, Mode: w.TagMode}
		if target, err := domain.ResolveEffectiveTagTarget(config, manifest, row.SelectionKey); err == nil {
			row.Selectable, row.Remote, row.TargetBranch = true, target.Remote, value(target.TargetBranch)
		} else {
			row.Error = err.Error()
		}
		if service, err := config.GroupService(manifest.GroupID, w.GroupServiceID); err == nil && service.GenbuProbeEnabled {
			row.GenbuService = service.GenbuServiceName
		}
		out = append(out, row)
	}
	return out
}

func (a *App) CLITagList(ctx context.Context, taskID string) ([]CLITagTarget, error) {
	task, _, err := a.cliTagTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	return cliTagTargets(task), nil
}

func selectCLITagTargets(rows []CLITagTarget, selectors []string, all bool) ([]CLITagTarget, error) {
	if all == (len(selectors) > 0) {
		return nil, errors.New("请指定 --service 或 --all-services，二者只能选一种")
	}
	selected := []CLITagTarget{}
	seen := map[string]bool{}
	for _, selector := range selectors {
		service, module, hasModule := strings.Cut(strings.TrimSpace(selector), ":")
		matches := []CLITagTarget{}
		serviceID := ""
		for _, row := range rows {
			if service == "" || service != row.ServiceID && service != row.Service && service != row.RepositoryID {
				continue
			}
			if hasModule && (module == "" || module != row.ModuleID && module != row.Module) {
				continue
			}
			if serviceID != "" && serviceID != row.ServiceID || hasModule && len(matches) > 0 {
				return nil, fmt.Errorf("服务选择 %q 不唯一，请使用 list 返回的 selectionKey", selector)
			}
			serviceID = row.ServiceID
			if row.Selectable {
				matches = append(matches, row)
			} else if hasModule {
				return nil, fmt.Errorf("%s：%s", selector, row.Error)
			}
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("没有匹配且可构建的服务模块：%s", selector)
		}
		for _, row := range matches {
			if !seen[row.SelectionKey] {
				selected = append(selected, row)
				seen[row.SelectionKey] = true
			}
		}
	}
	if all {
		for _, row := range rows {
			if row.Selectable {
				selected = append(selected, row)
			}
		}
	}
	if len(selected) == 0 {
		return nil, errors.New("当前任务没有可构建的服务模块")
	}
	return selected, nil
}

func (a *App) CLITagPreflight(ctx context.Context, taskID, planID string, selectors []string, all bool) (CLITagPreflightResult, error) {
	task, _, err := a.cliTagTask(ctx, taskID)
	if err != nil {
		return CLITagPreflightResult{}, err
	}
	rows, err := selectCLITagTargets(cliTagTargets(task), selectors, all)
	if err != nil {
		return CLITagPreflightResult{}, err
	}
	plan, err := task.PrepareBatch(ctx, planID, cliTagKeys(rows), all)
	out := CLITagPreflightResult{ID: plan.ID, Items: make([]CLITagPreflightItem, 0, len(plan.Items))}
	seenServices := map[string]int{}
	for i, item := range plan.Items {
		genbuService := strings.TrimSpace(rows[i].GenbuService)
		if genbuService != "" {
			seenServices[genbuService]++
			if seenServices[genbuService] == 2 {
				out.Warnings = append(out.Warnings, "多个构建项共用 Genbu 服务 "+genbuService+"，请确认分批或按操作核对 UAT 部署范围")
			}
		}
		out.Items = append(out.Items, CLITagPreflightItem{Target: rows[i], Preview: item.Preview, Error: item.Error,
			OperationID: item.OperationID, Worktree: item.Worktree, BlockedBy: item.BlockedBy})
	}
	return out, err
}

func (a *App) CLITagBuild(ctx context.Context, taskID, planID string, selectors []string, all bool) (CLITagJobResult, error) {
	task, _, err := a.cliTagTask(ctx, taskID)
	if err != nil {
		return CLITagJobResult{}, err
	}
	rows, err := selectCLITagTargets(cliTagTargets(task), selectors, all)
	if err != nil {
		return CLITagJobResult{}, err
	}
	reports, err := task.ApplyBatch(ctx, planID, cliTagKeys(rows), all)
	out := CLITagJobResult{Task: taskID}
	if len(reports) > 0 {
		out.BatchID = planID
	}
	for _, report := range reports {
		op := report.Operation
		record := tagRecord(taskID, report)
		item := CLITagJobItem{OperationID: op.OperationID, SelectionKey: op.GroupServiceID + ":" + op.ModuleID, Record: &record}
		switch {
		case op.State == domain.TagOperationSuccess:
			item.Phase = TagJobPhaseCompleted
		case op.BlockedBy != "":
			item.Phase = TagJobPhaseBlocked
		case op.State == domain.TagOperationFailed || op.State == domain.TagOperationPartial:
			item.Phase, item.Error = TagJobPhaseFailed, value(op.Message)
		default:
			item.Phase, item.Error = TagJobPhaseFailed, value(op.Message)
		}
		out.Items = append(out.Items, item)
	}
	return out, err
}

func cliTagKeys(rows []CLITagTarget) []string {
	keys := make([]string, 0, len(rows))
	for _, row := range rows {
		keys = append(keys, row.SelectionKey)
	}
	return keys
}

func (a *App) CLITagRetry(ctx context.Context, taskID, operationID string) (CLITagJobResult, error) {
	taskID, records, err := a.findCLITagHistory(ctx, CLITagHistoryQuery{Task: taskID, Operation: operationID})
	if err != nil {
		return CLITagJobResult{}, err
	}
	_, scope, err := a.cliTagTask(ctx, taskID)
	if err != nil {
		return CLITagJobResult{}, err
	}
	result, err := a.runCLITagJob(ctx, scope, Action{Kind: "tag.retry", TaskID: taskID, Payload: map[string]any{"operationId": operationID}})
	result.BatchID = value(records[0].Operation.BatchID)
	return result, err
}

func (a *App) runCLITagJob(ctx context.Context, scope TagJobScope, action Action) (CLITagJobResult, error) {
	snapshot, err := a.StartTagJob(ctx, scope, action)
	if err != nil {
		return CLITagJobResult{}, err
	}
	accept := func(current TagJobSnapshot) { snapshot = current }
	err = a.WatchTagJob(ctx, scope, snapshot.ID, snapshot.Sequence, accept)
	if ctx.Err() != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		err = errors.Join(err, a.CancelTagJob(cleanup, scope, snapshot.ID),
			a.WatchTagJob(cleanup, scope, snapshot.ID, snapshot.Sequence, accept))
	}
	if snapshot.Error != "" {
		err = errors.Join(err, errors.New(snapshot.Error))
	}
	items := make([]CLITagJobItem, 0, len(snapshot.Items))
	for _, item := range snapshot.Items {
		items = append(items, CLITagJobItem{OperationID: item.OperationID, SelectionKey: item.SelectionKey,
			Phase: item.Phase, Record: item.Record, Error: item.Error})
	}
	return CLITagJobResult{Task: scope.TaskID, BatchID: snapshot.BatchID, Items: items}, err
}

func (a *App) findCLITagHistory(ctx context.Context, query CLITagHistoryQuery) (string, []delivery.TagOperationReport, error) {
	if (query.Batch == "") == (query.Operation == "") {
		return "", nil, errors.New("请指定一个 --batch 或 --operation")
	}
	config, err := a.cliTagConfig(ctx)
	if err != nil {
		return "", nil, err
	}
	folders := []string{query.Task}
	if query.Task == "" {
		tasks, err := a.Manifests.List(value(config.TaskRoot))
		if err != nil {
			return "", nil, err
		}
		folders = folders[:0]
		for _, entry := range tasks {
			folders = append(folders, entry.Manifest.TaskDirectoryName)
		}
	}
	service := a.delivery(config)
	var found string
	var matches []delivery.TagOperationReport
	for _, folder := range folders {
		if err := ctx.Err(); err != nil {
			return "", nil, err
		}
		if _, err := a.Manifests.LoadTask(value(config.TaskRoot), folder); err != nil {
			return "", nil, err
		}
		records, err := service.History(ctx, folder)
		if err != nil {
			return "", nil, err
		}
		for _, record := range records {
			op := record.Operation
			if query.Batch != "" && value(op.BatchID) == query.Batch || query.Operation != "" && op.OperationID == query.Operation {
				if found != "" && found != folder {
					return "", nil, errors.New("历史 ID 匹配多个任务，请指定 --task")
				}
				found = folder
				matches = append(matches, record)
			}
		}
	}
	if len(matches) == 0 {
		return "", nil, errors.New("找不到对应 Tag 批次或操作，请检查 ID 和任务目录")
	}
	return found, matches, nil
}

func (a *App) CLITagStatus(ctx context.Context, query CLITagHistoryQuery) ([]delivery.TagObservation, error) {
	folder, records, err := a.findCLITagHistory(ctx, query)
	if err != nil {
		return nil, err
	}
	task, _, err := a.cliTagTask(ctx, folder)
	if err != nil {
		return nil, err
	}
	out := make([]delivery.TagObservation, 0, len(records))
	var queryErr error
	for _, row := range records {
		observation, err := task.Observe(ctx, row.Operation.OperationID)
		if err != nil {
			queryErr = errors.Join(queryErr, err)
			observation = delivery.TagObservation{TagOperationReport: row, Git: delivery.TagPushStatus{State: "UNKNOWN", Error: err.Error()}}
		}
		out = append(out, observation)
	}
	task.ObserveUATRuntime(ctx, out, query.UATNamespace)
	return out, errors.Join(queryErr, ctx.Err())
}
