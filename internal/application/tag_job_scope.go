package application

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"

	"silverwing-go/internal/delivery"
	"silverwing-go/internal/domain"
)

type tagTaskRunner interface {
	Config() domain.AppConfig
	Manifest() domain.TaskManifest
	Directory() string
	WithLock(func() error) error
	SelectionKeys([]string, bool) ([]string, error)
	Plan(context.Context, string, string, string) (delivery.TagTaskItem, error)
	Initialize(context.Context, delivery.TagTaskItem) error
	Run(context.Context, string, delivery.TagTaskItem, func(delivery.TagProgress)) (domain.TagOperation, *delivery.TagWorkspaceCheckReport, error)
	CancelPending(delivery.TagTaskItem) (domain.TagOperation, error)
}

func (a *App) tagJobManager() *tagJobManager {
	a.tagJobsMu.Lock()
	defer a.tagJobsMu.Unlock()
	if a.tagJobs == nil {
		a.tagJobs = &tagJobManager{app: a, jobs: map[string]*tagJob{}, resources: map[string]bool{}, probeFlights: map[string]*tagProbeFlight{}}
		a.tagJobs.openTask = func(config domain.AppConfig, id string) (tagTaskRunner, error) {
			return a.delivery(config).OpenTagTask(id)
		}
		a.tagJobs.probe = func(ctx context.Context, config domain.AppConfig, tasks []domain.TaskManifest, force bool, ids []string, emit func(string, domain.TagOperation)) (bool, error) {
			return a.delivery(config).ProbeWithProgress(ctx, config, tasks, force, ids, emit)
		}
	}
	return a.tagJobs
}

func configTagIdentity(config domain.AppConfig) string {
	encoded, _ := json.Marshal(config)
	return TagJobConfigIdentity(string(encoded))
}

func (a *App) validateTagJobScope(ctx context.Context, scope TagJobScope) (domain.AppConfig, error) {
	if err := ctx.Err(); err != nil {
		return domain.AppConfig{}, err
	}
	if scope.Home == "" || scope.TaskRoot == "" || scope.TaskID == "" || scope.TaskPath == "" || scope.TaskCreatedAt == "" || scope.ConfigIdentity == "" {
		return domain.AppConfig{}, errors.New("缺少Tag作业所属任务身份")
	}
	// Admission must never initialize a removed configuration or task tree.
	config, err := a.Config.LoadForPreview(ctx)
	if err != nil {
		return domain.AppConfig{}, err
	}
	if scope.Home != a.Paths.Home || !sameTagHistoryTaskPath(scope.TaskRoot, value(config.TaskRoot)) ||
		!sameTagHistoryTaskPath(scope.TaskPath, filepath.Join(scope.TaskRoot, scope.TaskID)) ||
		configTagIdentity(config) != scope.ConfigIdentity {
		return domain.AppConfig{}, errors.New("Tag作业配置或任务目录已变化，请重新确认")
	}
	manifest, err := a.Manifests.Load(scope.TaskPath)
	if err != nil {
		return domain.AppConfig{}, err
	}
	if manifest.CreatedAt != scope.TaskCreatedAt || manifest.TaskDirectoryName != scope.TaskID || manifest.LifecycleStatus == domain.TaskArchived {
		return domain.AppConfig{}, errors.New("Tag作业所属任务已变化或已归档")
	}
	return config, nil
}

func tagJobScopeKey(scope TagJobScope) string {
	return scope.Home + "\x00" + scope.TaskPath + "\x00" + scope.TaskCreatedAt
}

func (m *tagJobManager) prepare(ctx context.Context, scope TagJobScope, action Action, task tagTaskRunner, batchID string) ([]*tagJobWork, error) {
	if action.TaskID != scope.TaskID {
		return nil, errors.New("Tag请求与任务身份不一致")
	}
	var selections []string
	operationID := textPayload(action, "operationId")
	switch action.Kind {
	case "tag.build":
		selections = historyOperationIDs(action.Payload["selectionKeys"])
		if action.ServiceID != "" {
			selections = []string{action.ServiceID + ":" + action.ModuleID}
		}
		var err error
		selections, err = task.SelectionKeys(selections, boolPayload(action, "allServices"))
		if err != nil {
			return nil, err
		}
	case "tag.retry", "tag.workspace-check":
		if operationID == "" {
			return nil, errors.New("缺少Tag操作记录ID")
		}
		selections = []string{""}
	default:
		return nil, errors.New("不支持的Tag作业动作")
	}
	works := make([]*tagJobWork, 0, len(selections))
	for _, key := range selections {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		planned, err := task.Plan(ctx, key, operationID, batchID)
		if err != nil {
			return nil, err
		}
		record := tagRecord(scope.TaskID, delivery.DescribeTagOperation(planned.Operation, scope.TaskID))
		works = append(works, &tagJobWork{planned: planned, view: TagJobItem{ID: delivery.NewTagJobID(), Kind: action.Kind,
			SelectionKey: planned.SelectionKey, ServiceID: record.ServiceID, ModuleID: record.ModuleID,
			WorkspacePath: planned.WorkspacePath, OperationID: record.ID, Phase: TagJobPhaseQueued, Step: "等待执行", Busy: true, Queued: true, Record: &record}})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return works, nil
}
