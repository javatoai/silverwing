package application

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"silverwing-go/internal/domain"
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
	base := value(config.RequirementMaterialsRoot)
	if base == "" {
		return func() {}, nil
	}
	rel, err := filepath.Rel(base, root)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return func() {}, nil
	}
	release, err := storage.AcquireLock(filepath.Join(base, ".silverwing-requirement-materials.lock"))
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
	return workspace.MaterialsGitScope{Home: home, Root: value(config.RequirementMaterialsRoot), Subdirectory: value(config.RequirementMaterialsSubdirectory), URL: settings.RepositoryURL, Branch: settings.Branch, EnvironmentID: fmt.Sprintf("%x", sha256.Sum256(env))}
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
	git.Environment["GIT_TERMINAL_PROMPT"] = "0"
	git.Environment["GCM_INTERACTIVE"] = "never"
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
		unlock, lockErr := storage.AcquireLock(filepath.Join(expected.Root, ".silverwing-requirement-materials.lock"))
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
