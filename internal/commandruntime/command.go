// Package commandruntime prepares application-owned external commands without
// owning their business arguments, lifetime, or output interpretation.
package commandruntime

import (
	"context"
	"net/url"
	"strings"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/executable"
	"silverwing-go/internal/process"
)

// Prepare retains inherited environment unless config supplies a proxy policy.
// Callers overlay operation-specific environment after this preparation.
func Prepare(ctx context.Context, runner executable.Runner, config *domain.AppConfig, target domain.CommandProxyTarget, tool executable.Tool, command string) (process.Spec, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	resolver := executable.NewResolver(runner)
	var env map[string]string
	var err error
	if tool != "" {
		var resolution executable.Resolution
		resolution, env, err = resolver.Prepare(ctx, tool, command)
		command = resolution.Command
	} else {
		command, env, err = resolver.PrepareCommand(ctx, command)
	}
	if err != nil {
		return process.Spec{}, err
	}
	if env == nil {
		env = map[string]string{}
	}
	var remove []string
	if config != nil {
		proxy, removed := ProxyEnvironment(*config, target)
		remove = removed
		for name, value := range proxy {
			env[name] = value
		}
	}
	return process.Spec{Executable: command, Env: env, RemoveEnv: remove}, nil
}

func ProxyEnvironment(config domain.AppConfig, target domain.CommandProxyTarget) (map[string]string, []string) {
	remove := []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy"}
	env := map[string]string{}
	enabled := false
	for _, candidate := range config.CommandProxyTargets {
		enabled = enabled || candidate == target
	}
	if !enabled {
		return env, remove
	}
	endpoint, err := domain.NormalizeCommandProxyURL(config.CommandProxyURL)
	if err != nil || endpoint == nil {
		return env, remove
	}
	parsed, err := url.Parse(*endpoint)
	if err != nil {
		return env, remove
	}
	if config.CommandProxyUsername != nil && strings.TrimSpace(*config.CommandProxyUsername) != "" && config.CommandProxyPassword != nil && *config.CommandProxyPassword != "" {
		parsed.User = url.UserPassword(strings.TrimSpace(*config.CommandProxyUsername), *config.CommandProxyPassword)
	}
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		env[name] = parsed.String()
	}
	if noProxy, err := domain.NormalizeCommandProxyNoProxy(config.CommandProxyNoProxy); err == nil && noProxy != nil {
		env["NO_PROXY"], env["no_proxy"] = *noProxy, *noProxy
	}
	return env, remove
}

func Redact(config domain.AppConfig, message string) string {
	if config.CommandProxyURL != nil && *config.CommandProxyURL != "" {
		message = strings.ReplaceAll(message, *config.CommandProxyURL, "[代理地址已隐藏]")
		if parsed, err := url.Parse(*config.CommandProxyURL); err == nil {
			if config.CommandProxyUsername != nil && config.CommandProxyPassword != nil {
				parsed.User = url.UserPassword(strings.TrimSpace(*config.CommandProxyUsername), *config.CommandProxyPassword)
				message = strings.ReplaceAll(message, parsed.String(), "[代理地址已隐藏]")
			}
			if parsed.Host != "" {
				message = strings.ReplaceAll(message, parsed.Host, "[代理地址已隐藏]")
			}
		}
	}
	for _, secret := range []*string{config.CommandProxyUsername, config.CommandProxyPassword} {
		if secret != nil && *secret != "" {
			message = strings.ReplaceAll(message, *secret, "[已隐藏]")
		}
	}
	return message
}
