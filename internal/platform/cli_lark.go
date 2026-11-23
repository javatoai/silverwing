package platform

import (
	"context"
	"fmt"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/executable"
	"silverwing-go/internal/larkcli"
	"silverwing-go/internal/process"
)

func (s *Service) larkCLIStatus(ctx context.Context, cfg domain.AppConfig, force, versionOnly, deferDiscovery bool) (any, error) {
	resolution, e := s.resolveCLIExecutableWithSourceForce(ctx, cfg, executable.ToolLark, force)
	if e != nil {
		return nil, e
	}
	command := resolution.Command
	client := larkcli.Client{Runner: s.runner, Config: cfg}
	client.Config.LarkExecutablePath = &command
	version, e := client.Run(ctx, []string{"--version"}, 10*time.Second)
	out := map[string]any{"tool": "lark", "command": command, "source": string(resolution.Source), "enabled": true, "installed": e == nil && version.ExitCode == 0, "version": nil, "authenticationState": "NOT_CHECKED"}
	if e != nil {
		if process.IsCleanupFailure(e) {
			return nil, e
		}
		out["error"] = e.Error()
		return out, nil
	}
	if version.ExitCode != 0 {
		out["error"] = "Lark CLI 版本检测失败"
		return out, nil
	}
	out["version"] = firstNonBlank(version.Stdout, version.Stderr, "未知")
	if !deferDiscovery {
		cfg, e = s.persistDiscoveredCLIExecutable(ctx, cfg, executable.ToolLark, resolution)
		if e != nil {
			return nil, e
		}
	}
	if versionOnly {
		return out, nil
	}
	current, e := s.loadConfig()
	if e != nil {
		return nil, e
	}
	path := current.OptionalCLIPath("lark")
	if !current.OptionalCLIEnabled("lark") {
		return disabledCLIStatus("lark", path), nil
	}
	if path != cfg.OptionalCLIPath("lark") {
		return nil, fmt.Errorf("CLI 命令路径已变化，请重新读取状态")
	}
	client.Config = current
	client.Config.LarkExecutablePath = &command
	return out, readLarkIdentity(ctx, client, out)
}

// ReadVerifiedLarkIdentity uses the command accepted by the application probe.
func (s *Service) ReadVerifiedLarkIdentity(ctx context.Context, command string, check func(context.Context) error, out map[string]any) error {
	cfg, err := s.loadConfig()
	if err != nil {
		return err
	}
	client := larkcli.Client{Runner: s.runner, Config: cfg, Executable: command, BeforeRun: check}
	return readLarkIdentity(ctx, client, out)
}

func readLarkIdentity(ctx context.Context, client larkcli.Client, out map[string]any) error {
	identity, e := client.Identity(ctx)
	if e != nil {
		if process.IsCleanupFailure(e) {
			return e
		}
		out["authenticationState"] = "CHECK_FAILED"
		out["authenticationError"] = e.Error()
		return nil
	}
	out["userState"] = identity.UserState
	out["userName"] = identity.Name
	out["userOpenId"] = identity.OpenID
	out["botReady"] = identity.BotReady
	out["authenticationState"] = "LOCAL_IDENTITY"
	return nil
}
