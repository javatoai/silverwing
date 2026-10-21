package application

import (
	"context"
	"strings"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

// The task and its generated documents are committed before this boundary.
// Materials/tool follow-ups have their own statuses and must not turn a
// successful create into a retry of the entire provisioning transaction.
func finishCreatedTask(ctx context.Context, service *workspace.TaskApplicationService, taskPath string, manifest domain.TaskManifest, toolIDs []string, onProgress func(workspace.TaskCreationProgress)) ActionResult {
	warnings := []string{}
	if manifest.RequirementMaterials.Status == domain.MaterialsFailed {
		message := "需求资料准备失败"
		if reason := manifest.RequirementMaterials.FailureReason; reason != nil && strings.TrimSpace(*reason) != "" {
			message += "：" + strings.TrimSpace(*reason)
		}
		warnings = append(warnings, message)
	}
	if hasSelectedWorkspaceTools(toolIDs) {
		launching := workspace.TaskCreationProgress{Phase: workspace.TaskCreationPhaseLaunchingTools}
		emitTaskCreationProgress(onProgress, launching, workspace.TaskCreationProgressStarted, "正在打开所选工作区工具")
		launched, err := service.LaunchWorkspaceTools(ctx, taskPath, manifest, toolIDs)
		// Launch returns the last known manifest even if status persistence fails.
		if launched.TaskDirectoryName == manifest.TaskDirectoryName && launched.CreatedAt == manifest.CreatedAt {
			manifest = launched
		}
		failure := ""
		if err != nil {
			failure = err.Error()
		} else {
			failure = workspaceToolLaunchFailure(manifest, toolIDs)
		}
		if failure != "" {
			warnings = append(warnings, "工作区工具未全部打开："+failure)
			emitTaskCreationProgress(onProgress, launching, workspace.TaskCreationProgressFailed, failure)
		} else {
			emitTaskCreationProgress(onProgress, launching, workspace.TaskCreationProgressCompleted, "所选工作区工具已打开")
		}
	}
	emitTaskCreationProgress(onProgress, workspace.TaskCreationProgress{Phase: workspace.TaskCreationPhaseCompleted}, workspace.TaskCreationProgressCompleted, "任务创建完成")
	return ActionResult{Message: "任务已创建", Text: strings.Join(warnings, "\n"), Data: domain.NormalizeTaskManifest(manifest)}
}
