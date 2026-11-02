package workspace

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
	"silverwing-go/internal/taskscope"
)

type TaskMaterialsDeletePreview struct {
	Root        string   `json:"root"`
	CanDelete   bool     `json:"canDelete"`
	Reason      string   `json:"reason,omitempty"`
	SharedTasks []string `json:"sharedTasks,omitempty"`
	Guard       string   `json:"guard,omitempty"`
}
type TaskDeletePreview struct {
	Risks     []domain.DeleteRisk        `json:"risks"`
	Materials TaskMaterialsDeletePreview `json:"materials"`
}
type TaskDeleteOptions struct {
	ForceDiscard    bool
	DeleteMaterials bool
	MaterialsGuard  string
}

func (s *TaskApplicationService) InspectDeletion(ctx context.Context, config domain.AppConfig, taskPath string) (TaskDeletePreview, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	risks, err := s.InspectDeleteRisks(ctx, config, taskPath)
	if err != nil {
		return TaskDeletePreview{}, err
	}
	manifest, err := s.Manifests.Load(taskPath)
	if err != nil {
		return TaskDeletePreview{}, err
	}
	return TaskDeletePreview{Risks: risks, Materials: s.inspectMaterialsDeletion(ctx, config, taskPath, manifest)}, nil
}

func (s *TaskApplicationService) inspectMaterialsDeletion(ctx context.Context, config domain.AppConfig, taskPath string, manifest domain.TaskManifest) (preview TaskMaterialsDeletePreview) {
	owned := stringValue(manifest.RequirementMaterials.WriteRoot)
	if owned == "" {
		preview.Reason = "无独立任务资料目录"
		return
	}
	if _, local := documents.LocalScope(owned); local {
		preview.Reason = "任务内资料随任务回收"
		return
	}
	scope, ok := documents.AnnualScope(owned)
	paths, err := config.MaterialsPaths()
	if !ok || err != nil || !samePath(scope.Base, paths.Requirements) || !strings.EqualFold(filepath.Base(owned), stringValue(config.RequirementMaterialsSubdirectory)) {
		preview.Reason = "无法安全确认任务资料目录"
		return
	}
	preview.Root = scope.Root
	id := stringValue(manifest.RequirementID)
	if link := domain.ParseFeishuWorkItemLink(manifest.RequirementLink); link != nil {
		id = link.WorkItemID
	}
	if id == "" || !strings.HasPrefix(filepath.Base(scope.Root), id+"-") ||
		isWithin(taskPath, scope.Root) || isWithin(stringValue(config.TaskRoot), scope.Root) {
		preview.Reason = "资料目录与需求身份不一致或包含受保护的任务目录"
		return
	}
	if isWithin(scope.Root, taskPath) {
		preview.Reason = "任务内资料随任务回收"
		return
	}
	identity, err := documents.DirectoryFingerprint(scope.Root)
	if err != nil {
		preview.Reason = "资料目录不存在或不可安全读取"
		return
	}
	scanner, ok := s.Manifests.(interface {
		Scan(string) (storage.ManifestScanResult, error)
	})
	if !ok || config.TaskRoot == nil {
		preview.Reason = "无法完整检查资料共用关系"
		return
	}
	scan, err := scanner.Scan(*config.TaskRoot)
	if err != nil || len(scan.Failures) != 0 || len(scan.UnsupportedPaths) != 0 {
		preview.Reason = "任务清单未能完整读取，保留资料"
		return
	}
	for _, entry := range scan.Current {
		if ctx.Err() != nil {
			preview.Reason = "资料检查已取消"
			return
		}
		if samePath(entry.Path, taskPath) {
			continue
		}
		other, valid := documents.AnnualScope(stringValue(entry.Manifest.RequirementMaterials.WriteRoot))
		if !valid {
			continue
		}
		shared := samePath(other.Root, scope.Root)
		if !shared {
			otherID, err := documents.DirectoryFingerprint(other.Root)
			shared = err == nil && otherID == identity
		}
		if shared {
			preview.SharedTasks = append(preview.SharedTasks, entry.Manifest.FolderName)
		}
	}
	if len(preview.SharedTasks) > 0 {
		preview.Reason = "与其他任务共用，资料将保留：" + strings.Join(preview.SharedTasks, "、")
		return
	}
	guard := sha256.Sum256([]byte(strings.Join([]string{scope.Root, identity, manifest.CreatedAt, manifest.RequirementLink, owned, paths.Root}, "\x00")))
	preview.Guard = fmt.Sprintf("%x", guard)
	preview.CanDelete = true
	return
}

func (s *TaskApplicationService) DeleteWithOptions(ctx context.Context, config domain.AppConfig, taskPath string, options TaskDeleteOptions) error {
	return s.Locks.WithTaskRoot(stringValue(config.TaskRoot), func() error {
		return s.withTaskManifestMutation(ctx, config, taskPath, func(ctx context.Context, manifest domain.TaskManifest) error {
			var release func()
			if paths, err := config.MaterialsPaths(); err == nil {
				release, err = storage.AcquireMaterialsRepositoryLock(paths.Root)
				if err != nil {
					return err
				}
				defer release()
			}
			risks, err := s.Lifecycle.InspectDeleteRisks(ctx, config, taskPath, manifest)
			if err != nil {
				return err
			}
			for _, risk := range risks {
				if risk.StatusCheckError != nil {
					return errors.New("存在无法安全验证的工作区，请先修复后再回收")
				}
			}
			if len(risks) > 0 && !options.ForceDiscard {
				return errors.New("工作区包含未提交内容，请确认后再移入回收站")
			}
			preview := TaskMaterialsDeletePreview{}
			if options.DeleteMaterials {
				preview = s.inspectMaterialsDeletion(ctx, config, taskPath, manifest)
				if !preview.CanDelete {
					return errors.New(preview.Reason)
				}
				if options.MaterialsGuard == "" || options.MaterialsGuard != preview.Guard {
					return errors.New("资料目录或关联已变化，请重新检查")
				}
			}
			materialsRecycled := false
			before := func() error {
				if !options.DeleteMaterials {
					return nil
				}
				current := s.inspectMaterialsDeletion(ctx, config, taskPath, manifest)
				if !current.CanDelete || current.Guard != preview.Guard {
					return errors.New("资料目录或共用关系已变化，请重新检查")
				}
				if err := taskscope.Validate(ctx, config, taskPath, manifest); err != nil {
					return err
				}
				if err := s.Lifecycle.directoryRecycler().Recycle(ctx, preview.Root); err != nil {
					return fmt.Errorf("资料未能回收，任务保留：%w", err)
				}
				materialsRecycled = true
				return nil
			}
			warnings, err := s.Lifecycle.recycleTask(ctx, config, taskPath, manifest, options.ForceDiscard, before)
			if err != nil && materialsRecycled {
				return fmt.Errorf("资料已移入回收站（%s），任务尚未完成回收：%w", preview.Root, err)
			}
			if err != nil {
				return err
			}
			if len(warnings) > 0 {
				return &TaskRecycleCleanupWarning{Details: strings.Join(warnings, "；")}
			}
			return nil
		})
	})
}

type TaskRecycleCleanupWarning struct{ Details string }

func (e *TaskRecycleCleanupWarning) Error() string {
	return "任务已移入回收站，工作区登记清理未完成：" + e.Details
}

func (l *WorkspaceLifecycle) directoryRecycler() documents.DirectoryRecycler {
	if l.Recycler != nil {
		return l.Recycler
	}
	return documents.SystemDirectoryRecycler{}
}

func (l *WorkspaceLifecycle) recycleTask(ctx context.Context, config domain.AppConfig, taskPath string, manifest domain.TaskManifest, force bool, before func() error) (warnings []string, resultErr error) {
	if err := l.validateTaskDirectory(config, taskPath, manifest); err != nil {
		return nil, err
	}
	workspaces := physicalWorkspaces(manifest)
	if err := l.validateRemovalCandidates(config, taskPath, workspaces); err != nil {
		return nil, err
	}
	resultErr = l.withRemovalRepositoryLocks(config, taskPath, manifest, func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := l.validateRemovalCandidates(config, taskPath, workspaces); err != nil {
			return err
		}
		if err := l.requireNoUntrackedWorkspaces(config, taskPath, manifest); err != nil {
			return err
		}
		existing, err := l.existingWorkspaces(taskPath, workspaces)
		if err != nil {
			return err
		}
		if err := l.requireNoGitOperation(existing, taskPath); err != nil {
			return err
		}
		if !force {
			if err := l.requireClean(existing, taskPath); err != nil {
				return err
			}
		}
		identity, err := documents.DirectoryFingerprint(taskPath)
		if err != nil {
			return err
		}
		// Validate the full UI guard before our own materials move. Materials
		// disappearance after that move is an expected effect of this action.
		if err := taskscope.Validate(ctx, config, taskPath, manifest); err != nil {
			return err
		}
		if err := before(); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		current, err := documents.DirectoryFingerprint(taskPath)
		if err != nil || current != identity {
			return errors.New("任务目录身份已变化，回收已停止")
		}
		if err := l.directoryRecycler().Recycle(ctx, taskPath); err != nil {
			return err
		}
		if _, err := os.Lstat(taskPath); !os.IsNotExist(err) {
			return errors.New("系统未确认任务目录已回收，请刷新后核对")
		}
		for _, ws := range existing {
			if ws.Strategy != domain.WorkspaceStandardWorktree {
				continue
			}
			repo, err := l.configuredRepository(config, ws)
			if err == nil {
				if _, statErr := os.Lstat(ws.WorktreePath); !os.IsNotExist(statErr) {
					err = errors.New("工作区路径重新出现，保留其 Git 登记")
				} else {
					err = l.Git.RemoveWorktree(repo, ws.WorktreePath, true)
				}
			}
			if err != nil {
				warnings = append(warnings, err.Error())
			}
		}
		return nil
	})
	return
}
