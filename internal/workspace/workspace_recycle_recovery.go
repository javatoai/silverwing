package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

const (
	workspaceRecycleVersion = 1
	workspaceRecycleFile    = "receipt.json"
	workspaceRecycleAdmin   = "admin"
	workspaceRecyclePending = "prepared"
	workspaceRecycleMoved   = "recycled"
	workspaceRecycleDone    = "restored"
)

type workspaceRecycleRow struct {
	Position  int                     `json:"position"`
	Workspace domain.ServiceWorkspace `json:"workspace"`
}

type workspaceRecycleReceipt struct {
	Version            int                        `json:"version"`
	State              string                     `json:"state"`
	TaskPath           string                     `json:"taskPath,omitempty"`
	ProfileID          string                     `json:"profileId,omitempty"`
	TaskIdentity       string                     `json:"taskIdentity"`
	TaskCreatedAt      string                     `json:"taskCreatedAt"`
	TaskDirectoryName  string                     `json:"taskDirectoryName"`
	TaskGroupID        string                     `json:"taskGroupId"`
	WorkspaceDirectory string                     `json:"workspaceDirectory,omitempty"`
	Workspace          domain.ServiceWorkspace    `json:"workspace"`
	Target             string                     `json:"target"`
	TargetIdentity     string                     `json:"targetIdentity"`
	RepositoryRoot     string                     `json:"repositoryRoot,omitempty"`
	RepositoryIdentity string                     `json:"repositoryIdentity,omitempty"`
	CommonDirectory    string                     `json:"commonDirectory"`
	CommonIdentity     string                     `json:"commonIdentity"`
	AdminDirectory     string                     `json:"adminDirectory,omitempty"`
	AdminIdentity      string                     `json:"adminIdentity,omitempty"`
	AdminMode          uint32                     `json:"adminMode,omitempty"`
	AdminGitDirFile    string                     `json:"adminGitDirFile,omitempty"`
	GitFileDigest      string                     `json:"gitFileDigest,omitempty"`
	Branch             string                     `json:"branch"`
	Head               string                     `json:"head"`
	CloneOwner         cloneRecycleOwnership      `json:"cloneOwner,omitempty"`
	ModuleRows         []workspaceRecycleRow      `json:"moduleRows,omitempty"`
	AdminFiles         []workspaceRecycleMetadata `json:"adminFiles,omitempty"`
	IndexPack          *workspaceRecyclePack      `json:"indexPack,omitempty"`
}

type workspaceRecycleRecord struct {
	workspaceRecycleReceipt
	directory         string
	directoryIdentity string
	receiptDigest     string
	statusOutput      string
	taskPath          string
}

func (l *WorkspaceLifecycle) prepareModuleRecycle(ctx context.Context, taskPath string, manifest domain.TaskManifest, ws domain.ServiceWorkspace, repo domain.RepositoryConfig) (*workspaceRecycleRecord, error) {
	target, err := l.validateTarget(taskPath, ws)
	if err != nil {
		return nil, err
	}
	git := workspaceRecycleGit(l.Git)
	proof := forcedWorkspaceProof{workspace: ws, target: target}
	switch ws.Strategy {
	case domain.WorkspaceStandardWorktree:
		expectedOrigin := strings.TrimSpace(stringValue(repo.OriginURL))
		if recorded := strings.TrimSpace(stringValue(ws.OriginURL)); recorded != "" {
			if expectedOrigin != "" && expectedOrigin != recorded {
				return nil, errors.New("仓库配置 origin 与模块工作区记录不一致")
			}
			expectedOrigin = recorded
		}
		validator := TaskApplicationService{Git: git}
		if err := validator.validateLinkedWorktree(ctx, ws, target, repo.RootPath, repo.GitCommonDirectory, expectedOrigin, &proof); err != nil {
			return nil, err
		}
	case domain.WorkspaceIndependentClone:
		proof.cloneOwner, err = verifyModuleRecycleClone(ctx, git, taskPath, ws, target)
		if err != nil {
			return nil, err
		}
		proof.commonDirectory = filepath.Join(target, ".git")
		proof.commonIdentity, err = documents.DirectoryFingerprint(proof.commonDirectory)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("工作区策略不支持回收：%s", ws.Strategy)
	}
	proof.targetIdentity, err = documents.DirectoryFingerprint(target)
	if err != nil {
		return nil, err
	}
	proof.branch, proof.head, proof.statusOutput, err = workspaceStateFingerprint(ctx, git, target)
	if err != nil {
		return nil, err
	}
	var rows []workspaceRecycleRow
	for index, row := range manifest.Services {
		if sameRecyclePath(mustAbs(row.WorktreePath), target) {
			rows = append(rows, workspaceRecycleRow{Position: index, Workspace: row})
		}
	}
	if len(rows) == 0 {
		return nil, errors.New("任务清单中找不到要回收的模块工作区")
	}
	return l.prepareWorkspaceRecycle(ctx, taskPath, manifest, proof, rows)
}

func verifyModuleRecycleClone(ctx context.Context, git *GitClient, taskPath string, ws domain.ServiceWorkspace, target string) (cloneRecycleOwnership, error) {
	owner, err := recycleCloneOwner(target, cloneOwnership(taskPath, ws.RepositoryID, ws.GroupServiceID, ws.ModuleID))
	if err != nil {
		return "", err
	}
	if owner == cloneRecycleMissing {
		return "", fmt.Errorf("独立克隆缺少可核验的所有权标记：%s", target)
	}
	validator := TaskApplicationService{Git: git}
	if err := validator.validateIndependentCloneForRecycle(ctx, ws, target); err != nil {
		return "", err
	}
	common, err := gitCommonDirectoryContext(ctx, git, target)
	if err != nil || !sameRecyclePath(common, filepath.Join(target, ".git")) {
		return "", fmt.Errorf("独立克隆的 Git 公共目录与工作区不匹配：%s", target)
	}
	worktrees, err := git.WorktreesContext(ctx, target)
	if err != nil {
		return "", err
	}
	if len(worktrees) != 1 || !sameRecyclePath(worktrees[0].Path, target) || worktrees[0].Bare {
		return "", fmt.Errorf("独立克隆存在其他 Git 工作区登记：%s", target)
	}
	return owner, nil
}

func (l *WorkspaceLifecycle) prepareTaskWorkspaceRecycle(ctx context.Context, taskPath string, manifest domain.TaskManifest, proof forcedWorkspaceProof) (*workspaceRecycleRecord, error) {
	return l.prepareWorkspaceRecycle(ctx, taskPath, manifest, proof, nil)
}

func (l *WorkspaceLifecycle) prepareWorkspaceRecycle(ctx context.Context, taskPath string, manifest domain.TaskManifest, proof forcedWorkspaceProof, rows []workspaceRecycleRow) (_ *workspaceRecycleRecord, resultErr error) {
	taskPath = mustAbs(taskPath)
	taskIdentity, err := documents.DirectoryFingerprint(taskPath)
	if err != nil {
		return nil, err
	}
	record := &workspaceRecycleRecord{
		workspaceRecycleReceipt: workspaceRecycleReceipt{
			Version: workspaceRecycleVersion, State: workspaceRecyclePending,
			TaskPath: taskPath, TaskIdentity: taskIdentity, TaskCreatedAt: manifest.CreatedAt,
			TaskDirectoryName: manifest.TaskDirectoryName, TaskGroupID: manifest.GroupID,
			ProfileID: l.Locks.Paths.ProfileID,
			Workspace: proof.workspace, Target: proof.target, TargetIdentity: proof.targetIdentity,
			RepositoryRoot: proof.repositoryRoot, CommonDirectory: proof.commonDirectory, CommonIdentity: proof.commonIdentity,
			AdminDirectory: proof.adminDirectory, AdminIdentity: proof.adminIdentity, AdminGitDirFile: proof.adminGitDirFile,
			Branch: proof.branch, Head: proof.head, CloneOwner: proof.cloneOwner, ModuleRows: rows,
		},
		statusOutput: proof.statusOutput,
		taskPath:     taskPath,
	}
	if !record.matchesTask(l.Locks.Paths.ProfileID, taskPath, taskIdentity, manifest) || !filepath.IsAbs(record.Target) ||
		!sameRecyclePath(filepath.Dir(record.Target), record.workspaceDirectory()) || !sameRecyclePath(mustAbs(record.Workspace.WorktreePath), record.Target) ||
		!validGitObjectID(record.Head) || record.Branch != record.Workspace.Branch {
		return nil, errors.New("回收工作区的任务或 Git 身份不完整")
	}
	if err := record.checkTargetIdentity(); err != nil {
		return nil, err
	}
	if err := checkRecycleDirectory(record.CommonDirectory, record.CommonIdentity); err != nil {
		return nil, err
	}
	if record.Workspace.Strategy == domain.WorkspaceStandardWorktree {
		record.RepositoryIdentity, err = documents.DirectoryFingerprint(record.RepositoryRoot)
		if err != nil {
			return nil, err
		}
		if err := checkRecycleDirectory(record.AdminDirectory, record.AdminIdentity); err != nil {
			return nil, err
		}
		info, err := os.Lstat(record.AdminDirectory)
		if err != nil {
			return nil, err
		}
		record.AdminMode = uint32(info.Mode().Perm())
		pointer, err := record.readLinkedPointer()
		if err != nil || strings.TrimSpace(string(pointer)) != proof.gitDirFile {
			return nil, fmt.Errorf("工作区 Git 指针与回收确认不一致：%s", record.Target)
		}
		record.GitFileDigest = workspaceRecycleDigest(pointer)
	}
	root, err := workspaceRecycleRoot(l.Locks, taskPath, true)
	if err != nil {
		return nil, err
	}
	record.directory, err = os.MkdirTemp(root, "receipt-")
	if err != nil {
		return nil, err
	}
	record.directoryIdentity, err = documents.DirectoryFingerprint(record.directory)
	if err != nil {
		return nil, err
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, record.abort())
		}
	}()
	git := workspaceRecycleGit(l.Git)
	if record.Workspace.Strategy == domain.WorkspaceStandardWorktree {
		if err := record.captureLinkedMetadata(ctx, git); err != nil {
			return nil, err
		}
	}
	if err := record.verifyLive(ctx, git); err != nil {
		return nil, err
	}
	if err := record.saveState(workspaceRecyclePending); err != nil {
		return nil, err
	}
	return record, nil
}

func workspaceRecycleRoot(locks *OperationLocks, taskPath string, create bool) (string, error) {
	if locks == nil || strings.TrimSpace(locks.Directory) == "" {
		return "", errors.New("应用回收记录目录未配置")
	}
	home := mustAbs(filepath.Dir(locks.Directory))
	base := filepath.Join(home, "workspace-recycle")
	if isWithin(base, taskPath) {
		return "", errors.New("回收记录目录必须位于任务目录之外")
	}
	if _, err := documents.DirectoryFingerprint(home); err != nil {
		return "", err
	}
	key := filepath.Clean(mustAbs(taskPath))
	if locks.Paths.ProfileID != "" {
		key = locks.Paths.ProfileID + "\x00" + filepath.Base(key)
	}
	if runtime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	root := filepath.Join(base, workspaceRecycleDigest([]byte(key)))
	for _, dir := range []string{base, root} {
		if create {
			if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
				return "", err
			} else if err == nil {
				if err := syncRecycleDirectory(filepath.Dir(dir)); err != nil {
					return "", err
				}
			}
		}
		if _, err := documents.DirectoryFingerprint(dir); err != nil {
			return "", err
		}
	}
	return root, nil
}

func sameRecyclePath(a, b string) bool {
	if !filepath.IsAbs(a) || !filepath.IsAbs(b) {
		return false
	}
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func (r *workspaceRecycleRecord) matchesTask(profileID, path, identity string, manifest domain.TaskManifest) bool {
	if r.ProfileID != profileID || !sameRecyclePath(r.TaskPath, path) || filepath.Base(path) != r.TaskDirectoryName {
		return false
	}
	return r.TaskIdentity != "" && r.TaskIdentity == identity &&
		r.TaskCreatedAt != "" && r.TaskCreatedAt == manifest.CreatedAt &&
		r.TaskDirectoryName == manifest.TaskDirectoryName && r.TaskGroupID == manifest.GroupID
}

func (r *workspaceRecycleRecord) workspaceDirectory() string {
	if r.WorkspaceDirectory != "" {
		return r.WorkspaceDirectory
	}
	return r.TaskPath
}

func (r *workspaceRecycleRecord) checkTargetIdentity() error {
	taskPath := r.taskPath
	if taskPath == "" {
		taskPath = r.TaskPath
	}
	if err := checkRecycleDirectory(taskPath, r.TaskIdentity); err != nil {
		return err
	}
	return checkRecycleDirectory(r.Target, r.TargetIdentity)
}

func checkRecycleDirectory(path, expected string) error {
	if !filepath.IsAbs(path) || expected == "" {
		return fmt.Errorf("回收记录目录身份不完整：%s", path)
	}
	actual, err := documents.DirectoryFingerprint(path)
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("回收记录目录身份已变化：%s", path)
	}
	return nil
}

func (r *workspaceRecycleRecord) saveState(state string) error {
	if !r.validReceipt() {
		return errors.New("无法保存格式无效的工作区回收记录")
	}
	if err := checkRecycleDirectory(r.directory, r.directoryIdentity); err != nil {
		return err
	}
	if r.receiptDigest != "" {
		if err := r.verifyReceipt(); err != nil {
			return err
		}
	} else if _, err := os.Lstat(filepath.Join(r.directory, workspaceRecycleFile)); !os.IsNotExist(err) {
		return errors.New("工作区回收记录位置已被占用")
	}
	receipt := r.workspaceRecycleReceipt
	receipt.State = state
	data, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	if err := storage.WriteFileAtomic(filepath.Join(r.directory, workspaceRecycleFile), data, 0o600); err != nil {
		return err
	}
	r.State, r.receiptDigest = state, workspaceRecycleDigest(data)
	if err := syncRecycleDirectory(r.directory); err != nil {
		return err
	}
	if err := syncRecycleDirectory(filepath.Dir(r.directory)); err != nil {
		return err
	}
	return nil
}

func (r *workspaceRecycleRecord) verifyReceipt() error {
	if err := checkRecycleDirectory(r.directory, r.directoryIdentity); err != nil {
		return err
	}
	data, err := readRecycleRegular(filepath.Join(r.directory, workspaceRecycleFile), 64*1024*1024)
	if err != nil {
		return err
	}
	if workspaceRecycleDigest(data) != r.receiptDigest {
		return errors.New("工作区回收记录内容已变化")
	}
	return nil
}

func (r *workspaceRecycleRecord) revalidate(ctx context.Context, git *GitClient) error {
	if err := r.verifyBackup(ctx); err != nil {
		return err
	}
	return r.verifyLive(ctx, workspaceRecycleGit(git))
}

func (r *workspaceRecycleRecord) finish(git *GitClient) error {
	if _, err := os.Lstat(r.Target); !os.IsNotExist(err) {
		return fmt.Errorf("无法确认原工作区已回收：%s", r.Target)
	}
	if err := r.saveState(workspaceRecycleMoved); err != nil {
		return fmt.Errorf("保存工作区回收记录失败：%w", err)
	}
	if r.Workspace.Strategy != domain.WorkspaceStandardWorktree {
		return nil
	}
	ctx := context.Background()
	if err := r.verifyBackup(ctx); err != nil {
		return err
	}
	if err := r.verifyOriginalAdmin(ctx); err != nil {
		return err
	}
	proof := forcedWorkspaceProof{
		target: r.Target, repositoryRoot: r.RepositoryRoot, commonDirectory: r.CommonDirectory, commonIdentity: r.CommonIdentity,
		adminDirectory: r.AdminDirectory, adminIdentity: r.AdminIdentity, adminGitDirFile: r.AdminGitDirFile,
		branch: r.Branch, head: r.Head,
	}
	return removeWorktreeRegistrationOnly(workspaceRecycleGit(git), proof)
}

// Keep a complete receipt when the native recycler's outcome is ambiguous.
func (r *workspaceRecycleRecord) abort() error {
	if r.State != workspaceRecyclePending || r.checkTargetIdentity() != nil {
		return nil
	}
	return r.removeReceipt()
}

func (r *workspaceRecycleRecord) removeReceipt() error {
	if err := checkRecycleDirectory(r.directory, r.directoryIdentity); err != nil {
		return err
	}
	return os.RemoveAll(r.directory)
}
