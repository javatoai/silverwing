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

type CompareMode string

const (
	CompareMainline       CompareMode = "MAINLINE"
	CompareCommits        CompareMode = "COMMITS"
	CompareUncommitted    CompareMode = "UNCOMMITTED"
	maxCompareFilesOutput             = 16 * 1024 * 1024
	maxComparePatchOutput             = 64 * 1024 * 1024
	compareTimeout                    = 2 * time.Minute
)

type CompareRequest struct {
	Mode                 CompareMode `json:"mode"`
	CommitHashes         []string    `json:"commitHashes,omitempty"`
	StartCommit          string      `json:"startCommit,omitempty"`
	EndCommit            string      `json:"endCommit,omitempty"`
	IncludeWorkingTree   bool        `json:"includeWorkingTree,omitempty"`
	SimulateOnMainline   bool        `json:"simulateOnMainline,omitempty"`
	ExpectedBaseCommit   string      `json:"expectedBaseCommit,omitempty"`
	ExpectedTargetCommit string      `json:"expectedTargetCommit,omitempty"`
}

type CompareChange struct {
	Path    string `json:"path"`
	OldPath string `json:"oldPath,omitempty"`
	Kind    string `json:"kind"`
	// Blob object IDs are populated by the raw tree diff and are intentionally
	// omitted from the wire format. They let the review session batch-read the
	// two immutable snapshot blobs without resolving paths again.
	baseBlob   string
	targetBlob string
}

type CompareFilesResult struct {
	Files        []CompareChange `json:"files"`
	BaselineRef  string          `json:"baselineRef"`
	BaseCommit   string          `json:"baseCommit"`
	TargetCommit string          `json:"targetCommit"`
	Notice       string          `json:"notice"`
	SessionID    string          `json:"sessionId"`
}

type GitCompareConflictError struct {
	Commit  string
	Purpose string
	Paths   []string
	Cause   string
}

const PreviewOriginSimulated WorkspaceFileContentOrigin = "SIMULATED"

func (e *GitCompareConflictError) Error() string {
	purpose := firstNonEmpty(e.Purpose, "无法应用到已选提交范围")
	message := "提交 " + shortGitHash(e.Commit) + " " + purpose
	if len(e.Paths) > 0 {
		message += "；冲突文件：" + strings.Join(e.Paths, "、")
	}
	if e.Cause != "" {
		message += "；" + e.Cause
	}
	return message
}

type WorkspaceGitDiffService struct {
	Git   *GitClient
	Locks *OperationLocks
}

func NewWorkspaceGitDiffService(git *GitClient, locks *OperationLocks) *WorkspaceGitDiffService {
	if git == nil {
		git = NewGitClient(nil, "")
	}
	if locks == nil {
		locks = NewOperationLocks("")
	}
	return &WorkspaceGitDiffService{Git: git, Locks: locks}
}

// ResolveWorkspaceDiffMainBranch deliberately ignores the task's creation BaseRef:
// the review baseline always follows the current configured module/service master.
func ResolveWorkspaceDiffMainBranch(config domain.AppConfig, task domain.TaskManifest, ws domain.ServiceWorkspace) (WorkspaceMainBranch, bool, error) {
	service, err := config.GroupService(task.GroupID, ws.GroupServiceID)
	if err != nil {
		return WorkspaceMainBranch{}, false, fmt.Errorf("无法读取工作区对应的仓库主分支配置：%w", err)
	}
	branch := strings.TrimSpace(service.MasterBranch)
	if module := findModule(service.Modules, ws.ModuleID); module != nil {
		branch = strings.TrimSpace(service.EffectiveMasterBranch(*module))
	}
	if branch == "" {
		return WorkspaceMainBranch{}, false, nil
	}
	ref, err := ParseRemoteBranchRef(branch)
	if err != nil {
		return WorkspaceMainBranch{}, false, err
	}
	local := ref
	if ws.Strategy == domain.WorkspaceIndependentClone {
		local = domain.RemoteBranchRef{Remote: "origin", Branch: ref.Branch}
	}
	return WorkspaceMainBranch{DisplayRef: FormatRemoteBranchRef(ref), LocalRef: FormatRemoteBranchRef(local)}, true, nil
}

func (s *WorkspaceGitDiffService) FilesContext(ctx context.Context, ws domain.ServiceWorkspace, request CompareRequest, mainBranch WorkspaceMainBranch) (CompareFilesResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var result CompareFilesResult
	err := s.withWorkspace(ctx, ws, func(root string) error {
		state, err := s.prepare(ctx, root, request, mainBranch)
		if err != nil {
			return err
		}
		defer state.close()
		files, err := state.files(ctx)
		if err != nil {
			return err
		}
		result = CompareFilesResult{
			Files: files, BaselineRef: state.baselineRef, BaseCommit: state.baseCommit,
			TargetCommit: state.targetCommit, Notice: state.notice,
		}
		return nil
	})
	if err != nil {
		return CompareFilesResult{}, err
	}
	return result, nil
}

func (s *WorkspaceGitDiffService) PreviewContext(ctx context.Context, ws domain.ServiceWorkspace, request CompareRequest, change CompareChange, mainBranch WorkspaceMainBranch) (WorkspaceGitFilePreview, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var preview WorkspaceGitFilePreview
	err := s.withWorkspace(ctx, ws, func(root string) error {
		state, err := s.prepare(ctx, root, request, mainBranch)
		if err != nil {
			return err
		}
		defer state.close()
		if (request.ExpectedBaseCommit != "" && request.ExpectedBaseCommit != state.baseCommit) ||
			(request.ExpectedTargetCommit != "" && request.ExpectedTargetCommit != state.targetCommit) {
			return errors.New("Git 差异已变化，请重新读取文件列表后再预览")
		}
		files, err := state.files(ctx)
		if err != nil {
			return err
		}
		canonical, found := matchCompareChange(files, change)
		if !found {
			return errors.New("所选文件已不属于当前差异，请重新读取文件列表")
		}
		preview, err = state.preview(ctx, canonical)
		return err
	})
	if err != nil {
		return WorkspaceGitFilePreview{}, err
	}
	return preview, nil
}

func (s *WorkspaceGitDiffService) withWorkspace(ctx context.Context, ws domain.ServiceWorkspace, operation func(string) error) error {
	return s.withWorkspaceIdentity(ctx, ws, func(root, _ string) error { return operation(root) })
}

func (s *WorkspaceGitDiffService) withWorkspaceIdentity(ctx context.Context, ws domain.ServiceWorkspace, operation func(string, string) error) error {
	root, common, err := s.validateReadWorkspace(ctx, ws)
	if err != nil {
		return err
	}
	return s.Locks.WithRepository(common, func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		validatedRoot, validatedCommon, err := s.validateReadWorkspace(ctx, ws)
		if err != nil {
			return err
		}
		if !samePath(root, validatedRoot) || !samePath(common, validatedCommon) {
			return errors.New("工作区 Git 身份在读取期间发生变化，请重新打开审阅")
		}
		return operation(validatedRoot, validatedCommon)
	})
}

func (s *WorkspaceGitDiffService) validateReadWorkspace(ctx context.Context, ws domain.ServiceWorkspace) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	if ws.Health != domain.WorkspaceReady && ws.Health != domain.WorkspaceReadyWithWarnings {
		return "", "", errors.New("工作区尚未就绪，不能读取 Git 差异")
	}
	root, err := filepath.Abs(filepath.Clean(ws.WorktreePath))
	if err != nil {
		return "", "", err
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return "", "", fmt.Errorf("工作区不存在：%s", root)
	}
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	top, err := s.run(ctx, root, nil, 2*1024*1024, false, "rev-parse", "--show-toplevel")
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", "", ctxErr
	}
	if err != nil || top.ExitCode != 0 || !samePath(strings.TrimSpace(top.Stdout), root) {
		return "", "", errors.New("工作区路径不再是任务记录中的 Git 根目录")
	}
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	branch, err := s.run(ctx, root, nil, 2*1024*1024, false, "branch", "--show-current")
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", "", ctxErr
	}
	if err != nil || branch.ExitCode != 0 || strings.TrimSpace(branch.Stdout) != ws.Branch {
		return "", "", errors.New("工作区分支与任务记录不一致，请刷新后重试")
	}
	common, err := s.commonDirectory(ctx, root)
	if err != nil {
		return "", "", err
	}
	switch ws.Strategy {
	case domain.WorkspaceStandardWorktree:
		expected, err := s.commonDirectory(ctx, ws.RepositoryPath)
		if err != nil {
			return "", "", err
		}
		if !samePath(expected, common) {
			return "", "", errors.New("Worktree Git 身份与任务记录不一致")
		}
		common = expected
	case domain.WorkspaceIndependentClone:
		originResult, err := s.run(ctx, root, nil, 2*1024*1024, false, "remote", "get-url", "origin")
		if err != nil {
			return "", "", err
		}
		origin := ""
		if originResult.ExitCode == 0 {
			origin = strings.TrimSpace(originResult.Stdout)
		}
		if ws.OriginURL == nil || strings.TrimSpace(origin) != strings.TrimSpace(*ws.OriginURL) {
			return "", "", errors.New("独立克隆 origin 与任务记录不一致")
		}
	default:
		return "", "", fmt.Errorf("工作区策略不受支持：%s", ws.Strategy)
	}
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	return root, common, nil
}

func (s *WorkspaceGitDiffService) commonDirectory(ctx context.Context, repository string) (string, error) {
	result, err := s.run(ctx, repository, nil, 2*1024*1024, true, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	return filepath.Abs(strings.TrimSpace(result.Stdout))
}

type compareState struct {
	service            *WorkspaceGitDiffService
	root               string
	commonDir          string
	gitDir             string
	branch             string
	tempDir            string
	mode               CompareMode
	includeWorkingTree bool
	simulateOnMainline bool
	headCommit         string
	baseTree           string
	targetTree         string
	baseCommit         string
	targetCommit       string
	baselineRef        string
	notice             string
	indexEnv           map[string]string
	indexRemove        []string
}

func (s *WorkspaceGitDiffService) prepare(ctx context.Context, root string, request CompareRequest, mainBranch WorkspaceMainBranch) (*compareState, error) {
	mode := request.Mode
	if mode == "" {
		mode = CompareMainline
	}
	if mode != CompareMainline && mode != CompareCommits && mode != CompareUncommitted {
		return nil, fmt.Errorf("Git 差异模式不受支持：%s", mode)
	}
	state := &compareState{service: s, root: root, mode: mode}
	tempDir, err := os.MkdirTemp("", "silverwing-git-compare-")
	if err != nil {
		return nil, fmt.Errorf("创建临时 Git 差异空间失败：%w", err)
	}
	state.tempDir = tempDir
	if err := os.Mkdir(filepath.Join(tempDir, "objects"), 0o700); err != nil {
		state.close()
		return nil, err
	}
	objects, err := s.run(ctx, root, nil, 2*1024*1024, true, "rev-parse", "--path-format=absolute", "--git-path", "objects")
	if err != nil {
		state.close()
		return nil, err
	}
	if objects.StdoutTruncated || objects.ExitCode != 0 {
		state.close()
		return nil, errors.New("无法确定工作区 Git 对象目录")
	}
	sourceObjects := strings.TrimSpace(objects.Stdout)
	if info, err := os.Stat(sourceObjects); err != nil || !info.IsDir() {
		state.close()
		return nil, errors.New("工作区 Git 对象目录不可用")
	}
	state.indexEnv = map[string]string{
		"GIT_INDEX_FILE":                   filepath.Join(tempDir, "index"),
		"GIT_OBJECT_DIRECTORY":             filepath.Join(tempDir, "objects"),
		"GIT_ALTERNATE_OBJECT_DIRECTORIES": sourceObjects,
		"GIT_OPTIONAL_LOCKS":               "0",
	}
	state.indexRemove = gitCompareEnvironmentRemovals(s.Git.RemoveEnvironment)
	headResult, err := s.run(ctx, root, nil, 2*1024*1024, true, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		state.close()
		return nil, fmt.Errorf("无法读取当前工作区 HEAD：%w", err)
	}
	head := strings.TrimSpace(headResult.Stdout)
	if headResult.StdoutTruncated || head == "" {
		state.close()
		return nil, errors.New("无法读取当前工作区 HEAD")
	}
	state.headCommit = head
	switch mode {
	case CompareMainline:
		mainline, err := state.localMainlineCommit(ctx, mainBranch)
		if err != nil {
			state.close()
			return nil, err
		}
		mergeBase, err := s.run(ctx, root, nil, 2*1024*1024, false, "merge-base", mainline, head)
		if err != nil {
			state.close()
			return nil, fmt.Errorf("读取主分支与当前工作区的共同祖先失败：%w", err)
		}
		if mergeBase.ExitCode != 0 || strings.TrimSpace(mergeBase.Stdout) == "" {
			state.close()
			return nil, fmt.Errorf("主分支 %s 与当前工作区没有共同祖先", firstNonEmpty(mainBranch.DisplayRef, mainBranch.LocalRef))
		}
		state.baseCommit = strings.TrimSpace(mergeBase.Stdout)
		state.baselineRef = firstNonEmpty(mainBranch.DisplayRef, mainBranch.LocalRef)
		if request.IncludeWorkingTree {
			state.includeWorkingTree = true
			state.notice = "左侧为主分支与当前 HEAD 的共同祖先；右侧包含已提交、已暂存、未暂存和未跟踪文件。"
			if err := state.buildWorktreeTree(ctx, state.baseCommit); err != nil {
				state.close()
				return nil, err
			}
		} else {
			state.targetCommit = head
			state.notice = "左侧为主分支与当前 HEAD 的共同祖先；右侧为当前 HEAD。"
			if err := state.buildTreePair(ctx, state.baseCommit, head); err != nil {
				state.close()
				return nil, err
			}
		}
	case CompareUncommitted:
		state.baseCommit = head
		state.baselineRef = "HEAD"
		state.notice = "左侧为 HEAD；右侧包含已暂存、未暂存和未跟踪文件。"
		if err := state.buildWorktreeTree(ctx, head); err != nil {
			state.close()
			return nil, err
		}
	case CompareCommits:
		if err := state.buildCommitRange(ctx, head, request, mainBranch); err != nil {
			state.close()
			return nil, err
		}
	}
	return state, nil
}

func (state *compareState) localMainlineCommit(ctx context.Context, mainBranch WorkspaceMainBranch) (string, error) {
	if strings.TrimSpace(mainBranch.LocalRef) == "" {
		return "", errors.New("当前服务或模块未配置主分支")
	}
	localBranch, err := ParseRemoteBranchRef(mainBranch.LocalRef)
	if err != nil {
		return "", fmt.Errorf("主分支引用格式无效：%w", err)
	}
	qualifiedRef := "refs/remotes/" + FormatRemoteBranchRef(localBranch)
	ref, err := state.service.run(ctx, state.root, nil, 2*1024*1024, false, "rev-parse", "--verify", "--quiet", qualifiedRef+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("读取本地主分支 %s 失败：%w", firstNonEmpty(mainBranch.DisplayRef, mainBranch.LocalRef), err)
	}
	if ref.ExitCode != 0 || strings.TrimSpace(ref.Stdout) == "" {
		return "", fmt.Errorf("本地不存在主分支 %s；未执行自动 fetch，请先更新仓库主分支", firstNonEmpty(mainBranch.DisplayRef, mainBranch.LocalRef))
	}
	return strings.TrimSpace(ref.Stdout), nil
}

func (state *compareState) buildTreePair(ctx context.Context, base, target string) error {
	if err := state.readTree(ctx, base, base == ""); err != nil {
		return fmt.Errorf("初始化隔离 Git 基线失败：%w", err)
	}
	baseTree, err := state.writeTree(ctx)
	if err != nil {
		return fmt.Errorf("读取 Git 基线树失败：%w", err)
	}
	state.baseTree = baseTree
	if err := state.readTree(ctx, target, false); err != nil {
		return fmt.Errorf("读取目标提交树失败：%w", err)
	}
	targetTree, err := state.writeTree(ctx)
	if err != nil {
		return fmt.Errorf("读取目标提交树失败：%w", err)
	}
	state.targetTree = targetTree
	return nil
}

func (state *compareState) readTree(ctx context.Context, revision string, empty bool) error {
	if empty {
		_, err := state.service.runInState(ctx, state, 2*1024*1024, true, nil, "read-tree", "--empty")
		return err
	}
	_, err := state.service.runInState(ctx, state, 2*1024*1024, true, nil, "read-tree", revision)
	return err
}

func (state *compareState) writeTree(ctx context.Context) (string, error) {
	result, err := state.service.runInState(ctx, state, 2*1024*1024, true, nil, "write-tree")
	if err != nil {
		return "", err
	}
	tree := strings.TrimSpace(result.Stdout)
	if tree == "" {
		return "", errors.New("Git 未返回树对象")
	}
	return tree, nil
}

func (s *WorkspaceGitDiffService) run(ctx context.Context, root string, isolated *compareState, maxOutput int, check bool, arguments ...string) (process.Result, error) {
	if err := ctx.Err(); err != nil {
		return process.Result{}, err
	}
	if maxOutput <= 0 {
		maxOutput = maxCompareFilesOutput
	}
	env := make(map[string]string, len(s.Git.Environment)+4)
	for key, value := range s.Git.Environment {
		if !strings.HasPrefix(strings.ToUpper(key), "GIT_") {
			env[key] = value
		}
	}
	removals := gitCompareEnvironmentRemovals(s.Git.RemoveEnvironment)
	if isolated != nil {
		for key, value := range isolated.indexEnv {
			env[key] = value
		}
		removals = append(removals, isolated.indexRemove...)
	}
	args := []string{"--no-optional-locks", "-c", "core.longpaths=true", "-c", "core.splitIndex=false", "-c", "core.fsmonitor=false", "-c", "index.sparse=false", "-C", root}
	args = append(args, arguments...)
	result, err := s.Git.Runner.Run(ctx, process.Spec{
		Executable: s.Git.Executable, Arguments: args, Directory: root,
		Env: env, RemoveEnv: removals, Timeout: compareTimeout,
		MaxStdoutBytes: maxOutput, MaxStderrBytes: 1024 * 1024, KillOnOutputLimit: true,
	})
	if err != nil {
		return result, err
	}
	if result.StdoutTruncated || result.StderrTruncated {
		return result, errors.New("Git 差异输出超过安全上限，已停止读取")
	}
	if check && result.ExitCode != 0 {
		return result, &GitException{Message: "Git 差异读取失败", Result: result}
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	return result, nil
}

func (s *WorkspaceGitDiffService) runInState(ctx context.Context, state *compareState, maxOutput int, check bool, stdin []byte, arguments ...string) (process.Result, error) {
	if err := ctx.Err(); err != nil {
		return process.Result{}, err
	}
	if maxOutput <= 0 {
		maxOutput = maxCompareFilesOutput
	}
	env := make(map[string]string, len(s.Git.Environment)+len(state.indexEnv))
	for key, value := range s.Git.Environment {
		if !strings.HasPrefix(strings.ToUpper(key), "GIT_") {
			env[key] = value
		}
	}
	for key, value := range state.indexEnv {
		env[key] = value
	}
	removals := gitCompareEnvironmentRemovals(s.Git.RemoveEnvironment)
	removals = append(removals, state.indexRemove...)
	args := []string{"--no-optional-locks", "-c", "core.longpaths=true", "-c", "core.splitIndex=false", "-c", "core.fsmonitor=false", "-c", "index.sparse=false", "-C", state.root}
	args = append(args, arguments...)
	result, err := s.Git.Runner.Run(ctx, process.Spec{
		Executable: s.Git.Executable, Arguments: args, Directory: state.root,
		Env: env, RemoveEnv: removals, Stdin: stdin, Timeout: compareTimeout,
		MaxStdoutBytes: maxOutput, MaxStderrBytes: 1024 * 1024, KillOnOutputLimit: true,
	})
	if err != nil {
		return result, err
	}
	if result.StdoutTruncated || result.StderrTruncated {
		return result, errors.New("Git 差异输出超过安全上限，已停止读取")
	}
	if check && result.ExitCode != 0 {
		return result, &GitException{Message: "Git 差异读取失败", Result: result}
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	return result, nil
}

func (state *compareState) buildWorktreeTree(ctx context.Context, base string) error {
	if _, err := state.service.runInState(ctx, state, 2*1024*1024, true, nil, "read-tree", base); err != nil {
		return fmt.Errorf("初始化隔离 Git 索引失败：%w", err)
	}
	tree, err := state.service.runInState(ctx, state, 2*1024*1024, true, nil, "write-tree")
	if err != nil {
		return fmt.Errorf("读取基线树失败：%w", err)
	}
	state.baseTree = strings.TrimSpace(tree.Stdout)
	// Seed the target from the real index's tracked entries. Starting from the
	// comparison baseline loses force-added ignored files and files introduced
	// after that baseline. Importing stage records keeps all writes isolated.
	tracked, err := state.service.run(ctx, state.root, nil, maxCompareFilesOutput, true, "ls-files", "--stage", "-z")
	if err != nil {
		return fmt.Errorf("读取工作区已跟踪文件失败：%w", err)
	}
	if err := state.readTree(ctx, "", true); err != nil {
		return fmt.Errorf("初始化隔离工作区索引失败：%w", err)
	}
	if _, err := state.service.runInState(ctx, state, 2*1024*1024, true, []byte(tracked.Stdout), "update-index", "-z", "--index-info"); err != nil {
		return fmt.Errorf("读取工作区索引记录失败：%w", err)
	}
	if _, err := state.service.runInState(ctx, state, 2*1024*1024, true, nil, "add", "-A", "--", "."); err != nil {
		return fmt.Errorf("读取工作区当前文件失败：%w", err)
	}
	target, err := state.service.runInState(ctx, state, 2*1024*1024, true, nil, "write-tree")
	if err != nil {
		return fmt.Errorf("生成隔离工作区快照失败：%w", err)
	}
	state.targetTree = strings.TrimSpace(target.Stdout)
	state.targetCommit = "worktree:" + state.headCommit + ":" + state.targetTree
	return nil
}

func (state *compareState) close() {
	if state != nil && state.tempDir != "" {
		_ = os.RemoveAll(state.tempDir)
		state.tempDir = ""
	}
}

func gitCompareEnvironmentRemovals(existing []string) []string {
	remove := append([]string(nil), existing...)
	remove = append(remove,
		"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR", "GIT_OBJECT_DIRECTORY",
		"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_NAMESPACE", "GIT_PREFIX", "GIT_CEILING_DIRECTORIES",
		"GIT_DISCOVERY_ACROSS_FILESYSTEM", "GIT_CONFIG", "GIT_CONFIG_SYSTEM", "GIT_CONFIG_GLOBAL",
		"GIT_CONFIG_NOSYSTEM", "GIT_CONFIG_COUNT", "GIT_CONFIG_PARAMETERS", "GIT_OPTIONAL_LOCKS",
	)
	return remove
}

func shortGitHash(hash string) string { return hash[:min(7, len(hash))] }
