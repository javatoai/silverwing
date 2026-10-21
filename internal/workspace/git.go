package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"silverwing-go/internal/commandruntime"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/executable"
	"silverwing-go/internal/process"
)

// GitException retains the process result so callers can distinguish Git's
// own rejection from a process-start failure without losing diagnostic text.
type GitException struct {
	Message string
	Result  process.Result
	Cause   error
}

func (e *GitException) Error() string {
	diagnostic := strings.TrimSpace(e.Result.Stderr)
	if diagnostic == "" {
		diagnostic = strings.TrimSpace(e.Result.Stdout)
	}
	if e.Cause != nil {
		if diagnostic != "" {
			return e.Message + ": " + diagnostic + ": " + e.Cause.Error()
		}
		return e.Message + ": " + e.Cause.Error()
	}
	if diagnostic == "" {
		return e.Message
	}
	return e.Message + "\n" + diagnostic
}

func (e *GitException) Unwrap() error { return e.Cause }

type WorktreeRecord struct {
	Path     string
	Head     string
	Branch   string
	Bare     bool
	Detached bool
	Locked   bool
}

type RepositoryStatus struct {
	Staged              bool
	Unstaged            bool
	Untracked           bool
	UnpushedCommits     int
	OperationInProgress string
}

func (s RepositoryStatus) SafeToArchive() bool {
	return !s.Staged && !s.Unstaged && !s.Untracked && s.UnpushedCommits == 0 && s.OperationInProgress == ""
}

func (s RepositoryStatus) HasUncommittedChanges() bool {
	return s.Staged || s.Unstaged || s.Untracked || s.OperationInProgress != ""
}

// GitClient provides the repository-scoped process boundary used by workspace
// operations. Local inspections can opt out of Git's optional index locks.
type GitClient struct {
	Runner            *process.Runner
	Executable        string
	Environment       map[string]string
	RemoveEnvironment []string
}

func NewGitClient(runner *process.Runner, executable string) *GitClient {
	if runner == nil {
		runner = process.NewRunner()
	}
	if strings.TrimSpace(executable) == "" {
		executable = "git"
	}
	return &GitClient{Runner: runner, Executable: executable}
}

// NewGitClientWithConfig creates a Git client whose child-process environment
// reflects the configured Git proxy target. Proxy variables inherited from the
// desktop process are removed when Git is not explicitly selected as a target.
func NewGitClientWithConfig(runner *process.Runner, config domain.AppConfig) *GitClient {
	client := NewGitClient(runner, gitConfigValue(config.GitExecutablePath))
	client.Environment, client.RemoveEnvironment = proxyProcessEnvironment(config, domain.ProxyTargetGit)
	return client
}

func (g *GitClient) processSpec(args []string, directory string, timeout time.Duration) process.Spec {
	if g == nil {
		g = NewGitClient(nil, "")
	}
	return process.Spec{
		Executable: g.Executable, Arguments: args, Directory: directory, Timeout: timeout,
		Env: g.Environment, RemoveEnv: g.RemoveEnvironment,
	}
}

func (g *GitClient) runProcess(ctx context.Context, spec process.Spec) (process.Result, error) {
	prepared, err := commandruntime.Prepare(ctx, g.Runner, nil, "", executable.ToolGit, spec.Executable)
	if err != nil {
		return process.Result{}, err
	}
	for name, value := range spec.Env {
		prepared.Env[name] = value
	}
	spec.Executable, spec.Env = prepared.Executable, prepared.Env
	return g.Runner.Run(ctx, spec)
}

func (g *GitClient) Run(repository string, arguments ...string) (process.Result, error) {
	return g.RunContext(context.Background(), repository, 10*time.Minute, true, false, arguments...)
}

func (g *GitClient) RunContext(ctx context.Context, repository string, timeout time.Duration, check, readOnly bool, arguments ...string) (process.Result, error) {
	if g == nil {
		g = NewGitClient(nil, "")
	}
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	args := make([]string, 0, len(arguments)+6)
	if readOnly {
		args = append(args, "--no-optional-locks")
	}
	args = append(args, "-c", "core.longpaths=true", "-C", repository)
	args = append(args, arguments...)
	result, err := g.runProcess(ctx, g.processSpec(args, repository, timeout))
	if err != nil {
		return result, &GitException{Message: commandFailureMessage(arguments, readOnly), Result: result, Cause: err}
	}
	if check && result.ExitCode != 0 {
		return result, &GitException{Message: commandFailureMessage(arguments, readOnly), Result: result}
	}
	return result, nil
}

// RunGlobalContext executes Git without a repository working directory. It is
// used for validation commands such as check-ref-format that do not need -C.
func (g *GitClient) RunGlobalContext(ctx context.Context, timeout time.Duration, check, readOnly bool, arguments ...string) (process.Result, error) {
	if g == nil {
		g = NewGitClient(nil, "")
	}
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	args := make([]string, 0, len(arguments)+4)
	if readOnly {
		args = append(args, "--no-optional-locks")
	}
	args = append(args, "-c", "core.longpaths=true")
	args = append(args, arguments...)
	result, err := g.runProcess(ctx, g.processSpec(args, "", timeout))
	if err != nil {
		return result, &GitException{Message: commandFailureMessage(arguments, readOnly), Result: result, Cause: err}
	}
	if check && result.ExitCode != 0 {
		return result, &GitException{Message: commandFailureMessage(arguments, readOnly), Result: result}
	}
	return result, nil
}

func commandFailureMessage(arguments []string, readOnly bool) string {
	verb := "Git 命令失败"
	if readOnly {
		verb = "Git read-only command failed"
	}
	return verb + "：git " + strings.Join(arguments, " ")
}

func (g *GitClient) CommonDirectory(repository string) (string, error) {
	result, err := g.Run(repository, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	return filepath.Abs(strings.TrimSpace(result.Stdout))
}

func (g *GitClient) TopLevel(repository string) (string, error) {
	result, err := g.Run(repository, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return filepath.Abs(strings.TrimSpace(result.Stdout))
}

func (g *GitClient) CurrentBranch(repository string) (string, error) {
	result, err := g.Run(repository, "branch", "--show-current")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result.Stdout), nil
}

func (g *GitClient) RemoteURL(repository, remote string) (string, error) {
	if remote == "" {
		remote = "origin"
	}
	result, err := g.RunContext(context.Background(), repository, 2*time.Minute, false, false, "remote", "get-url", remote)
	if err != nil {
		return "", err
	}
	if result.ExitCode != 0 {
		return "", nil
	}
	return strings.TrimSpace(result.Stdout), nil
}

func (g *GitClient) RemoteNames(repository string) ([]string, error) {
	result, err := g.Run(repository, "remote")
	if err != nil {
		return nil, err
	}
	return nonEmptyLines(result.Stdout), nil
}

func (g *GitClient) AddRemote(repository, name, url string) error {
	if strings.TrimSpace(name) == "" || strings.Contains(name, "/") || name == "." || name == ".." {
		return fmt.Errorf("远程名称不合法：%s", name)
	}
	if strings.TrimSpace(url) == "" {
		return errors.New("远程 URL 不能为空")
	}
	_, err := g.Run(repository, "remote", "add", name, url)
	return err
}

func (g *GitClient) RemoteDefaultBranch(repository, remote string) (string, error) {
	if remote == "" {
		remote = "origin"
	}
	result, err := g.RunContext(context.Background(), repository, 2*time.Minute, false, false,
		"symbolic-ref", "--quiet", "--short", "refs/remotes/"+remote+"/HEAD")
	if err != nil {
		return "", err
	}
	if result.ExitCode != 0 {
		return "", nil
	}
	return strings.TrimPrefix(strings.TrimSpace(result.Stdout), remote+"/"), nil
}

func (g *GitClient) CloneRepository(ctx context.Context, originURL, target, branch string) error {
	if err := ensureParent(target); err != nil {
		return err
	}
	result, err := g.runProcess(ctx, g.processSpec([]string{"-c", "core.longpaths=true", "clone", "--branch", branch, originURL, target}, "", 10*time.Minute))
	if err != nil {
		return &GitException{Message: "Git 克隆失败：" + originURL + "#" + branch, Result: result, Cause: err}
	}
	if result.ExitCode != 0 {
		return &GitException{Message: "Git 克隆失败：" + originURL + "#" + branch, Result: result}
	}
	return nil
}

func (g *GitClient) Version(ctx context.Context) (process.Result, error) {
	if g == nil {
		g = NewGitClient(nil, "")
	}
	return g.runProcess(ctx, g.processSpec([]string{"--version"}, "", 10*time.Second))
}

func gitConfigValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (g *GitClient) Worktrees(repository string) ([]WorktreeRecord, error) {
	result, err := g.Run(repository, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var records []WorktreeRecord
	var current WorktreeRecord
	hasPath := false
	flush := func() {
		if hasPath {
			if absolute, absErr := filepath.Abs(current.Path); absErr == nil {
				current.Path = filepath.Clean(absolute)
			}
			records = append(records, current)
		}
		current = WorktreeRecord{}
		hasPath = false
	}
	for _, line := range strings.Split(result.Stdout, "\n") {
		line = strings.TrimSuffix(line, "\r")
		switch {
		case strings.TrimSpace(line) == "":
			flush()
		case strings.HasPrefix(line, "worktree "):
			flush()
			current.Path = strings.TrimPrefix(line, "worktree ")
			hasPath = true
		case strings.HasPrefix(line, "HEAD "):
			current.Head = strings.TrimPrefix(line, "HEAD ")
		case strings.HasPrefix(line, "branch "):
			current.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		case line == "bare":
			current.Bare = true
		case line == "detached":
			current.Detached = true
		case line == "locked" || strings.HasPrefix(line, "locked "):
			current.Locked = true
		}
	}
	flush()
	return records, nil
}

func (g *GitClient) PruneWorktrees(repository string) error {
	_, err := g.Run(repository, "worktree", "prune")
	return err
}

func (g *GitClient) RefExists(repository, ref string) (bool, error) {
	result, err := g.RunContext(context.Background(), repository, 10*time.Minute, false, false, "show-ref", "--verify", "--quiet", ref)
	if err != nil {
		return false, err
	}
	return result.ExitCode == 0, nil
}

func (g *GitClient) Resolve(repository, ref string) (string, error) {
	result, err := g.Run(repository, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result.Stdout), nil
}

func (g *GitClient) IsAncestor(repository, ancestor, descendant string) (bool, error) {
	result, err := g.RunContext(context.Background(), repository, 10*time.Minute, false, false,
		"merge-base", "--is-ancestor", ancestor, descendant)
	if err != nil {
		return false, err
	}
	if result.ExitCode == 0 {
		return true, nil
	}
	if result.ExitCode == 1 {
		return false, nil
	}
	return false, &GitException{Message: "Git 祖先关系检查失败", Result: result}
}

func (g *GitClient) Fetch(repository, remote string) error {
	if remote == "" {
		remote = "origin"
	}
	_, err := g.RunContext(context.Background(), repository, 5*time.Minute, true, false, "fetch", "--prune", "--no-tags", remote)
	return err
}

func (g *GitClient) AddWorktree(repository, target, branch, baseRef string, trackingRemote ...string) error {
	remote := "origin"
	if len(trackingRemote) > 0 && strings.TrimSpace(trackingRemote[0]) != "" {
		remote = trackingRemote[0]
	}
	created := false
	_, err := g.RunContext(context.Background(), repository, 5*time.Minute, true, false,
		"-c", "core.symlinks=false", "worktree", "add", "-b", branch, "--no-track", target, baseRef)
	if err == nil {
		created = true
		_, err = g.Run(repository, "config", "branch."+branch+".remote", remote)
	}
	if err == nil {
		_, err = g.Run(repository, "config", "branch."+branch+".merge", "refs/heads/"+branch)
	}
	if err != nil && created {
		_ = g.RemoveWorktree(repository, target, true)
		_, _ = g.RunContext(context.Background(), repository, 10*time.Minute, false, false, "branch", "-D", branch)
		_, _ = g.RunContext(context.Background(), repository, 10*time.Minute, false, false, "worktree", "prune")
	}
	return err
}

func (g *GitClient) AddExistingWorktree(repository, target, branch string, force bool) error {
	args := []string{"-c", "core.symlinks=false", "worktree", "add"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, target, branch)
	_, err := g.RunContext(context.Background(), repository, 5*time.Minute, true, false, args...)
	return err
}

func (g *GitClient) AddTrackedRemoteWorktree(repository, target, branch, remote string) error {
	created := false
	_, err := g.RunContext(context.Background(), repository, 5*time.Minute, true, false,
		"-c", "core.symlinks=false", "worktree", "add", "-b", branch, "--track", target, remote+"/"+branch)
	if err == nil {
		created = true
	}
	if err != nil && created {
		_ = g.RemoveWorktree(repository, target, true)
		_, _ = g.RunContext(context.Background(), repository, 10*time.Minute, false, false, "branch", "-D", branch)
		_, _ = g.RunContext(context.Background(), repository, 10*time.Minute, false, false, "worktree", "prune")
	}
	return err
}

func (g *GitClient) SetBranchUpstream(repository, branch, remote string) error {
	if _, err := g.Run(repository, "config", "branch."+branch+".remote", remote); err != nil {
		return err
	}
	_, err := g.Run(repository, "config", "branch."+branch+".merge", "refs/heads/"+branch)
	return err
}

func (g *GitClient) AddDetachedWorktree(repository, target, ref string) error {
	_, err := g.RunContext(context.Background(), repository, 5*time.Minute, true, false,
		"-c", "core.symlinks=false", "worktree", "add", "--detach", target, ref)
	return err
}

func (g *GitClient) RemoveWorktree(repository, target string, force bool) error {
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, target)
	_, err := g.RunContext(context.Background(), repository, 5*time.Minute, true, false, args...)
	return err
}

func (g *GitClient) MoveWorktree(repository, source, target string) error {
	_, err := g.RunContext(context.Background(), repository, 5*time.Minute, true, false, "worktree", "move", source, target)
	return err
}

func (g *GitClient) RepairWorktree(repository, target string) error {
	_, err := g.RunContext(context.Background(), repository, 5*time.Minute, true, false, "worktree", "repair", target)
	return err
}

func (g *GitClient) Status(repository string) (RepositoryStatus, error) {
	result, err := g.Run(repository, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return RepositoryStatus{}, err
	}
	status := parseRepositoryPorcelain(result.Stdout)
	upstream, upstreamErr := g.RunContext(context.Background(), repository, 2*time.Minute, false, false,
		"rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	var countResult process.Result
	if upstreamErr != nil {
		return status, upstreamErr
	}
	var countErr error
	if upstream.ExitCode == 0 {
		countResult, countErr = g.Run(repository, "rev-list", "--count", "@{upstream}..HEAD")
	} else {
		countResult, countErr = g.Run(repository, "rev-list", "--count", "HEAD", "--not", "--remotes")
	}
	if countErr != nil {
		return status, countErr
	}
	status.UnpushedCommits, _ = strconv.Atoi(strings.TrimSpace(countResult.Stdout))
	status.OperationInProgress, err = g.operationInProgress(repository)
	return status, err
}

func (g *GitClient) LocalOnlyCommitCount(repository string) (int, error) {
	result, err := g.Run(repository, "rev-list", "--count", "HEAD", "--all", "--not", "--remotes")
	if err != nil {
		return 0, err
	}
	count, parseErr := strconv.Atoi(strings.TrimSpace(result.Stdout))
	if parseErr != nil {
		return 0, fmt.Errorf("无法解析本地未推送提交数量：%s", repository)
	}
	return count, nil
}

func (g *GitClient) operationInProgress(repository string) (string, error) {
	result, err := g.Run(repository, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", err
	}
	gitDirectory := strings.TrimSpace(result.Stdout)
	for _, entry := range []struct{ name, kind string }{
		{"MERGE_HEAD", "merge"}, {"rebase-merge", "rebase"}, {"rebase-apply", "rebase"},
		{"CHERRY_PICK_HEAD", "cherry-pick"}, {"REVERT_HEAD", "revert"}, {"BISECT_LOG", "bisect"}, {"BISECT_START", "bisect"},
	} {
		if fileExists(filepath.Join(gitDirectory, entry.name)) {
			return entry.kind, nil
		}
	}
	return "", nil
}

func nonEmptyLines(text string) []string {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func ensureParent(path string) error {
	parent := filepath.Dir(path)
	if parent == "." || parent == "" {
		return nil
	}
	return os.MkdirAll(parent, 0o755)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
