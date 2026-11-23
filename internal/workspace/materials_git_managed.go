package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
)

const managedGitHooks = "core.hooksPath=.git/silverwing-disabled-hooks"

var errManagedRemoteBranchMoved = errors.New("远端分支在拉取期间有新更新，请重试同步")

func (s *MaterialsGitSyncService) syncManagedDocuments(ctx context.Context, scope MaterialsGitScope, resume bool, progress func(string)) (MaterialsGitSyncStatus, error) {
	var status MaterialsGitSyncStatus
	if s.Git == nil {
		return status, errors.New("Git 同步执行器不可用")
	}
	if err := s.checkRemoteScope(ctx); err != nil {
		return status, err
	}
	directory, err := managedDocumentsLayout(scope)
	if err != nil {
		return status, err
	}
	unlock, err := scope.AcquireRepositoryLock()
	if err != nil {
		return status, fmt.Errorf("资料目录正在被其他操作使用：%w", err)
	}
	defer unlock()
	state, err := s.load(scope)
	if err != nil {
		return status, fmt.Errorf("读取资料同步状态失败：%w", err)
	}
	if !materialsGitStatusMatches(state, scope) {
		state.MaterialsGitSyncStatus = MaterialsGitSyncStatus{}
	}
	if state.Paused && !resume {
		return state.MaterialsGitSyncStatus, errors.New(state.Error)
	}
	state.StatusURL, state.StatusBranch = scope.URL, scope.Branch
	if progress == nil {
		progress = func(string) {}
	}
	err = s.syncManagedDocumentsLocked(ctx, scope, directory, &state, progress)
	if err == nil {
		state.LastSuccess, state.Error, state.Paused = time.Now().UTC().Format(time.RFC3339), "", false
	} else if !errors.Is(err, context.Canceled) && !errors.Is(err, ErrMaterialsGitScopeChanged) {
		state.Error, state.Paused = err.Error(), !(materialsGitRetryable(err) || errors.Is(err, errManagedRemoteBranchMoved))
	}
	if saveErr := s.save(scope, state); saveErr != nil {
		state.Paused, state.Error = true, "保存 Git 同步状态失败："+saveErr.Error()
		err = errors.Join(err, errors.New(state.Error))
	}
	return state.MaterialsGitSyncStatus, err
}

func (s *MaterialsGitSyncService) syncManagedDocumentsLocked(ctx context.Context, scope MaterialsGitScope, directory string, state *materialsGitBinding, progress func(string)) error {
	progress("核对仓库与分支")
	cloned, err := managedRepositoryDirectory(scope.Root)
	if err != nil {
		return err
	}
	if !cloned && (scope.BindingID != "" || state.URL != "" && !state.Initializing) {
		return errors.New("应用资料仓库的 Git 元数据缺失，请恢复原仓库后重试")
	}
	checkoutTargetBranch := ""
	if scope.RepositoryRole == domain.ProjectInstructionsRepositoryRole {
		checkoutTargetBranch = scope.Branch
	}
	prepared := *s
	prepared.Git = localDocumentsGitClient(s.Git)
	if scope.BindingID != "" {
		if err := prepared.verifyDocumentsCheckoutRoot(ctx, scope); err != nil {
			return err
		}
		if err := prepared.verifyLocalDocumentsRemote(ctx, scope); err != nil {
			return err
		}
		if state.Branch == "" {
			current, err := prepared.managedRead(ctx, scope, "symbolic-ref", "--short", "HEAD")
			if err != nil {
				return errors.New("本地资料仓库处于分离 HEAD，请切回同步分支后重试")
			}
			state.Branch = strings.TrimSpace(current.Stdout)
		}
	}
	endpoint, branch, err := prepared.managedRemoteBranch(ctx, scope)
	if err != nil {
		return err
	}
	prepared.Git, prepared.remoteOptions = endpoint.git, endpoint.options
	s = &prepared
	scope.Branch = branch
	if !cloned {
		progress("克隆应用资料仓库")
		if err := s.cloneManagedDocuments(ctx, scope, directory, endpoint, checkoutTargetBranch, state); err != nil {
			return err
		}
	}
	state.Changed = true
	if err := s.verifyManagedCheckout(ctx, scope, endpoint); err != nil {
		return err
	}
	if state.CheckoutPending {
		if state.Branch != scope.Branch {
			return errors.New("首次检出尚未完成，请恢复原远端分支后重试")
		}
		if _, err := s.run(ctx, scope, false, "-c", managedGitHooks, "checkout", "--no-recurse-submodules", scope.Branch); err != nil {
			return fmt.Errorf("首次检出失败，本地文件已保留：%w", err)
		}
		state.CheckoutPending, state.Initializing = false, false
		if err := s.save(scope, *state); err != nil {
			return err
		}
	}
	progress("拉取并合并远端更新")
	remote, err := s.fetchManagedDocuments(ctx, scope, directory, endpoint)
	if err != nil {
		return err
	}
	if err := s.switchManagedBranch(ctx, scope, remote, checkoutTargetBranch, state); err != nil {
		return err
	}
	if _, err := s.checkManagedIndex(ctx, scope, directory); err != nil {
		return err
	}
	head, err := s.managedRead(ctx, scope, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return err
	}
	if err := s.checkManagedPending(ctx, scope, remote, strings.TrimSpace(head.Stdout), directory); err != nil {
		return err
	}
	if _, err := s.run(ctx, scope, false, "-c", managedGitHooks, "-c", "commit.gpgSign=false", "merge", "--no-edit", "--ff", "--no-squash", "--no-autostash", remote); err != nil {
		return fmt.Errorf("合并远端资料失败，本地修改已保留，请处理 Git 冲突后恢复同步：%w", err)
	}
	if _, err := managedDocumentsDirectory(scope.Root, directory, true); err != nil {
		return err
	}
	progress("提交本地资料")
	if err := s.commitManagedDocuments(ctx, scope, directory); err != nil {
		return err
	}
	head, err = s.managedRead(ctx, scope, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return err
	}
	commit := strings.TrimSpace(head.Stdout)
	if commit == remote {
		return nil
	}
	if err := s.checkManagedPending(ctx, scope, remote, commit, directory); err != nil {
		return err
	}
	changes, err := s.managedRead(ctx, scope, "diff-tree", "-r", "--no-commit-id", "--no-renames", "--name-only", "-z", remote, commit)
	if err != nil {
		return err
	}
	if err := checkManagedGitPaths(changes.Stdout, directory); err != nil {
		return err
	}
	progress("推送资料")
	if err := s.runRemoteMutation(ctx, scope, "-c", managedGitHooks, "push", "--porcelain", "--no-follow-tags", "--recurse-submodules=no", "--", endpoint.url, commit+":refs/heads/"+scope.Branch); err != nil {
		return err
	}
	// An explicit push URL may leave the tracking ref unchanged.
	tracking := managedDocumentsTrackingRef(scope, scope.Branch)
	if tracked, exists, err := s.optional(ctx, scope, "rev-parse", "--verify", tracking); err == nil && exists && tracked == remote {
		_, err = s.run(ctx, scope, false, "update-ref", tracking, commit, remote)
		return err
	}
	return nil
}

func managedDocumentsTrackingRef(scope MaterialsGitScope, branch string) string {
	name := scope.RemoteName
	if name == "" {
		name = "origin"
	}
	return "refs/remotes/" + name + "/" + branch
}

func (s *MaterialsGitSyncService) managedRemoteBranch(ctx context.Context, scope MaterialsGitScope) (materialsGitEndpoint, string, error) {
	mode, pattern := "--heads", "refs/heads/"+scope.Branch
	if scope.RepositoryRole == domain.ProjectInstructionsRepositoryRole && scope.Branch == "" {
		mode, pattern = "--symref", "HEAD"
	}
	result, url, git, options, err := s.readRemoteRefsAt(ctx, scope, scope.remoteReadRoot(), false, 2*time.Minute, 4096, mode, pattern)
	if err != nil {
		return materialsGitEndpoint{}, "", err
	}
	branch := scope.Branch
	var tip string
	if mode == "--symref" {
		branch, tip, err = parseManagedDefaultBranch(result.Stdout)
	} else {
		tip, _, err = materialsGitRemoteBranch(result.Stdout, branch)
	}
	if err != nil {
		return materialsGitEndpoint{}, "", err
	}
	if _, err := git.RunGlobalContext(ctx, time.Minute, true, true, "check-ref-format", "refs/heads/"+branch); err != nil {
		return materialsGitEndpoint{}, "", err
	}
	return materialsGitEndpoint{url: url, branchTip: tip, git: git, options: options}, branch, nil
}

func parseManagedDefaultBranch(output string) (branch, tip string, err error) {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[0] == "ref:" && strings.HasPrefix(fields[1], "refs/heads/") && fields[2] == "HEAD" && branch == "" {
			branch = strings.TrimPrefix(fields[1], "refs/heads/")
		} else if len(fields) == 2 && fields[1] == "HEAD" && tip == "" {
			tip = fields[0]
		}
	}
	if branch == "" || strings.HasPrefix(branch, "-") || strings.HasPrefix(branch, "refs/") || !validGitObjectID(tip) {
		return "", "", errors.New("无法确定远端默认分支，请先初始化仓库并设置默认分支")
	}
	return branch, tip, nil
}

func (s *MaterialsGitSyncService) cloneManagedDocuments(ctx context.Context, scope MaterialsGitScope, directory string, endpoint materialsGitEndpoint, checkoutTargetBranch string, state *materialsGitBinding) (resultErr error) {
	parent := filepath.Dir(scope.Root)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	temporary, err := os.MkdirTemp(parent, ".clone-")
	if err != nil {
		return err
	}
	defer func() {
		if !process.IsCleanupFailure(resultErr) {
			_ = os.RemoveAll(temporary)
		}
	}()
	if err := s.checkRemoteScope(ctx); err != nil {
		return err
	}
	args := append(append([]string(nil), endpoint.options...), "clone", "--template=", "--config", "core.longpaths=true", "--no-local", "--no-checkout", "--no-tags", "--no-recurse-submodules", "--single-branch", "--branch", scope.Branch, "--", endpoint.url, temporary)
	if _, err := endpoint.git.RunContext(ctx, parent, 2*time.Minute, true, false, args...); err != nil {
		return diagnoseGitRemoteError(ctx, err)
	}
	staged := scope
	staged.Root = temporary
	if err := s.checkManagedTree(ctx, staged, "HEAD", directory); err != nil {
		return err
	}
	if err := s.checkRemoteScope(ctx); err != nil {
		return err
	}
	state.URL, state.Branch, state.Initializing, state.CheckoutPending = scope.URL, scope.Branch, true, true
	state.CheckoutTargetBranch = checkoutTargetBranch
	if err := s.save(scope, *state); err != nil {
		return err
	}
	if _, err := os.Lstat(scope.Root); os.IsNotExist(err) {
		return os.Rename(temporary, scope.Root)
	} else if err != nil {
		return err
	}
	// Task creation may have prepared requirements before the first sync.
	// Installing the verified .git directory preserves every existing file.
	return os.Rename(filepath.Join(temporary, ".git"), filepath.Join(scope.Root, ".git"))
}

func (s *MaterialsGitSyncService) verifyManagedCheckout(ctx context.Context, scope MaterialsGitScope, endpoint materialsGitEndpoint) error {
	if err := s.verifyDocumentsCheckoutRoot(ctx, scope); err != nil {
		return err
	}
	operation, err := gitOperationInProgressContext(ctx, s.Git, scope.Root)
	if err != nil {
		return err
	}
	if operation != "" {
		return fmt.Errorf("仓库存在未完成的 %s 操作，请处理后恢复同步", operation)
	}
	if scope.BindingID != "" {
		return s.verifyLocalDocumentsRemote(ctx, scope)
	}
	for _, args := range [][]string{{"remote", "get-url", "--all", "origin"}, {"remote", "get-url", "--push", "--all", "origin"}} {
		urls, err := s.managedRead(ctx, scope, args...)
		if err != nil {
			return err
		}
		value := strings.TrimSpace(urls.Stdout)
		if value == "" || strings.ContainsAny(value, "\r\n") {
			return errors.New("应用资料仓库的 origin 与配置地址不一致，请检查后恢复同步")
		}
		remoteScope := scope
		remoteScope.URL = endpoint.url
		confirmed, err := s.confirmMaterialsGitRemote(ctx, remoteScope, endpoint, value)
		if err != nil {
			return err
		}
		if confirmed.url == "" {
			return errors.New("应用资料仓库的 origin 与配置地址不一致，请检查后恢复同步")
		}
	}
	return nil
}

func (s *MaterialsGitSyncService) fetchManagedDocuments(ctx context.Context, scope MaterialsGitScope, directory string, endpoint materialsGitEndpoint) (string, error) {
	remoteRef := managedDocumentsTrackingRef(scope, scope.Branch)
	previous, hasPrevious, err := s.optional(ctx, scope, "rev-parse", "--verify", remoteRef)
	if err != nil {
		return "", err
	}
	if err := s.runRemoteMutation(ctx, scope, "fetch", "--no-tags", "--no-recurse-submodules", "--refmap=", "--", endpoint.url, "refs/heads/"+scope.Branch); err != nil {
		return "", err
	}
	fetched, err := s.managedRead(ctx, scope, "rev-parse", "--verify", "FETCH_HEAD^{commit}")
	if err != nil {
		return "", err
	}
	remote := strings.TrimSpace(fetched.Stdout)
	if remote != endpoint.branchTip {
		return "", errManagedRemoteBranchMoved
	}
	if hasPrevious && previous != remote {
		if _, err := s.managedRead(ctx, scope, "merge-base", "--is-ancestor", previous, remote); err != nil {
			return "", fmt.Errorf("远端资料分支历史已改写，请检查后恢复同步：%w", err)
		}
	}
	if err := s.checkManagedTree(ctx, scope, remote, directory); err != nil {
		return "", err
	}
	args := []string{"update-ref", remoteRef, remote}
	if hasPrevious {
		args = append(args, previous)
	}
	if _, err := s.run(ctx, scope, false, args...); err != nil {
		return "", err
	}
	return remote, nil
}

func (s *MaterialsGitSyncService) switchManagedBranch(ctx context.Context, scope MaterialsGitScope, remote, checkoutTargetBranch string, state *materialsGitBinding) error {
	current, err := s.managedRead(ctx, scope, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return errors.New("应用资料仓库处于分离 HEAD，请切回同步分支后重试")
	}
	branch := strings.TrimSpace(current.Stdout)
	if branch != scope.Branch {
		if state.Branch != branch {
			return fmt.Errorf("当前资料分支已被切换，请切回 %s 后重试", state.Branch)
		}
		dirty, err := s.managedRead(ctx, scope, "status", "--porcelain=v1", "-z", "--untracked-files=all")
		if err != nil {
			return err
		}
		if dirty.Stdout != "" {
			return errors.New("当前分支仍有本地改动，请处理后再切换同步分支")
		}
		if _, err := s.managedRead(ctx, scope, "merge-base", "--is-ancestor", "HEAD", managedDocumentsTrackingRef(scope, branch)); err != nil {
			return errors.New("当前分支的提交尚未确认推送，请处理后再切换同步分支")
		}
		_, exists, err := s.optional(ctx, scope, "show-ref", "--verify", "--quiet", "refs/heads/"+scope.Branch)
		if err != nil {
			return err
		}
		args := []string{"-c", managedGitHooks, "checkout", "--no-recurse-submodules"}
		if exists {
			args = append(args, scope.Branch)
		} else {
			args = append(args, "-b", scope.Branch, remote)
		}
		if _, err := s.run(ctx, scope, false, args...); err != nil {
			return err
		}
	}
	state.URL, state.Branch = scope.URL, scope.Branch
	state.CheckoutTargetBranch = checkoutTargetBranch
	return s.save(scope, *state)
}

func (s *MaterialsGitSyncService) commitManagedDocuments(ctx context.Context, scope MaterialsGitScope, directory string) error {
	needsStage, err := s.checkManagedWorkingPaths(ctx, scope, directory)
	if err != nil {
		return err
	}
	if needsStage {
		if _, err := s.run(ctx, scope, false, "add", "-A", "--", directory+"/"); err != nil {
			return err
		}
	}
	changed, err := s.checkManagedIndex(ctx, scope, directory)
	if err != nil || !changed {
		return err
	}
	message := "docs: sync task materials"
	if scope.RepositoryRole == domain.ProjectInstructionsRepositoryRole {
		message = "docs: sync project materials"
	}
	_, err = s.run(ctx, scope, false, "-c", managedGitHooks, "-c", "commit.gpgSign=false", "commit", "--only", "-m", message, "--", directory+"/")
	return err
}
