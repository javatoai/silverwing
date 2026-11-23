package application

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

func (a *App) configureDocumentsGit(ctx context.Context, action Action) (ActionResult, error) {
	role := textPayload(action, "role")
	var settings func(domain.AppConfig) domain.MaterialsGitSyncSettings
	var scope, otherScope func(string, domain.AppConfig) workspace.MaterialsGitScope
	switch role {
	case domain.TaskMaterialsRepositoryRole:
		settings, scope, otherScope = domain.AppConfig.MaterialsGitSettings, MaterialsGitScope, ProjectInstructionsGitScope
	case domain.ProjectInstructionsRepositoryRole:
		settings, scope, otherScope = domain.AppConfig.ProjectInstructionsGitSettings, ProjectInstructionsGitScope, MaterialsGitScope
	default:
		return ActionResult{}, errors.New("请选择任务资料或项目资料仓库")
	}
	var expected workspace.MaterialsGitScope
	if err := decodeField(action, "scope", &expected); err != nil {
		return ActionResult{}, err
	}
	if !a.materialsGitGate.TryLock() {
		return ActionResult{}, errors.New("资料保存或 Git 同步正在执行，请稍后重试")
	}
	defer a.materialsGitGate.Unlock()
	current, err := a.Config.LoadForPreview(ctx)
	if err != nil {
		return ActionResult{}, err
	}
	if scope(a.Paths.Home, current) != expected {
		return ActionResult{}, workspace.ErrMaterialsGitScopeChanged
	}
	if expected.URL != "" && expected.Root != "" {
		release, err := expected.AcquireRepositoryLock()
		if err != nil {
			return ActionResult{}, err
		}
		defer release()
	}
	var result ActionResult
	_, err = a.Config.Update(func(config domain.AppConfig) (domain.AppConfig, error) {
		if err := ctx.Err(); err != nil {
			return config, err
		}
		if scope(a.Paths.Home, config) != expected {
			return config, workspace.ErrMaterialsGitScopeChanged
		}
		next := settings(config)
		if action.Kind == "config.documents-git.unbind" {
			next.Enabled, next.RepositoryURL, next.LocalRoot, next.RemoteName, next.BindingID = false, "", "", "", ""
			result.Message = "Git 仓库已解绑"
		} else {
			service := workspace.MaterialsGitSyncService{Git: a.git(config), Paths: a.Paths}
			candidate, err := service.InspectLocalDocumentsCheckout(ctx, strings.TrimSpace(actionPath(action)))
			if err != nil {
				return config, err
			}
			other := otherScope(a.Paths.Home, config)
			shared := other.URL != "" && workspace.SameDocumentsCheckout(other.Root, candidate.Root)
			if shared && !domain.SameMaterialsGitRepositoryURL(other.URL, candidate.URL) {
				return config, errors.New("所选仓库的 Git 地址已变化，请先在另一个页签重新关联仓库")
			}
			if next.RepositoryURL != "" && !domain.SameMaterialsGitRepositoryURL(next.RepositoryURL, candidate.URL) &&
				!(shared && sameSharedDocumentsGitRemote(next.RepositoryURL, candidate.URL)) {
				return config, errors.New("所选仓库的 Git 地址与当前绑定不同，请先解绑当前仓库")
			}
			if next.LocalRoot == "" || !workspace.SameDocumentsCheckout(next.LocalRoot, candidate.Root) || next.RemoteName != candidate.RemoteName || next.RepositoryURL != candidate.URL {
				var id [16]byte
				if _, err := rand.Read(id[:]); err != nil {
					return config, err
				}
				if next.RepositoryURL == "" || next.Branch == "" {
					next.Branch = candidate.Branch
				}
				if next.RepositoryURL == "" {
					next.Enabled = role == domain.ProjectInstructionsRepositoryRole && (config.ProjectInstructionsGitEnabled == nil || *config.ProjectInstructionsGitEnabled)
				}
				next.RepositoryURL = candidate.URL
				next.LocalRoot, next.RemoteName, next.BindingID = candidate.Root, candidate.RemoteName, hex.EncodeToString(id[:])
			}
			if shared {
				next.Branch = other.Branch
				if next.Branch == "" {
					// Only a managed project repository may track an unnamed default branch.
					next.Branch = candidate.Branch
					config.ProjectInstructionsGitBranch = candidate.Branch
				}
			}
			result.Message, result.Path = "本地 Git 仓库已绑定", next.LocalRoot
		}
		result.Data = next
		return setDocumentsGitSettings(config, role, next), ctx.Err()
	})
	if err != nil {
		return ActionResult{}, err
	}
	return result, nil
}

// A verified shared checkout may use the HTTP spelling of an already configured HTTPS remote.
func sameSharedDocumentsGitRemote(saved, selected string) bool {
	if upgraded := workspace.MaterialsGitHTTPSUpgrade(saved); upgraded != "" {
		saved = upgraded
	}
	if upgraded := workspace.MaterialsGitHTTPSUpgrade(selected); upgraded != "" {
		selected = upgraded
	}
	return domain.SameMaterialsGitRepositoryURL(saved, selected)
}

func setDocumentsGitSettings(config domain.AppConfig, role string, settings domain.MaterialsGitSyncSettings) domain.AppConfig {
	if role == domain.TaskMaterialsRepositoryRole {
		config.MaterialsGitSync = &settings
		return config
	}
	config.ProjectInstructionsGitURL = settings.RepositoryURL
	config.ProjectInstructionsGitBranch = settings.Branch
	config.ProjectInstructionsGitEnabled = &settings.Enabled
	config.ProjectInstructionsGitIntervalMinutes = &settings.IntervalMinutes
	config.ProjectInstructionsGitLocalRoot = settings.LocalRoot
	config.ProjectInstructionsGitRemoteName = settings.RemoteName
	config.ProjectInstructionsGitBindingID = settings.BindingID
	return config
}

func (a *App) validateDocumentsGitSettingsEdit(current, next domain.AppConfig) error {
	for _, pair := range [][2]domain.MaterialsGitSyncSettings{{current.MaterialsGitSettings(), next.MaterialsGitSettings()}, {current.ProjectInstructionsGitSettings(), next.ProjectInstructionsGitSettings()}} {
		old, updated := pair[0], pair[1]
		if old.LocalRoot != updated.LocalRoot || old.RemoteName != updated.RemoteName || old.BindingID != updated.BindingID ||
			old.LocalRoot != "" && old.RepositoryURL != updated.RepositoryURL ||
			a.Paths.ProfileID != "" && old.RepositoryURL != "" && updated.RepositoryURL != "" && !domain.SameMaterialsGitRepositoryURL(old.RepositoryURL, updated.RepositoryURL) {
			return errors.New("请通过资料仓库设置选择或解绑本地 Git 仓库")
		}
	}
	return nil
}
