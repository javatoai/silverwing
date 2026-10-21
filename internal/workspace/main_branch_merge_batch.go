package workspace

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"silverwing-go/internal/domain"
)

// WorkspaceMainBranchMergeBatchConfirmation pins the user-approved task and
// workspace records between opening the confirmation dialog and dispatching
// the batch operation.
type WorkspaceMainBranchMergeBatchConfirmation struct {
	TaskDirectoryName string                                            `json:"taskDirectoryName"`
	CreatedAt         string                                            `json:"createdAt"`
	GroupID           string                                            `json:"groupId"`
	FeatureBranch     string                                            `json:"featureBranch"`
	Workspaces        []WorkspaceMainBranchMergeBatchConfirmedWorkspace `json:"workspaces"`
}

type WorkspaceMainBranchMergeBatchConfirmedWorkspace struct {
	Record    domain.ServiceWorkspace `json:"record"`
	SourceRef string                  `json:"sourceRef"`
}

func (confirmation WorkspaceMainBranchMergeBatchConfirmation) ConfirmedWorkspaces(config domain.AppConfig, manifest domain.TaskManifest, selected []domain.ServiceWorkspace) ([]domain.ServiceWorkspace, error) {
	if confirmation.TaskDirectoryName != manifest.TaskDirectoryName || confirmation.CreatedAt != manifest.CreatedAt ||
		confirmation.GroupID != manifest.GroupID || confirmation.FeatureBranch != manifest.FeatureBranch {
		return nil, errors.New("任务信息已变化，请刷新后重新选择工作区")
	}
	if len(confirmation.Workspaces) == 0 || len(selected) != len(confirmation.Workspaces) {
		return nil, errors.New("所选工作区信息已变化，请刷新后重新选择")
	}

	currentByKey := make(map[string]domain.ServiceWorkspace, len(selected))
	for _, item := range selected {
		key := item.SelectionKey()
		if _, exists := currentByKey[key]; exists {
			return nil, errors.New("所选工作区不唯一，请刷新后重新选择")
		}
		currentByKey[key] = item
	}

	confirmed := make([]domain.ServiceWorkspace, 0, len(confirmation.Workspaces))
	seen := make(map[string]bool, len(confirmation.Workspaces))
	for _, approved := range confirmation.Workspaces {
		item := approved.Record
		key := item.SelectionKey()
		current, exists := currentByKey[key]
		if !exists || seen[key] || !reflect.DeepEqual(current, item) {
			return nil, errors.New("工作区信息已变化，请刷新后重新选择")
		}
		main, found, err := ResolveWorkspaceMainBranch(config, manifest, item)
		if err != nil || !found || main.DisplayRef != approved.SourceRef {
			return nil, errors.New("主分支配置已变化，请刷新后重新选择工作区")
		}
		seen[key] = true
		confirmed = append(confirmed, item)
	}
	if len(seen) != len(currentByKey) {
		return nil, errors.New("所选工作区信息已变化，请刷新后重新选择")
	}
	return confirmed, nil
}

type WorkspaceMainBranchMergeBatchResult struct {
	Items []WorkspaceMainBranchMergeResult `json:"items"`
}

// MergeBatch executes each selected workspace through the existing guarded
// single-workspace merge. Sequential execution lets the existing task and
// common-directory locks serialize shared worktrees without another scheduler.
func (s *WorkspaceMainBranchMergeService) MergeBatch(ctx context.Context, config domain.AppConfig, taskPath string, expectedTask domain.TaskManifest, requested []domain.ServiceWorkspace) WorkspaceMainBranchMergeBatchResult {
	if ctx == nil {
		ctx = context.Background()
	}
	result := WorkspaceMainBranchMergeBatchResult{Items: make([]WorkspaceMainBranchMergeResult, 0, len(requested))}
	for index, item := range requested {
		if err := ctx.Err(); err != nil {
			for _, pending := range requested[index:] {
				result.Items = append(result.Items, workspaceMainBranchMergeFailure(pending, config, expectedTask, fmt.Errorf("批量操作已中断，未执行：%w", err)))
			}
			break
		}
		merged, err := s.Merge(ctx, config, taskPath, expectedTask, item)
		if err != nil {
			merged = workspaceMainBranchMergeFailure(item, config, expectedTask, err)
		} else if merged.Outcome == MainBranchConflict {
			merged.Message = workspaceMainBranchMergeConflictMessage(merged.SourceRef, merged.TargetBranch)
		}
		result.Items = append(result.Items, merged)
	}
	return result
}

func workspaceMainBranchMergeConflictMessage(sourceRef, targetBranch string) string {
	return fmt.Sprintf("我将%s合并到%s时出现冲突，请你帮忙解决。", sourceRef, targetBranch)
}

func workspaceMainBranchMergeFailure(item domain.ServiceWorkspace, config domain.AppConfig, task domain.TaskManifest, err error) WorkspaceMainBranchMergeResult {
	sourceRef := "远程主分支"
	if source, ok, resolveErr := ResolveWorkspaceMainBranch(config, task, item); resolveErr == nil && ok && source.DisplayRef != "" {
		sourceRef = source.DisplayRef
	}
	return WorkspaceMainBranchMergeResult{
		WorkspacePath: item.WorktreePath,
		ServiceName:   item.ServiceName,
		ModuleName:    item.ModuleName,
		SourceRef:     sourceRef,
		TargetBranch:  item.Branch,
		Outcome:       MainBranchFailed,
		Message:       err.Error(),
		ConflictFiles: []string{},
	}
}
