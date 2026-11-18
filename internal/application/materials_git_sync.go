package application

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"silverwing-go/internal/commandruntime"
	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/executable"
	"silverwing-go/internal/storage"
	"silverwing-go/internal/workspace"
)

// Optional native-desktop capability; the existing Action/CLI contract stays
// unchanged. Reading status never starts Git, contacts a remote, or syncs files.
type MaterialsGitBackend interface {
	MaterialsGitStatus(context.Context, workspace.MaterialsGitScope) (workspace.MaterialsGitSyncStatus, error)
	SyncMaterialsGit(context.Context, workspace.MaterialsGitScope, bool, func(string)) (workspace.MaterialsGitSyncStatus, error)
}

// Optional read-only capability. No repository initialization or sync occurs.
type MaterialsGitBranchesBackend interface {
	MaterialsGitBranches(context.Context, workspace.MaterialsGitScope) ([]string, error)
}

type MaterialsGitLoginBackend interface {
	VerifyMaterialsGitLogin(context.Context, workspace.MaterialsGitScope) error
	MaterialsGitTerminalLoginCommand(context.Context, workspace.MaterialsGitScope) (string, error)
}

func (a *App) MaterialsGitBranches(ctx context.Context, expected workspace.MaterialsGitScope) ([]string, error) {
	service, err := a.materialsGitService(expected)
	if err != nil {
		return nil, err
	}
	if err := domain.ValidateRemoteGitURL(expected.URL); err != nil {
		return nil, err
	}
	return service.RemoteBranches(ctx, expected)
}

func (a *App) VerifyMaterialsGitLogin(ctx context.Context, expected workspace.MaterialsGitScope) error {
	service, err := a.materialsGitService(expected)
	if err != nil {
		return err
	}
	return service.VerifyHTTPSLogin(ctx, expected)
}

func (a *App) MaterialsGitTerminalLoginCommand(ctx context.Context, expected workspace.MaterialsGitScope) (string, error) {
	service, err := a.materialsGitService(expected)
	if err != nil {
		return "", err
	}
	args, err := service.HTTPSLoginArguments(ctx, expected)
	if err != nil {
		return "", err
	}
	git, err := commandruntime.Prepare(ctx, a.Runner, nil, "", executable.ToolGit, service.Git.Executable)
	if err != nil {
		return "", errors.New("无法定位用于验证的 Git，请在设置中检查 Git 路径")
	}
	if err := service.Check(); err != nil {
		return "", err
	}
	return materialsGitTerminalLoginCommand(runtime.GOOS, git.Executable, args), ctx.Err()
}

func materialsGitTerminalLoginCommand(osName, git string, args []string) string {
	args = append([]string{git, "--no-optional-locks", "-c", "core.longpaths=true"}, args...)
	if isWindowsOS(osName) {
		for index, arg := range args {
			args[index] = powerShellLiteral(arg)
		}
		return `& { $saved = @{}; try { foreach ($entry in @{GIT_ALLOW_PROTOCOL='https';GIT_TERMINAL_PROMPT='1';GCM_INTERACTIVE='auto';GCM_TRACE='0';GCM_TRACE_SECRETS='0';GIT_TRACE_CURL='0';GIT_CURL_VERBOSE=$null;GIT_TRACE_REDACT='1'}.GetEnumerator()) { $saved[$entry.Key] = [Environment]::GetEnvironmentVariable($entry.Key); [Environment]::SetEnvironmentVariable($entry.Key, $entry.Value) }; & ` +
			strings.Join(args, " ") + ` } finally { foreach ($entry in $saved.GetEnumerator()) { [Environment]::SetEnvironmentVariable($entry.Key, $entry.Value) } } }`
	}
	for index, arg := range args {
		args[index] = posixTerminalLiteral(arg)
	}
	return "env -u GIT_CURL_VERBOSE GIT_ALLOW_PROTOCOL=https GIT_TERMINAL_PROMPT=1 GCM_INTERACTIVE=auto GCM_TRACE=0 GCM_TRACE_SECRETS=0 GIT_TRACE_CURL=0 GIT_TRACE_REDACT=1 " + strings.Join(args, " ")
}

// The provisioner already shares this root lock. Document writes take it as
// well, including writes made by another CLI/desktop process.
func (a *App) lockMaterialsGitDocumentMutation(kind, root string) (func(), error) {
	switch kind {
	case "document.read", "document.search", "document.draft", "document.draft-read", "document.draft-discard":
		return func() {}, nil
	}
	config, err := a.Config.Load()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(value(config.RequirementMaterialsRoot)) == "" {
		return func() {}, nil
	}
	paths, pathErr := config.MaterialsPaths()
	if pathErr != nil {
		return nil, pathErr
	}
	base := paths.Root
	if base == "" {
		return func() {}, nil
	}
	rel, err := filepath.Rel(base, root)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return func() {}, nil
	}
	release, err := storage.AcquireMaterialsRepositoryLock(base)
	if err != nil {
		return nil, fmt.Errorf("资料 Git 同步或文件操作正在执行，请稍后重试：%w", err)
	}
	return release, nil
}

func MaterialsGitScope(home string, config domain.AppConfig) workspace.MaterialsGitScope {
	settings := config.MaterialsGitSettings()
	// Include execution/credential routing and enablement, but not unrelated
	// service configuration. The digest never exposes proxy credentials to UI.
	env, _ := json.Marshal([]any{config.GitExecutablePath, config.CommandProxyURL, config.CommandProxyNoProxy, config.CommandProxyUsername, config.CommandProxyPassword, config.CommandProxyTargets, settings.Enabled})
	paths, pathsErr := config.MaterialsPaths()
	rawRoot := filepath.Clean(value(config.RequirementMaterialsRoot))
	return workspace.MaterialsGitScope{Home: home, Root: paths.Root, RequirementsRoot: paths.Requirements, RequireRootConfirmation: pathsErr == nil && !sameTaskContextPath(rawRoot, paths.Root), Subdirectory: value(config.RequirementMaterialsSubdirectory), URL: settings.RepositoryURL, Branch: settings.Branch, EnvironmentID: fmt.Sprintf("%x", sha256.Sum256(env))}
}

func (a *App) materialsGitService(expected workspace.MaterialsGitScope) (*workspace.MaterialsGitSyncService, error) {
	config, err := a.Config.Load()
	if err != nil {
		return nil, err
	}
	if MaterialsGitScope(a.Paths.Home, config) != expected {
		return nil, workspace.ErrMaterialsGitScopeChanged
	}
	git := workspace.NewGitClientWithConfig(a.Runner, config)
	if git.Environment == nil {
		git.Environment = map[string]string{}
	}
	git.Environment["GIT_MERGE_AUTOEDIT"] = "no"
	git.Environment["GIT_EDITOR"] = "false"
	return &workspace.MaterialsGitSyncService{Git: git, Paths: a.Paths, Check: func() error {
		current, err := a.Config.Load()
		if err != nil {
			return err
		}
		if MaterialsGitScope(a.Paths.Home, current) != expected {
			return workspace.ErrMaterialsGitScopeChanged
		}
		return nil
	}}, nil
}

func (a *App) MaterialsGitStatus(ctx context.Context, expected workspace.MaterialsGitScope) (workspace.MaterialsGitSyncStatus, error) {
	if err := ctx.Err(); err != nil {
		return workspace.MaterialsGitSyncStatus{}, err
	}
	service, err := a.materialsGitService(expected)
	if err != nil {
		return workspace.MaterialsGitSyncStatus{}, err
	}
	return service.Status(expected)
}

func (a *App) SyncMaterialsGit(ctx context.Context, expected workspace.MaterialsGitScope, resume bool, progress func(string)) (workspace.MaterialsGitSyncStatus, error) {
	if !a.materialsGitGate.TryLock() {
		return workspace.MaterialsGitSyncStatus{}, errors.New("资料保存或 Git 同步正在执行，请稍后重试")
	}
	defer a.materialsGitGate.Unlock()
	service, err := a.materialsGitService(expected)
	if err != nil {
		return workspace.MaterialsGitSyncStatus{}, err
	}
	config, err := a.Config.Load()
	if err != nil {
		return workspace.MaterialsGitSyncStatus{}, err
	}
	if err := config.MaterialsGitSettings().Validate(); err != nil {
		return workspace.MaterialsGitSyncStatus{}, err
	}
	if _, err := documents.ValidateMaterialsPaths(value(config.RequirementMaterialsRoot)); err != nil {
		return workspace.MaterialsGitSyncStatus{}, err
	}
	if strings.TrimSpace(expected.URL) == "" {
		return workspace.MaterialsGitSyncStatus{}, errors.New("请填写 Git 仓库地址")
	}
	if _, err := domain.ValidateRequirementMaterialsSubdirectory(expected.Subdirectory); err != nil {
		return workspace.MaterialsGitSyncStatus{}, err
	}
	status, syncErr := service.Sync(ctx, expected, resume, progress)
	return a.finishMaterialsGitSync(ctx, expected, status, syncErr, service.Check)
}

func (a *App) finishMaterialsGitSync(ctx context.Context, expected workspace.MaterialsGitScope, status workspace.MaterialsGitSyncStatus, syncErr error, check func() error) (workspace.MaterialsGitSyncStatus, error) {
	// Git may have applied a checkout/merge before a push failed (or stopped
	// with conflicts). Describe the actual local tree regardless of push outcome.
	if check != nil && check() == nil {
		unlock, lockErr := storage.AcquireMaterialsRepositoryLock(expected.Root)
		if lockErr == nil {
			catalogErr := a.refreshMaterialsAgentCatalogs(ctx, expected)
			unlock()
			if catalogErr != nil {
				syncErr = errors.Join(syncErr, fmt.Errorf("本地资料目录说明更新失败，请刷新资料重试（不重复同步）：%w", catalogErr))
			}
		} else {
			syncErr = errors.Join(syncErr, lockErr)
		}
	}
	return status, syncErr
}
