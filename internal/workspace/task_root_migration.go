package workspace

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

type TaskRootMigrationMode string

const (
	TaskRootDirectSwitch   TaskRootMigrationMode = "DIRECT_SWITCH"
	TaskRootSameFileStore  TaskRootMigrationMode = "SAME_FILE_STORE"
	TaskRootCrossFileStore TaskRootMigrationMode = "CROSS_FILE_STORE"
)

type TaskRootMigrationPreview struct {
	SourceRoot     string                           `json:"sourceRoot"`
	TargetRoot     string                           `json:"targetRoot"`
	Mode           TaskRootMigrationMode            `json:"mode"`
	TaskCount      int                              `json:"taskCount"`
	WorkspaceCount int                              `json:"workspaceCount"`
	TotalBytes     int64                            `json:"totalBytes"`
	Blockers       []string                         `json:"blockers"`
	CanMigrate     bool                             `json:"canMigrate"`
	Roots          TaskRootMigrationRootsChange     `json:"roots"`
	PathMappings   []TaskRootMigrationPathMapping   `json:"pathMappings"`
	Materials      []TaskRootMigrationTaskMaterials `json:"materials"`
}

type TaskRootMigrationPhase string

const (
	MigrationPreparing                TaskRootMigrationPhase = "PREPARING"
	MigrationTransferringAndVerifying TaskRootMigrationPhase = "TRANSFERRING_AND_VERIFYING"
	MigrationUpdatingConfig           TaskRootMigrationPhase = "UPDATING_CONFIG"
	MigrationCleaningSource           TaskRootMigrationPhase = "CLEANING_SOURCE"
	MigrationCompleted                TaskRootMigrationPhase = "COMPLETED"
)

type TaskRootMigrationProgress struct {
	Phase          TaskRootMigrationPhase `json:"phase"`
	CompletedTasks int                    `json:"completedTasks"`
	TotalTasks     int                    `json:"totalTasks"`
	CurrentTask    string                 `json:"currentTask"`
}

type TaskRootMigrationResult struct {
	Config          domain.AppConfig               `json:"config"`
	MigratedTasks   int                            `json:"migratedTasks"`
	CleanupFailures []string                       `json:"cleanupFailures"`
	PathMappings    []TaskRootMigrationPathMapping `json:"pathMappings"`
}

type TaskRootMigrationService struct {
	Paths       storage.Paths
	ConfigStore *storage.ConfigStore
	Manifests   *storage.ManifestStore
	Documents   *AgentDocumentService
	Drafts      *documents.Service
	Git         *GitClient
	Locks       *OperationLocks
	Now         func() time.Time
}

const taskRootMigrationMarker = ".silverwing-task-root-migration"

func (s *TaskRootMigrationService) Preview(config domain.AppConfig, requestedTarget string) (TaskRootMigrationPreview, error) {
	plan, err := s.buildMigrationPlan(config, requestedTarget)
	if err != nil {
		return TaskRootMigrationPreview{}, err
	}
	return plan.Preview, nil
}

func (s *TaskRootMigrationService) Migrate(ctx context.Context, config domain.AppConfig, requestedTarget string, onProgress func(TaskRootMigrationProgress)) (TaskRootMigrationResult, error) {
	if onProgress == nil {
		onProgress = func(TaskRootMigrationProgress) {}
	}
	plan, err := s.buildMigrationPlan(config, requestedTarget)
	if err != nil {
		return TaskRootMigrationResult{}, err
	}
	if len(plan.Preview.Blockers) != 0 {
		return TaskRootMigrationResult{}, fmt.Errorf("%s", strings.Join(plan.Preview.Blockers, "；"))
	}
	if plan.Preview.SourceRoot == "" || len(plan.Tasks) == 0 {
		var result TaskRootMigrationResult
		err = s.withMigrationLocks(plan, func() error {
			if err := s.requireNoPendingMigration(ctx); err != nil {
				return err
			}
			onProgress(TaskRootMigrationProgress{Phase: MigrationPreparing})
			if err := os.MkdirAll(plan.Preview.TargetRoot, 0o755); err != nil {
				return err
			}
			onProgress(TaskRootMigrationProgress{Phase: MigrationUpdatingConfig})
			updated, err := s.updateConfiguredRoots(plan.Preview.Roots)
			if err != nil {
				return err
			}
			result = TaskRootMigrationResult{Config: updated, PathMappings: plan.Preview.PathMappings}
			onProgress(TaskRootMigrationProgress{Phase: MigrationCompleted})
			return nil
		})
		return result, err
	}
	var result TaskRootMigrationResult
	err = s.withMigrationLocks(plan, func() error {
		if err := s.requireNoPendingMigration(ctx); err != nil {
			return err
		}
		lockedPlan, buildErr := s.buildMigrationPlan(config, requestedTarget)
		if buildErr != nil {
			return buildErr
		}
		if len(lockedPlan.Preview.Blockers) != 0 {
			return fmt.Errorf("%s", strings.Join(lockedPlan.Preview.Blockers, "；"))
		}
		return s.migrateLocked(ctx, config, lockedPlan, onProgress, &result)
	})
	return result, err
}

func (s *TaskRootMigrationService) requireNoPendingMigration(ctx context.Context) error {
	pending, err := TaskRootMigrationPending(ctx, s.Paths)
	if err != nil {
		return fmt.Errorf("无法读取任务根目录迁移恢复状态，不能开始新迁移：%w", err)
	}
	if *pending {
		return fmt.Errorf("存在未完成的任务根目录迁移，请先恢复；恢复日志已保留：%s", s.journalPath())
	}
	return nil
}

func (s *TaskRootMigrationService) migrateLocked(ctx context.Context, config domain.AppConfig, plan migrationPlan, onProgress func(TaskRootMigrationProgress), result *TaskRootMigrationResult) error {
	id, err := migrationID()
	if err != nil {
		return err
	}
	roots := plan.Preview.Roots
	journal := migrationJournal{ID: id, Version: 2, SourceRoot: plan.Preview.SourceRoot, TargetRoot: plan.Preview.TargetRoot, Mode: plan.Preview.Mode, Phase: "PREPARED", ConfigRoots: &roots, RepositoryLocks: append([]string(nil), plan.RepositoryLocks...), Tasks: make([]migrationJournalTask, 0, len(plan.Tasks))}
	for _, task := range plan.Tasks {
		jt := migrationJournalTask{SourceDirectory: task.Source, TargetDirectory: task.Target, OriginalManifest: cloneMigrationManifest(task.Manifest)}
		for i, ws := range task.Workspaces {
			jt.Workspaces = append(jt.Workspaces, migrationJournalWorkspace{Strategy: ws.Strategy, RepositoryPath: ws.RepositoryPath, RepositoryID: ws.RepositoryID, GroupServiceID: ws.GroupServiceID, ModuleID: ws.ModuleID, RelativePath: filepath.ToSlash(task.Relative[i])})
		}
		journal.Tasks = append(journal.Tasks, jt)
	}
	if err := s.writeJournal(journal); err != nil {
		return err
	}
	// Persist the migration intent before creating the target root. If root creation
	// fails partway through, startup recovery still has the journal needed to keep
	// the source authoritative and report any leftover target paths.
	if err := os.MkdirAll(plan.Preview.TargetRoot, 0o755); err != nil {
		return fmt.Errorf("创建任务迁移目标目录失败，恢复日志已保留：%s: %w", s.journalPath(), err)
	}
	onProgress(TaskRootMigrationProgress{Phase: MigrationPreparing, TotalTasks: len(plan.Tasks)})
	transferred := 0
	rollback := func(primary error) error {
		failures := s.rollbackMigration(journal, config)
		if len(failures) == 0 {
			_ = os.Remove(s.journalPath())
		}
		if len(failures) > 0 {
			return fmt.Errorf("任务根目录迁移失败且自动回滚未完整完成，恢复日志已保留：%s: %w", s.journalPath(), errors.Join(append([]error{primary}, failures...)...))
		}
		return fmt.Errorf("任务根目录迁移失败，已恢复原目录：%w", primary)
	}
	for i, task := range plan.Tasks {
		select {
		case <-ctx.Done():
			return rollback(ctx.Err())
		default:
		}
		onProgress(TaskRootMigrationProgress{Phase: MigrationTransferringAndVerifying, CompletedTasks: i, TotalTasks: len(plan.Tasks), CurrentTask: filepath.Base(task.Source)})
		if err := s.transferAndVerifyTask(config, plan.Preview.Mode, task, id, plan.Preview.PathMappings); err != nil {
			return rollback(err)
		}
		transferred++
	}
	if err := s.relocateMigrationDrafts(ctx, journal, false); err != nil {
		return rollback(err)
	}
	onProgress(TaskRootMigrationProgress{Phase: MigrationUpdatingConfig, CompletedTasks: transferred, TotalTasks: len(plan.Tasks)})
	// A canceled final transfer or progress observer is still pre-commit. Use
	// the same rollback as a transfer failure before changing the durable root.
	if err := ctx.Err(); err != nil {
		return rollback(err)
	}
	updated, err := s.updateConfiguredRoots(plan.Preview.Roots)
	if err != nil {
		observed, readErr := s.ConfigStore.Load()
		if readErr != nil {
			return fmt.Errorf("任务根目录迁移提交状态无法确认，目录与恢复日志已保留：%w", errors.Join(err, readErr))
		}
		committed, stateErr := migrationConfigCommitted(observed, journal)
		if stateErr != nil {
			return fmt.Errorf("任务根目录迁移提交状态无法确认，目录与恢复日志已保留：%w", errors.Join(err, stateErr))
		}
		if !committed {
			return rollback(err)
		}
		updated = observed
	}
	journal.Phase = "CONFIG_UPDATED"
	*result = TaskRootMigrationResult{Config: updated, MigratedTasks: len(plan.Tasks), PathMappings: plan.Preview.PathMappings}
	if err := s.writeJournal(journal); err != nil {
		// The durable setting is the commit point. Keep both roots and the journal for startup recovery.
		return fmt.Errorf("任务根目录迁移已提交，但恢复日志更新失败，旧目录待清理：%w", err)
	}
	onProgress(TaskRootMigrationProgress{Phase: MigrationCleaningSource, CompletedTasks: len(plan.Tasks), TotalTasks: len(plan.Tasks)})
	cleanupFailures := s.cleanupMigrationSources(journal)
	if len(cleanupFailures) == 0 {
		_ = os.Remove(s.journalPath())
	}
	onProgress(TaskRootMigrationProgress{Phase: MigrationCompleted, CompletedTasks: len(plan.Tasks), TotalTasks: len(plan.Tasks)})
	*result = TaskRootMigrationResult{Config: updated, MigratedTasks: len(plan.Tasks), CleanupFailures: cleanupFailures, PathMappings: plan.Preview.PathMappings}
	return nil
}

func (s *TaskRootMigrationService) buildMigrationPlan(config domain.AppConfig, requestedTarget string) (migrationPlan, error) {
	target, err := filepath.Abs(requestedTarget)
	if err != nil {
		return migrationPlan{}, err
	}
	target = filepath.Clean(target)
	source := ""
	if config.TaskRoot != nil && strings.TrimSpace(*config.TaskRoot) != "" {
		source = mustAbs(*config.TaskRoot)
	}
	preview := TaskRootMigrationPreview{SourceRoot: source, TargetRoot: target, Blockers: []string{}}
	add := func(msg string) { preview.Blockers = append(preview.Blockers, msg) }
	if source == target {
		add("新旧任务根目录不能相同")
	}
	if source != "" && (pathWithin(source, target) || pathWithin(target, source)) {
		add("新旧任务根目录不能互相包含")
	}
	if info, e := os.Stat(target); e == nil && !info.IsDir() {
		add("目标路径不是目录：" + target)
	} else if e != nil && !os.IsNotExist(e) {
		add(e.Error())
	}
	if info, e := os.Stat(target); e == nil && info.IsDir() {
		entries, re := os.ReadDir(target)
		if re != nil {
			add(re.Error())
		} else if len(entries) > 0 {
			add("目标任务根目录必须为空")
		}
	}
	writeParent := target
	for {
		if _, e := os.Stat(writeParent); e == nil {
			break
		}
		parent := filepath.Dir(writeParent)
		if parent == writeParent {
			break
		}
		writeParent = parent
	}
	if info, e := os.Stat(writeParent); e != nil || !info.IsDir() {
		add("目标任务根目录的上级路径不是目录：" + writeParent)
	} else if !isWritableDirectory(writeParent) {
		add("目标任务根目录不可写：" + target)
	}
	if source == "" {
		preview.Mode = TaskRootDirectSwitch
		if err := preview.prepareMaterials(config, nil); err != nil {
			return migrationPlan{}, err
		}
		preview.CanMigrate = len(preview.Blockers) == 0
		return migrationPlan{Preview: preview}, nil
	}
	var scan storage.ManifestScanResult
	if info, e := os.Stat(source); e == nil && info.IsDir() {
		var scanErr error
		scan, scanErr = s.Manifests.Scan(source)
		if scanErr != nil {
			return migrationPlan{}, scanErr
		}
	}
	for _, p := range scan.UnsupportedPaths {
		add(scan.UnsupportedReasons[p])
	}
	for p, reason := range scan.Failures {
		add("任务清单读取失败：" + p + "：" + reason)
	}
	recognized := map[string]bool{}
	for _, e := range scan.Current {
		recognized[filepath.Clean(e.Path)] = true
	}
	for _, p := range scan.UnsupportedPaths {
		recognized[filepath.Clean(p)] = true
	}
	for p := range scan.Failures {
		recognized[filepath.Clean(p)] = true
	}
	if entries, e := os.ReadDir(source); len(scan.Current) != 0 && e == nil {
		for _, entry := range entries {
			if !recognized[filepath.Clean(filepath.Join(source, entry.Name()))] {
				add("任务根目录包含无法识别的内容：" + filepath.Join(source, entry.Name()))
			}
		}
	} else if len(scan.Current) != 0 && !os.IsNotExist(e) {
		return migrationPlan{}, e
	}
	plan := migrationPlan{Preview: preview, Tasks: []migrationTask{}}
	for _, entry := range scan.Current {
		taskSource := mustAbs(entry.Path)
		manifest := entry.Manifest
		if filepath.Dir(taskSource) != source {
			add("任务必须是任务根目录的直接子目录：" + taskSource)
			continue
		}
		if filepath.Base(taskSource) != manifest.TaskDirectoryName {
			add("任务目录与清单名称不一致：" + taskSource)
			continue
		}
		if fileExists(filepath.Join(taskSource, taskRootMigrationMarker)) {
			add("任务目录包含未处理的迁移标记：" + taskSource)
			continue
		}
		targetTask := filepath.Clean(filepath.Join(target, manifest.TaskDirectoryName))
		if filepath.Dir(targetTask) != target {
			add("任务目录名不安全：" + manifest.TaskDirectoryName)
			continue
		}
		if fileExists(targetTask) {
			add("目标任务目录已存在：" + targetTask)
		}
		task := migrationTask{Source: taskSource, Target: targetTask, Manifest: manifest, Workspaces: []domain.ServiceWorkspace{}, Relative: []string{}}
		valid := true
		for _, ws := range manifest.Services {
			worktree := mustAbs(ws.WorktreePath)
			rel, e := filepath.Rel(taskSource, worktree)
			if e != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
				add("工作区不在任务目录内：" + worktree)
				valid = false
				continue
			}
			if info, e := os.Stat(worktree); e != nil || !info.IsDir() {
				add("工作区不存在：" + worktree)
				valid = false
				continue
			}
			if ws.Strategy == domain.WorkspaceIndependentClone {
				if e := requireOwnedClone(worktree, cloneOwnership(taskSource, ws.RepositoryID, ws.GroupServiceID, ws.ModuleID)); e != nil {
					add(e.Error())
					valid = false
					continue
				}
			}
			if ws.Strategy == domain.WorkspaceStandardWorktree {
				repository, ok := repositoryByID(config, ws.RepositoryID)
				if !ok {
					add("工作区对应仓库配置不存在：" + ws.RepositoryID)
					valid = false
					continue
				}
				plan.RepositoryLocks = append(plan.RepositoryLocks, repository.GitCommonDirectory)
			} else if common, e := s.Git.CommonDirectory(worktree); e == nil {
				plan.RepositoryLocks = append(plan.RepositoryLocks, common)
			} else {
				add("无法读取独立克隆 Git 身份：" + worktree + "：" + e.Error())
				valid = false
				continue
			}
			task.Workspaces = append(task.Workspaces, ws)
			task.Relative = append(task.Relative, rel)
		}
		if !valid || len(task.Workspaces) != len(manifest.Services) {
			continue
		}
		plan.Tasks = append(plan.Tasks, task)
	}
	preview.TaskCount = len(plan.Tasks)
	for _, t := range plan.Tasks {
		preview.WorkspaceCount += len(t.Workspaces)
		size, e := directoryBytes(t.Source)
		if e != nil {
			add("无法计算任务目录大小：" + t.Source + "：" + e.Error())
		} else {
			preview.TotalBytes += size
		}
	}
	mode := TaskRootCrossFileStore
	if len(plan.Tasks) == 0 {
		mode = TaskRootDirectSwitch
	} else if sameFileStorePath(source, writeParent) {
		mode = TaskRootSameFileStore
	}
	preview.Mode = mode
	if mode != TaskRootDirectSwitch && sameMigrationRoot(config.RequirementMaterialsRoot, &source) {
		add("资料根与任务根相同，无法确认未规划资料已完整搬迁；请先将资料根设置为独立目录")
	}
	if err := preview.prepareMaterials(config, plan.Tasks); err != nil {
		return migrationPlan{}, err
	}
	if mode == TaskRootCrossFileStore {
		if usable, ok := availableDiskBytes(writeParent); ok && usable < uint64(preview.TotalBytes) {
			add("目标磁盘空间不足")
		}
	}
	preview.Blockers = uniqueStrings(preview.Blockers)
	plan.RepositoryLocks = uniqueSorted(plan.RepositoryLocks)
	preview.CanMigrate = len(preview.Blockers) == 0
	plan.Preview = preview
	return plan, nil
}

func (s *TaskRootMigrationService) transferAndVerifyTask(config domain.AppConfig, mode TaskRootMigrationMode, task migrationTask, id string, mappings []TaskRootMigrationPathMapping) error {
	updated, err := migratedTaskManifest(task.Manifest, mappings)
	if err != nil {
		return err
	}
	snapshots := make([]workspaceMigrationSnapshot, len(task.Workspaces))
	for i, ws := range task.Workspaces {
		snap, err := s.snapshotWorkspace(ws, ws.WorktreePath)
		if err != nil {
			return err
		}
		snapshots[i] = snap
	}
	if mode == TaskRootSameFileStore {
		if err := os.WriteFile(filepath.Join(task.Source, taskRootMigrationMarker), []byte(id), 0o600); err != nil {
			return err
		}
		if err := os.Rename(task.Source, task.Target); err != nil {
			return err
		}
	} else if mode == TaskRootCrossFileStore {
		if err := os.MkdirAll(task.Target, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(task.Target, taskRootMigrationMarker), []byte(id), 0o600); err != nil {
			return err
		}
		if err := copyTree(task.Source, task.Target, taskRootMigrationMarker); err != nil {
			return err
		}
	} else {
		return fmt.Errorf("DIRECT_SWITCH 模式不应包含任务目录")
	}
	updated.UpdatedAt = domain.FormatSilverWingTime(s.now())
	for i, ws := range task.Workspaces {
		newPath := filepath.Join(task.Target, task.Relative[i])
		if ws.Strategy == domain.WorkspaceIndependentClone {
			oldOwner := cloneOwnership(task.Source, ws.RepositoryID, ws.GroupServiceID, ws.ModuleID)
			newOwner := cloneOwnership(task.Target, ws.RepositoryID, ws.GroupServiceID, ws.ModuleID)
			if err := changeCloneOwner(newPath, oldOwner, newOwner); err != nil {
				return err
			}
		}
		if ws.Strategy == domain.WorkspaceStandardWorktree {
			if err := s.Git.RepairWorktree(ws.RepositoryPath, newPath); err != nil {
				return err
			}
		}
	}
	if err := s.Manifests.Save(task.Target, updated); err != nil {
		return err
	}
	if _, err := s.Documents.WriteTaskDocument(task.Target, updated, config); err != nil {
		return err
	}
	for i, ws := range task.Workspaces {
		if err := s.verifyWorkspace(ws, filepath.Join(task.Target, task.Relative[i]), snapshots[i]); err != nil {
			return err
		}
	}
	return nil
}

func (s *TaskRootMigrationService) snapshotWorkspace(ws domain.ServiceWorkspace, path string) (workspaceMigrationSnapshot, error) {
	head, err := s.Git.Resolve(path, "HEAD")
	if err != nil {
		return workspaceMigrationSnapshot{}, err
	}
	branch, err := s.Git.CurrentBranch(path)
	if err != nil {
		return workspaceMigrationSnapshot{}, err
	}
	status, err := s.Git.Run(path, "status", "--porcelain=v2", "--branch", "-z", "--untracked-files=all")
	if err != nil {
		return workspaceMigrationSnapshot{}, err
	}
	common, err := s.Git.CommonDirectory(path)
	if err != nil {
		return workspaceMigrationSnapshot{}, err
	}
	remote, err := s.Git.RemoteURL(path, "origin")
	if err != nil {
		return workspaceMigrationSnapshot{}, err
	}
	return workspaceMigrationSnapshot{Head: head, Branch: branch, Status: status.Stdout, CommonDirectory: common, RemoteURL: remote, Strategy: ws.Strategy}, nil
}

func (s *TaskRootMigrationService) verifyWorkspace(ws domain.ServiceWorkspace, path string, want workspaceMigrationSnapshot) error {
	top, err := s.Git.TopLevel(path)
	if err != nil || !samePath(top, path) {
		return fmt.Errorf("迁移后的路径不是 Git 顶层目录：%s", path)
	}
	got, err := s.snapshotWorkspace(ws, path)
	if err != nil {
		return err
	}
	if got.Head != want.Head {
		return fmt.Errorf("迁移后 HEAD 发生变化：%s", path)
	}
	if got.Branch != want.Branch {
		return fmt.Errorf("迁移后分支发生变化：%s", path)
	}
	if got.Status != want.Status {
		return fmt.Errorf("迁移后工作区文件状态发生变化：%s", path)
	}
	if ws.Strategy == domain.WorkspaceStandardWorktree && !samePath(got.CommonDirectory, want.CommonDirectory) {
		return fmt.Errorf("迁移后 Worktree 仓库身份发生变化：%s", path)
	}
	if ws.Strategy == domain.WorkspaceIndependentClone && got.RemoteURL != want.RemoteURL {
		return fmt.Errorf("迁移后独立克隆 origin 发生变化：%s", path)
	}
	return nil
}

func (s *TaskRootMigrationService) updateConfiguredRoots(change TaskRootMigrationRootsChange) (domain.AppConfig, error) {
	return s.ConfigStore.Update(func(current domain.AppConfig) (domain.AppConfig, error) {
		if !sameMigrationConfigRoots(migrationConfigRoots(current), change.Before) {
			return domain.AppConfig{}, fmt.Errorf("任务根或资料根在迁移期间已被其他操作修改")
		}
		updated := current
		updated.TaskRoot = cloneMigrationPath(change.After.TaskRoot)
		updated.RequirementMaterialsRoot = cloneMigrationPath(change.After.MaterialsRoot)
		return updated, nil
	})
}

func (s *TaskRootMigrationService) withMigrationLocks(plan migrationPlan, fn func() error) error {
	roots := []string{}
	if plan.Preview.SourceRoot != "" {
		roots = append(roots, plan.Preview.SourceRoot)
	}
	if plan.Preview.TargetRoot != "" {
		roots = append(roots, plan.Preview.TargetRoot)
	}
	roots = uniqueSorted(roots)
	var lockRoots func(int) error
	lockRoots = func(i int) error {
		if i == len(roots) {
			return s.withTaskLocks(migrationTaskPaths(plan), 0, func() error {
				return s.withRepositoryLocks(plan.RepositoryLocks, 0, fn)
			})
		}
		return s.Locks.WithTaskRoot(roots[i], func() error { return lockRoots(i + 1) })
	}
	return lockRoots(0)
}

func (s *TaskRootMigrationService) withRepositoryLocks(repositories []string, index int, fn func() error) error {
	repositories = uniqueSorted(repositories)
	if index == len(repositories) {
		return fn()
	}
	return s.Locks.WithRepository(repositories[index], func() error {
		return s.withRepositoryLocks(repositories, index+1, fn)
	})
}

func (s *TaskRootMigrationService) withTaskLocks(tasks []string, index int, fn func() error) error {
	tasks = uniqueSorted(tasks)
	if index == len(tasks) {
		return fn()
	}
	return s.Locks.WithTask(tasks[index], func() error { return s.withTaskLocks(tasks, index+1, fn) })
}

func (s *TaskRootMigrationService) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

type migrationPlan struct {
	Preview         TaskRootMigrationPreview
	Tasks           []migrationTask
	RepositoryLocks []string
}
type migrationTask struct {
	Source, Target string
	Manifest       domain.TaskManifest
	Workspaces     []domain.ServiceWorkspace
	Relative       []string
}
type workspaceMigrationSnapshot struct {
	Head, Branch, Status, CommonDirectory, RemoteURL string
	Strategy                                         domain.WorkspaceStrategy
}

func requireMigrationOwnedTarget(target, id string) error {
	data, err := os.ReadFile(filepath.Join(target, taskRootMigrationMarker))
	if err != nil || string(data) != id {
		return fmt.Errorf("无法确认目标目录属于当前迁移，已保留目录：%s", target)
	}
	return nil
}
func changeCloneOwner(target, expected, next string) error {
	marker := cloneMarker(target)
	data, err := os.ReadFile(marker)
	if err != nil {
		return err
	}
	if string(data) != expected {
		if string(data) == next {
			return nil
		}
		return fmt.Errorf("独立克隆所有权标记与迁移记录不符：%s", target)
	}
	return writeAtomicDocument(marker, next)
}
func copyTree(source, target, skipName string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		// The marker belongs only at the task root. A nested file with the same
		// name is user data and must not disappear during a cross-volume copy.
		if path != source && filepath.Dir(path) == source && entry.Name() == skipName {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(target, rel)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, e := os.Readlink(path)
			if e != nil {
				return e
			}
			if e = os.MkdirAll(filepath.Dir(dst), 0o755); e != nil {
				return e
			}
			return os.Symlink(link, dst)
		}
		if info.IsDir() {
			return os.MkdirAll(dst, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("不支持迁移特殊文件：%s", path)
		}
		if err = os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		in, e := os.Open(path)
		if e != nil {
			return e
		}
		defer in.Close()
		out, e := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
		if e != nil {
			return e
		}
		_, copyErr := ioCopy(out, in)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		return os.Chtimes(dst, info.ModTime(), info.ModTime())
	})
}
func ioCopy(dst *os.File, src *os.File) (int64, error) { return dst.ReadFrom(src) }
func removeTree(root string) error {
	if !fileExists(root) {
		return nil
	}
	paths := []string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return err
	}
	manifestPath := filepath.Join(root, storage.TaskManifestFileName)
	manifestPresent := false
	for i := len(paths) - 1; i >= 0; i-- {
		if paths[i] == root {
			continue
		}
		// Startup cleanup validates the original manifest before deleting the
		// remaining source. Keep it until every child removal has succeeded.
		if samePath(paths[i], manifestPath) {
			manifestPresent = true
			continue
		}
		if err := os.Remove(paths[i]); err != nil {
			return err
		}
	}
	if manifestPresent {
		if err := os.Remove(manifestPath); err != nil {
			return err
		}
	}
	return os.Remove(root)
}
func directoryBytes(root string) (int64, error) {
	var size int64
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		info, e := entry.Info()
		if e != nil {
			return e
		}
		if info.Mode().IsRegular() {
			size += info.Size()
		}
		return nil
	})
	return size, err
}
func sameFileStorePath(source, targetParent string) bool {
	a, e1 := os.Stat(source)
	b, e2 := os.Stat(targetParent)
	if e1 != nil || e2 != nil {
		return false
	}
	av, bv := reflect.ValueOf(a.Sys()), reflect.ValueOf(b.Sys())
	if av.IsValid() && av.Kind() == reflect.Pointer {
		av = av.Elem()
	}
	if bv.IsValid() && bv.Kind() == reflect.Pointer {
		bv = bv.Elem()
	}
	for _, name := range []string{"Dev", "VolumeSerialNumber"} {
		if !av.IsValid() || !bv.IsValid() || av.Kind() != reflect.Struct || bv.Kind() != reflect.Struct {
			break
		}
		af, bf := av.FieldByName(name), bv.FieldByName(name)
		if af.IsValid() && bf.IsValid() && af.CanInterface() && bf.CanInterface() {
			return reflect.DeepEqual(af.Interface(), bf.Interface())
		}
	}
	return filepath.VolumeName(source) == filepath.VolumeName(targetParent)
}
func isWritableDirectory(path string) bool {
	// Preview must not probe writability by creating a file in the user's
	// selected directory. Use the permissions reported by stat instead. This
	// can conservatively reject directories whose ACL grants write access while
	// their mode bits are read-only, but never mutates the target as a side
	// effect of inspection.
	info, err := os.Stat(path)
	return err == nil && info.IsDir() && info.Mode().Perm()&0o222 != 0
}
func pathWithin(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && (rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}
func uniqueSorted(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range values {
		if v != "" && !seen[strings.ToLower(filepath.Clean(v))] {
			seen[strings.ToLower(filepath.Clean(v))] = true
			out = append(out, filepath.Clean(v))
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out
}
func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range values {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
func migrationTaskPaths(plan migrationPlan) []string {
	out := []string{}
	for _, t := range plan.Tasks {
		out = append(out, t.Source, t.Target)
	}
	return out
}
