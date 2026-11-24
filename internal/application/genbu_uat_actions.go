package application

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"strings"

	"silverwing-go/internal/delivery"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

// NewUATCLI opens an existing profile without desktop startup or task writes.
func NewUATCLI(home string) (*App, error) {
	paths, _, err := applicationPaths(home)
	if err != nil {
		return nil, err
	}
	paths.ProfileID, err = storage.ReadProfileID(paths.Home)
	if err != nil {
		return nil, fmt.Errorf("读取 Silverwing 数据目录的实例标识失败：%w", err)
	}
	return composeApp(paths, storage.NewProfileManifestStore(paths)), nil
}

type CLIUATOperationRequest struct {
	Action, Service, Tag, Preflight string
}

type CLIUATOperationResult struct {
	Action       string `json:"action"`
	Status       string `json:"status"`
	Service      string `json:"service"`
	GenbuService string `json:"genbuService"`
	Environment  string `json:"environment"`
	GroupID      int    `json:"groupId"`
	Group        string `json:"group"`
	Namespace    string `json:"namespace"`
	Tag          string `json:"tag,omitempty"`
	PodCount     int    `json:"podCount"`
	Preflight    string `json:"preflight,omitempty"`
	EnableStatus string `json:"enableStatus,omitempty"`
}

type uatServiceTarget struct {
	ServiceID, Service, GenbuService, RepositoryID, QueryIdentity  string
	TaskPath, TaskCreatedAt, HistoryID, HistoryCreatedAt, ModuleID string
	BatchIdentity                                                  string `json:"batchIdentity,omitempty"`
}

type uatServiceResolver func(context.Context) (domain.AppConfig, uatServiceTarget, error)

func (a *App) CLIUATOperation(ctx context.Context, request CLIUATOperationRequest) (CLIUATOperationResult, error) {
	client, err := a.genbuClient(ctx)
	if err != nil {
		return CLIUATOperationResult{}, err
	}
	return a.runUATPodOperation(ctx, request, request.Preflight != "", a.cliUATServiceResolver(request.Service), client)
}

func (a *App) cliUATServiceResolver(selector string) uatServiceResolver {
	return func(ctx context.Context) (domain.AppConfig, uatServiceTarget, error) {
		cfg, service, err := a.CLIResolveGenbuService(ctx, selector)
		if err != nil {
			return cfg, uatServiceTarget{}, err
		}
		return uatConfiguredTarget(cfg, service)
	}
}

func (a *App) uatPodAction(ctx context.Context, action Action) (ActionResult, error) {
	if rawEnv, ok := action.Payload["environment"]; ok {
		if env, ok := rawEnv.(string); !ok || !strings.EqualFold(env, "uat") {
			return ActionResult{}, errors.New("此操作仅支持 UAT")
		}
	}
	request := CLIUATOperationRequest{
		Action: textPayload(action, "operation"), Tag: textPayload(action, "tag"), Preflight: textPayload(action, "preflight"),
	}
	resolve, err := a.uatActionServiceResolver(action, request)
	if err != nil {
		return ActionResult{}, err
	}
	client, err := a.genbuClient(ctx)
	if err != nil {
		return ActionResult{}, err
	}
	if taskServiceOperation(action.Kind) == "uat-tags" {
		page, err := numberPayload(action, "page")
		if err != nil {
			return ActionResult{}, err
		}
		release, err := acquireTaskServiceReadSlot(ctx)
		if err != nil {
			return ActionResult{}, err
		}
		defer release()
		cfg, target, err := resolve(ctx)
		if err != nil {
			return ActionResult{}, err
		}
		result, err := client.TagList(ctx, cfg, target.GenbuService, textPayload(action, "keyword"), page)
		if err != nil {
			return ActionResult{}, err
		}
		_, current, err := resolve(ctx)
		if err != nil {
			return ActionResult{}, err
		}
		if current != target {
			return ActionResult{}, errors.New("Genbu 服务映射已变化，请重新打开发布")
		}
		return ActionResult{Data: result}, nil
	}
	result, err := a.runUATPodOperation(ctx, request, strings.HasSuffix(action.Kind, ".uat-submit"), resolve, client)
	if result.Status == "" {
		return ActionResult{}, err
	}
	return ActionResult{Data: result}, err
}

func (a *App) uatActionServiceResolver(action Action, request CLIUATOperationRequest) (uatServiceResolver, error) {
	if strings.HasPrefix(action.Kind, "tag.history.") {
		if request.Action != "release" {
			return nil, errors.New("Tag 历史仅支持发布到 UAT")
		}
		return func(ctx context.Context) (domain.AppConfig, uatServiceTarget, error) {
			return a.uatTagHistoryTarget(ctx, action, request.Tag)
		}, nil
	}
	action.Payload = maps.Clone(action.Payload)
	if action.Payload == nil {
		action.Payload = make(map[string]any)
	}
	action.Payload["environment"] = "uat"
	scope, err := serviceScopeForAction(action)
	if err != nil {
		return nil, err
	}
	if scope.Context.Archived {
		return nil, errors.New("已归档的任务不能执行 UAT 操作")
	}
	return func(ctx context.Context) (domain.AppConfig, uatServiceTarget, error) {
		cfg, service, err := a.taskServiceIdentity(ctx, action, scope)
		if err != nil {
			return cfg, uatServiceTarget{}, err
		}
		cfg, target, err := uatConfiguredTarget(cfg, service)
		target.TaskPath, target.TaskCreatedAt = scope.Context.TaskPath, scope.Context.TaskCreatedAt
		return cfg, target, err
	}, nil
}

func uatConfiguredTarget(cfg domain.AppConfig, service domain.ServiceConfig) (domain.AppConfig, uatServiceTarget, error) {
	if err := validateConfiguredGenbuService(service); err != nil {
		return cfg, uatServiceTarget{}, err
	}
	return cfg, uatServiceTarget{ServiceID: service.ID, Service: service.DisplayName, GenbuService: service.GenbuServiceName,
		RepositoryID: service.RepositoryID, QueryIdentity: TaskServiceQueryIdentity(cfg)}, nil
}

func (a *App) uatTagHistoryTarget(ctx context.Context, action Action, tag string) (domain.AppConfig, uatServiceTarget, error) {
	cfg, err := a.Config.LoadForPreview(ctx)
	if err != nil {
		return cfg, uatServiceTarget{}, err
	}
	_, tasks, err := a.tagHistoryActionScope(cfg, action)
	if err != nil {
		return cfg, uatServiceTarget{}, err
	}
	task := tasks[0]
	if task.LifecycleStatus == domain.TaskArchived {
		return cfg, uatServiceTarget{}, errors.New("已归档的任务不能执行 UAT 操作")
	}
	path := filepath.Join(*cfg.TaskRoot, task.TaskDirectoryName)
	op, err := delivery.ReadTagHistoryOperation(path, textPayload(action, "operationId"))
	if err != nil {
		return cfg, uatServiceTarget{}, err
	}
	if strings.HasPrefix(action.Kind, "tag.history.uat-batch-") && uatTagBatchGroupID(op) != textPayload(action, "groupId") {
		return cfg, uatServiceTarget{}, errors.New("Tag 记录已移出当前批次，请刷新 Tag 历史")
	}
	if action.ServiceID == "" || action.ModuleID == "" || action.ServiceID != op.GroupServiceID || action.ModuleID != op.ModuleID || tag != value(op.Tag) ||
		!domain.TagGenbuProbeEligible(op.State, tag) || op.GenbuStatus.Build != domain.GenbuSuccess || op.GenbuStatus.NotFound || value(op.GenbuStatus.FailureReason) != "" {
		return cfg, uatServiceTarget{}, errors.New("当前 Tag 记录身份不匹配或尚未构建成功，请刷新 Tag 历史")
	}
	service, err := cfg.Service(op.GroupServiceID)
	if err != nil {
		return cfg, uatServiceTarget{}, err
	}
	if op.RepositoryID != service.RepositoryID {
		return cfg, uatServiceTarget{}, errors.New("Tag 记录的仓库已变化，请刷新 Tag 历史")
	}
	for _, ws := range task.Services {
		if ws.GroupServiceID == service.ID && ws.ModuleID == op.ModuleID && ws.RepositoryID == op.RepositoryID {
			cfg, target, err := uatConfiguredTarget(cfg, service)
			target.TaskPath, target.TaskCreatedAt = path, task.CreatedAt
			target.HistoryID, target.HistoryCreatedAt, target.ModuleID = op.OperationID, op.CreatedAt, op.ModuleID
			return cfg, target, err
		}
	}
	return cfg, uatServiceTarget{}, errors.New("Tag 记录的服务模块已从任务移除，请刷新 Tag 历史")
}
