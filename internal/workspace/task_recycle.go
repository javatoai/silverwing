package workspace

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
	"silverwing-go/internal/systemtrash"
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
	Force     TaskForceDeletePreview     `json:"force"`
	Guard     string                     `json:"guard,omitempty"`
}
type TaskDeleteOptions struct {
	ForceDiscard    bool
	DeleteMaterials bool
	MaterialsGuard  string
	ForceGuard      string
	Guard           string
}

type TaskForceDeletePreview struct {
	CanForce   bool                `json:"canForce"`
	Guard      string              `json:"guard,omitempty"`
	Reason     string              `json:"reason,omitempty"`
	Workspaces []string            `json:"workspaces,omitempty"`
	Risks      []domain.DeleteRisk `json:"risks,omitempty"`
	TaskPath   string              `json:"taskPath,omitempty"`
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
	force, _, _, err := s.inspectForceDeletion(ctx, config, taskPath, manifest)
	if err != nil {
		return TaskDeletePreview{}, err
	}
	for _, ws := range physicalWorkspaces(manifest) {
		if _, configured := repositoryByID(config, ws.RepositoryID); configured {
			continue
		}
		target, targetErr := s.Lifecycle.validateTarget(taskPath, ws)
		if targetErr != nil {
			continue
		}
		if _, statErr := os.Lstat(target); statErr != nil {
			continue
		}
		found := false
		for _, risk := range risks {
			if risk.ServiceName == ws.ServiceName && risk.StatusCheckError != nil {
				found = true
				break
			}
		}
		if !found {
			risks = append(risks, domain.DeleteRisk{ServiceName: ws.ServiceName, StatusCheckError: ptr("仓库配置不存在，请使用已验证的强制回收")})
		}
	}
	preview := TaskDeletePreview{Risks: risks, Materials: s.inspectMaterialsDeletion(ctx, config, taskPath, manifest), Force: force}
	if force.CanForce || force.Reason != "" {
		return preview, nil
	}
	for _, risk := range risks {
		if risk.StatusCheckError != nil || risk.OperationInProgress != nil {
			return preview, nil
		}
	}
	preview.Guard, err = s.Lifecycle.taskRecycleGuard(ctx, taskPath, manifest)
	if err == nil && len(risks) > 0 {
		var current []domain.DeleteRisk
		current, err = s.Lifecycle.InspectDeleteRisks(ctx, config, taskPath, manifest)
		if err == nil && !reflect.DeepEqual(risks, current) {
			err = errors.New("工作区风险在检查期间发生变化，请重新检查")
		}
	}
	return preview, err
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
	if settings := config.MaterialsGitSettings(); settings.LocalRoot != "" {
		if err := documents.ValidateLocalDocumentsBranch(settings.LocalRoot, settings.Branch); err != nil {
			preview.Reason = err.Error()
			return
		}
	}
	preview.Root = scope.Root
	id := stringValue(manifest.RequirementID)
	if link := domain.ParseFeishuWorkItemLink(manifest.RequirementLink); link != nil {
		id = link.WorkItemID
	}
	if id == "" || !strings.HasPrefix(filepath.Base(scope.Root), id+"-") ||
		isWithin(taskPath, scope.Root) || isWithin(s.Locks.TaskRoot(config), scope.Root) {
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
	taskRoot := s.Locks.TaskRoot(config)
	if !ok || taskRoot == "" {
		preview.Reason = "无法完整检查资料共用关系"
		return
	}
	scan, err := scanner.Scan(taskRoot)
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
		other := documents.ResolveBoundTaskMaterials(entry.Manifest, config)
		otherOwned := strings.TrimSpace(stringValue(other.RequirementMaterials.WriteRoot))
		if otherOwned == "" {
			continue
		}
		shared, err := sharesTaskMaterials(otherOwned, scope, entry.Path)
		if err != nil {
			preview.Reason = "无法确认以下任务是否共用资料，保留资料：" + entry.Manifest.FolderName
			return preview
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

func sharesTaskMaterials(owned string, target documents.AnnualMaterialsScope, otherTask string) (bool, error) {
	if !filepath.IsAbs(owned) {
		return false, errors.New("资料路径不是绝对路径")
	}
	targetInfo, err := os.Stat(target.Root)
	if err != nil {
		return false, err
	}
	// Some Windows junctions have stable file identity but cannot be expanded
	// to their configured drive spelling by EvalSymlinks.
	for path := filepath.Clean(owned); !isWithin(otherTask, path); path = filepath.Dir(path) {
		if info, err := os.Stat(path); err == nil && os.SameFile(info, targetInfo) {
			return true, nil
		}
		if path == filepath.Dir(path) {
			break
		}
	}
	// If a child folder is missing, check its actual existing ancestors. This
	// also detects legacy paths that lead into the yearly tree via a junction.
	resolved := filepath.Clean(owned)
	for {
		path, err := filepath.EvalSymlinks(resolved)
		if err == nil {
			if isWithin(path, target.Root) || samePath(path, target.Root) {
				return true, nil
			}
			break
		}
		if !os.IsNotExist(err) {
			return false, err
		}
		if _, statErr := os.Lstat(resolved); !os.IsNotExist(statErr) {
			return false, err
		}
		next := filepath.Dir(resolved)
		if next == resolved {
			return false, err
		}
		resolved = next
	}
	if other, valid := documents.AnnualScope(owned); valid && samePath(other.Root, target.Root) {
		return true, nil
	}
	return false, nil
}

func (s *TaskApplicationService) DeleteWithOptions(ctx context.Context, config domain.AppConfig, taskPath string, options TaskDeleteOptions) error {
	return s.Locks.WithTaskRoot(s.Locks.TaskRoot(config), func() error {
		return s.withTaskManifestMutation(ctx, config, taskPath, func(ctx context.Context, manifest domain.TaskManifest) error {
			var release func()
			if paths, err := config.MaterialsPaths(); err == nil {
				acquire := storage.AcquireMaterialsRepositoryLock
				if config.MaterialsGitSettings().LocalRoot != "" {
					acquire = storage.AcquireDocumentsRepositoryLock
				}
				release, err = acquire(paths.Root)
				if err != nil {
					return err
				}
				defer release()
			}
			var forcedRoots []string
			if options.ForceGuard == "" {
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
					return errors.New("工作区包含未提交内容，请确认后再移入" + systemtrash.Label())
				}
			} else {
				force, _, roots, err := s.inspectForceDeletion(ctx, config, taskPath, manifest)
				if err != nil {
					return err
				}
				if err := validateForceDeleteApproval(force, options.ForceGuard); err != nil {
					return err
				}
				forcedRoots = roots
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
			var before func() error
			if options.DeleteMaterials {
				before = func() error {
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
			}
			var warnings []string
			var err error
			if options.ForceGuard == "" {
				warnings, err = s.Lifecycle.recycleTask(ctx, config, taskPath, manifest, options, before)
			} else {
				warnings, err = s.recycleVerifiedTask(ctx, config, taskPath, manifest, forcedRoots, options.ForceGuard, before)
			}
			if err != nil && materialsRecycled {
				return fmt.Errorf("资料已移入%s（%s），任务尚未完成回收：%w", systemtrash.Label(), preview.Root, err)
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
	return "任务已移入" + systemtrash.Label() + "，工作区登记清理未完成：" + e.Details
}

func (l *WorkspaceLifecycle) directoryRecycler() documents.DirectoryRecycler {
	if l.Recycler != nil {
		return l.Recycler
	}
	return documents.SystemDirectoryRecycler{}
}

func (l *WorkspaceLifecycle) recycleTaskDirectory(ctx context.Context, config domain.AppConfig, taskPath string, manifest domain.TaskManifest, before, revalidate func() error) error {
	identity, err := documents.DirectoryFingerprint(taskPath)
	if err != nil {
		return err
	}
	// Validate the UI scope before moving materials, whose later absence is an expected effect.
	if err := taskscope.Validate(ctx, config, taskPath, manifest); err != nil {
		return err
	}
	if before != nil {
		if err := revalidate(); err != nil {
			return err
		}
		if err := before(); err != nil {
			return err
		}
	}
	if err := revalidate(); err != nil {
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
	return nil
}

func (l *WorkspaceLifecycle) recycleTask(ctx context.Context, config domain.AppConfig, taskPath string, manifest domain.TaskManifest, options TaskDeleteOptions, before func() error) (warnings []string, resultErr error) {
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
		if !options.ForceDiscard {
			if err := l.requireClean(existing, taskPath); err != nil {
				return err
			}
		}
		guard, err := l.taskRecycleGuard(ctx, taskPath, manifest)
		if err != nil {
			return err
		}
		if options.Guard != "" && options.Guard != guard {
			return errors.New("工作区身份或 Git 状态已变化，请重新检查")
		}
		var proofs []forcedWorkspaceProof
		for _, ws := range existing {
			if ws.Strategy != domain.WorkspaceStandardWorktree {
				continue
			}
			repo, _ := repositoryByID(config, ws.RepositoryID)
			target := mustAbs(ws.WorktreePath)
			identity, err := documents.DirectoryFingerprint(target)
			if err != nil {
				return err
			}
			proof := forcedWorkspaceProof{workspace: ws, target: target, targetIdentity: identity}
			verifier := TaskApplicationService{Git: l.Git}
			if err := verifier.validateLinkedWorktree(ctx, ws, target, repo.RootPath, repo.GitCommonDirectory, stringValue(repo.OriginURL), &proof); err != nil {
				return err
			}
			proof.statusOutput, err = recycleWorkspaceStatus(ctx, l.Git, target)
			if err != nil {
				return err
			}
			proofs = append(proofs, proof)
		}
		warnings, err = l.recycleTaskWithRecovery(ctx, config, taskPath, manifest, proofs, before, func() error {
			if err := l.requireNoUntrackedWorkspaces(config, taskPath, manifest); err != nil {
				return err
			}
			return l.checkTaskRecycleGuard(ctx, taskPath, manifest, guard)
		})
		return err
	})
	return
}
