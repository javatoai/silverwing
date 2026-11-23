package application

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

const ProjectInstructionsScopePayloadKey = "projectInstructionsScope"

func isProjectInstructionsDocumentAction(action Action) bool {
	_, present := action.Payload[ProjectInstructionsScopePayloadKey]
	return present
}

// ProjectInstructionsDocumentTaskID identifies an editor and its draft cache.
// Keep the original ID for the remote default and isolate explicit branches.
func ProjectInstructionsDocumentTaskID(scope workspace.MaterialsGitScope) string {
	id := domain.ProjectInstructionsRepositoryRole
	if scope.Branch != "" {
		id += ":" + scope.Branch
	}
	if scope.BindingID != "" {
		digest := sha256.Sum256([]byte(scope.URL))
		id += fmt.Sprintf(":%x", digest[:16])
	}
	return id
}

func (a *App) projectInstructionsDocumentAction(ctx context.Context, action Action) (ActionResult, error) {
	var expected workspace.MaterialsGitScope
	if err := decodeField(action, ProjectInstructionsScopePayloadKey, &expected); err != nil {
		return ActionResult{}, err
	}
	if expected.URL == "" {
		return ActionResult{}, errors.New("请先配置项目资料 Git 地址")
	}
	config, err := a.Config.LoadForPreview(ctx)
	if err != nil {
		return ActionResult{}, err
	}
	scope := ProjectInstructionsGitScope(a.Paths.Home, config)
	cacheOnly := action.Kind == "document.draft" || action.Kind == "document.draft-discard" || action.Kind == "document.draft-read"
	if expected != scope {
		if !cacheOnly {
			return ActionResult{}, workspace.ErrMaterialsGitScopeChanged
		}
		config.ProjectInstructionsGitURL = expected.URL
		config.ProjectInstructionsGitBranch = expected.Branch
		if expected.BindingID != "" {
			config.ProjectInstructionsGitLocalRoot = expected.Root
		} else {
			config.ProjectInstructionsGitLocalRoot = ""
		}
		config.ProjectInstructionsGitRemoteName = expected.RemoteName
		config.ProjectInstructionsGitBindingID = expected.BindingID
		if err := config.Validate(); err != nil {
			return ActionResult{}, err
		}
		scope = ProjectInstructionsGitScope(a.Paths.Home, config)
		// A retired editor may still cache its draft. Git executable and sync
		// settings do not change that cache identity.
		scope.EnvironmentID = expected.EnvironmentID
		if expected != scope {
			return ActionResult{}, workspace.ErrMaterialsGitScopeChanged
		}
	}
	taskID := ProjectInstructionsDocumentTaskID(scope)
	if (action.TaskID != "" && action.TaskID != taskID) || action.ServiceID != "" || action.ModuleID != "" {
		return ActionResult{}, workspace.ErrMaterialsGitScopeChanged
	}
	action.TaskID = taskID
	root := filepath.Join(scope.Root, scope.Subdirectory)
	for key, path := range map[string]string{"draftTaskPath": root, "draftMaterialsRoot": root,
		"desktopRenameTaskPath": root, "desktopRenameMaterialsRoot": root, "materialsHome": scope.Home} {
		if captured := textPayload(action, key); captured != "" && !sameTaskContextPath(captured, path) {
			return ActionResult{}, workspace.ErrMaterialsGitScopeChanged
		}
	}
	if cacheOnly {
		return a.documentActionAtRoot(ctx, action, root)
	}
	release, err := scope.AcquireRepositoryLock()
	if err != nil {
		return ActionResult{}, err
	}
	defer release()
	latest, err := a.Config.LoadForPreview(ctx)
	if err != nil {
		return ActionResult{}, err
	}
	if ProjectInstructionsGitScope(a.Paths.Home, latest) != scope {
		return ActionResult{}, workspace.ErrMaterialsGitScopeChanged
	}
	service := workspace.MaterialsGitSyncService{Git: a.git(latest), Paths: a.Paths}
	if err := service.VerifyLocalDocumentsScope(ctx, scope); err != nil {
		return ActionResult{}, err
	}
	root, err = service.ProjectInstructionsDocumentsRoot(scope, action.Kind == "document.list")
	if err != nil {
		return ActionResult{}, err
	}
	return a.documentActionAtRoot(ctx, action, root)
}
