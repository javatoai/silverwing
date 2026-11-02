package production

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"silverwing-go/internal/domain"
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

func (g CLIGenbu) Pods(ctx context.Context, cfg domain.AppConfig, env, service string) (ServicePods, error) {
	env, err := serviceEnvironment(env)
	if err != nil {
		return ServicePods{}, err
	}
	if strings.TrimSpace(service) == "" {
		return ServicePods{}, errors.New("请先配置 Genbu 服务名")
	}
	data, err := g.run(ctx, cfg, "query-service-pods", "--json", env, service)
	if err != nil {
		return ServicePods{}, err
	}
	var wire struct {
		Service     string        `json:"service"`
		Environment string        `json:"environment"`
		Pods        *[]RuntimePod `json:"pods"`
	}
	if json.Unmarshal(data, &wire) != nil || wire.Pods == nil || wire.Service == "" {
		return ServicePods{}, errors.New("无法识别 Genbu Pod 输出")
	}
	actual := strings.ToLower(wire.Environment)
	if actual == "prd" {
		actual = "prod"
	}
	if actual != env {
		return ServicePods{}, errors.New("Genbu 返回的 Pod 环境不匹配")
	}
	result := ServicePods{Service: wire.Service, Environment: env, Pods: []RuntimePod{}}
	seen := map[string]bool{}
	for _, pod := range *wire.Pods {
		if pod.Name == "" || pod.Namespace == "" {
			return ServicePods{}, errors.New("Genbu Pod 缺少实例名称或 namespace")
		}
		key := pod.Namespace + "\x00" + pod.Name
		if !seen[key] {
			result.Pods = append(result.Pods, pod)
			seen[key] = true
		}
	}
	return result, nil
}

func (g CLIGenbu) ServiceConfig(ctx context.Context, cfg domain.AppConfig, env, service, namespace string) (string, error) {
	env, err := serviceEnvironment(env)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(service) == "" {
		return "", errors.New("请先配置 Genbu 服务名")
	}
	args := []string{"query-service-config", env, service}
	if namespace != "" {
		args = append(args, "-namespace", namespace)
	}
	data, err := g.run(ctx, cfg, args...)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
