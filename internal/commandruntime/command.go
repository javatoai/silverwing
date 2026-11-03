// Package commandruntime prepares application-owned external commands without
// owning their business arguments, lifetime, or output interpretation.
package commandruntime

import (
	"context"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/executable"
	"silverwing-go/internal/networkproxy"
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
	return networkproxy.Environment(config, target)
}

func Redact(config domain.AppConfig, message string) string {
	return networkproxy.Redact(config, message)
}
