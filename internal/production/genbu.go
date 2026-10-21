package production

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"silverwing-go/internal/commandruntime"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/executable"
	"silverwing-go/internal/process"
)

type Runner interface {
	Run(context.Context, process.Spec) (process.Result, error)
}

type Genbu interface {
	Versions(context.Context, domain.AppConfig, string) ([]string, error)
	Status(context.Context, domain.AppConfig, string, string) (domain.GenbuTagProbeStatus, error)
}

type CLIGenbu struct{ Runner Runner }

func unknownStatus() domain.GenbuTagProbeStatus { return normalizeStatus(domain.GenbuTagProbeStatus{}) }
func normalizeStatus(s domain.GenbuTagProbeStatus) domain.GenbuTagProbeStatus {
	if s.Build == "" {
		s.Build = domain.GenbuUnknown
	}
	if s.UAT == "" {
		s.UAT = domain.GenbuUnknown
	}
	if s.Production == "" {
		s.Production = domain.GenbuUnknown
	}
	return s
}

func (g CLIGenbu) run(ctx context.Context, config domain.AppConfig, arguments ...string) ([]byte, error) {
	command := ""
	if config.GenbuExecutablePath != nil {
		command = *config.GenbuExecutablePath
	}
	spec, err := commandruntime.Prepare(ctx, g.Runner, &config, domain.ProxyTargetGenbu, executable.ToolGenbu, command)
	if err != nil {
		return nil, err
	}
	spec.Arguments, spec.Timeout = arguments, 20*time.Second
	spec.MaxStdoutBytes, spec.MaxStderrBytes, spec.KillOnOutputLimit = 4<<20, 64<<10, true
	result, err := g.Runner.Run(ctx, spec)
	if err != nil {
		return nil, errors.New(commandruntime.Redact(config, err.Error()))
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("Genbu 查询失败（%d）：%s", result.ExitCode, commandruntime.Redact(config, strings.TrimSpace(result.Stderr+"\n"+result.Stdout)))
	}
	return []byte(result.Stdout), nil
}

func (g CLIGenbu) Versions(ctx context.Context, config domain.AppConfig, service string) ([]string, error) {
	if strings.TrimSpace(service) == "" {
		return nil, errors.New("请先为服务配置 Genbu 服务名")
	}
	data, err := g.run(ctx, config, "query-service-pods", "--json", "prod", service)
	if err != nil {
		return nil, err
	}
	var response struct {
		Service     string `json:"service"`
		Environment string `json:"environment"`
		Pods        *[]struct {
			Version string `json:"app_version"`
		} `json:"pods"`
	}
	if err := json.Unmarshal(data, &response); err != nil || response.Pods == nil {
		return nil, errors.New("无法识别 Genbu 生产 Pod 输出")
	}
	if !strings.EqualFold(response.Environment, "PRD") && !strings.EqualFold(response.Environment, "prod") {
		return nil, errors.New("Genbu 返回的环境不是生产环境，请重新检查")
	}
	// Read runtime versions only; Pod presence is not proof of pipeline success.
	seen := map[string]bool{}
	for _, pod := range *response.Pods {
		v := strings.TrimSpace(pod.Version)
		if v == "" {
			return nil, errors.New("生产 Pod 缺少运行版本，请手工确认生产基线")
		}
		seen[v] = true
	}
	versions := make([]string, 0, len(seen))
	for v := range seen {
		versions = append(versions, v)
	}
	sort.Strings(versions)
	return versions, nil
}

func (g CLIGenbu) Status(ctx context.Context, config domain.AppConfig, service, tag string) (domain.GenbuTagProbeStatus, error) {
	status := unknownStatus()
	checked := timestamp()
	status.CheckedAt = &checked
	data, err := g.run(ctx, config, "query-tag", "--json", service, tag)
	if err != nil {
		return status, err
	}
	var response struct {
		Build        string  `json:"build_status"`
		UAT          string  `json:"uat_status"`
		Production   string  `json:"production_status"`
		BuiltAt      *string `json:"build_completed_at"`
		UATAt        *string `json:"uat_completed_at"`
		ProductionAt *string `json:"production_completed_at"`
	}
	if err = json.Unmarshal(data, &response); err != nil || response.Build == "" {
		return status, errors.New("无法识别 Genbu Tag 输出")
	}
	stage := func(v string) domain.GenbuStageStatus {
		switch v {
		case "成功":
			return domain.GenbuSuccess
		case "失败":
			return domain.GenbuFailed
		case "构建中":
			return domain.GenbuBuilding
		case "初始":
			return domain.GenbuInitial
		default:
			return domain.GenbuUnknown
		}
	}
	status.Build, status.UAT, status.Production = stage(response.Build), stage(response.UAT), stage(response.Production)
	status.BuiltCompletedAt, status.ReleasedCompletedAt, status.ProductionReleasedCompletedAt = response.BuiltAt, response.UATAt, response.ProductionAt
	return status, nil
}
