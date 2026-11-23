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

	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
	"silverwing-go/internal/storage"
)

// MaterialsGitScope is captured before a job starts. Check is called again
// before every command, so configuration edits cannot redirect an active job.
type MaterialsGitScope struct {
	Home, Root, Subdirectory, URL, Branch, EnvironmentID string
	RepositoryRole                                       string
	RemoteName, BindingID                                string
}

// AcquireRepositoryLock coordinates Git with document writers and preparation.
func (scope MaterialsGitScope) AcquireRepositoryLock() (func(), error) {
	if scope.BindingID != "" {
		return storage.AcquireDocumentsRepositoryLock(scope.Root)
	}
	return storage.AcquireMaterialsRepositoryLock(scope.Root)
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
	// CheckoutTargetBranch records the project branch setting applied to the
	// checkout. An empty setting tracks the remote default, including old binds.
	CheckoutTargetBranch string `json:",omitempty"`
	// Written before init. Kept until the initial histories have been joined.
	Initializing    bool
	CheckoutPending bool
	Staging         bool
	StageHead       string
	StagePaths      []string
}

type MaterialsGitSyncService struct {
	Git           *GitClient
	Paths         storage.Paths
	Check         func() error
	remoteOptions []string
}

type materialsGitEndpoint struct {
	url, branchTip string
	git            *GitClient
	options        []string
}

var ErrMaterialsGitScopeChanged = errors.New("Git 同步配置已变化，旧操作已停止")

func (s *MaterialsGitSyncService) statePath(scope MaterialsGitScope) string {
	if scope.BindingID != "" {
		return filepath.Join(s.Paths.Cache, "materials-git", scope.RepositoryRole, scope.BindingID+".json")
	}
	if scope.RepositoryRole != "" {
		return scope.Root + ".json"
	}
	digest := sha256.Sum256([]byte(filepath.Clean(scope.Root)))
	return filepath.Join(s.Paths.Cache, "materials-git", fmt.Sprintf("%x.json", digest[:16]))
}

func (s *MaterialsGitSyncService) load(scope MaterialsGitScope) (materialsGitBinding, error) {
	var state materialsGitBinding
	if scope.RepositoryRole != "" {
		if _, err := managedDocumentsLayout(scope); err != nil {
			return state, err
		}
		parent := filepath.Dir(s.statePath(scope))
		if err := materialsGitSafePath(parent, s.statePath(scope), false); err != nil {
			return state, err
		}
	}
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
	if !readOnly && scope.RepositoryRole != "" {
		cloned, err := managedRepositoryDirectory(scope.Root)
		if err != nil {
			return process.Result{}, err
		}
		if !cloned {
			return process.Result{}, errors.New("应用资料仓库的 Git 元数据已变化")
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
		args = materialsGitNoOverwrite(args)
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
	if scope.RepositoryRole != "" {
		return s.syncManagedDocuments(ctx, scope, resume, progress)
	}
	if progress == nil {
		progress = func(string) {}
	}
	if s.Git == nil {
		return status, errors.New("Git 同步执行器不可用")
	}
	if err := ctx.Err(); err != nil {
		return status, err
	}
	if s.Check != nil {
		if err := s.Check(); err != nil {
			return status, err
		}
	}
	if scope.Root == "" || !filepath.IsAbs(scope.Root) {
		return status, errors.New("请先配置任务资料根目录")
	}
	if err := materialsGitSafePath(scope.Root, scope.Root, false); err != nil {
		return status, err
	}
	if _, err := documents.ValidateMaterialsPaths(scope.Root); err != nil {
		return MaterialsGitSyncStatus{Paused: true, Error: err.Error()}, err
	}
	resultErr = func() error {
		unlock, err := scope.AcquireRepositoryLock()
		if err != nil {
			return fmt.Errorf("资料目录正在被其他操作使用：%w", err)
		}
		defer unlock()
		if _, err := documents.ValidateMaterialsPaths(scope.Root); err != nil {
			status = MaterialsGitSyncStatus{Paused: true, Error: err.Error()}
			return err
		}
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
	}()
	return status, resultErr
}

func materialsGitStatusMatches(state materialsGitBinding, scope MaterialsGitScope) bool {
	if state.StatusURL != "" {
		return materialsGitTargetMatches(state.StatusURL, state.StatusBranch, scope)
	}
	return materialsGitTargetMatches(state.URL, state.Branch, scope)
}

func materialsGitTargetMatches(url, branch string, scope MaterialsGitScope) bool {
	return branch == scope.Branch && domain.SameMaterialsGitRepositoryURL(url, scope.URL)
}

func (s *MaterialsGitSyncService) syncLocked(ctx context.Context, scope MaterialsGitScope, state *materialsGitBinding, progress func(string)) error {
	gitMetadata := filepath.Join(scope.Root, ".git")
	progress("核对仓库与分支")
	if _, err := s.run(ctx, scope, true, "check-ref-format", "--branch", scope.Branch); err != nil {
		return err
	}
	result, endpoint, git, options, err := s.readRemoteBranchHeadsAt(ctx, scope, scope.Root, false, 2*time.Minute, 4096, "refs/heads/"+scope.Branch)
	if err != nil {
		return err
	}
	branchTip, _, err := materialsGitRemoteBranch(result.Stdout, scope.Branch)
	if err != nil {
		return err
	}
	configuredRemote := materialsGitEndpoint{url: endpoint, branchTip: branchTip, git: git, options: options}
	activeRemote := configuredRemote
	prepared := *s
	prepared.Git, prepared.remoteOptions = git, options
	s = &prepared
	if _, err := os.Lstat(gitMetadata); os.IsNotExist(err) {
		if state.URL != "" && !state.Initializing {
			return errors.New("原资料仓库的 Git 元数据已变化，请检查仓库后重新接入")
		}
		state.URL, state.Branch, state.Initializing = scope.URL, scope.Branch, true
		state.CheckoutPending = false
		if err := s.save(scope, *state); err != nil {
			return err
		}
		// Init leaves local bytes in place; checkout applies native collision checks.
		if _, err := s.run(ctx, scope, false, "init", "--initial-branch="+scope.Branch, "."); err != nil {
			return err
		}
	} else if err != nil {
		return err
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
	if !hasOrigin && state.Initializing && materialsGitTargetMatches(state.URL, state.Branch, scope) {
		if _, err := s.run(ctx, scope, false, "remote", "add", "origin", endpoint); err != nil {
			return err
		}
	} else {
		verified, err := s.confirmMaterialsGitRemote(ctx, scope, configuredRemote, url)
		if err != nil {
			return err
		}
		if !hasOrigin || verified.url == "" {
			return errors.New("本地仓库 origin 与配置地址不一致，请先使用 Git 工具处理，SW 不会覆盖原 Git 源")
		}
		activeRemote = verified
	}
	pushURLs, err := s.run(ctx, scope, true, "remote", "get-url", "--push", "--all", "origin")
	if err != nil {
		return err
	}
	for _, pushURL := range strings.Fields(pushURLs.Stdout) {
		if hasOrigin && pushURL == url {
			continue
		}
		verified, err := s.confirmMaterialsGitRemote(ctx, scope, configuredRemote, pushURL)
		if err != nil {
			return err
		}
		if verified.url == "" {
			return errors.New("origin 推送地址与配置不一致，已停止同步")
		}
	}
	// Keep the saved address for scope checks. Use the existing origin's
	// verified endpoint when both spellings are independently reachable.
	endpoint = activeRemote.url
	s.Git, s.remoteOptions = activeRemote.git, activeRemote.options
	remoteScope := scope
	remoteScope.URL = endpoint
	if err := s.migrateGeneratedIgnores(ctx, scope); err != nil {
		return err
	}
	if state.CheckoutPending {
		if !materialsGitTargetMatches(state.URL, state.Branch, scope) {
			return errors.New("首次克隆尚未完成，请先恢复原同步目标并重新检查")
		}
		if err := s.checkoutMaterialsGitBranch(ctx, scope); err != nil {
			return err
		}
		state.CheckoutPending = false
	}
	if err := s.switchBranch(ctx, scope, state, endpoint); err != nil {
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
	// Merge before staging so the latest remote ignores govern local uploads.
	// Native merge preconditions protect existing staged and working edits.
	progress("拉取并合并远端更新")
	if _, _, err := s.remoteBranch(ctx, remoteScope); err != nil {
		return err
	}
	remote := "refs/remotes/origin/" + scope.Branch
	if err := s.runRemoteMutation(ctx, scope, "fetch", "--no-tags", "--no-recurse-submodules", endpoint, "+refs/heads/"+scope.Branch+":"+remote); err != nil {
		return err
	}
	if err := s.checkMaterialsGitTreeLayout(ctx, scope, remote); err != nil {
		return err
	}
	_, hasHead, err := s.optional(ctx, scope, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return err
	}
	if !hasHead {
		if _, err := s.run(ctx, scope, false, "checkout", "-b", scope.Branch, remote); err != nil {
			return fmt.Errorf("接入远端资料失败，本地文件已保留，请处理同名文件后重试：%w", err)
		}
	} else {
		args := []string{"-c", "commit.gpgSign=false", "merge", "--no-commit", "--no-edit", "--no-stat", "--ff", "--no-squash", "--no-autostash"}
		if state.Initializing {
			args = append(args, "--allow-unrelated-histories")
		}
		args = append(args, remote)
		if _, err := s.run(ctx, scope, false, args...); err != nil {
			return fmt.Errorf("合并远端更新失败，本地修改已保留，请使用 Git 工具处理后重新检查：%w", err)
		}
	}
	state.Initializing = false
	if err := s.save(scope, *state); err != nil {
		return err
	}
	// A remote tree may still contain an older generated ignore/runtime file.
	if err := s.migrateGeneratedIgnores(ctx, scope); err != nil {
		return err
	}
	progress("提交本地资料")
	head, _, err := s.optional(ctx, scope, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return err
	}
	state.Staging, state.StageHead, state.StagePaths = true, head, nil
	if err := s.save(scope, *state); err != nil {
		return err
	}
	// Whole repository: existing staged entries, tracked ignored files,
	// deletions and ordinary metadata all use Git's own ignore semantics.
	if _, err := s.run(ctx, scope, false, "add", "-A", "--", "."); err != nil {
		return err
	}
	staged, err := s.run(ctx, scope, true, "diff", "--cached", "--name-only", "-z")
	if err != nil {
		return err
	}
	_, merging, err := s.optional(ctx, scope, "rev-parse", "--verify", "MERGE_HEAD")
	if err != nil {
		return err
	}
	if staged.Stdout != "" || merging {
		commit := []string{"-c", "commit.gpgSign=false", "commit", "-m", "docs: sync task materials"}
		if merging {
			// A necessary merge records ancestry even for equal final trees. Keep
			// its native message rather than duplicating a prior local sync commit.
			commit = []string{"-c", "commit.gpgSign=false", "commit", "--no-edit"}
		}
		if _, err := s.run(ctx, scope, false, commit...); err != nil {
			return err
		}
	}
	state.Staging, state.StageHead, state.StagePaths = false, "", nil
	if err := s.save(scope, *state); err != nil {
		return err
	}
	progress("推送资料")
	if _, _, err := s.remoteBranch(ctx, remoteScope); err != nil {
		return err
	}
	if err := s.runRemoteMutation(ctx, scope, "push", "--porcelain", "--no-follow-tags", "--recurse-submodules=no", "--", endpoint, "refs/heads/"+scope.Branch+":refs/heads/"+scope.Branch); err != nil {
		return err
	}
	state.Changed = true
	state.URL, state.Branch = scope.URL, scope.Branch
	return nil
}

func (s *MaterialsGitSyncService) runRemoteMutation(ctx context.Context, scope MaterialsGitScope, args ...string) error {
	if scope.BindingID != "" {
		if err := s.verifyLocalDocumentsRemote(ctx, scope); err != nil {
			return err
		}
	}
	_, err := s.run(ctx, scope, false, append(append([]string(nil), s.remoteOptions...), args...)...)
	var gitErr *GitException
	if errors.As(err, &gitErr) {
		return diagnoseGitRemoteError(ctx, err)
	}
	return err
}

func (s *MaterialsGitSyncService) remoteBranch(ctx context.Context, scope MaterialsGitScope) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	if s.Check != nil {
		if err := s.Check(); err != nil {
			return "", false, err
		}
	}
	args := append(append([]string(nil), s.remoteOptions...), "ls-remote", "--heads", "--", scope.URL, "refs/heads/"+scope.Branch)
	result, err := s.Git.runReadBudget(ctx, scope.Root, 2*time.Minute, 4096, args...)
	if err != nil {
		return "", false, diagnoseGitRemoteError(ctx, err)
	}
	return materialsGitRemoteBranch(result.Stdout, scope.Branch)
}

func materialsGitRemoteBranch(stdout, branch string) (string, bool, error) {
	for _, line := range strings.Split(stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == "refs/heads/"+branch {
			return fields[0], true, nil
		}
	}
	if strings.TrimSpace(stdout) != "" {
		return "", false, fmt.Errorf("远端不存在分支 %s，请修改同步分支", branch)
	}
	return "", false, errors.New("请先在远程仓库创建分支")
}

func (s *MaterialsGitSyncService) switchBranch(ctx context.Context, scope MaterialsGitScope, state *materialsGitBinding, endpoint string) error {
	current, err := s.run(ctx, scope, true, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return errors.New("资料仓库处于分离 HEAD 状态，请先切回同步分支")
	}
	branch := strings.TrimSpace(current.Stdout)
	sameBinding := materialsGitTargetMatches(state.URL, state.Branch, scope)
	if sameBinding && branch != scope.Branch && !state.Initializing {
		return fmt.Errorf("当前分支已变为 %s，请先切回 %s 后重新检查", branch, scope.Branch)
	}
	changedBinding := state.URL != "" && !sameBinding
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
			if err := s.runRemoteMutation(ctx, scope, "fetch", "--no-tags", endpoint, "refs/heads/"+branch+":refs/remotes/origin/"+branch); err != nil {
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
			if err := s.runRemoteMutation(ctx, scope, "fetch", "--no-tags", endpoint, "refs/heads/"+scope.Branch+":refs/remotes/origin/"+scope.Branch); err != nil {
				return err
			}
			if err := s.checkoutMaterialsGitBranch(ctx, scope); err != nil {
				return err
			}
		}
	}
	state.URL, state.Branch = scope.URL, scope.Branch
	return nil
}

func (s *MaterialsGitSyncService) checkoutMaterialsGitBranch(ctx context.Context, scope MaterialsGitScope) error {
	ref := "refs/heads/" + scope.Branch
	_, exists, err := s.optional(ctx, scope, "show-ref", "--verify", ref)
	if err != nil {
		return err
	}
	args := []string{"checkout", scope.Branch}
	if !exists {
		ref = "refs/remotes/origin/" + scope.Branch
		args = []string{"checkout", "--track", "-b", scope.Branch, ref}
	}
	if err := s.checkMaterialsGitTreeLayout(ctx, scope, ref); err != nil {
		return err
	}
	_, err = s.run(ctx, scope, false, args...)
	return err
}

// Inspect only the root directories and immediate entries of year directories.
// Large unrelated subtrees do not need to be listed to check this layout.
func (s *MaterialsGitSyncService) checkMaterialsGitTreeLayout(ctx context.Context, scope MaterialsGitScope, ref string) error {
	list := func(args ...string) (process.Result, error) {
		if err := s.checkRemoteScope(ctx); err != nil {
			return process.Result{}, err
		}
		return s.Git.runReadBudget(ctx, scope.Root, time.Minute, 8*1024*1024, append([]string{"ls-tree", "-z"}, args...)...)
	}
	dirs, err := list("-d", "--name-only", ref)
	if err != nil {
		return err
	}
	for _, year := range splitGitNUL(dirs.Stdout) {
		if !documents.MaterialsYear(year) {
			continue
		}
		children, err := list(ref + ":" + year)
		if err != nil {
			return err
		}
		for _, child := range splitGitNUL(children.Stdout) {
			metadata, name, _ := strings.Cut(child, "\t")
			if documents.MaterialsRequirementName(name) && (strings.HasPrefix(metadata, "040000 tree ") || strings.HasPrefix(metadata, "120000 blob ")) {
				return fmt.Errorf("Git 引用 %s 包含未归入 requirements 的年度需求资料：%s", ref, filepath.Join(scope.Root, year, name))
			}
		}
	}
	return nil
}

func materialsGitUserChanges(status string) bool { return status != "" }

func materialsGitSameURL(a, b string) bool {
	a, b = strings.TrimSuffix(strings.TrimSpace(a), "/"), strings.TrimSuffix(strings.TrimSpace(b), "/")
	// Sync explicitly uses the confirmed URL. An existing HTTP origin for the
	// same repository can stay configured when the user upgrades to HTTPS.
	return a == b || MaterialsGitUsesHTTPS(b) && MaterialsGitHTTPSUpgrade(a) == b
}

func (s *MaterialsGitSyncService) confirmMaterialsGitRemote(ctx context.Context, scope MaterialsGitScope, remote materialsGitEndpoint, origin string) (materialsGitEndpoint, error) {
	if materialsGitSameURL(origin, scope.URL) || materialsGitSameURL(origin, remote.url) {
		return remote, nil
	}
	if MaterialsGitUsesHTTPS(remote.url) {
		if upgraded := MaterialsGitHTTPSUpgrade(origin); upgraded != "" {
			origin = upgraded
		}
	}
	if !domain.SameMaterialsGitRepositoryURL(strings.TrimSuffix(origin, "/"), strings.TrimSuffix(remote.url, "/")) {
		return materialsGitEndpoint{}, nil
	}
	// Accept the alternate spelling after a read confirms the same transport
	// endpoint or the same branch tip. Different paths may coexist on a server.
	originScope := scope
	originScope.URL = origin
	result, resolved, git, options, err := s.readRemoteBranchHeadsAt(ctx, originScope, scope.Root, false, 2*time.Minute, 4096, "refs/heads/"+scope.Branch)
	if err != nil {
		return materialsGitEndpoint{}, err
	}
	if materialsGitSameURL(resolved, remote.url) {
		return remote, nil
	}
	originTip, _, err := materialsGitRemoteBranch(result.Stdout, scope.Branch)
	if err != nil || originTip != remote.branchTip {
		return materialsGitEndpoint{}, err
	}
	return materialsGitEndpoint{url: resolved, branchTip: originTip, git: git, options: options}, nil
}

func materialsGitRetryable(err error) bool {
	if process.IsCleanupFailure(err) {
		return false
	}
	if errors.Is(err, storage.ErrLockBusy) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var remote *GitRemoteFailure
	if errors.As(err, &remote) {
		switch remote.Kind {
		case GitRemotePortRefused, GitRemoteClosed, GitRemoteUnavailable, GitRemoteTimeout, GitRemotePushRejected:
			return true
		default:
			return false
		}
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
