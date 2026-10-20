package delivery

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"silverwing-go/internal/domain"
)

// GroupTagHistory preserves complete batches while ordering their children like
// Source TagHistoryQueryService. It never changes the input operation slice.
func GroupTagHistory(operations []domain.TagOperation) []domain.TagHistoryItem {
	groups := make(map[string][]domain.TagOperation)
	for _, operation := range operations {
		key := operation.OperationID
		if batch := strings.TrimSpace(value(operation.BatchID)); batch != "" {
			key = value(operation.BatchID)
		}
		groups[key] = append(groups[key], operation)
	}
	items := make([]domain.TagHistoryItem, 0, len(groups))
	for key, children := range groups {
		sort.SliceStable(children, func(i, j int) bool {
			if children[i].UpdatedAt != children[j].UpdatedAt {
				return children[i].UpdatedAt > children[j].UpdatedAt
			}
			return children[i].OperationID > children[j].OperationID
		})
		first := children[0]
		created, updated := first.CreatedAt, first.UpdatedAt
		for _, child := range children[1:] {
			if child.CreatedAt < created || child.CreatedAt == created && child.OperationID < first.OperationID {
				first = child
				created = child.CreatedAt
			}
			if child.UpdatedAt > updated {
				updated = child.UpdatedAt
			}
		}
		batch := children[0].BatchID
		if strings.TrimSpace(value(batch)) == "" {
			batch = nil
		}
		items = append(items, domain.TagHistoryItem{GroupID: key, BatchID: batch, FolderName: first.FolderName, CreatedAt: created, UpdatedAt: updated, Operations: children})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].UpdatedAt != items[j].UpdatedAt {
			return items[i].UpdatedAt > items[j].UpdatedAt
		}
		return items[i].GroupID < items[j].GroupID
	})
	return items
}

// HistoryRetentionOperationIDs is a read-only plan: retaining a group keeps all
// of its child operations, rather than cutting a batch at the record limit.
func HistoryRetentionOperationIDs(items []domain.TagHistoryItem, maxGroups int) ([]string, error) {
	if maxGroups < 1 || maxGroups > domain.MaxTagHistoryGroups {
		return nil, fmt.Errorf("Tag组保留数量必须在 1 到 %d 之间", domain.MaxTagHistoryGroups)
	}
	selected := []string{}
	seen := map[string]bool{}
	for _, item := range items[min(maxGroups, len(items)):] {
		for _, operation := range item.Operations {
			if operation.OperationID != "" && !seen[operation.OperationID] {
				selected = append(selected, operation.OperationID)
				seen[operation.OperationID] = true
			}
		}
	}
	return selected, nil
}

func (s *Service) HistoryItems(ctx context.Context, config domain.AppConfig, tasks []domain.TaskManifest) ([]domain.TagHistoryItem, error) {
	ctx = historyContext(ctx)
	operations := []domain.TagOperation{}
	for _, task := range tasks {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		directory, err := historyTaskDirectory(config, task)
		if err != nil {
			return nil, err
		}
		if directory == "" {
			continue
		}
		items, err := s.store.list(directory)
		if err == nil {
			operations = append(operations, items...)
		}
		// Source lists other known tasks even if one task's history cannot be read.
	}
	return GroupTagHistory(operations), ctx.Err()
}

func (s *Service) EnforceHistoryRetention(ctx context.Context, config domain.AppConfig, tasks []domain.TaskManifest) (int, error) {
	if _, err := HistoryRetentionOperationIDs(nil, config.TagHistoryMaxGroups); err != nil {
		return 0, err
	}
	return s.mutateHistory(ctx, config, tasks, func(directory string) (int, error) {
		// Each task owns its allowance. Plan and remove under the same task
		// lease, so a batch cannot gain a child between selection and deletion.
		operations, err := s.store.list(directory)
		if err != nil {
			return 0, err
		}
		selected, err := HistoryRetentionOperationIDs(GroupTagHistory(operations), config.TagHistoryMaxGroups)
		if err != nil {
			return 0, err
		}
		return s.store.deleteSelected(historyContext(ctx), directory, selected)
	})
}

func (s *Service) DeleteHistory(ctx context.Context, config domain.AppConfig, tasks []domain.TaskManifest, operationIDs []string) (int, error) {
	selected := make(map[string]bool, len(operationIDs))
	for _, id := range operationIDs {
		selected[id] = true
	}
	return s.mutateHistory(ctx, config, tasks, func(directory string) (int, error) {
		// Recheck under the same task lock as deletion so a stale selection or
		// a caller bypassing the desktop cannot remove only part of a batch.
		operations, err := s.store.list(directory)
		if err != nil {
			return 0, err
		}
		for _, group := range GroupTagHistory(operations) {
			count := 0
			for _, operation := range group.Operations {
				if selected[operation.OperationID] {
					count++
				}
			}
			if count > 0 && count != len(group.Operations) {
				return 0, errors.New("Tag历史只能按完整批次删除，所选批次已变化，请刷新后重新确认")
			}
		}
		return s.store.deleteSelected(historyContext(ctx), directory, operationIDs)
	})
}

func (s *Service) ClearHistory(ctx context.Context, config domain.AppConfig, tasks []domain.TaskManifest) (int, error) {
	return s.mutateHistory(ctx, config, tasks, func(directory string) (int, error) {
		return s.store.clear(historyContext(ctx), directory)
	})
}

func (s *Service) mutateHistory(ctx context.Context, config domain.AppConfig, tasks []domain.TaskManifest, mutate func(string) (int, error)) (int, error) {
	ctx = historyContext(ctx)
	deleted := 0
	for _, task := range tasks {
		if err := ctx.Err(); err != nil {
			return deleted, err
		}
		directory, err := historyTaskDirectory(config, task)
		if err != nil {
			return deleted, err
		}
		if directory == "" {
			continue
		}
		err = s.locks.WithTask(directory, func() error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if task.CreatedAt != "" {
				current, err := s.manifests.Load(directory)
				if err != nil {
					return err
				}
				if current.TaskDirectoryName != task.TaskDirectoryName || current.CreatedAt != task.CreatedAt {
					return errors.New("Tag历史所属任务已重新创建，请刷新后重试")
				}
			}
			count, inner := mutate(directory)
			deleted += count
			return inner
		})
		if err != nil {
			return deleted, err
		}
	}
	return deleted, nil
}

func historyTaskDirectory(config domain.AppConfig, task domain.TaskManifest) (string, error) {
	root := strings.TrimSpace(value(config.TaskRoot))
	if root == "" {
		return "", nil
	}
	name := task.TaskDirectoryName
	if !filepath.IsLocal(name) || name == "." || filepath.Base(name) != name || strings.ContainsAny(name, `/\`) {
		return "", errors.New("Tag历史任务目录必须是任务根目录的直接子目录")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	directory := filepath.Join(root, name)
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("Tag历史任务目录不是独立的本地任务目录")
	}
	return directory, nil
}

func historyContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// FormatTagAnnouncement only produces copyable text; it never publishes it.
func FormatTagAnnouncement(requirementTitle string, operations []domain.TagOperation, includeFailures bool) string {
	successes, failures := []domain.TagOperation{}, []domain.TagOperation{}
	for _, operation := range operations {
		if operation.State == domain.TagOperationSuccess && strings.TrimSpace(value(operation.Tag)) != "" {
			successes = append(successes, operation)
		} else {
			failures = append(failures, operation)
		}
	}
	lines := []string{}
	for _, operation := range successes {
		lines = append(lines, operation.ServiceName+" · "+value(operation.Tag))
	}
	if len(lines) == 0 {
		lines = append(lines, "（无）")
	}
	text := ""
	title := strings.TrimSpace(requirementTitle)
	if title != "" {
		text = "需求：" + title
	}
	if includeFailures && len(failures) > 0 {
		if title != "" {
			text += "\n\n"
		}
		text += strings.Join(lines, "\n") + "\n\n构建失败：\n"
		for i, operation := range failures {
			if i > 0 {
				text += "\n"
			}
			text += operation.ServiceName + " · " + stateLabel(operation.State)
			if message := value(operation.Message); strings.TrimSpace(message) != "" {
				text += " · " + message
			}
		}
		return text + "\n\n测试Tag未全部构建成功，请处理失败项后重试"
	}
	if title != "" {
		text += "\n"
	}
	return text + "测试Tag已构建完毕，辛苦发版：\n\n" + strings.Join(lines, "\n")
}
