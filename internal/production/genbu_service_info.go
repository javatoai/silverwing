package production

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/snowball/genbuapi"
)

// RuntimePod describes an observed instance, not a desired replica or a release.
type RuntimePod struct {
	GroupName   string `json:"group_name"`
	Namespace   string `json:"namespace"`
	Name        string `json:"pod_name"`
	Version     string `json:"app_version"`
	Restarts    int    `json:"restart_count"`
	Phase       string `json:"phase"`
	Ready       bool   `json:"ready"`
	CreatedTime string `json:"created_time"`
}

type ServicePods struct {
	Service     string       `json:"service"`
	Environment string       `json:"environment"`
	Pods        []RuntimePod `json:"pods"`
}

func serviceEnvironment(env string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(env)) {
	case "uat":
		return "uat", nil
	case "prod":
		return "prod", nil
	default:
		return "", errors.New("请选择 UAT 或 PROD 环境")
	}
}

func (g NativeGenbu) Pods(ctx context.Context, cfg domain.AppConfig, env, service string) (ServicePods, error) {
	wire, err := g.queryPods(ctx, cfg, env, service)
	if err != nil {
		return ServicePods{}, err
	}
	result := ServicePods{Service: wire.Service, Environment: genbuResponseEnvironment(wire.Environment), Pods: []RuntimePod{}}
	seen := make(map[string]bool, len(wire.Pods))
	for _, pod := range wire.Pods {
		if !genbuIdentifier(pod.Name) || !genbuIdentifier(pod.Namespace) {
			return ServicePods{}, errors.New("Genbu Pod 缺少有效的实例名称或 namespace")
		}
		key := pod.Namespace + "\x00" + pod.Name
		if !seen[key] {
			result.Pods = append(result.Pods, RuntimePod{
				GroupName: pod.GroupName, Namespace: pod.Namespace, Name: pod.Name, Version: pod.AppVersion,
				Restarts: pod.RestartCount, Phase: pod.Phase, Ready: pod.Ready, CreatedTime: pod.CreatedTime,
			})
			seen[key] = true
		}
	}
	return result, nil
}

func (g NativeGenbu) queryPods(ctx context.Context, cfg domain.AppConfig, env, service string) (genbuapi.PodSnapshot, error) {
	env, err := serviceEnvironment(env)
	if err != nil {
		return genbuapi.PodSnapshot{}, err
	}
	service = strings.TrimSpace(service)
	if !genbuName(service) {
		return genbuapi.PodSnapshot{}, errors.New("请先配置有效的 Genbu 服务名")
	}
	ctx, client, err := g.client(ctx, cfg)
	if err != nil {
		return genbuapi.PodSnapshot{}, err
	}
	wire, err := client.QueryServicePods(ctx, genbuapi.ServicePodsRequest{Service: service, Environment: env})
	if scopeErr := checkGenbuReadScope(ctx); scopeErr != nil {
		return genbuapi.PodSnapshot{}, scopeErr
	}
	if err != nil {
		return genbuapi.PodSnapshot{}, genbuReadError(ctx, cfg, client, err)
	}
	if genbuResponseEnvironment(wire.Environment) != env || wire.Service != genbuapi.ResolveService(service).Name {
		return genbuapi.PodSnapshot{}, errors.New("Genbu 返回的 Pod 服务或环境不匹配")
	}
	return wire, nil
}

func (g NativeGenbu) ServiceConfig(ctx context.Context, cfg domain.AppConfig, env, service, namespace string) (string, error) {
	env, err := serviceEnvironment(env)
	if err != nil {
		return "", err
	}
	service, namespace = strings.TrimSpace(service), strings.TrimSpace(namespace)
	if !genbuName(service) {
		return "", errors.New("请先配置有效的 Genbu 服务名")
	}
	if namespace != "" && !serviceConfigNamespaceName.MatchString(namespace) {
		return "", errors.New("请选择有效的 Genbu namespace")
	}
	ctx, client, err := g.client(ctx, cfg)
	if err != nil {
		return "", err
	}
	wire, err := client.QueryServiceConfig(ctx, genbuapi.ServiceConfigRequest{Service: service, Environment: env, Namespace: namespace})
	if scopeErr := checkGenbuReadScope(ctx); scopeErr != nil {
		return "", scopeErr
	}
	if err != nil {
		var ambiguity *genbuapi.NamespaceAmbiguityError
		if namespace == "" && errors.As(err, &ambiguity) && genbuResponseEnvironment(ambiguity.Environment) == env && len(ambiguity.Candidates) > 1 {
			for _, candidate := range ambiguity.Candidates {
				if !serviceConfigNamespaceName.MatchString(candidate) {
					return "", errors.New("Genbu 返回的 namespace 无效")
				}
			}
			return "", &ServiceConfigNamespaceError{Candidates: ambiguity.Candidates}
		}
		return "", genbuReadError(ctx, cfg, client, err)
	}
	if genbuResponseEnvironment(wire.Environment) != env || wire.Service != genbuapi.ResolveService(service).Name ||
		namespace != "" && wire.Namespace != namespace {
		return "", errors.New("Genbu 返回的配置目标与查询不匹配")
	}
	return genbuDisplayText(cfg, wire.Content, "暂无 "+strings.ToUpper(wire.Environment)+" 配置")
}

type ServiceConfigNamespaceError struct {
	Candidates []string
}

func (e *ServiceConfigNamespaceError) Error() string { return "请选择配置所在的 namespace" }

var serviceConfigNamespaceName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
