package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"silverwing-go/internal/process"
	"silverwing-go/internal/storage"
)

// MaterialsGitScope is captured before a job starts. Check is called again
// before every command, so configuration edits cannot redirect an active job.
type MaterialsGitScope struct {
	Home, Root, Subdirectory, URL, Branch, EnvironmentID string
}

type MaterialsGitSyncStatus struct {
	LastSuccess string `json:"lastSuccess"`
	Error       string `json:"error"`
	Paused      bool   `json:"paused"`
	Changed     bool   `json:"-"`
}

type materialsGitBinding struct {
	MaterialsGitSyncStatus
	URL, Branch             string
	StatusURL, StatusBranch string
	// Written before init. Kept until the initial histories have been joined.
	Initializing    bool
	CheckoutPending bool
	Staging         bool
	StageHead       string
	StagePaths      []string
}

type MaterialsGitSyncService struct {
	Git   *GitClient
	Paths storage.Paths
	Check func() error
}

var ErrMaterialsGitScopeChanged = errors.New("Git 同步配置已变化，旧操作已停止")

func (s *MaterialsGitSyncService) statePath(scope MaterialsGitScope) string {
	digest := sha256.Sum256([]byte(filepath.Clean(scope.Root)))
	return filepath.Join(s.Paths.Cache, "materials-git", fmt.Sprintf("%x.json", digest[:16]))
}

func (s *MaterialsGitSyncService) load(scope MaterialsGitScope) (materialsGitBinding, error) {
	var state materialsGitBinding
	data, err := os.ReadFile(s.statePath(scope))
	if os.IsNotExist(err) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	err = json.Unmarshal(data, &state)
	return state, err
}

func (s *MaterialsGitSyncService) save(scope MaterialsGitScope, state materialsGitBinding) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	path := s.statePath(scope)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return storage.WriteFileAtomic(path, data, 0o600)
}

func (s *MaterialsGitSyncService) Status(scope MaterialsGitScope) (MaterialsGitSyncStatus, error) {
	state, err := s.load(scope)
	if !materialsGitStatusMatches(state, scope) {
		return MaterialsGitSyncStatus{}, err
	}
	return state.MaterialsGitSyncStatus, err
}

func (s *MaterialsGitSyncService) run(ctx context.Context, scope MaterialsGitScope, readOnly bool, args ...string) (process.Result, error) {
	if err := ctx.Err(); err != nil {
		return process.Result{}, err
	}
	if s.Check != nil {
		if err := s.Check(); err != nil {
			return process.Result{}, err
		}
	}
	if !readOnly && materialsGitCheckedMutation(args) {
		root, err := s.Git.RunContext(ctx, scope.Root, time.Minute, true, true, "rev-parse", "--show-toplevel")
		if err != nil {
			return process.Result{}, err
		}
		branch, err := s.Git.RunContext(ctx, scope.Root, time.Minute, true, true, "symbolic-ref", "--short", "HEAD")
		if err != nil {
			return process.Result{}, err
		}
		if !samePath(strings.TrimSpace(root.Stdout), scope.Root) || strings.TrimSpace(branch.Stdout) != scope.Branch {
			return process.Result{}, errors.New("资料仓库身份或当前分支已变化，请重新核对后恢复同步")
		}
	}
	if !readOnly {
		command, tail := gitMutationCommand(args)
		if (command == "checkout" || command == "merge") && len(tail) > 0 {
			if err := s.checkMaterialsTree(ctx, scope, tail[len(tail)-1]); err != nil {
				return process.Result{}, err
			}
		}
	}
	return s.Git.RunContext(ctx, scope.Root, 2*time.Minute, true, readOnly, args...)
}

func materialsGitCheckedMutation(args []string) bool {
	for i := 0; i < len(args); i++ {
		if args[i] == "-c" {
			i++
			continue
		}
		if strings.HasPrefix(args[i], "-") {
			continue
		}
		switch args[i] {
		case "add", "commit", "merge", "push":
			return true
		}
		return false
	}
	return false
}

func (s *MaterialsGitSyncService) optional(ctx context.Context, scope MaterialsGitScope, args ...string) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	if s.Check != nil {
		if err := s.Check(); err != nil {
			return "", false, err
		}
	}
	r, err := s.Git.RunContext(ctx, scope.Root, time.Minute, false, true, args...)
	return strings.TrimSpace(r.Stdout), r.ExitCode == 0, err
}

// Sync never resets, cleans, stashes, aborts a merge, or forces a push. Git
// retains both committed sides when ordinary merging requires manual work.
func (s *MaterialsGitSyncService) Sync(ctx context.Context, scope MaterialsGitScope, resume bool, progress func(string)) (status MaterialsGitSyncStatus, resultErr error) {
	if progress == nil {
		progress = func(string) {}
	}
	if s.Git == nil {
		return status, errors.New("Git 同步执行器不可用")
	}
	if scope.Root == "" || !filepath.IsAbs(scope.Root) {
		return status, errors.New("请先配置任务资料根目录")
	}
	if err := materialsGitSafePath(scope.Root, scope.Root, false); err != nil {
		return status, err
	}
	if err := os.MkdirAll(scope.Root, 0o755); err != nil {
		return status, err
	}
	locks := NewOperationLocks(s.Paths.Locks)
	resultErr = locks.WithRepository(scope.Root, func() error {
		unlock, err := storage.AcquireLock(filepath.Join(scope.Root, ".silverwing-requirement-materials.lock"))
		if err != nil {
			return fmt.Errorf("资料目录正在被其他操作使用：%w", err)
		}
		defer unlock()
		state, err := s.load(scope)
		if err != nil {
			return fmt.Errorf("读取 Git 同步恢复状态失败：%w", err)
		}
		if !materialsGitStatusMatches(state, scope) {
			state.MaterialsGitSyncStatus = MaterialsGitSyncStatus{}
		}
		status = state.MaterialsGitSyncStatus
		if state.Paused && !resume {
			return errors.New(state.Error)
		}
		state.StatusURL, state.StatusBranch = scope.URL, scope.Branch
		defer func() { status = state.MaterialsGitSyncStatus }()
		err = s.syncLocked(ctx, scope, &state, progress)
		// Even a failed merge/push may have changed the checked-out tree.
		// Refreshing its local view is safe and never repeats Git work.
		state.Changed = true
		if err == nil {
			state.LastSuccess, state.Error, state.Paused = time.Now().UTC().Format(time.RFC3339), "", false
		} else if !errors.Is(err, context.Canceled) && !errors.Is(err, ErrMaterialsGitScopeChanged) {
			state.Error = err.Error()
			state.Paused = !materialsGitRetryable(err)
		}
		if saveErr := s.save(scope, state); saveErr != nil {
			state.Paused, state.Error = true, "保存 Git 同步状态失败："+saveErr.Error()
			return errors.Join(err, errors.New(state.Error))
		}
		return err
	})
	return status, resultErr
}

func materialsGitStatusMatches(state materialsGitBinding, scope MaterialsGitScope) bool {
	if state.StatusURL != "" {
		return state.StatusURL == scope.URL && state.StatusBranch == scope.Branch
	}
	return state.URL == scope.URL && state.Branch == scope.Branch
}

func (s *MaterialsGitSyncService) syncLocked(ctx context.Context, scope MaterialsGitScope, state *materialsGitBinding, progress func(string)) error {
	progress("核对仓库与分支")
	if _, err := s.run(ctx, scope, true, "check-ref-format", "--branch", scope.Branch); err != nil {
		return err
	}
	// Verify the selected branch before initialization or local commits.
	if _, _, err := s.remoteBranch(ctx, scope); err != nil {
		return err
	}
	// Ignore a repository inherited from a parent directory. Never alter it.
	_, statErr := os.Lstat(filepath.Join(scope.Root, ".git"))
	if statErr != nil && !os.IsNotExist(statErr) {
		return statErr
	}
	if os.IsNotExist(statErr) {
		if state.URL != "" && !state.Initializing {
			return errors.New("原资料仓库的 Git 元数据已变化，请检查仓库后重新接入")
		}
		state.URL, state.Branch, state.Initializing = scope.URL, scope.Branch, true
		state.CheckoutPending = false
		if err := s.save(scope, *state); err != nil {
			return err
		}
		_, exists, err := s.remoteBranch(ctx, scope)
		if err != nil {
			return err
		}
		entries, err := os.ReadDir(scope.Root)
		if err != nil {
			return err
		}
		empty := true
		for _, entry := range entries {
			if entry.Name() != ".silverwing-requirement-materials.lock" {
				empty = false
			}
		}
		if empty && exists {
			// The shared materials lock prevents Git clone from using the root
			// directly. Clone without checkout, into an empty sibling, then move
			// only the Git metadata; checkout itself uses Git's collision checks.
			stage, err := os.MkdirTemp(filepath.Dir(scope.Root), ".silverwing-git-clone-")
			if err != nil {
				return err
			}
			defer os.Remove(stage) // only removes an empty staging directory
			if s.Check != nil {
				if err := s.Check(); err != nil {
					return err
				}
			}
			_, err = s.Git.RunGlobalContext(ctx, 2*time.Minute, true, false, "clone", "--no-checkout", "--single-branch", "--branch", scope.Branch, "--", scope.URL, stage)
			if err != nil {
				return err
			}
			state.CheckoutPending = true
			if err := s.save(scope, *state); err != nil {
				return err
			}
			if err := os.Rename(filepath.Join(stage, ".git"), filepath.Join(scope.Root, ".git")); err != nil {
				return err
			}

		} else {
			if _, err := s.run(ctx, scope, false, "init", "--initial-branch="+scope.Branch, "."); err != nil {
				return err
			}
		}
	}
	root, err := s.run(ctx, scope, true, "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	if !samePath(strings.TrimSpace(root.Stdout), scope.Root) {
		return errors.New("资料目录不是独立仓库根目录，已停止同步")
	}
	operation, err := gitOperationInProgressContext(ctx, s.Git, scope.Root)
	if err != nil {
		return err
	}
	if operation != "" {
		return fmt.Errorf("仓库存在未完成的 %s 操作，请使用 Git 工具处理并提交后重新检查", operation)
	}
	url, hasOrigin, err := s.optional(ctx, scope, "remote", "get-url", "origin")
	if err != nil {
		return err
	}
	if !hasOrigin && state.Initializing && state.URL == scope.URL {
		if _, err = s.run(ctx, scope, false, "remote", "add", "origin", scope.URL); err != nil {
			return err
		}
	} else if !hasOrigin || !materialsGitSameURL(url, scope.URL) {
		return errors.New("本地仓库 origin 与配置地址不一致，请先使用 Git 工具处理，SW 不会覆盖原 Git 源")
	}
	pushURLs, err := s.run(ctx, scope, true, "remote", "get-url", "--push", "--all", "origin")
	if err != nil {
		return err
	}
	for _, pushURL := range strings.Fields(pushURLs.Stdout) {
		if !materialsGitSameURL(pushURL, scope.URL) {
			return errors.New("origin 推送地址与配置不一致，已停止同步")
		}
	}
	if err := s.excludeRuntimeFiles(ctx, scope); err != nil {
		return err
	}
	if head, ok, err := s.optional(ctx, scope, "rev-parse", "--verify", "HEAD"); err != nil {
		return err
	} else if ok {
		if err := s.checkMaterialsTree(ctx, scope, head); err != nil {
			return err
		}
	}
	if state.CheckoutPending {
		if state.URL != scope.URL || state.Branch != scope.Branch {
			return errors.New("首次克隆尚未完成，请先恢复原同步目标并重新检查")
		}
		if _, err := s.run(ctx, scope, false, "checkout", scope.Branch); err != nil {
			return err
		}
		state.CheckoutPending, state.Initializing = false, false
		if err := s.save(scope, *state); err != nil {
			return err
		}
	}
	if err := s.switchBranch(ctx, scope, state); err != nil {
		return err
	}
	for _, key := range []string{"user.name", "user.email"} {
		value, ok, err := s.optional(ctx, scope, "config", key)
		if err != nil {
			return err
		}
		if !ok || value == "" {
			return errors.New("请先在 Git 中配置 user.name 和 user.email")
		}
	}
	// Do not accidentally commit a user's pre-staged changes, even in scope.
	staged, err := s.run(ctx, scope, true, "diff", "--cached", "--name-only", "-z")
	if err != nil {
		return err
	}
	head, _, err := s.optional(ctx, scope, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return err
	}
	if staged.Stdout != "" && !materialsGitRecoverableIndex(*state, head, splitGitNUL(staged.Stdout)) {
		return errors.New("仓库已有暂存内容，请先使用 Git 工具提交或取消暂存后重试")
	}
	for _, path := range splitGitNUL(staged.Stdout) {
		if !materialsGitOwned(path, scope.Subdirectory) {
			return fmt.Errorf("恢复暂存区含范围外资料，已停止：%s", path)
		}
	}
	progress("提交本地资料")
	paths, err := s.materialPaths(ctx, scope)
	if err != nil {
		return err
	}
	state.Staging, state.StageHead, state.StagePaths = true, head, append([]string(nil), paths...)
	if err := s.save(scope, *state); err != nil {
		return err
	}
	// Stage in bounded batches to support Windows' command-line limit.
	for len(paths) > 0 {
		n := 1
		size := len(paths[0])
		for n < len(paths) && n < 64 && size+len(paths[n]) < 6000 {
			size += len(paths[n])
			n++
		}
		args := append([]string{"--literal-pathspecs", "add", "-A", "--"}, paths[:n]...)
		if _, err := s.run(ctx, scope, false, args...); err != nil {
			return err
		}
		paths = paths[n:]
	}
	staged, err = s.run(ctx, scope, true, "diff", "--cached", "--name-only", "-z")
	if err != nil {
		return err
	}
	if staged.Stdout != "" {
		// Recheck paths after staging; no user-added path may enter this commit.
		for _, path := range splitGitNUL(staged.Stdout) {
			if !materialsGitOwned(path, scope.Subdirectory) {
				return errors.New("暂存区出现范围外文件，已停止提交，请检查仓库")
			}
			if err := materialsGitSafePath(scope.Root, filepath.Join(scope.Root, filepath.FromSlash(path)), true); err != nil {
				return err
			}
		}
		if _, err := s.run(ctx, scope, false, "-c", "commit.gpgSign=false", "commit", "-m", "docs: sync task materials"); err != nil {
			return err
		}
	}
	state.Staging, state.StageHead, state.StagePaths = false, "", nil
	if err := s.save(scope, *state); err != nil {
		return err
	}
	progress("拉取并合并远端更新")
	_, exists, err := s.remoteBranch(ctx, scope)
	if err != nil {
		return err
	}
	if exists {
		if _, err := s.run(ctx, scope, false, "fetch", "--no-tags", "--no-recurse-submodules", scope.URL, "+refs/heads/"+scope.Branch+":refs/remotes/origin/"+scope.Branch); err != nil {
			return err
		}
		_, headExists, err := s.optional(ctx, scope, "rev-parse", "--verify", "HEAD")
		if err != nil {
			return err
		}
		if !headExists {
			if _, err := s.run(ctx, scope, false, "checkout", "-b", scope.Branch, "refs/remotes/origin/"+scope.Branch); err != nil {
				return err
			}
		} else {
			if err := s.checkMaterialsOutgoing(ctx, scope, "refs/remotes/origin/"+scope.Branch, true); err != nil {
				return err
			}
			args := []string{"-c", "commit.gpgSign=false", "merge", "--no-edit", "--no-stat", "--ff", "--no-squash", "--no-autostash"}
			if state.Initializing {
				args = append(args, "--allow-unrelated-histories")
			}
			args = append(args, "refs/remotes/origin/"+scope.Branch)
			if _, err := s.run(ctx, scope, false, args...); err != nil {
				return fmt.Errorf("合并远端更新失败，请打开仓库使用 Git 工具处理后重新检查：%w", err)
			}
		}
	}
	state.Initializing = false
	if err := s.save(scope, *state); err != nil {
		return err
	}
	_, headExists, err := s.optional(ctx, scope, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return err
	}
	if !headExists {
		return nil
	} // an empty repository with no saved materials
	progress("推送资料")
	if err := s.checkMaterialsTree(ctx, scope, "HEAD"); err != nil {
		return err
	}
	if exists {
		if err := s.checkMaterialsOutgoing(ctx, scope, "refs/remotes/origin/"+scope.Branch, false); err != nil {
			return err
		}
	}
	if _, _, err := s.remoteBranch(ctx, scope); err != nil {
		return err
	}
	if _, err := s.run(ctx, scope, false, "push", "--porcelain", "--no-follow-tags", "--recurse-submodules=no", "--", scope.URL, "refs/heads/"+scope.Branch+":refs/heads/"+scope.Branch); err != nil {
		return err
	}
	state.Changed = true
	state.URL, state.Branch = scope.URL, scope.Branch
	return nil
}

func (s *MaterialsGitSyncService) remoteBranch(ctx context.Context, scope MaterialsGitScope) (string, bool, error) {
	result, err := s.run(ctx, scope, true, "ls-remote", "--", scope.URL)
	if err != nil {
		return "", false, err
	}
	for _, line := range strings.Split(result.Stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == "refs/heads/"+scope.Branch {
			return fields[0], true, nil
		}
	}
	if strings.TrimSpace(result.Stdout) != "" {
		return "", false, fmt.Errorf("远端不存在分支 %s，请修改同步分支", scope.Branch)
	}
	return "", false, errors.New("请先在远程仓库创建分支")
}

func (s *MaterialsGitSyncService) switchBranch(ctx context.Context, scope MaterialsGitScope, state *materialsGitBinding) error {
	current, err := s.run(ctx, scope, true, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return errors.New("资料仓库处于分离 HEAD 状态，请先切回同步分支")
	}
	branch := strings.TrimSpace(current.Stdout)
	if state.URL == scope.URL && state.Branch == scope.Branch && branch != scope.Branch && !state.Initializing {
		return fmt.Errorf("当前分支已变为 %s，请先切回 %s 后重新检查", branch, scope.Branch)
	}
	changedBinding := state.URL != "" && (state.URL != scope.URL || state.Branch != scope.Branch)
	if branch != scope.Branch || changedBinding {
		dirty, err := s.run(ctx, scope, true, "-c", "status.renames=false", "status", "--porcelain=v1", "-z", "--untracked-files=all")
		if err != nil {
			return err
		}
		if materialsGitUserChanges(dirty.Stdout) {
			return errors.New("切换仓库或分支前请先提交现有资料，并处理未跟踪文件")
		}
		if _, hasHead, headErr := s.optional(ctx, scope, "rev-parse", "--verify", "HEAD"); headErr != nil {
			return headErr
		} else if hasHead {
			_, err := s.run(ctx, scope, false, "fetch", "--no-tags", scope.URL, "refs/heads/"+branch+":refs/remotes/origin/"+branch)
			if err != nil {
				return err
			}
			ahead, err := s.run(ctx, scope, true, "rev-list", "--count", "refs/remotes/origin/"+branch+"..HEAD")
			if err != nil {
				return err
			}
			if strings.TrimSpace(ahead.Stdout) != "0" {
				return errors.New("当前分支存在未推送提交，请先推送后再切换同步分支")
			}
		}
		if branch != scope.Branch {
			if _, err := s.run(ctx, scope, false, "fetch", "--no-tags", scope.URL, "refs/heads/"+scope.Branch+":refs/remotes/origin/"+scope.Branch); err != nil {
				return err
			}
			_, exists, err := s.optional(ctx, scope, "show-ref", "--verify", "refs/heads/"+scope.Branch)
			if err != nil {
				return err
			}
			args := []string{"checkout", scope.Branch}
			if !exists {
				args = []string{"checkout", "--track", "-b", scope.Branch, "refs/remotes/origin/" + scope.Branch}
			}
			if _, err := s.run(ctx, scope, false, args...); err != nil {
				return err
			}
		}
	}
	state.URL, state.Branch = scope.URL, scope.Branch
	return nil
}

func materialsGitRecoverableIndex(state materialsGitBinding, head string, staged []string) bool {
	if !state.Staging || state.StageHead != head {
		return false
	}
	paths := make(map[string]bool, len(state.StagePaths))
	for _, path := range state.StagePaths {
		paths[path] = true
	}
	for _, path := range staged {
		if !paths[path] {
			return false
		}
	}
	return true
}

func materialsGitUserChanges(status string) bool {
	for _, entry := range splitGitNUL(status) {
		if len(entry) < 4 {
			return true
		}
		path := entry[3:]
		if entry[:2] == "??" && strings.HasPrefix(filepath.Base(path), ".silverwing-") {
			continue
		}
		return true
	}
	return false
}

func materialsGitSameURL(a, b string) bool {
	return strings.TrimSuffix(strings.TrimSpace(a), "/") == strings.TrimSuffix(strings.TrimSpace(b), "/")
}

func materialsGitRetryable(err error) bool {
	if errors.Is(err, storage.ErrLockBusy) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	text := strings.ToLower(err.Error())
	var gitError *GitException
	if errors.As(err, &gitError) {
		text += "\n" + strings.ToLower(gitError.Result.Stdout+"\n"+gitError.Result.Stderr)
	}
	for _, fragment := range []string{"could not resolve", "couldn't resolve", "failed to connect", "connection timed out", "connection reset", "connection refused", "network is unreachable", "tls handshake", "http 50", "returned error: 50", "remote end hung up", "[rejected]", "fetch first", "non-fast-forward"} {
		if strings.Contains(text, fragment) {
			return true
		}
	}
	return false
}

func (s *MaterialsGitSyncService) excludeRuntimeFiles(ctx context.Context, scope MaterialsGitScope) error {
	result, err := s.run(ctx, scope, true, "rev-parse", "--path-format=absolute", "--git-path", "info/exclude")
	if err != nil {
		return err
	}
	path := strings.TrimSpace(result.Stdout)
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	rules := []string{".silverwing-*", "/[0-9][0-9][0-9][0-9]/*/[tT][eE][mM][pP]/"}
	var missing []string
	for _, rule := range rules {
		found := false
		for _, line := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(line) == rule {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, rule)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = file.WriteString("\n# Silverwing local runtime and temporary files\n" + strings.Join(missing, "\n") + "\n")
	return errors.Join(err, file.Close())
}
