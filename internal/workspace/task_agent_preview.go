package workspace

import (
	"context"
	"path/filepath"
	"silverwing-go/internal/domain"
	"strings"
)

func (s *TaskApplicationService) PreviewAgentInstructions(ctx context.Context, config domain.AppConfig, request CreateTaskRequest, materials domain.RequirementMaterialsDirectory) (AgentDocumentPreview, error) {
	if err := ctx.Err(); err != nil {
		return AgentDocumentPreview{}, err
	}
	if _, err := config.Group(request.GroupID); err != nil {
		return AgentDocumentPreview{}, err
	}
	name := strings.TrimSpace(request.FolderName)
	if name == "" {
		name = "任务名称"
	}
	directory, err := RequireValidTaskDirectoryName(name)
	if err != nil {
		directory = "任务名称"
	}
	manifest := domain.DefaultTaskManifest()
	manifest.FolderName, manifest.TaskDirectoryName, manifest.GroupID = name, directory, request.GroupID
	manifest.RequirementLink = request.RequirementLink
	manifest.RequirementMaterials = materials
	return s.Documents.RenderPreviewChecked(filepath.Join(stringValue(config.TaskRoot), directory), manifest, config, request.TaskNotes)
}
