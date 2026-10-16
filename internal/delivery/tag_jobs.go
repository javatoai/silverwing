package delivery

import (
	"context"
	"errors"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"silverwing-go/internal/domain"
)

// TagTask keeps the validated configuration/manifest snapshot for one task
// lease. App owns scheduling; all Git rules remain in the existing service.
type TagTask struct {
	service *Service
	task    resolvedTask
}

type TagTaskItem struct {
	SelectionKey, WorkspacePath string
	TagMessagePrefix            string
	Resources                   []string
	Operation                   domain.TagOperation
	planFailure                 error
}

func (s *Service) OpenTagTask(folder string) (*TagTask, error) {
	task, err := s.resolveTask(folder)
	if err != nil {
		return nil, err
	}
	if !task.config.TagEnabled {
		return nil, errors.New("全局测试Tag已关闭")
	}
	if _, err := task.config.Group(task.manifest.GroupID); err != nil {
		return nil, err
	}
	return &TagTask{service: s, task: task}, nil
}

func (t *TagTask) Config() domain.AppConfig      { return t.task.config }
func (t *TagTask) Manifest() domain.TaskManifest { return t.task.manifest }
func (t *TagTask) Directory() string             { return t.task.directory }
func (t *TagTask) WithLock(run func() error) error {
	return t.service.locks.WithTask(t.task.directory, run)
}

func NewTagJobID() string { return newUUID() }

func DescribeTagOperation(operation domain.TagOperation, folder string) TagOperationReport {
	return operationReport(operation, folder)
}

func (t *TagTask) SelectionKeys(selections []string, all bool) ([]string, error) {
	if all && len(selections) != 0 {
		return nil, errors.New("不能同时选择全部服务与指定模块")
	}
	var keys []string
	if all {
		for _, w := range t.task.manifest.Services {
			if w.TagEnabled {
				keys = appendUnique(keys, w.SelectionKey())
			}
		}
	} else {
		for _, selection := range selections {
			key, err := canonicalSelection(t.task.manifest, selection)
			if err != nil {
				return nil, err
			}
			keys = appendUnique(keys, key)
		}
	}
	if len(keys) == 0 {
		return nil, errors.New("请选择要构建的任务模块")
	}
	return keys, nil
}

func (t *TagTask) Plan(ctx context.Context, selection, operationID, batchID string) (TagTaskItem, error) {
	var operation domain.TagOperation
	if operationID != "" {
		var err error
		operation, err = t.service.store.load(t.task.directory, operationID)
		if err != nil {
			return TagTaskItem{}, err
		}
		selection = operation.GroupServiceID + ":" + operation.ModuleID
	}
	w, found, err := resolveWorkspace(t.task.manifest, selection)
	if err != nil || !found {
		return TagTaskItem{}, errors.New("Tag模块不属于当前任务")
	}
	if operationID == "" {
		now := formatSilverwingTime(time.Now())
		operation = domain.TagOperation{OperationID: newUUID(), FolderName: t.task.manifest.FolderName,
			ServiceName: w.ServiceName, RepositoryID: w.RepositoryID, SourceBranch: w.Branch,
			Remote: w.PushRemote, TagMode: w.TagMode, TargetBranch: w.TagTargetRef,
			GroupServiceID: w.GroupServiceID, ModuleID: w.ModuleID, BatchID: nonEmptyPointer(batchID),
			State: domain.TagOperationCreated, CreatedAt: now, UpdatedAt: now,
			ConflictFiles: []string{}, GenbuStatus: initialGenbuStatus()}
		if target, err := domain.ResolveEffectiveTagTarget(t.task.config, t.task.manifest, selection); err == nil {
			operation.Remote, operation.TagMode, operation.TargetBranch = target.Remote, target.Mode, target.TargetBranch
		}
	}
	physical := filepath.Join(w.WorktreePath, ".git")
	if repository, ok := repositoryByID(t.task.config, w.RepositoryID); ok && w.Strategy != domain.WorkspaceIndependentClone {
		physical = repository.GitCommonDirectory
	}
	resources := []string{"git:" + normalizedResourcePath(physical)}
	remote, remoteErr := t.service.remoteURL(ctx, w.WorktreePath, operation.Remote)
	if remote != "" {
		resources = append(resources, "remote:"+NormalizeTagRemote(remote))
	} else {
		resources = append(resources, "repository:"+w.RepositoryID)
	}
	return TagTaskItem{SelectionKey: w.SelectionKey(), WorkspacePath: w.WorktreePath, TagMessagePrefix: w.TagMessagePrefix, Resources: resources, Operation: operation, planFailure: remoteErr}, nil
}

func normalizedResourcePath(path string) string {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	if absolute, err := filepath.Abs(path); err == nil {
		path = absolute
	}
	return strings.ToLower(filepath.Clean(path))
}

// HTTPS and SSH aliases of the same hosted repository share one lane. Userinfo
// is excluded, so credentials never become a queue key or progress field.
func NormalizeTagRemote(remote string) string {
	remote = strings.TrimSpace(remote)
	if !strings.Contains(remote, "://") && strings.Contains(remote, ":") && !filepath.IsAbs(remote) {
		parts := strings.SplitN(remote, ":", 2)
		if strings.Contains(parts[0], "@") {
			remote = "ssh://" + parts[0] + "/" + parts[1]
		}
	}
	if parsed, err := url.Parse(remote); err == nil && parsed.Host != "" {
		return strings.ToLower(parsed.Hostname() + "/" + strings.TrimSuffix(strings.Trim(parsed.Path, "/"), ".git"))
	}
	return normalizedResourcePath(strings.TrimPrefix(remote, "file://"))
}

func (t *TagTask) Initialize(ctx context.Context, item TagTaskItem) error {
	return t.service.store.save(ctx, t.task.directory, item.Operation)
}

func (t *TagTask) Run(ctx context.Context, kind string, item TagTaskItem, progress func(TagProgress)) (domain.TagOperation, *TagWorkspaceCheckReport, error) {
	service := *t.service
	service.lockedTask, service.progress = t.task.directory, progress
	if item.planFailure != nil {
		operation := item.Operation
		if kind == "tag.build" {
			operation.State, operation.UpdatedAt, operation.Message = domain.TagOperationFailed, formatSilverwingTime(time.Now()), stringPointer(item.planFailure.Error())
			if err := service.store.save(ctx, t.task.directory, operation); err != nil {
				return operation, nil, errors.Join(item.planFailure, err)
			}
			service.notifyTag("failed", item.planFailure.Error(), tagFailureKind(item.planFailure), &operation)
		}
		return operation, nil, item.planFailure
	}
	switch kind {
	case "tag.build":
		operation, err := service.buildOperationSafely(ctx, t.task, item.SelectionKey, value(item.Operation.BatchID), &item.Operation)
		return operation, nil, err
	case "tag.retry":
		report, err := service.retryTask(ctx, t.task, item.Operation.OperationID)
		return report.Operation, nil, err
	case "tag.workspace-check":
		report, err := service.workspaceCheckTask(ctx, t.task, item.Operation.OperationID)
		return item.Operation, &report, err
	default:
		return domain.TagOperation{}, nil, errors.New("不支持的Tag作业")
	}
}

func (t *TagTask) CancelPending(item TagTaskItem) (domain.TagOperation, error) {
	operation := item.Operation
	operation.State, operation.UpdatedAt = domain.TagOperationFailed, formatSilverwingTime(time.Now())
	operation.Message = stringPointer("已取消，尚未执行Git操作")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return operation, t.service.store.save(ctx, t.task.directory, operation)
}
