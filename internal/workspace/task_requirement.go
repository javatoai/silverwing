package workspace

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/taskscope"
)

// LinkRequirement changes only the previously unlinked task's association.
// Local materials, workspaces, notes and tool sessions remain in place.
func (s *TaskApplicationService) LinkRequirement(ctx context.Context, config domain.AppConfig, taskPath, requirementURL string) (domain.TaskManifest, error) {
	link := strings.TrimSpace(requirementURL)
	parsed := domain.ParseFeishuWorkItemLink(link)
	if parsed == nil {
		return domain.TaskManifest{}, errors.New("请填写有效的飞书需求完整链接")
	}
	var updated domain.TaskManifest
	err := s.withTaskManifestMutation(ctx, config, taskPath, func(ctx context.Context, original domain.TaskManifest) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if original.RequirementLink == link {
			updated = original
			return nil
		}
		if strings.TrimSpace(original.RequirementLink) != "" {
			return errors.New("任务已经关联需求，请刷新任务后查看")
		}
		if scope, local := documents.LocalScope(stringValue(original.RequirementMaterials.WriteRoot)); local && !samePath(scope.Base, taskPath) {
			return errors.New("本地资料目录与任务身份不一致")
		}
		if err := taskscope.Validate(ctx, config, taskPath, original); err != nil {
			return err
		}
		next := original
		next.RequirementLink = link
		next.RequirementID = &parsed.WorkItemID
		next.UpdatedAt = s.now().UTC().Format(time.RFC3339Nano)
		if err := s.Manifests.Save(taskPath, next); err != nil {
			return err
		}
		if _, err := s.Documents.writeTaskDocuments(taskPath, next, repositoryInfos(config), nil, false); err != nil {
			rollback := s.Manifests.Save(taskPath, original)
			_, documentsErr := s.Documents.writeTaskDocuments(taskPath, original, repositoryInfos(config), nil, false)
			return fmt.Errorf("关联需求时更新说明失败：%w", errors.Join(err, rollback, documentsErr))
		}
		updated = next
		return nil
	})
	return updated, err
}
