package platform

import (
	"context"
	"silverwing-go/internal/larkcli"
	"silverwing-go/internal/process"
	"strings"
	"time"
)

func (s *Service) larkCLIStatus(ctx context.Context, force bool) (any, error) {
	cfg, e := s.loadConfig()
	if e != nil {
		return nil, e
	}
	cfg, resolution, _, e := s.resolveCLIExecutableWithSourceForce(ctx, cfg, "lark", force)
	if e != nil {
		return nil, e
	}
	command := resolution.Command
	cfg.LarkExecutablePath = &command
	client := larkcli.Client{Runner: s.runner, Config: cfg}
	version, e := client.Run(ctx, []string{"--version"}, 10*time.Second)
	out := map[string]any{"tool": "lark", "command": command, "source": string(resolution.Source), "installed": e == nil && version.ExitCode == 0, "authenticationState": "NOT_CHECKED"}
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
	out["version"] = strings.TrimSpace(version.Stdout)
	// Preserve user configuration; auto-discovery uses the existing guarded save.
	original, e := s.loadConfig()
	if e != nil {
		return nil, e
	}
	if _, e = s.persistDiscoveredCLIExecutable(ctx, original, "lark", resolution); e != nil {
		return nil, e
	}
	identity, e := client.Identity(ctx)
	if e != nil {
		if process.IsCleanupFailure(e) {
			return nil, e
		}
		out["authenticationState"] = "CHECK_FAILED"
		out["authenticationError"] = e.Error()
		return out, nil
	}
	out["userState"] = identity.UserState
	out["userName"] = identity.Name
	out["userOpenId"] = identity.OpenID
	out["botReady"] = identity.BotReady
	out["authenticationState"] = "LOCAL_IDENTITY"
	return out, nil
}
