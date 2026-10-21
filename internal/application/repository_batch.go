package application

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

// RepositoryBatchRequest is the payload accepted by service.add-repositories.
type RepositoryBatchRequest struct {
	GroupID string   `json:"groupId"`
	Paths   []string `json:"paths"`
}

type RepositoryBatchSkip struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// RepositoryBatchResult matches the desktop contract for an atomic batch add.
type RepositoryBatchResult struct {
	Config  domain.AppConfig      `json:"config"`
	Added   []string              `json:"added"`
	Skipped []RepositoryBatchSkip `json:"skipped"`
}

type inspectedRepository struct {
	path       string
	repository domain.RepositoryConfig
	err        error
}

// A transform returning this error lets a batch that became a no-op between
// preflight and acquiring ConfigStore's interprocess lock exit without saving.
var errRepositoryBatchNoChanges = errors.New("repository batch produced no changes")

func (a *App) addRepositories(ctx context.Context, action Action) (ActionResult, error) {
	if err := ctx.Err(); err != nil {
		return ActionResult{}, err
	}
	var request RepositoryBatchRequest
	if err := decodePayload(action.Payload, &request); err != nil {
		return ActionResult{}, err
	}
	if strings.TrimSpace(request.GroupID) == "" {
		return ActionResult{}, errors.New("groupId 不能为空")
	}
	if request.Paths == nil {
		return ActionResult{}, errors.New("paths 必须是字符串数组")
	}

	config, err := a.Config.Load()
	if err != nil {
		return ActionResult{}, err
	}
	inspector := workspace.NewRepositoryInspector(a.git(config))
	inspected := make([]inspectedRepository, 0, len(request.Paths))
	for _, path := range request.Paths {
		if err := ctx.Err(); err != nil {
			return ActionResult{}, err
		}
		repository, inspectErr := inspector.InspectContext(ctx, path)
		inspected = append(inspected, inspectedRepository{path: path, repository: repository, err: inspectErr})
	}
	// A cancellation during the final Git inspection must not fall through to
	// planning and persistence.
	if err := ctx.Err(); err != nil {
		return ActionResult{}, err
	}

	// Preflight avoids even acquiring the mutation lock when every selected
	// directory is invalid, already registered, or already bound to the group.
	_, planned, err := resolveRepositoryBatch(config, request.GroupID, inspected)
	if err != nil {
		return ActionResult{}, err
	}
	if len(planned.Added) == 0 {
		return ActionResult{Message: "仓库批量添加完成", Data: planned}, nil
	}
	if err := ctx.Err(); err != nil {
		return ActionResult{}, err
	}

	var resolved RepositoryBatchResult
	updated, err := a.Config.Update(func(current domain.AppConfig) (domain.AppConfig, error) {
		if err := ctx.Err(); err != nil {
			return current, err
		}
		next, result, resolveErr := resolveRepositoryBatch(current, request.GroupID, inspected)
		if resolveErr != nil {
			return current, resolveErr
		}
		resolved = result
		if len(result.Added) == 0 {
			return current, errRepositoryBatchNoChanges
		}
		return next, nil
	})
	if errors.Is(err, errRepositoryBatchNoChanges) {
		// Another process may have added the same repositories after preflight.
		// The transform did not persist; reload so the response reflects current state.
		updated, err = a.Config.Load()
		if err != nil {
			return ActionResult{}, err
		}
	} else if err != nil {
		return ActionResult{}, err
	}
	resolved.Config = updated
	return ActionResult{Message: "仓库批量添加完成", Data: resolved}, nil
}

func resolveRepositoryBatch(config domain.AppConfig, groupID string, inspected []inspectedRepository) (domain.AppConfig, RepositoryBatchResult, error) {
	result := RepositoryBatchResult{Added: []string{}, Skipped: []RepositoryBatchSkip{}}
	group, err := config.Group(groupID)
	if err != nil {
		return config, result, err
	}

	knownRepositories := make(map[string]domain.RepositoryConfig, len(config.Repositories))
	for _, repository := range config.Repositories {
		knownRepositories[strings.ToLower(repository.GitCommonDirectory)] = repository
	}
	services := append([]domain.GroupServiceConfig{}, group.Services...)
	repositories := append([]domain.RepositoryConfig{}, config.Repositories...)
	seenCommonDirectories := make(map[string]struct{}, len(inspected))

	for _, item := range inspected {
		if item.err != nil {
			reason := item.err.Error()
			if strings.TrimSpace(reason) == "" {
				reason = "不是可用的 Git 主仓库"
			}
			result.Skipped = append(result.Skipped, RepositoryBatchSkip{Path: item.path, Reason: reason})
			continue
		}

		commonKey := strings.ToLower(item.repository.GitCommonDirectory)
		if _, duplicate := seenCommonDirectories[commonKey]; duplicate {
			result.Skipped = append(result.Skipped, RepositoryBatchSkip{Path: item.path, Reason: "所选目录指向重复的 Git 仓库"})
			continue
		}
		seenCommonDirectories[commonKey] = struct{}{}

		repository, exists := knownRepositories[commonKey]
		if !exists {
			repository = item.repository
			knownRepositories[commonKey] = repository
			repositories = append(repositories, repository)
		}
		alreadyBound := false
		for _, service := range services {
			if service.RepositoryID == repository.ID {
				alreadyBound = true
				break
			}
		}
		if alreadyBound {
			result.Skipped = append(result.Skipped, RepositoryBatchSkip{Path: item.path, Reason: fmt.Sprintf("该仓库已存在于组 %s 中", group.Name)})
			continue
		}

		services = append(services, standardRepositoryService(repository, config.DefaultDevelopmentTool))
		result.Added = append(result.Added, item.path)
	}

	result.Config = config
	if len(result.Added) == 0 {
		return config, result, nil
	}

	groups := append([]domain.GroupConfig{}, config.Groups...)
	for i := range groups {
		if groups[i].ID == groupID {
			groups[i].Services = services
			break
		}
	}
	config.Repositories = repositories
	config.Groups = groups
	result.Config = config
	return config, result, nil
}

func standardRepositoryService(repository domain.RepositoryConfig, developmentTool domain.DevelopmentToolType) domain.GroupServiceConfig {
	defaultBranch := "master"
	if repository.DefaultRemoteBranch != nil && *repository.DefaultRemoteBranch != "" {
		defaultBranch = *repository.DefaultRemoteBranch
	}
	return domain.GroupServiceConfig{
		ID:           "service-" + strings.TrimPrefix(repository.ID, "repo-"),
		RepositoryID: repository.ID,
		DisplayName:  repository.Name,
		Enabled:      true,

		MasterBranch: "origin/" + defaultBranch,

		DevelopmentTool: developmentTool,
		Modules: []domain.ServiceModuleConfig{{
			ID:       "default",
			Name:     "default",
			Strategy: domain.WorkspaceStandardWorktree,

			CustomCommands: []domain.WorkspaceCommandConfig{},
		}},
		Bootstrap: domain.BootstrapConfig{
			CopyRules: []domain.BootstrapCopyRule{},
			Commands:  []domain.BootstrapCommand{},
		},
		CommitMessageTemplate: "",
	}
}
