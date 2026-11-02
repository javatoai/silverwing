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

// No credentials are read. This identifies only the existing configured CLI
// execution context; proxy secrets participate only in a one-way digest.
func TaskServiceQueryIdentity(cfg domain.AppConfig) string {
	data, _ := json.Marshal([]any{cfg.GenbuExecutablePath, cfg.CommandProxyURL, cfg.CommandProxyNoProxy, cfg.CommandProxyUsername, cfg.CommandProxyPassword, cfg.CommandProxyTargets})
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

var taskServiceReadSlots = make(chan struct{}, 4)
var taskServiceNamespace = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

type taskServiceReader interface {
	Pods(context.Context, domain.AppConfig, string, string) (production.ServicePods, error)
	ServiceConfig(context.Context, domain.AppConfig, string, string, string) (string, error)
}

func (a *App) taskServicesAction(ctx context.Context, action Action) (ActionResult, error) {
	return a.taskServicesActionWithReader(ctx, action, production.CLIGenbu{Runner: a.Runner})
}

func (a *App) taskServicesActionWithReader(ctx context.Context, action Action, reader taskServiceReader) (ActionResult, error) {
	if action.Kind != "task.services.pods" && action.Kind != "task.services.config" {
		return ActionResult{}, errors.New("服务信息仅支持 Pod 和配置读取")
	}
	var expected TaskServicesContext
	raw, ok := action.Payload["taskServicesContext"]
	data, err := json.Marshal(raw)
	if !ok || err != nil || json.Unmarshal(data, &expected) != nil {
		return ActionResult{}, errors.New("服务信息读取上下文缺失")
	}
	copyAction := action
	copyAction.Payload = map[string]any{"taskFilesContext": raw}
	if _, err := taskFilesContext(copyAction); err != nil {
		return ActionResult{}, err
	}
	env := textPayload(action, "environment")
	if env != "uat" && env != "prod" {
		return ActionResult{}, errors.New("服务环境必须为 UAT 或 PROD")
	}
	namespace := strings.TrimSpace(textPayload(action, "namespace"))
	if action.Kind == "task.services.pods" {
		namespace = ""
	}
	if action.Kind == "task.services.config" && namespace != "" && (len(namespace) > 63 || !taskServiceNamespace.MatchString(namespace)) {
		return ActionResult{}, errors.New("namespace 格式无效")
	}
	resolve := func() (domain.AppConfig, domain.GroupServiceConfig, error) {
		cfg, err := a.Config.LoadForPreview(ctx)
		if err != nil {
			return cfg, domain.GroupServiceConfig{}, err
		}
		manifest, _, err := a.taskFilesIdentity(ctx, action, expected.TaskFilesContext)
		if err != nil {
			return cfg, domain.GroupServiceConfig{}, err
		}
		groupID := manifest.GroupID
		if groupID == "" {
			groupID = domain.DefaultGroupID
		}
		if expected.GroupID != groupID || expected.QueryIdentity != TaskServiceQueryIdentity(cfg) {
			return cfg, domain.GroupServiceConfig{}, errors.New("任务或 Genbu 配置已变化，请刷新服务信息")
		}
		service, err := cfg.GroupService(groupID, action.ServiceID)
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
			}
		}
		if !member || service.GenbuServiceName == "" || service.GenbuServiceName != expected.GenbuServiceName {
			return cfg, service, errors.New("服务已移除或 Genbu 服务映射已变化")
		}
		return cfg, service, nil
	}
	_, _, err = resolve()
	if err != nil {
		return ActionResult{}, err
	}
	select {
	case taskServiceReadSlots <- struct{}{}:
		defer func() { <-taskServiceReadSlots }()
	case <-ctx.Done():
		return ActionResult{}, ctx.Err()
	}
	cfg, service, err := resolve()
	if err != nil {
		return ActionResult{}, err
	}
	var result ActionResult
	stamp := func() string { return time.Now().UTC().Format(time.RFC3339Nano) }
	if action.Kind == "task.services.pods" {
		pods, err := reader.Pods(ctx, cfg, env, service.GenbuServiceName)
		if err != nil {
			return ActionResult{}, err
		}
		result.Data = TaskServicePodsResult{ServiceID: service.ID, GenbuServiceName: service.GenbuServiceName, Environment: env, FetchedAt: stamp(), Pods: pods.Pods}
	} else {
		text, err := reader.ServiceConfig(ctx, cfg, env, service.GenbuServiceName, namespace)
		if err != nil {
			return ActionResult{}, err
		}
		result.Data = TaskServiceConfigResult{ServiceID: service.ID, GenbuServiceName: service.GenbuServiceName, Environment: env, Namespace: namespace, FetchedAt: stamp(), Text: text}
	}
	if _, _, err = resolve(); err != nil {
		return ActionResult{}, err
	}
	return result, nil
}
