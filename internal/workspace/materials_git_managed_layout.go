package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
)

func managedDocumentsLayout(scope MaterialsGitScope) (string, error) {
	var root, directory string
	switch scope.RepositoryRole {
	case domain.TaskMaterialsRepositoryRole:
		root = domain.ManagedTaskMaterialsRoot(scope.Home, domain.MaterialsGitSyncSettings{RepositoryURL: scope.URL})
		directory = domain.RequirementsDirectory
	case domain.ProjectInstructionsRepositoryRole:
		root = domain.ManagedProjectInstructionsRoot(scope.Home, scope.URL)
		directory = domain.ProjectInstructionsDirectory
	default:
		return "", errors.New("无法确认应用资料仓库的用途")
	}
	if scope.BindingID != "" {
		settings := domain.MaterialsGitSyncSettings{RepositoryURL: scope.URL, LocalRoot: scope.Root, RemoteName: scope.RemoteName, BindingID: scope.BindingID,
			Branch: scope.Branch, IntervalMinutes: domain.DefaultMaterialsGitSyncSettings().IntervalMinutes}
		if err := settings.Validate(); err != nil {
			return "", err
		}
		root = scope.Root
	}
	if root == "" || !filepath.IsAbs(root) || !samePath(root, scope.Root) {
		return "", errors.New("无法确认应用资料仓库的位置")
	}
	return directory, materialsGitSafePath(root, root, false)
}

// ValidateManagedDocumentsRoot returns the writable subtree of a checked-out
// application repository. The caller owns the repository lock during writes.
func ValidateManagedDocumentsRoot(scope MaterialsGitScope) (string, error) {
	directory, err := validateManagedDocumentsCheckout(scope)
	if err != nil {
		return "", err
	}
	return managedDocumentsDirectory(scope.Root, directory, false)
}

func validateManagedDocumentsCheckout(scope MaterialsGitScope) (string, error) {
	directory, err := managedDocumentsLayout(scope)
	if err != nil {
		return "", err
	}
	cloned, err := managedRepositoryDirectory(scope.Root)
	if err != nil {
		return "", err
	}
	if !cloned {
		return "", errors.New("资料仓库尚未克隆，请先完成 Git 同步")
	}
	return directory, nil
}

// ProjectInstructionsDocumentsRoot verifies both the applied branch setting and
// the local Git HEAD before using or creating the project directory. The caller
// holds the repository lock through document IO.
func (s *MaterialsGitSyncService) ProjectInstructionsDocumentsRoot(scope MaterialsGitScope, ensure bool) (string, error) {
	if scope.RepositoryRole != domain.ProjectInstructionsRepositoryRole {
		return "", errors.New("无法确认项目资料仓库")
	}
	directory, err := validateManagedDocumentsCheckout(scope)
	if err != nil {
		return "", err
	}
	if scope.BindingID != "" {
		if err := documents.ValidateLocalDocumentsBranch(scope.Root, scope.Branch); err != nil {
			return "", err
		}
		return managedDocumentsDirectory(scope.Root, directory, ensure)
	}
	state, err := s.load(scope)
	if err != nil {
		return "", fmt.Errorf("读取项目资料检出状态失败：%w", err)
	}
	if state.Initializing || state.CheckoutPending || state.Branch == "" ||
		!domain.SameMaterialsGitRepositoryURL(state.URL, scope.URL) || state.CheckoutTargetBranch != scope.Branch ||
		scope.Branch != "" && state.Branch != scope.Branch {
		return "", errors.New("项目资料尚未检出当前配置的分支，请先同步")
	}
	if err := documents.ValidateLocalDocumentsBranch(scope.Root, state.Branch); err != nil {
		return "", fmt.Errorf("项目资料仓库当前分支与同步记录不一致：%w", err)
	}
	return managedDocumentsDirectory(scope.Root, directory, ensure)
}

func managedRepositoryDirectory(root string) (bool, error) {
	metadata := filepath.Join(root, ".git")
	if err := materialsGitSafePath(root, metadata, false); err != nil {
		return false, err
	}
	info, err := os.Lstat(metadata)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return false, errors.New("应用资料必须使用独立的 Git 仓库")
	}
	return true, nil
}

func managedDocumentsDirectory(root, directory string, ensure bool) (string, error) {
	target := filepath.Join(root, directory)
	if err := materialsGitSafePath(root, target, true); err != nil {
		return "", err
	}
	if ensure {
		if err := os.Mkdir(target, 0o755); err != nil && !os.IsExist(err) {
			return "", err
		}
	}
	info, err := os.Lstat(target)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("资料目录不可用：%s", target)
	}
	if _, err := os.Lstat(filepath.Join(target, ".git")); err == nil {
		return "", fmt.Errorf("资料子目录包含独立 Git 仓库：%s", target)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	return target, nil
}

func (s *MaterialsGitSyncService) managedRead(ctx context.Context, scope MaterialsGitScope, args ...string) (process.Result, error) {
	if err := s.checkRemoteScope(ctx); err != nil {
		return process.Result{}, err
	}
	return s.Git.runReadBudget(ctx, scope.Root, time.Minute, materialsBranchOutputLimit, args...)
}

func (s *MaterialsGitSyncService) checkManagedTree(ctx context.Context, scope MaterialsGitScope, tree, directory string) error {
	entries, err := s.managedRead(ctx, scope, "ls-tree", "-r", "-t", "-z", tree, "--", directory)
	if err != nil {
		return err
	}
	return checkManagedGitModes(entries.Stdout, directory)
}

func checkManagedGitModes(output, directory string) error {
	for _, entry := range splitGitNUL(output) {
		metadata, path, ok := strings.Cut(entry, "\t")
		mode, _, hasMode := strings.Cut(metadata, " ")
		if !ok || !hasMode || (path == directory && mode != "040000") || mode == "120000" || mode == "160000" {
			return fmt.Errorf("同步目录包含链接、子模块或同名文件：%s", path)
		}
	}
	return nil
}

func checkManagedGitPaths(output, directory string) error {
	for _, path := range splitGitNUL(output) {
		if !strings.HasPrefix(path, directory+"/") {
			return fmt.Errorf("存在同步目录外的改动，请检查 Git 后恢复同步：%s", path)
		}
	}
	return nil
}

func (s *MaterialsGitSyncService) checkManagedIndex(ctx context.Context, scope MaterialsGitScope, directory string) (bool, error) {
	staged, err := s.managedRead(ctx, scope, "diff", "--cached", "--no-renames", "--name-only", "-z")
	if err != nil {
		return false, err
	}
	if err := checkManagedGitPaths(staged.Stdout, directory); err != nil {
		return false, err
	}
	entries, err := s.managedRead(ctx, scope, "ls-files", "--stage", "-z", "--", directory)
	if err != nil {
		return false, err
	}
	return staged.Stdout != "", checkManagedGitModes(entries.Stdout, directory)
}

func (s *MaterialsGitSyncService) checkManagedWorkingPaths(ctx context.Context, scope MaterialsGitScope, directory string) (bool, error) {
	status, err := s.managedRead(ctx, scope, "status", "--porcelain=v1", "--no-renames", "-z", "--untracked-files=all", "--", directory)
	if err != nil {
		return false, err
	}
	needsStage := false
	for _, entry := range splitGitNUL(status.Stdout) {
		if len(entry) < 4 {
			return false, errors.New("无法确认本地 Git 改动路径")
		}
		needsStage = needsStage || entry[1] != ' '
		name := strings.TrimSuffix(entry[3:], "/")
		path := filepath.Join(scope.Root, filepath.FromSlash(name))
		if err := materialsGitSafePath(scope.Root, path, true); err != nil {
			return false, err
		}
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return false, err
		}
		if info.IsDir() {
			if _, err := os.Lstat(filepath.Join(path, ".git")); err == nil {
				return false, fmt.Errorf("资料路径包含独立 Git 仓库：%s", path)
			} else if !os.IsNotExist(err) {
				return false, err
			}
		}
	}
	return needsStage, nil
}

func (s *MaterialsGitSyncService) checkManagedPending(ctx context.Context, scope MaterialsGitScope, remote, head, directory string) error {
	const commitLimit = 2000
	commits, err := s.managedRead(ctx, scope, "rev-list", fmt.Sprintf("--max-count=%d", commitLimit+1), remote+".."+head)
	if err != nil {
		return err
	}
	ids := strings.Fields(commits.Stdout)
	if len(ids) > commitLimit {
		return errors.New("未推送提交过多，无法确认同步范围")
	}
	// A merge can carry changes already present on one side. Combined diff
	// examines the merge's own changes, while each unpublished parent is checked
	// separately. The final diff against the remote is checked before pushing.
	for _, commit := range ids {
		changed, err := s.managedRead(ctx, scope, "diff-tree", "--root", "-r", "-c", "--no-commit-id", "--no-renames", "--name-only", "-z", commit)
		if err != nil {
			return err
		}
		if err := checkManagedGitPaths(changed.Stdout, directory); err != nil {
			return err
		}
		if err := s.checkManagedTree(ctx, scope, commit, directory); err != nil {
			return err
		}
	}
	return nil
}
