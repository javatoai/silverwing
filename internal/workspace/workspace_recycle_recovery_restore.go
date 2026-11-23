package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
	"silverwing-go/internal/taskscope"
)

// RestoreRecycledWorkspaces reattaches workspaces already restored to their
// original locations by the operating system.
func (s *TaskApplicationService) RestoreRecycledWorkspaces(ctx context.Context, config domain.AppConfig, taskPath string) (restored int, resultErr error) {
	taskPath = mustAbs(taskPath)
	resultErr = s.Locks.WithTaskRoot(s.Locks.TaskRoot(config), func() error {
		return s.withTaskManifestMutation(ctx, config, taskPath, func(ctx context.Context, manifest domain.TaskManifest) error {
			if err := s.Lifecycle.validateTaskDirectory(config, taskPath, manifest); err != nil {
				return err
			}
			records, conflicts := findRestoredWorkspaceRecords(ctx, s.Locks, taskPath, manifest)
			if len(records) == 0 {
				return conflicts
			}
			roots := make(map[string]string)
			workspaces := make([]domain.ServiceWorkspace, 0, len(records))
			for _, record := range records {
				if record.Workspace.Strategy == domain.WorkspaceStandardWorktree {
					roots[strings.ToLower(record.CommonDirectory)] = record.CommonDirectory
				}
				workspaces = append(workspaces, record.Workspace)
			}
			err := s.Lifecycle.withWorkspaceLocks(valuesSorted(roots), workspaces, func() error {
				var err error
				restored, err = s.restoreWorkspaceRecords(ctx, config, taskPath, manifest, records)
				return err
			})
			return errors.Join(conflicts, err)
		})
	})
	return restored, resultErr
}

func findRestoredWorkspaceRecords(ctx context.Context, locks *OperationLocks, taskPath string, manifest domain.TaskManifest) ([]*workspaceRecycleRecord, error) {
	root, err := workspaceRecycleRoot(locks, taskPath, false)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	taskIdentity, err := documents.DirectoryFingerprint(taskPath)
	if err != nil {
		return nil, err
	}
	selected := make([]*workspaceRecycleRecord, 0, len(entries))
	var replaced []*workspaceRecycleRecord
	var duplicateTargets []string
	var conflicts []error
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !strings.HasPrefix(entry.Name(), "receipt-") {
			continue
		}
		record, err := readWorkspaceRecycleRecord(filepath.Join(root, entry.Name()))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if record.State == workspaceRecycleDone || !record.matchesTask(locks.Paths.ProfileID, taskPath, taskIdentity, manifest) {
			continue
		}
		record.taskPath = taskPath
		identity, err := documents.DirectoryFingerprint(record.Target)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			conflicts = append(conflicts, err)
			continue
		}
		if identity != record.TargetIdentity {
			replaced = append(replaced, record)
			continue
		}
		if slices.ContainsFunc(selected, func(other *workspaceRecycleRecord) bool { return sameRecyclePath(other.Target, record.Target) }) {
			duplicateTargets = append(duplicateTargets, record.Target)
			continue
		}
		selected = append(selected, record)
	}
	for _, record := range replaced {
		if !slices.ContainsFunc(selected, func(other *workspaceRecycleRecord) bool { return sameRecyclePath(other.Target, record.Target) }) {
			conflicts = append(conflicts, fmt.Errorf("原路径已被其他目录使用，无法恢复工作区：%s", record.Target))
		}
	}
	selected = slices.DeleteFunc(selected, func(record *workspaceRecycleRecord) bool {
		if !slices.ContainsFunc(duplicateTargets, func(path string) bool { return sameRecyclePath(path, record.Target) }) {
			return false
		}
		conflicts = append(conflicts, fmt.Errorf("找到多个对应的工作区回收记录：%s", record.Target))
		return true
	})
	return selected, errors.Join(conflicts...)
}

func readWorkspaceRecycleRecord(path string) (*workspaceRecycleRecord, error) {
	identity, err := documents.DirectoryFingerprint(path)
	if err != nil {
		return nil, err
	}
	data, err := readRecycleRegular(filepath.Join(path, workspaceRecycleFile), 64*1024*1024)
	if err != nil {
		return nil, err
	}
	record := &workspaceRecycleRecord{directory: path, directoryIdentity: identity}
	record.receiptDigest = workspaceRecycleDigest(data)
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record.workspaceRecycleReceipt); err != nil {
		return nil, fmt.Errorf("无法读取工作区回收记录 %s：%w", path, err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF || !record.validReceipt() {
		return nil, fmt.Errorf("工作区回收记录格式无法验证：%s", path)
	}
	return record, nil
}

func (r *workspaceRecycleRecord) validReceipt() bool {
	if r.Version != workspaceRecycleVersion || (r.State != workspaceRecyclePending && r.State != workspaceRecycleMoved && r.State != workspaceRecycleDone) ||
		r.TaskIdentity == "" || r.TaskCreatedAt == "" || r.TargetIdentity == "" ||
		!sameRecyclePath(filepath.Dir(r.Target), r.workspaceDirectory()) || !sameRecyclePath(mustAbs(r.Workspace.WorktreePath), r.Target) ||
		r.Workspace.RepositoryID == "" || r.Branch == "" || r.Branch != r.Workspace.Branch || !validGitObjectID(r.Head) || r.CommonIdentity == "" {
		return false
	}
	if _, err := RequireValidTaskDirectoryName(r.TaskDirectoryName); err != nil {
		return false
	}
	taskPath := r.TaskPath
	if taskPath == "" && r.ProfileID != "" {
		// Legacy profile receipts recorded a separate checkout. They are
		// readable but cannot match a task in the configured root.
		taskPath = r.WorkspaceDirectory
	}
	if r.TaskDirectoryName != filepath.Base(taskPath) || !filepath.IsAbs(taskPath) ||
		(r.WorkspaceDirectory != "" && !sameRecyclePath(taskPath, r.WorkspaceDirectory)) {
		return false
	}
	previous := -1
	for _, row := range r.ModuleRows {
		if row.Position <= previous || !sameRecyclePath(mustAbs(row.Workspace.WorktreePath), r.Target) {
			return false
		}
		previous = row.Position
	}
	if r.Workspace.Strategy == domain.WorkspaceIndependentClone {
		return sameRecyclePath(r.CommonDirectory, filepath.Join(r.Target, ".git")) && len(r.ModuleRows) != 0 &&
			(r.CloneOwner == cloneRecycleCurrent || r.CloneOwner == cloneRecycleLegacy) && r.IndexPack == nil
	}
	if r.Workspace.Strategy != domain.WorkspaceStandardWorktree || !filepath.IsAbs(r.RepositoryRoot) || r.RepositoryIdentity == "" ||
		!sameRecyclePath(filepath.Dir(r.AdminDirectory), filepath.Join(r.CommonDirectory, "worktrees")) ||
		r.AdminIdentity == "" || r.AdminMode > 0o777 || r.AdminGitDirFile == "" || !validRecycleDigest(r.GitFileDigest) || len(r.AdminFiles) == 0 {
		return false
	}
	seenFiles := make(map[string]bool, len(r.AdminFiles))
	for _, file := range r.AdminFiles {
		if _, err := filepath.Localize(file.Path); err != nil || !fs.ValidPath(file.Path) || file.Path == "." ||
			file.Mode > 0o777 || seenFiles[file.Path] || (!file.Directory && !validRecycleDigest(file.Digest)) {
			return false
		}
		seenFiles[file.Path] = true
	}
	if r.IndexPack != nil {
		if !validGitObjectID(r.IndexPack.Hash) || len(r.IndexPack.Hash) != len(r.Head) || !validRecycleDigest(r.IndexPack.Digest) || len(r.IndexPack.Objects) == 0 {
			return false
		}
		for _, object := range r.IndexPack.Objects {
			if !validGitObjectID(object) || len(object) != len(r.Head) {
				return false
			}
		}
	}
	return true
}

func validRecycleDigest(value string) bool { return len(value) == 64 && validGitObjectID(value) }

func (s *TaskApplicationService) restoreWorkspaceRecords(ctx context.Context, config domain.AppConfig, taskPath string, manifest domain.TaskManifest, records []*workspaceRecycleRecord) (restored int, resultErr error) {
	git := workspaceRecycleGit(s.Git)
	updated := manifest
	updated.Services = slices.Clone(manifest.Services)
	var changed, moduleRecords bool
	var conflicts []error
	selected := make([]*workspaceRecycleRecord, 0, len(records))
	for _, record := range records {
		if err := record.verifyRestoredWorkspace(ctx, config, git); err != nil {
			conflicts = append(conflicts, err)
			continue
		}
		rows, err := record.restoreManifestRows(updated.Services)
		if err != nil {
			conflicts = append(conflicts, err)
			continue
		}
		changed = changed || len(rows) != len(updated.Services)
		moduleRecords = moduleRecords || len(record.ModuleRows) != 0
		updated.Services = rows
		selected = append(selected, record)
	}
	if len(selected) == 0 {
		return 0, errors.Join(conflicts...)
	}
	defer func() { resultErr = errors.Join(append(conflicts, resultErr)...) }()
	var taskAgents *workspaceTaskAgentsUpdate
	if moduleRecords {
		var err error
		taskAgents, err = captureWorkspaceTaskAgents(taskPath)
		if err != nil {
			return 0, err
		}
	}
	if changed {
		updated.UpdatedAt = domain.FormatSilverWingTime(s.now())
		if err := updated.Validate(); err != nil {
			return 0, err
		}
	}
	if err := taskscope.Validate(ctx, config, taskPath, manifest); err != nil {
		return 0, err
	}
	taskIdentity := selected[0].TaskIdentity
	if err := s.checkWorkspaceRecycleManifest(taskPath, taskIdentity, manifest); err != nil {
		return 0, err
	}
	var undo []func() error
	defer func() {
		if resultErr != nil && restored == 0 {
			for _, rollback := range slices.Backward(undo) {
				resultErr = errors.Join(resultErr, rollback())
			}
		}
	}()
	for _, record := range selected {
		if record.Workspace.Strategy != domain.WorkspaceStandardWorktree {
			continue
		}
		rollback, err := record.attachRestoredWorktree(ctx, git)
		if rollback != nil {
			undo = append(undo, rollback)
		}
		if err != nil {
			return 0, err
		}
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := taskscope.Validate(ctx, config, taskPath, manifest); err != nil {
		return 0, err
	}
	if err := s.checkWorkspaceRecycleManifest(taskPath, taskIdentity, manifest); err != nil {
		return 0, err
	}
	if changed {
		if err := s.Manifests.Save(taskPath, updated); err != nil {
			return 0, err
		}
		undo = append(undo, func() error {
			if err := s.checkWorkspaceRecycleManifest(taskPath, taskIdentity, updated); err != nil {
				return err
			}
			return s.Manifests.Save(taskPath, manifest)
		})
	}
	if taskAgents != nil {
		undo = append(undo, taskAgents.rollback)
		if err := taskAgents.write(s.Documents, updated, config); err != nil {
			return 0, err
		}
	}
	for _, record := range selected {
		if err := record.saveState(workspaceRecycleDone); err != nil {
			resultErr = errors.Join(resultErr, err)
		} else if err := record.removeReceipt(); err != nil {
			resultErr = errors.Join(resultErr, err)
		}
	}
	return len(selected), resultErr
}

func (s *TaskApplicationService) checkWorkspaceRecycleManifest(taskPath, identity string, expected domain.TaskManifest) error {
	if err := checkRecycleDirectory(taskPath, identity); err != nil {
		return err
	}
	if err := checkRecycleOptionalRegular(filepath.Join(taskPath, storage.TaskManifestFileName)); err != nil {
		return err
	}
	actual, err := s.Manifests.Load(taskPath)
	if err != nil {
		return err
	}
	if !sameTaskManifest(actual, expected) {
		return fmt.Errorf("任务清单已变化，请重新检查工作区恢复：%s", taskPath)
	}
	return nil
}

func (r *workspaceRecycleRecord) restoreManifestRows(rows []domain.ServiceWorkspace) ([]domain.ServiceWorkspace, error) {
	if len(r.ModuleRows) == 0 {
		if slices.ContainsFunc(rows, func(row domain.ServiceWorkspace) bool {
			return sameRecyclePath(mustAbs(row.WorktreePath), r.Target) && row.SelectionKey() == r.Workspace.SelectionKey() &&
				row.RepositoryID == r.Workspace.RepositoryID && row.Strategy == r.Workspace.Strategy && row.Branch == r.Branch
		}) {
			return rows, nil
		}
		return nil, fmt.Errorf("恢复的工作区与当前任务清单不匹配：%s", r.Target)
	}
	for _, row := range rows {
		if !sameRecyclePath(mustAbs(row.WorktreePath), r.Target) && !slices.ContainsFunc(r.ModuleRows, func(saved workspaceRecycleRow) bool {
			return row.SelectionKey() == saved.Workspace.SelectionKey()
		}) {
			continue
		}
		if !slices.ContainsFunc(r.ModuleRows, func(saved workspaceRecycleRow) bool { return reflect.DeepEqual(row, saved.Workspace) }) {
			return nil, fmt.Errorf("当前模块配置占用了待恢复模块的路径或标识：%s", r.Target)
		}
	}
	// A partially completed recovery can already have some or all original
	// rows. Match their occurrences so same-path aliases are restored exactly.
	matched := make([]bool, len(rows))
	var missing []workspaceRecycleRow
	for _, saved := range r.ModuleRows {
		index := slices.IndexFunc(rows, func(row domain.ServiceWorkspace) bool { return reflect.DeepEqual(row, saved.Workspace) })
		for index >= 0 && matched[index] {
			next := slices.IndexFunc(rows[index+1:], func(row domain.ServiceWorkspace) bool { return reflect.DeepEqual(row, saved.Workspace) })
			if next < 0 {
				index = -1
			} else {
				index += next + 1
			}
		}
		if index >= 0 {
			matched[index] = true
		} else {
			missing = append(missing, saved)
		}
	}
	for _, saved := range missing {
		rows = slices.Insert(rows, min(saved.Position, len(rows)), saved.Workspace)
	}
	return rows, nil
}

func (r *workspaceRecycleRecord) verifyRestoredWorkspace(ctx context.Context, config domain.AppConfig, git *GitClient) error {
	if err := r.checkTargetIdentity(); err != nil {
		return err
	}
	if err := checkRecycleDirectory(r.CommonDirectory, r.CommonIdentity); err != nil {
		return err
	}
	if err := r.verifyBackup(ctx); err != nil {
		return err
	}
	if r.Workspace.Strategy == domain.WorkspaceIndependentClone {
		return r.verifyRestoredClone(ctx, git)
	}
	if repo, configured := repositoryByID(config, r.Workspace.RepositoryID); configured &&
		(!sameRecyclePath(mustAbs(repo.RootPath), r.RepositoryRoot) || !sameRecyclePath(mustAbs(repo.GitCommonDirectory), r.CommonDirectory)) {
		return fmt.Errorf("当前仓库配置与原工作区来源不一致：%s", r.RepositoryRoot)
	}
	_, err := r.restoredWorktreeSlot(ctx, git)
	return err
}

func (r *workspaceRecycleRecord) verifyRestoredClone(ctx context.Context, git *GitClient) error {
	owner, err := verifyModuleRecycleClone(ctx, git, r.workspaceDirectory(), r.Workspace, r.Target)
	if err != nil {
		return err
	}
	branch, head, _, err := workspaceStateFingerprint(ctx, git, r.Target)
	if err != nil {
		return err
	}
	if owner != r.CloneOwner || branch != r.Branch || head != r.Head {
		return fmt.Errorf("原独立克隆的所有权、分支或提交已变化：%s", r.Target)
	}
	return nil
}

// restoredWorktreeSlot returns true when this worktree's original
// registration survived recycle, or an earlier recovery published it.
func (r *workspaceRecycleRecord) restoredWorktreeSlot(ctx context.Context, git *GitClient) (bool, error) {
	if err := r.checkTargetIdentity(); err != nil {
		return false, err
	}
	if err := checkRecycleDirectory(r.RepositoryRoot, r.RepositoryIdentity); err != nil {
		return false, err
	}
	if err := checkRecycleDirectory(r.CommonDirectory, r.CommonIdentity); err != nil {
		return false, err
	}
	if _, err := r.readLinkedPointer(); err != nil {
		return false, err
	}
	common, err := gitCommonDirectoryContext(ctx, git, r.RepositoryRoot)
	if err != nil || !sameRecyclePath(common, r.CommonDirectory) {
		return false, fmt.Errorf("主仓库 Git 公共目录已变化：%s", r.RepositoryRoot)
	}
	ref := "refs/heads/" + r.Branch
	head, err := git.RunContext(ctx, r.RepositoryRoot, 2*time.Minute, true, true, "show-ref", "--verify", "--hash", ref)
	if err != nil || strings.TrimSpace(head.Stdout) != r.Head {
		return false, fmt.Errorf("原分支 %s 已不存在或移动，无法恢复工作区：%s", r.Branch, r.Target)
	}
	parent := filepath.Dir(r.AdminDirectory)
	if _, err := documents.DirectoryFingerprint(parent); err != nil && !os.IsNotExist(err) {
		return false, err
	}
	occupied := true
	if _, err := os.Lstat(r.AdminDirectory); os.IsNotExist(err) {
		occupied = false
	} else if err != nil {
		return false, err
	} else if err := r.verifyAdminSnapshot(ctx, r.AdminDirectory, true); err != nil {
		return false, fmt.Errorf("原 Git 登记位置已被占用或发生变化：%s：%w", r.AdminDirectory, err)
	}
	entries, err := git.WorktreesContext(ctx, r.RepositoryRoot)
	if err != nil {
		return false, err
	}
	registered := 0
	for _, entry := range entries {
		if sameRecyclePath(entry.Path, r.Target) {
			if !occupied || entry.Bare || entry.Detached || entry.Locked || entry.Branch != r.Branch || entry.Head != r.Head {
				return false, fmt.Errorf("工作区路径已存在不同的 Git 登记：%s", r.Target)
			}
			registered++
		} else if entry.Branch == r.Branch {
			return false, fmt.Errorf("原分支 %s 已在另一个工作区检出：%s", r.Branch, entry.Path)
		}
	}
	if occupied && registered != 1 {
		return false, fmt.Errorf("无法确认原 Git 工作区登记：%s", r.Target)
	}
	return occupied, nil
}

func (r *workspaceRecycleRecord) attachRestoredWorktree(ctx context.Context, git *GitClient) (func() error, error) {
	occupied, err := r.restoredWorktreeSlot(ctx, git)
	if err != nil {
		return nil, err
	}
	if err := r.installIndexPack(ctx, git); err != nil {
		return nil, err
	}
	if occupied {
		return nil, r.verifyAttachedWorktree(ctx, git)
	}
	parent := filepath.Dir(r.AdminDirectory)
	if err := os.Mkdir(parent, 0o755); err != nil && !os.IsExist(err) {
		return nil, err
	}
	parentIdentity, err := documents.DirectoryFingerprint(parent)
	if err != nil {
		return nil, err
	}
	stage, err := os.MkdirTemp(r.CommonDirectory, ".silverwing-restore-")
	if err != nil {
		return nil, err
	}
	stageIdentity, err := documents.DirectoryFingerprint(stage)
	if err != nil {
		return nil, err
	}
	defer func() {
		if checkRecycleDirectory(stage, stageIdentity) == nil {
			_ = os.RemoveAll(stage)
		}
	}()
	if err := r.stageWorktreeAdmin(ctx, stage); err != nil {
		return nil, err
	}
	if occupied, err := r.restoredWorktreeSlot(ctx, git); err != nil {
		return nil, err
	} else if occupied {
		return nil, r.verifyAttachedWorktree(ctx, git)
	}
	if err := checkRecycleDirectory(parent, parentIdentity); err != nil {
		return nil, err
	}
	if err := checkRecycleDirectory(stage, stageIdentity); err != nil {
		return nil, err
	}
	if err := documents.MoveDirectoryNoReplace(stage, r.AdminDirectory); err != nil {
		return nil, fmt.Errorf("无法恢复原 Git 登记位置 %s：%w", r.AdminDirectory, err)
	}
	rollback := func() error {
		if err := checkRecycleDirectory(r.AdminDirectory, stageIdentity); err != nil {
			return err
		}
		if err := r.verifyAdminSnapshot(context.Background(), r.AdminDirectory, true); err != nil {
			return err
		}
		return os.RemoveAll(r.AdminDirectory)
	}
	if err := syncRecycleDirectory(parent); err != nil {
		return rollback, err
	}
	return rollback, r.verifyAttachedWorktree(ctx, git)
}

func (r *workspaceRecycleRecord) stageWorktreeAdmin(ctx context.Context, stage string) error {
	backup := filepath.Join(r.directory, workspaceRecycleAdmin)
	for _, entry := range r.AdminFiles {
		if err := ctx.Err(); err != nil {
			return err
		}
		path, err := filepath.Localize(entry.Path)
		if err != nil {
			return err
		}
		target := filepath.Join(stage, path)
		if entry.Directory {
			if err := os.Mkdir(target, 0o700); err != nil {
				return err
			}
		} else {
			digest, err := copyRecycleFile(ctx, filepath.Join(backup, path), target, os.FileMode(entry.Mode))
			if err != nil {
				return err
			}
			if digest != entry.Digest {
				return fmt.Errorf("原 Git 索引备份内容已变化：%s", path)
			}
		}
	}
	for _, entry := range slices.Backward(r.AdminFiles) {
		if entry.Directory {
			target := filepath.Join(stage, filepath.FromSlash(entry.Path))
			if err := os.Chmod(target, os.FileMode(entry.Mode)); err != nil {
				return err
			}
			if err := syncRecycleDirectory(target); err != nil {
				return err
			}
		}
	}
	if err := os.Chmod(stage, os.FileMode(r.AdminMode)); err != nil {
		return err
	}
	return syncRecycleDirectory(stage)
}

func (r *workspaceRecycleRecord) verifyAttachedWorktree(ctx context.Context, git *GitClient) error {
	occupied, err := r.restoredWorktreeSlot(ctx, git)
	if err != nil {
		return err
	}
	if !occupied {
		return fmt.Errorf("Git 未识别已恢复的工作区：%s", r.Target)
	}
	top, err := git.RunContext(ctx, r.Target, 2*time.Minute, true, true, "rev-parse", "--show-toplevel")
	if err != nil || !sameRecyclePath(strings.TrimSpace(top.Stdout), r.Target) {
		return fmt.Errorf("恢复后的 Git 工作区顶层不匹配：%s", r.Target)
	}
	if _, err := git.RunContext(ctx, r.Target, 2*time.Minute, true, true, "status", "--porcelain=v1", "--untracked-files=all"); err != nil {
		return err
	}
	return r.verifyRestoredIndexObjects(ctx, git)
}

func (r *workspaceRecycleRecord) verifyRestoredIndexObjects(ctx context.Context, git *GitClient) error {
	if r.IndexPack == nil {
		return nil
	}
	args := []string{"--no-optional-locks", "-c", "core.longpaths=true", "-C", r.Target, "cat-file", "--batch-check=%(objectname) %(objecttype)"}
	spec := git.processSpec(args, r.Target, 2*time.Minute)
	spec.Stdin = []byte(strings.Join(r.IndexPack.Objects, "\n") + "\n")
	result, err := git.runProcess(ctx, spec)
	if err != nil || result.ExitCode != 0 {
		return &GitException{Message: "无法核对恢复后的 Git 暂存对象", Result: result, Cause: err}
	}
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(result.Stdout, "\r\n", "\n")), "\n")
	if len(lines) != len(r.IndexPack.Objects) {
		return errors.New("恢复后的 Git 暂存对象清单不完整")
	}
	for i, object := range r.IndexPack.Objects {
		if lines[i] != object+" blob" {
			return fmt.Errorf("恢复后的 Git 索引缺少暂存对象 %s", object)
		}
	}
	return nil
}

func (r *workspaceRecycleRecord) installIndexPack(ctx context.Context, git *GitClient) error {
	if r.IndexPack == nil {
		return nil
	}
	packDirectory, err := r.indexPackDestination(ctx, git)
	if err != nil {
		return err
	}
	for _, suffix := range []string{".pack", ".idx", ".rev"} {
		if err := checkRecycleOptionalRegular(filepath.Join(packDirectory, "pack-"+r.IndexPack.Hash+suffix)); err != nil {
			return err
		}
	}
	pack, err := openRecycleRegular(r.packPath())
	if err != nil {
		return err
	}
	defer pack.Close()
	args := []string{"-c", "core.longpaths=true", "-C", r.RepositoryRoot, "index-pack", "--stdin", "--strict"}
	spec := git.processSpec(args, r.RepositoryRoot, 10*time.Minute)
	spec.StdinFile = pack
	result, err := git.runProcess(ctx, spec)
	if err != nil || result.ExitCode != 0 {
		return &GitException{Message: "无法恢复 Git 暂存对象", Result: result, Cause: err}
	}
	fields := strings.Fields(result.Stdout)
	if len(fields) != 2 || fields[0] != "pack" || fields[1] != r.IndexPack.Hash {
		return errors.New("恢复后的 Git 暂存对象包校验失败")
	}
	return nil
}
