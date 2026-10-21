package platform

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"runtime"
	"strings"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
)

type gitConfigValue struct {
	Key    string  `json:"key"`
	Value  string  `json:"value"`
	Origin *string `json:"origin,omitempty"`
}

type gitEnvironmentSnapshot struct {
	GitExecutable           *string          `json:"gitExecutable,omitempty"`
	GitExecutableSource     string           `json:"gitExecutableSource"`
	GitVersion              *string          `json:"gitVersion,omitempty"`
	SystemUser              string           `json:"systemUser"`
	GlobalUserName          *gitConfigValue  `json:"globalUserName,omitempty"`
	GlobalUserEmail         *gitConfigValue  `json:"globalUserEmail,omitempty"`
	GlobalCredentialHelpers []gitConfigValue `json:"globalCredentialHelpers"`
	GlobalKeyConfig         []gitConfigValue `json:"globalKeyConfig"`
	Errors                  []string         `json:"errors"`
	CopyText                string           `json:"copyText"`
}

func (s *Service) gitEnvironment(ctx context.Context) (any, error) {
	cfg, err := s.loadConfig()
	if err != nil {
		return nil, err
	}
	cfg, resolution, _, err := s.resolveCLIExecutableWithSource(ctx, cfg, "git")
	if err != nil {
		return nil, err
	}
	executable := resolution.Command
	snapshot := gitEnvironmentSnapshot{
		GitExecutable:           stringPtr(executable),
		GitExecutableSource:     string(resolution.Source),
		SystemUser:              systemUserName(),
		GlobalCredentialHelpers: []gitConfigValue{},
		GlobalKeyConfig:         []gitConfigValue{},
		Errors:                  []string{},
	}
	versionResult, versionErr := s.runTool(ctx, cfg, domain.ProxyTargetGit, executable, []string{"--version"}, "", 10*time.Second)
	if process.IsCleanupFailure(versionErr) {
		return nil, versionErr
	}
	if versionErr != nil || versionResult.ExitCode != 0 {
		snapshot.Errors = append(snapshot.Errors, "读取 Git 版本失败："+commandFailure(versionResult, versionErr))
	} else if version := firstNonBlank(strings.TrimSpace(versionResult.Stdout), strings.TrimSpace(versionResult.Stderr)); version != "" {
		snapshot.GitVersion = &version
	}
	configResult, configErr := s.runTool(ctx, cfg, domain.ProxyTargetGit, executable, []string{"config", "--global", "--show-origin", "--list"}, "", 10*time.Second)
	if process.IsCleanupFailure(configErr) {
		return nil, configErr
	}
	if configErr != nil || configResult.ExitCode != 0 {
		snapshot.Errors = append(snapshot.Errors, "读取全局 Git 配置失败："+commandFailure(configResult, configErr))
	} else {
		values := parseGitConfigValues(configResult.Stdout)
		for _, value := range values {
			switch value.Key {
			case "user.name":
				copy := value
				snapshot.GlobalUserName = &copy
			case "user.email":
				copy := value
				snapshot.GlobalUserEmail = &copy
			case "credential.helper":
				snapshot.GlobalCredentialHelpers = append(snapshot.GlobalCredentialHelpers, value)
			}
			if isGitKeyConfig(value.Key) {
				snapshot.GlobalKeyConfig = append(snapshot.GlobalKeyConfig, value)
			}
		}
	}
	snapshot.CopyText = formatGitEnvironment(snapshot)
	return snapshot, nil
}

func parseGitConfigValues(output string) []gitConfigValue {
	values := []gitConfigValue{}
	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		origin := ""
		config := line
		if at := strings.IndexByte(line, '\t'); at >= 0 {
			origin = line[:at]
			config = line[at+1:]
		}
		separator := strings.IndexByte(config, '=')
		key, value := config, ""
		if separator >= 0 {
			key, value = config[:separator], config[separator+1:]
		}
		entry := gitConfigValue{Key: key, Value: value}
		if origin != "" {
			entry.Origin = stringPtr(origin)
		}
		values = append(values, entry)
	}
	return values
}

func isGitKeyConfig(key string) bool {
	switch key {
	case "user.name", "user.email", "credential.helper", "core.autocrlf", "core.ignorecase", "core.longpaths", "pull.rebase", "push.default", "init.defaultbranch", "commit.gpgsign", "user.signingkey":
		return true
	default:
		return false
	}
}

func formatGitEnvironment(snapshot gitEnvironmentSnapshot) string {
	lines := []string{
		"Git 可执行文件：" + pointerOr(snapshot.GitExecutable, "未读取到"),
		"Git 命令来源：" + firstNonBlank(snapshot.GitExecutableSource, "未读取到"),
		"Git 版本：" + pointerOr(snapshot.GitVersion, "未读取到"),
		"系统用户：" + firstNonBlank(snapshot.SystemUser, "未读取到"),
		"全局 user.name：" + formatGitValue(snapshot.GlobalUserName),
		"全局 user.email：" + formatGitValue(snapshot.GlobalUserEmail),
	}
	credentialValues := make([]string, 0, len(snapshot.GlobalCredentialHelpers))
	for _, item := range snapshot.GlobalCredentialHelpers {
		credentialValues = append(credentialValues, item.Value)
	}
	lines = append(lines, "全局 credential.helper："+firstNonBlank(strings.Join(credentialValues, ", "), "未配置"))
	if len(snapshot.GlobalKeyConfig) > 0 {
		lines = append(lines, "全局关键配置：")
		for _, item := range snapshot.GlobalKeyConfig {
			line := "  " + item.Key + "=" + item.Value
			if item.Origin != nil && *item.Origin != "" {
				line += "  [" + *item.Origin + "]"
			}
			lines = append(lines, line)
		}
	}
	for _, item := range snapshot.Errors {
		lines = append(lines, "全局读取错误："+item)
	}
	return strings.Join(lines, "\n")
}

func formatGitValue(value *gitConfigValue) string {
	if value == nil {
		return "未配置"
	}
	result := value.Value
	if value.Origin != nil && *value.Origin != "" {
		result += "  [" + *value.Origin + "]"
	}
	return result
}

func systemUserName() string {
	if runtime.GOOS == "windows" {
		return firstNonBlank(os.Getenv("USERNAME"), os.Getenv("USER"))
	}
	if current, err := user.Current(); err == nil && current.Username != "" {
		return current.Username
	}
	return firstNonBlank(os.Getenv("USERNAME"), os.Getenv("USER"))
}

func commandFailure(result process.Result, err error) string {
	if err != nil {
		return strings.TrimSpace(err.Error())
	}
	message := firstNonBlank(strings.TrimSpace(result.Stderr), strings.TrimSpace(result.Stdout))
	if message == "" {
		message = fmt.Sprintf("退出码 %d", result.ExitCode)
	}
	return message
}

func pointerOr(value *string, fallback string) string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return fallback
	}
	return *value
}
