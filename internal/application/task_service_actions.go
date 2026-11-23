package application

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/production"
)

type TaskServicesContext struct {
	TaskFilesContext
	GroupID          string `json:"groupId"`
	GenbuServiceName string `json:"genbuServiceName"`
	QueryIdentity    string `json:"queryIdentity"`
}

// ServiceRuntimeContext binds catalog actions to the active profile and its saved Genbu mapping.
type ServiceRuntimeContext struct {
	Home             string `json:"home"`
	GenbuServiceName string `json:"genbuServiceName"`
	QueryIdentity    string `json:"queryIdentity"`
}

const (
	taskServicesActionPrefix   = "task.services."
	serviceRuntimeActionPrefix = "service.runtime."
	serviceRuntimeContextKey   = "serviceRuntimeContext"
)

type TaskServicePodsResult struct {
	ServiceID        string                  `json:"serviceId"`
	GenbuServiceName string                  `json:"genbuServiceName"`
	Environment      string                  `json:"environment"`
	FetchedAt        string                  `json:"fetchedAt"`
	Pods             []production.RuntimePod `json:"pods"`
}

type TaskServiceConfigResult struct {
	ServiceID        string `json:"serviceId"`
	GenbuServiceName string `json:"genbuServiceName"`
	Environment      string `json:"environment"`
	Namespace        string `json:"namespace"`
	FetchedAt        string `json:"fetchedAt"`
	Text             string `json:"text"`
}

// The read is bound to the configured Genbu switch. Credential generations
// are checked by the connection bound to the execution of the read.
func TaskServiceQueryIdentity(cfg domain.AppConfig) string {
	data, _ := json.Marshal(cfg.OptionalCLIs.Genbu)
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

var taskServiceReadSlots = make(chan struct{}, 4)
var taskServiceNamespace = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

type taskServiceReader interface {
	Pods(context.Context, domain.AppConfig, string, string) (production.ServicePods, error)
	ServiceConfig(context.Context, domain.AppConfig, string, string, string) (string, error)
}

func (a *App) taskServicesAction(ctx context.Context, action Action) (ActionResult, error) {
	operation := taskServiceOperation(action.Kind)
	if operation == "config-preview-discard" {
		return a.taskServiceGenbuAction(ctx, action, nil)
	}
	client, err := a.genbuClient(ctx)
	if err != nil {
		return ActionResult{}, err
	}
	switch operation {
	case "pods", "config":
		result, err := a.taskServicesActionWithReader(ctx, action, client)
		if scopeErr := client.Require(ctx); scopeErr != nil {
			return ActionResult{}, scopeErr
		}
		return result, err
	default:
		return a.taskServiceGenbuAction(ctx, action, client)
	}
}

func (a *App) taskServicesActionWithReader(ctx context.Context, action Action, reader taskServiceReader) (ActionResult, error) {
	operation := taskServiceOperation(action.Kind)
	if operation != "pods" && operation != "config" {
		return ActionResult{}, errors.New("服务信息仅支持 Pod 和配置读取")
	}
	scope, err := serviceScopeForAction(action)
	if err != nil {
		return ActionResult{}, err
	}
	env := scope.Environment
	namespace := strings.TrimSpace(textPayload(action, "namespace"))
	if operation == "pods" {
		namespace = ""
	}
	if namespace != "" && (len(namespace) > 63 || !taskServiceNamespace.MatchString(namespace)) {
		return ActionResult{}, errors.New("namespace 格式无效")
	}
	cfg, service, release, err := a.beginTaskServiceQuery(ctx, action, scope)
	if err != nil {
		return ActionResult{}, err
	}
	defer release()
	var result ActionResult
	stamp := func() string { return time.Now().UTC().Format(time.RFC3339Nano) }
	if operation == "pods" {
		pods, err := reader.Pods(ctx, cfg, env, service.GenbuServiceName)
		if err != nil {
			return ActionResult{}, err
		}
		result.Data = TaskServicePodsResult{ServiceID: service.ID, GenbuServiceName: service.GenbuServiceName, Environment: env, FetchedAt: stamp(), Pods: pods.Pods}
	} else {
		text, readErr := reader.ServiceConfig(ctx, cfg, env, service.GenbuServiceName, namespace)
		if readErr != nil {
			// Namespace candidates, like a successful read, must belong to the active service.
			var ambiguity *production.ServiceConfigNamespaceError
			if errors.As(readErr, &ambiguity) {
				if _, _, err := a.taskServiceIdentity(ctx, action, scope); err != nil {
					return ActionResult{}, err
				}
			}
			return ActionResult{}, readErr
		}
		result.Data = TaskServiceConfigResult{ServiceID: service.ID, GenbuServiceName: service.GenbuServiceName, Environment: env, Namespace: namespace, FetchedAt: stamp(), Text: text}
	}
	if _, _, err = a.taskServiceIdentity(ctx, action, scope); err != nil {
		return ActionResult{}, err
	}
	return result, nil
}

func taskServiceOperation(kind string) string {
	if operation, ok := strings.CutPrefix(kind, serviceRuntimeActionPrefix); ok {
		return operation
	}
	if operation, ok := strings.CutPrefix(kind, taskServicesActionPrefix); ok {
		return operation
	}
	return ""
}

func serviceScopeForAction(action Action) (genbuConfigPreviewScope, error) {
	env, err := taskServiceEnvironment(action)
	scope := genbuConfigPreviewScope{TaskID: action.TaskID, ServiceID: action.ServiceID, Environment: env}
	if err != nil {
		return scope, err
	}
	if !strings.HasPrefix(action.Kind, serviceRuntimeActionPrefix) {
		scope.Context, err = taskServicesContextForAction(action)
		return scope, err
	}
	if action.TaskID != "" || action.ModuleID != "" || action.Path != "" || action.Text != "" || action.ServiceID == "" ||
		decodeField(action, serviceRuntimeContextKey, &scope.Runtime) != nil || scope.Runtime.Home == "" ||
		scope.Runtime.GenbuServiceName == "" || scope.Runtime.QueryIdentity == "" {
		return scope, errors.New("服务列表运行操作上下文无效，请重新打开服务")
	}
	return scope, nil
}

func taskServicesContextForAction(action Action) (TaskServicesContext, error) {
	var expected TaskServicesContext
	raw, ok := action.Payload["taskServicesContext"]
	data, err := json.Marshal(raw)
	if !ok || err != nil || json.Unmarshal(data, &expected) != nil {
		return expected, errors.New("服务信息读取上下文缺失")
	}
	copyAction := action
	copyAction.Payload = map[string]any{"taskFilesContext": raw}
	_, err = taskFilesContext(copyAction)
	return expected, err
}

func taskServiceEnvironment(action Action) (string, error) {
	env := textPayload(action, "environment")
	if env != "uat" && env != "prod" {
		return "", errors.New("服务环境必须为 UAT 或 PROD")
	}
	return env, nil
}

func acquireTaskServiceReadSlot(ctx context.Context) (func(), error) {
	select {
	case taskServiceReadSlots <- struct{}{}:
		return func() { <-taskServiceReadSlots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (a *App) beginTaskServiceQuery(ctx context.Context, action Action, scope genbuConfigPreviewScope) (domain.AppConfig, domain.ServiceConfig, func(), error) {
	cfg, service, err := a.taskServiceIdentity(ctx, action, scope)
	if err != nil {
		return cfg, service, nil, err
	}
	release, err := acquireTaskServiceReadSlot(ctx)
	if err != nil {
		return cfg, service, nil, err
	}
	if err := a.RequireOptionalCLI(ctx, "genbu"); err != nil {
		release()
		return cfg, service, nil, err
	}
	cfg, service, err = a.taskServiceIdentity(ctx, action, scope)
	if err != nil {
		release()
		return cfg, service, nil, err
	}
	return cfg, service, release, nil
}

func (a *App) taskServiceIdentity(ctx context.Context, action Action, scope genbuConfigPreviewScope) (domain.AppConfig, domain.ServiceConfig, error) {
	cfg, err := a.Config.LoadForPreview(ctx)
	if err != nil {
		return cfg, domain.ServiceConfig{}, err
	}
	if scope.Runtime.Home != "" {
		service, err := a.catalogServiceIdentity(cfg, scope.ServiceID, scope.Runtime)
		return cfg, service, err
	}
	expected := scope.Context
	manifest, _, err := a.taskFilesIdentityForConfig(action, expected.TaskFilesContext, cfg)
	if err != nil {
		return cfg, domain.ServiceConfig{}, err
	}
	if expected.QueryIdentity != TaskServiceQueryIdentity(cfg) {
		return cfg, domain.ServiceConfig{}, errors.New("任务或 Genbu 配置已变化，请刷新服务信息")
	}
	service, err := cfg.Service(action.ServiceID)
	if err != nil {
		return cfg, service, err
	}
	if !service.GenbuProbeEnabled {
		return cfg, service, errors.New("此服务未开启 Genbu 探测")
	}
	member := false
	for _, workspace := range manifest.Services {
		if workspace.GroupServiceID == service.ID || (workspace.GroupServiceID == "" && workspace.ServiceName == service.DisplayName) {
			member = true
			break
		}
	}
	if !member || service.GenbuServiceName == "" || service.GenbuServiceName != expected.GenbuServiceName {
		return cfg, service, errors.New("服务已移除或 Genbu 服务映射已变化")
	}
	return cfg, service, nil
}

func (a *App) catalogServiceIdentity(cfg domain.AppConfig, serviceID string, expected ServiceRuntimeContext) (domain.ServiceConfig, error) {
	if !sameTaskContextPath(expected.Home, a.Paths.Home) || expected.QueryIdentity != TaskServiceQueryIdentity(cfg) {
		return domain.ServiceConfig{}, errors.New("数据目录或 Genbu 配置已变化，请重新打开服务")
	}
	service, err := cfg.Service(serviceID)
	if err != nil {
		return service, err
	}
	if !service.Enabled {
		return service, errors.New("此服务已停用")
	}
	if !service.GenbuProbeEnabled {
		return service, errors.New("此服务未开启 Genbu 探测")
	}
	if service.GenbuServiceName == "" || service.GenbuServiceName != expected.GenbuServiceName {
		return service, errors.New("Genbu 服务映射已变化，请重新打开服务")
	}
	return service, nil
}
