package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
	"silverwing-go/internal/systemtrash"
	"silverwing-go/internal/taskscope"
)

type WorkspaceModuleRemovalPreview struct {
	WorkspacePath            string   `json:"workspacePath"`
	ServiceID                string   `json:"serviceId"`
	ModuleID                 string   `json:"moduleId"`
	ServiceName              string   `json:"serviceName"`
	ModuleName               string   `json:"moduleName"`
	Branch                   string   `json:"branch"`
	BaseRef                  *string  `json:"baseRef"`
	Staged                   bool     `json:"staged"`
	Unstaged                 bool     `json:"unstaged"`
	Untracked                bool     `json:"untracked"`
	ChangedFiles             []string `json:"changedFiles"`
	CommitsAheadOfBase       int      `json:"commitsAheadOfBase"`
	CommitsBehindBase        int      `json:"commitsBehindBase"`
	BaseComparisonKnown      bool     `json:"baseComparisonKnown"`
	UnpushedCommits          int      `json:"unpushedCommits"`
	UpstreamComparisonKnown  bool     `json:"upstreamComparisonKnown"`
	PathMissing              bool     `json:"pathMissing"`
	RequiresRiskConfirmation bool     `json:"requiresRiskConfirmation"`
	Fingerprint              string   `json:"fingerprint"`
}

type WorkspaceModuleRemovalConfirmation struct {
	Fingerprint         string `json:"fingerprint"`
	AcknowledgeDataLoss bool   `json:"acknowledgeDataLoss"`
}

type WorkspaceModuleRemovalResult struct {
	Manifest          domain.TaskManifest `json:"manifest"`
	DirectoryRecycled bool                `json:"directoryRecycled"`
	CleanupError      string              `json:"cleanupError"`
}

type WorkspaceModuleRemovalService struct {
	Manifests *storage.ManifestStore
	Documents *AgentDocumentService
	Locks     *OperationLocks
	Git       *GitClient
	Recycler  documents.DirectoryRecycler
	Now       func() time.Time
}

func NewWorkspaceModuleRemovalService(manifests *storage.ManifestStore, docs *AgentDocumentService, locks *OperationLocks, git *GitClient) *WorkspaceModuleRemovalService {
	if manifests == nil {
		manifests = storage.NewManifestStore()
	}
	if git == nil {
		git = NewGitClient(nil, "")
	}
	if docs == nil {
		docs = NewAgentDocumentService(storage.NewPaths("."), manifests)
	}
	if locks == nil {
		locks = NewOperationLocks("")
	}
	return &WorkspaceModuleRemovalService{Manifests: manifests, Documents: docs, Locks: locks, Git: git, Now: time.Now}
}

func (s *WorkspaceModuleRemovalService) Inspect(config domain.AppConfig, taskPath, workspacePath string) (WorkspaceModuleRemovalPreview, error) {
	return s.InspectContext(context.Background(), config, taskPath, workspacePath)
}

func (s *WorkspaceModuleRemovalService) InspectContext(ctx context.Context, config domain.AppConfig, taskPath, workspacePath string) (WorkspaceModuleRemovalPreview, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var preview WorkspaceModuleRemovalPreview
	err := s.withModuleRemovalTarget(ctx, config, taskPath, workspacePath, func(l *WorkspaceLifecycle, target moduleRemovalTarget) error {
		var err error
		preview, _, err = target.inspect(ctx, l.Git)
		return err
	})
	return preview, err
}

func (s *WorkspaceModuleRemovalService) Remove(ctx context.Context, config domain.AppConfig, taskPath string, preview WorkspaceModuleRemovalPreview, confirmation WorkspaceModuleRemovalConfirmation) (WorkspaceModuleRemovalResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return WorkspaceModuleRemovalResult{}, err
	}
	if preview.Fingerprint == "" || confirmation.Fingerprint != preview.Fingerprint {
		return WorkspaceModuleRemovalResult{}, errModuleRemovalChanged
	}
	var result WorkspaceModuleRemovalResult
	err := s.Locks.WithTaskRoot(stringValue(config.TaskRoot), func() error {
		return s.withModuleRemovalTarget(ctx, config, taskPath, preview.WorkspacePath, func(l *WorkspaceLifecycle, target moduleRemovalTarget) error {
			current, risk, err := target.inspect(ctx, l.Git)
			if err != nil {
				return err
			}
			if current.Fingerprint != preview.Fingerprint || current.ServiceID != preview.ServiceID || current.ModuleID != preview.ModuleID {
				return errModuleRemovalChanged
			}
			if current.RequiresRiskConfirmation && !confirmation.AcknowledgeDataLoss {
				return errors.New("请确认未提交内容、提交差异或无法核实的提交状态后再回收模块")
			}
			result, err = s.recycleModule(ctx, l, config, target, risk)
			return err
		})
	})
	return result, err
}

var errModuleRemovalChanged = errors.New("任务或工作区状态已变化，请重新检查后确认")

type moduleRemovalTarget struct {
	taskPath, taskIdentity  string
	path, directoryIdentity string
	manifest                domain.TaskManifest
	workspace               domain.ServiceWorkspace
	repository              domain.RepositoryConfig
}

func (s *WorkspaceModuleRemovalService) withModuleRemovalTarget(ctx context.Context, config domain.AppConfig, taskPath, workspacePath string, operation func(*WorkspaceLifecycle, moduleRemovalTarget) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	l := &WorkspaceLifecycle{Git: s.offlineGit(), Locks: s.Locks, Recycler: s.Recycler}
	return s.Locks.WithTask(taskPath, func() error {
		taskPath, err := filepath.Abs(taskPath)
		if err != nil {
			return err
		}
		if config.TaskRoot == nil || strings.TrimSpace(*config.TaskRoot) == "" || !samePath(filepath.Dir(taskPath), *config.TaskRoot) {
			return fmt.Errorf("任务目录与配置的任务根目录不匹配：%s", taskPath)
		}
		taskIdentity, err := documents.DirectoryFingerprint(taskPath)
		if err != nil {
			return err
		}
		entry, err := s.Manifests.LoadTask(*config.TaskRoot, filepath.Base(taskPath))
		if err != nil {
			return err
		}
		manifest := entry.Manifest
		if err := l.validateTaskDirectory(config, taskPath, manifest); err != nil {
			return err
		}
		if err := taskscope.Validate(ctx, config, taskPath, manifest); err != nil {
			return err
		}
		ws, ok := physicalWorkspace(manifest, workspacePath)
		if !ok {
			return fmt.Errorf("任务中不存在指定模块：%s", workspacePath)
		}
		path, err := l.validateTarget(taskPath, ws)
		if err != nil {
			return err
		}
		identity, err := moduleDirectoryIdentity(path)
		if err != nil {
			return err
		}
		target := moduleRemovalTarget{taskPath: taskPath, taskIdentity: taskIdentity, path: path, directoryIdentity: identity, manifest: manifest, workspace: ws}
		run := func() error {
			if err := target.revalidateDirectories(ctx); err != nil {
				return err
			}
			return operation(l, target)
		}
		if identity == "" {
			return run()
		}
		switch ws.Strategy {
		case domain.WorkspaceStandardWorktree:
			repo, ok := repositoryByID(config, ws.RepositoryID)
			if !ok || strings.TrimSpace(repo.GitCommonDirectory) == "" {
				return fmt.Errorf("模块仓库配置不存在或缺少 Git 公共目录：%s", ws.RepositoryID)
			}
			target.repository = repo
			return s.Locks.WithRepository(repo.GitCommonDirectory, run)
		case domain.WorkspaceIndependentClone:
			return s.Locks.WithRepository(filepath.Join(path, ".git"), run)
		default:
			return fmt.Errorf("未知工作区策略：%s", ws.Strategy)
		}
	})
}

func (s *WorkspaceModuleRemovalService) offlineGit() *GitClient {
	git := workspaceRecycleGit(s.Git)
	// Partial clones can fetch missing objects during status and revision walks.
	git.Environment["GIT_ALLOW_PROTOCOL"] = ""
	git.Environment["GIT_OPTIONAL_LOCKS"] = "0"
	return git
}

func moduleDirectoryIdentity(path string) (string, error) {
	identity, err := documents.DirectoryFingerprint(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	return identity, err
}

func (target moduleRemovalTarget) revalidateDirectories(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if current, err := documents.DirectoryFingerprint(target.taskPath); err != nil || current != target.taskIdentity {
		return errModuleRemovalChanged
	}
	if current, err := moduleDirectoryIdentity(target.path); err != nil || current != target.directoryIdentity {
		return errModuleRemovalChanged
	}
	return nil
}

func (target moduleRemovalTarget) inspect(ctx context.Context, git *GitClient) (WorkspaceModuleRemovalPreview, moduleRemovalRisk, error) {
	ws := target.workspace
	preview := WorkspaceModuleRemovalPreview{
		WorkspacePath: target.path, ServiceID: ws.GroupServiceID, ModuleID: ws.ModuleID,
		ServiceName: ws.ServiceName, ModuleName: ws.ModuleName, Branch: ws.Branch, BaseRef: ws.BaseRef,
		ChangedFiles: []string{}, PathMissing: target.directoryIdentity == "",
	}
	var risk moduleRemovalRisk
	var gitIdentity string
	if !preview.PathMissing {
		admin := filepath.Join(target.path, ".git")
		if ws.Strategy == domain.WorkspaceStandardWorktree {
			verifier := TaskApplicationService{Git: git}
			var proof forcedWorkspaceProof
			repo := target.repository
			expectedOrigin := strings.TrimSpace(stringValue(repo.OriginURL))
			if recorded := strings.TrimSpace(stringValue(ws.OriginURL)); recorded != "" {
				if expectedOrigin != "" && expectedOrigin != recorded {
					return WorkspaceModuleRemovalPreview{}, risk, errors.New("仓库配置 origin 与模块工作区记录不一致")
				}
				expectedOrigin = recorded
			}
			if err := verifier.validateLinkedWorktree(ctx, ws, target.path, repo.RootPath, repo.GitCommonDirectory, expectedOrigin, &proof); err != nil {
				return WorkspaceModuleRemovalPreview{}, risk, err
			}
			admin = proof.adminDirectory
			gitIdentity = strings.Join([]string{proof.commonIdentity, proof.adminIdentity, proof.gitDirFile, proof.adminGitDirFile}, "\x00")
		} else {
			owner, err := verifyModuleRecycleClone(ctx, git, target.taskPath, ws, target.path)
			if err != nil {
				return WorkspaceModuleRemovalPreview{}, risk, err
			}
			identity, err := documents.DirectoryFingerprint(admin)
			if err != nil {
				return WorkspaceModuleRemovalPreview{}, risk, err
			}
			gitIdentity = identity + "\x00" + string(owner)
		}
		var err error
		risk, err = inspectModuleRemovalRisk(ctx, git, ws, target.path, admin)
		if err != nil {
			return WorkspaceModuleRemovalPreview{}, risk, err
		}
		preview.Staged, preview.Unstaged, preview.Untracked = risk.staged, risk.unstaged, risk.untracked
		preview.ChangedFiles = risk.files
		preview.CommitsAheadOfBase, preview.CommitsBehindBase = risk.base.ahead, risk.base.behind
		preview.BaseComparisonKnown = risk.base.known
		preview.UnpushedCommits, preview.UpstreamComparisonKnown = risk.upstream.ahead, risk.upstream.known
		preview.RequiresRiskConfirmation = risk.staged || risk.unstaged || risk.untracked ||
			risk.base.ahead > 0 || risk.upstream.ahead > 0 || !risk.base.known || !risk.upstream.known
	}
	var rows []domain.ServiceWorkspace
	for _, candidate := range target.manifest.Services {
		if samePath(candidate.WorktreePath, target.path) {
			if !sameRecyclePath(mustAbs(candidate.WorktreePath), target.path) {
				return WorkspaceModuleRemovalPreview{}, risk, errors.New("任务清单中存在指向此工作区的重定向路径，请先核对")
			}
			rows = append(rows, candidate)
		}
	}
	encodedRows, err := json.Marshal(rows)
	if err != nil {
		return WorkspaceModuleRemovalPreview{}, risk, err
	}
	fingerprint := sha256.Sum256([]byte(strings.Join([]string{
		target.taskPath, target.taskIdentity, target.manifest.TaskDirectoryName, target.manifest.CreatedAt, target.manifest.GroupID,
		target.path, target.directoryIdentity, gitIdentity, string(encodedRows), risk.fingerprint,
	}, "\x00")))
	preview.Fingerprint = fmt.Sprintf("%x", fingerprint)
	return preview, risk, ctx.Err()
}

func (s *WorkspaceModuleRemovalService) recycleModule(ctx context.Context, l *WorkspaceLifecycle, config domain.AppConfig, target moduleRemovalTarget, risk moduleRemovalRisk) (WorkspaceModuleRemovalResult, error) {
	if err := taskscope.Validate(ctx, config, target.taskPath, target.manifest); err != nil {
		return WorkspaceModuleRemovalResult{}, err
	}
	taskContext, err := captureWorkspaceTaskContext(target.taskPath)
	if err != nil {
		return WorkspaceModuleRemovalResult{}, err
	}
	var record *workspaceRecycleRecord
	if target.directoryIdentity != "" {
		record, err = l.prepareModuleRecycle(ctx, target.taskPath, target.manifest, target.workspace, target.repository)
		if err != nil {
			return WorkspaceModuleRemovalResult{}, err
		}
	}
	original := target.manifest
	updated := original
	updated.UpdatedAt = domain.FormatSilverWingTime(s.now())
	updated.Services = make([]domain.ServiceWorkspace, 0, len(original.Services)-1)
	for _, candidate := range original.Services {
		if !sameRecyclePath(mustAbs(candidate.WorktreePath), target.path) {
			updated.Services = append(updated.Services, candidate)
		}
	}
	manifestSaved := false
	fail := func(primary error) (WorkspaceModuleRemovalResult, error) {
		rollbackErr := s.rollbackModuleRemoval(target, taskContext, updated, manifestSaved)
		if rollbackErr != nil {
			return WorkspaceModuleRemovalResult{}, fmt.Errorf("模块回收失败且自动恢复未完成：%w", errors.Join(primary, rollbackErr))
		}
		if record != nil {
			if err := target.revalidateDirectories(context.Background()); err != nil {
				return WorkspaceModuleRemovalResult{}, fmt.Errorf("模块回收未确认，已恢复任务记录并保留恢复数据：%w", primary)
			}
			if err := record.abort(); err != nil {
				return WorkspaceModuleRemovalResult{}, fmt.Errorf("模块回收未确认，已恢复任务记录并保留恢复数据：%w", errors.Join(primary, err))
			}
		}
		return WorkspaceModuleRemovalResult{}, primary
	}
	if err := target.revalidateDirectories(ctx); err != nil {
		return fail(err)
	}
	if err := s.checkModuleRemovalManifest(target.taskPath, original); err != nil {
		return fail(err)
	}
	if err := s.Manifests.Save(target.taskPath, updated); err != nil {
		return fail(err)
	}
	manifestSaved = true
	if err := taskContext.write(s.Documents, updated, config); err != nil {
		return fail(err)
	}
	result := WorkspaceModuleRemovalResult{Manifest: updated}
	if record != nil {
		if err := record.revalidate(ctx, l.Git); err != nil {
			return fail(err)
		}
		if rechecked, err := inspectModuleRemovalRisk(ctx, l.Git, target.workspace, target.path, risk.admin); err != nil {
			return fail(err)
		} else if rechecked.fingerprint != risk.fingerprint {
			return fail(errModuleRemovalChanged)
		}
	}
	if err := taskscope.Validate(ctx, config, target.taskPath, original); err != nil {
		return fail(err)
	}
	if err := s.checkModuleRemovalManifest(target.taskPath, updated); err != nil {
		return fail(err)
	}
	if err := target.revalidateDirectories(ctx); err != nil {
		return fail(err)
	}
	if record == nil {
		return result, nil
	}
	if err := requireModuleGitIdle(risk.admin); err != nil {
		return fail(err)
	}
	if err := l.directoryRecycler().Recycle(ctx, target.path); err != nil {
		return fail(fmt.Errorf("无法将模块目录移入系统%s：%w", systemtrash.Label(), err))
	}
	if _, err := os.Lstat(target.path); !os.IsNotExist(err) {
		return fail(fmt.Errorf("系统未确认模块目录已移入%s", systemtrash.Label()))
	}
	result.DirectoryRecycled = true
	if err := record.finish(l.Git); err != nil {
		result.CleanupError = err.Error()
	}
	return result, nil
}

func (s *WorkspaceModuleRemovalService) rollbackModuleRemoval(target moduleRemovalTarget, taskContext *workspaceTaskContextUpdate, updated domain.TaskManifest, manifestSaved bool) error {
	if !manifestSaved {
		return nil
	}
	if current, err := documents.DirectoryFingerprint(target.taskPath); err != nil || current != target.taskIdentity {
		return errModuleRemovalChanged
	}
	if err := s.checkModuleRemovalManifest(target.taskPath, updated); err != nil {
		return err
	}
	return errors.Join(s.Manifests.Save(target.taskPath, target.manifest), taskContext.rollback())
}

func (s *WorkspaceModuleRemovalService) checkModuleRemovalManifest(path string, expected domain.TaskManifest) error {
	entry, err := s.Manifests.LoadTask(filepath.Dir(path), filepath.Base(path))
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(domain.NormalizeTaskManifest(entry.Manifest), domain.NormalizeTaskManifest(expected)) {
		return errModuleRemovalChanged
	}
	return nil
}

func (s *WorkspaceModuleRemovalService) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
