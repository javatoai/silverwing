package integrations

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
)

// A locally valid link is not enough for a write: the CLI's current project
// mapping and returned work item must still identify the reviewed requirement.
func (s *Service) verifyRequirementCommentTarget(ctx context.Context, config domain.AppConfig, item domain.ParticipatedWorkItem) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	catalog, err := s.meegleProjects(ctx, config)
	if err != nil {
		if process.IsCleanupFailure(err) {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	projects, ok := object(catalog)["projects"].([]meegleProject)
	if !ok {
		return errors.New("当前 Meegle 项目查询结果格式无效")
	}
	if err := validateRequirementCommentProject(projects, item); err != nil {
		return err
	}
	result, err := s.runMeegle(ctx, config, []string{
		"workitem", "get", "--project-key", item.ProjectKey, "--work-item-id", item.ID, "--format", "json",
	}, 30*time.Second, "核对需求评论目标")
	if err != nil {
		if process.IsCleanupFailure(err) {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	value, err := decodeJSON(result.Stdout)
	if err != nil {
		return errors.New("需求查询响应格式无效")
	}
	return validateRequirementCommentWorkItem(value, item)
}

func validateRequirementCommentProject(projects []meegleProject, item domain.ParticipatedWorkItem) error {
	var matched *meegleProject
	for i := range projects {
		if projects[i].ProjectKey != item.ProjectKey {
			continue
		}
		if matched != nil {
			return fmt.Errorf("当前 Meegle 登录空间中未能唯一确认已配置项目 %s。", item.ProjectKey)
		}
		matched = &projects[i]
	}
	if matched == nil {
		return fmt.Errorf("当前 Meegle 登录空间中未能唯一确认已配置项目 %s。", item.ProjectKey)
	}
	link := domain.ParseFeishuWorkItemLink(item.URL)
	if link == nil || !strings.EqualFold(matched.SimpleName, item.ProjectName) || !strings.EqualFold(matched.SimpleName, link.Space) {
		return errors.New("已配置需求空间与当前 Meegle 项目查询结果不一致。")
	}
	if link.ProjectKey != nil && *link.ProjectKey != matched.ProjectKey {
		return errors.New("需求链接空间映射到的项目 Key 已变化。")
	}
	return nil
}

func validateRequirementCommentWorkItem(value any, item domain.ParticipatedWorkItem) error {
	root := object(value)
	if root == nil {
		return errors.New("需求查询响应格式无效。")
	}
	if len(root) == 0 {
		return errors.New("需求查询未返回有效需求，未发布评论。")
	}
	ids := requirementCommentIdentityValues(root, "itemid", "workitemid")
	_, hasAttributes := root["work_item_attribute"].(map[string]any)
	_, hasFields := root["work_item_fields"].([]any)
	if len(ids) == 0 && !hasAttributes && !hasFields && root["work_item_type"] == nil && root["work_item_status"] == nil {
		return errors.New("Meegle 查询响应未确认是需求对象。")
	}
	if id, ok := requirementCommentIdentityText(root["id"]); ok {
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return errors.New("Meegle 需求查询响应缺少需求 ID。")
	}
	for _, id := range ids {
		if id != item.ID {
			return errors.New("当前 Meegle 查询返回的需求 ID 与目标不一致。")
		}
	}
	for _, key := range requirementCommentIdentityValues(root, "projectkey") {
		if key != item.ProjectKey {
			return errors.New("当前 Meegle 查询返回的空间与目标项目不一致。")
		}
	}
	return nil
}

// Only the source gateway's identity wrappers participate. A name or label
// in an unrelated object must not count as evidence of a work item ID.
func requirementCommentIdentityText(value any) (string, bool) {
	if content, ok := requirementMetadataPrimitiveContent(value); ok {
		return content, true
	}
	if wrapped := object(value); wrapped != nil {
		for _, key := range []string{"long_value", "string_value", "text_value", "value"} {
			if content, ok := requirementCommentIdentityText(wrapped[key]); ok {
				return content, true
			}
		}
	}
	return "", false
}

func requirementCommentIdentityValues(value any, keys ...string) []string {
	var values []string
	var visit func(any)
	visit = func(value any) {
		switch current := value.(type) {
		case map[string]any:
			for key, child := range current {
				canonical := strings.ToLower(strings.ReplaceAll(key, "_", ""))
				matched := false
				for _, target := range keys {
					if canonical == target {
						matched = true
						break
					}
				}
				if matched {
					if content, ok := requirementCommentIdentityText(child); ok {
						values = append(values, content)
					}
				} else {
					visit(child)
				}
			}
		case []any:
			for _, child := range current {
				visit(child)
			}
		}
	}
	visit(value)
	return values
}

// A failed write command can have reached Meegle. A single readback confirms
// the stable marker and exact body; this function never retries the write.
func requirementCommentReadbackResult(comments []domain.RequirementComment, readErr error, content, marker string,
	expected domain.RequirementCommentWriteStatus, previousID *string, writeErr error) domain.RequirementCommentWriteResult {
	failed := func(message string, id *string) domain.RequirementCommentWriteResult {
		return domain.RequirementCommentWriteResult{Status: domain.RequirementCommentFailed, Message: message, CommentID: id}
	}
	if readErr != nil {
		if writeErr != nil {
			return failed("需求评论命令结果未知，且回读标记失败；请刷新后再同步："+writeErr.Error()+"；"+readErr.Error(), previousID)
		}
		return failed("需求评论命令已成功，但无法回读确认："+readErr.Error(), previousID)
	}
	var matching *domain.RequirementComment
	for i := range comments {
		count := markerLineCount(comments[i].Content, marker)
		if count == 0 {
			continue
		}
		if matching != nil {
			return failed("需求中出现多条相同 SilverWing 标记的评论，未自动重试。", nil)
		}
		matching = &comments[i]
		if count != 1 {
			return failed("目标评论中的 SilverWing 标记重复，未自动重试。", &matching.ID)
		}
	}
	if matching == nil {
		if writeErr != nil {
			return failed("需求评论命令结果未知，回读未找到稳定标记；未自动重试："+writeErr.Error(), previousID)
		}
		return failed("需求评论写入后回读未找到唯一稳定标记评论。", previousID)
	}
	if matching.Content != content {
		if writeErr != nil {
			return failed("需求评论命令结果未知，回读内容与本次正文不一致；未自动重试："+writeErr.Error(), &matching.ID)
		}
		return failed("需求评论写入后回读正文不一致。", &matching.ID)
	}
	message := "需求评论已写入并经回读确认。"
	if writeErr != nil {
		message = "需求评论已写入并经标记回读确认。"
	}
	return domain.RequirementCommentWriteResult{Status: expected, Message: message, CommentID: &matching.ID}
}
