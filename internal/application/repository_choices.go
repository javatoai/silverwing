package application

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

const repositoryChoiceGitTimeout = 20 * time.Second
const repositoryBranchCacheTTL = 30 * time.Second

// RepositoryChoicesRequest selects either the local remote list or one
// remote's read-only branch listing. Force bypasses the cache for that list.
type RepositoryChoicesRequest struct {
	RepositoryID string  `json:"repositoryId"`
	Remote       *string `json:"remote"`
	Force        bool    `json:"force"`
}

// RepositoryChoices is the structured editor response. Remote is null when
// the caller only requested the repository's local remote names.
type RepositoryChoices struct {
	RepositoryID string   `json:"repositoryId"`
	RootPath     string   `json:"rootPath"`
	Remotes      []string `json:"remotes"`
	Remote       *string  `json:"remote"`
	Branches     []string `json:"branches"`
}

type repositoryChoiceIdentity struct {
	repositoryID string
	rootPath     string
	commonDir    string
}

type remoteBranchChoiceKey struct {
	repository repositoryChoiceIdentity
	remote     string
}

type cachedRemoteBranches struct {
	branches []string
	loadedAt time.Time
}

type remoteBranchLoad struct {
	cancel   context.CancelFunc
	done     chan struct{}
	branches []string
	stale    []string
	err      error
}

type remoteListLoad struct {
	cancel  context.CancelFunc
	done    chan struct{}
	remotes []string
	err     error
}

type repositoryChoicesCache struct {
	mu          sync.Mutex
	remotes     map[repositoryChoiceIdentity][]string
	remoteLoads map[repositoryChoiceIdentity]*remoteListLoad
	branches    map[remoteBranchChoiceKey]cachedRemoteBranches
	loads       map[remoteBranchChoiceKey]*remoteBranchLoad
}

var repositoryChoicesCaches sync.Map // map[*App]*repositoryChoicesCache

func (a *App) repositoryChoices(ctx context.Context, action Action) (ActionResult, error) {
	if err := ctx.Err(); err != nil {
		return ActionResult{}, err
	}
	var request RepositoryChoicesRequest
	if err := decodePayload(action.Payload, &request); err != nil {
		return ActionResult{}, err
	}
	if strings.TrimSpace(request.RepositoryID) == "" {
		return ActionResult{}, errors.New("repositoryId 不能为空")
	}
	if request.Remote != nil && strings.TrimSpace(*request.Remote) == "" {
		return ActionResult{}, errors.New("远程名称不能为空")
	}

	config, err := a.Config.Load()
	if err != nil {
		return ActionResult{}, err
	}
	repository, ok := configuredRepository(config, request.RepositoryID)
	if !ok {
		return ActionResult{}, fmt.Errorf("找不到仓库：%s", request.RepositoryID)
	}
	git := a.git(config)
	identity, err := repositoryChoiceRepositoryIdentity(ctx, git, repository)
	if err != nil {
		return ActionResult{}, err
	}

	result := RepositoryChoices{
		RepositoryID: repository.ID,
		RootPath:     repository.RootPath,
		Remotes:      []string{},
		Branches:     []string{},
	}
	if request.Remote != nil {
		remote := *request.Remote
		result.Remote = &remote
	}

	cache := a.repositoryChoicesCache()
	refreshRemotes := request.Force && request.Remote == nil
	remotes, err := cache.listRemotes(ctx, identity, refreshRemotes, func(ctx context.Context) ([]string, error) {
		command, runErr := git.RunContext(ctx, repository.RootPath, repositoryChoiceGitTimeout, true, true, "remote")
		if runErr != nil {
			return nil, runErr
		}
		values := parseRepositoryRemoteNames(command.Stdout)
		if len(values) == 0 {
			return nil, errors.New("仓库没有可用的 Git 远程")
		}
		return values, nil
	})
	result.Remotes = remotes
	if err != nil {
		if identityErr := a.requireCurrentRepositoryChoiceIdentity(ctx, identity); identityErr != nil {
			return ActionResult{}, identityErr
		}
		return ActionResult{Message: "Git 远程加载失败", Data: result}, err
	}

	if request.Remote != nil {
		remote := *request.Remote
		if !containsExact(remotes, remote) {
			if identityErr := a.requireCurrentRepositoryChoiceIdentity(ctx, identity); identityErr != nil {
				return ActionResult{}, identityErr
			}
			return ActionResult{Message: "远程分支加载失败", Data: result}, fmt.Errorf("找不到远程：%s", remote)
		}
		key := remoteBranchChoiceKey{repository: identity, remote: remote}
		branches, _, branchErr := cache.listRemoteBranches(ctx, key, request.Force, func(ctx context.Context) ([]string, error) {
			command, runErr := git.RunContext(ctx, repository.RootPath, repositoryChoiceGitTimeout, true, true, "ls-remote", "--heads", remote)
			if runErr != nil {
				return nil, runErr
			}
			return parseRemoteBranchChoices(remote, command.Stdout), nil
		})
		result.Branches = branches
		if branchErr != nil {
			if identityErr := a.requireCurrentRepositoryChoiceIdentity(ctx, identity); identityErr != nil {
				return ActionResult{}, identityErr
			}
			return ActionResult{Message: "远程分支加载失败", Data: result}, branchErr
		}
	}

	if err := a.requireCurrentRepositoryChoiceIdentity(ctx, identity); err != nil {
		return ActionResult{}, err
	}
	return ActionResult{Message: "仓库选择项已读取", Data: result}, nil
}

func (a *App) repositoryChoicesCache() *repositoryChoicesCache {
	if existing, ok := repositoryChoicesCaches.Load(a); ok {
		return existing.(*repositoryChoicesCache)
	}
	created := &repositoryChoicesCache{
		remotes:     make(map[repositoryChoiceIdentity][]string),
		remoteLoads: make(map[repositoryChoiceIdentity]*remoteListLoad),
		branches:    make(map[remoteBranchChoiceKey]cachedRemoteBranches),
		loads:       make(map[remoteBranchChoiceKey]*remoteBranchLoad),
	}
	actual, _ := repositoryChoicesCaches.LoadOrStore(a, created)
	return actual.(*repositoryChoicesCache)
}

func (c *repositoryChoicesCache) listRemotes(ctx context.Context, identity repositoryChoiceIdentity, force bool, load func(context.Context) ([]string, error)) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return []string{}, err
	}
	c.mu.Lock()
	if !force {
		if cached, ok := c.remotes[identity]; ok {
			c.mu.Unlock()
			return append([]string{}, cached...), nil
		}
	}
	if current := c.remoteLoads[identity]; current != nil {
		if !force {
			done := current.done
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return []string{}, ctx.Err()
			case <-done:
				return append([]string{}, current.remotes...), current.err
			}
		}
		current.cancel()
	}
	loadContext, cancel := context.WithCancel(ctx)
	flight := &remoteListLoad{cancel: cancel, done: make(chan struct{})}
	c.remoteLoads[identity] = flight
	c.mu.Unlock()

	values, err := load(loadContext)
	cancel()
	values = append([]string{}, values...)
	c.mu.Lock()
	flight.remotes = values
	flight.err = err
	if c.remoteLoads[identity] == flight {
		delete(c.remoteLoads, identity)
		if err == nil {
			c.remotes[identity] = append([]string{}, values...)
		}
	}
	close(flight.done)
	c.mu.Unlock()
	if err != nil {
		return []string{}, err
	}
	return append([]string{}, values...), nil
}

func (c *repositoryChoicesCache) listRemoteBranches(
	ctx context.Context,
	key remoteBranchChoiceKey,
	force bool,
	load func(context.Context) ([]string, error),
) ([]string, []string, error) {
	c.mu.Lock()
	cached, hasCached := c.branches[key]
	stale := append([]string{}, cached.branches...)
	if !force && hasCached && time.Since(cached.loadedAt) < repositoryBranchCacheTTL {
		branches := append([]string{}, cached.branches...)
		c.mu.Unlock()
		return branches, stale, nil
	}
	if current := c.loads[key]; current != nil {
		if !force {
			done := current.done
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return stale, stale, ctx.Err()
			case <-done:
				if current.err != nil {
					return append([]string{}, current.stale...), stale, current.err
				}
				return append([]string{}, current.branches...), stale, nil
			}
		}
		current.cancel()
	}
	loadContext, cancel := context.WithCancel(ctx)
	flight := &remoteBranchLoad{cancel: cancel, done: make(chan struct{}), stale: stale}
	c.loads[key] = flight
	c.mu.Unlock()

	branches, err := load(loadContext)
	cancel()
	c.mu.Lock()
	flight.branches = append([]string{}, branches...)
	flight.err = err
	if c.loads[key] == flight {
		delete(c.loads, key)
		if err == nil {
			c.branches[key] = cachedRemoteBranches{branches: append([]string{}, branches...), loadedAt: time.Now()}
		}
	}
	close(flight.done)
	c.mu.Unlock()
	if err != nil {
		return stale, stale, err
	}
	return append([]string{}, branches...), stale, nil
}

func configuredRepository(config domain.AppConfig, repositoryID string) (domain.RepositoryConfig, bool) {
	for _, repository := range config.Repositories {
		if repository.ID == repositoryID {
			return repository, true
		}
	}
	return domain.RepositoryConfig{}, false
}

func repositoryChoiceRepositoryIdentity(ctx context.Context, git *workspace.GitClient, repository domain.RepositoryConfig) (repositoryChoiceIdentity, error) {
	if strings.TrimSpace(repository.RootPath) == "" || strings.TrimSpace(repository.GitCommonDirectory) == "" {
		return repositoryChoiceIdentity{}, fmt.Errorf("仓库身份配置不完整：%s", repository.ID)
	}
	top, err := git.RunContext(ctx, repository.RootPath, repositoryChoiceGitTimeout, true, true, "rev-parse", "--show-toplevel")
	if err != nil {
		return repositoryChoiceIdentity{}, err
	}
	common, err := git.RunContext(ctx, repository.RootPath, repositoryChoiceGitTimeout, true, true, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return repositoryChoiceIdentity{}, err
	}
	rootPath, err := repositoryChoicePathKey(repository.RootPath)
	if err != nil {
		return repositoryChoiceIdentity{}, err
	}
	actualRootPath, err := repositoryChoicePathKey(strings.TrimSpace(top.Stdout))
	if err != nil {
		return repositoryChoiceIdentity{}, err
	}
	commonDir, err := repositoryChoicePathKey(repository.GitCommonDirectory)
	if err != nil {
		return repositoryChoiceIdentity{}, err
	}
	actualCommonDir, err := repositoryChoicePathKey(strings.TrimSpace(common.Stdout))
	if err != nil {
		return repositoryChoiceIdentity{}, err
	}
	if rootPath != actualRootPath || commonDir != actualCommonDir {
		return repositoryChoiceIdentity{}, fmt.Errorf("仓库身份已变化，请刷新配置后重试：%s", repository.ID)
	}
	return repositoryChoiceIdentity{repositoryID: repository.ID, rootPath: rootPath, commonDir: commonDir}, nil
}

func (a *App) requireCurrentRepositoryChoiceIdentity(ctx context.Context, expected repositoryChoiceIdentity) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	config, err := a.Config.Load()
	if err != nil {
		return err
	}
	repository, ok := configuredRepository(config, expected.repositoryID)
	if !ok {
		return fmt.Errorf("找不到仓库：%s", expected.repositoryID)
	}
	git := a.git(config)
	current, err := repositoryChoiceRepositoryIdentity(ctx, git, repository)
	if err != nil {
		return err
	}
	if current != expected {
		return fmt.Errorf("仓库配置已变化，请重新加载选择项：%s", expected.repositoryID)
	}
	return nil
}

func repositoryChoicePathKey(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if resolved, resolveErr := filepath.EvalSymlinks(absolute); resolveErr == nil {
		absolute = resolved
	}
	key := filepath.Clean(absolute)
	if runtime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	return key, nil
}

func parseRepositoryRemoteNames(output string) []string {
	seen := map[string]struct{}{}
	remotes := make([]string, 0)
	for _, line := range strings.Split(output, "\n") {
		remote := strings.TrimSpace(line)
		if remote == "" {
			continue
		}
		if _, exists := seen[remote]; exists {
			continue
		}
		seen[remote] = struct{}{}
		remotes = append(remotes, remote)
	}
	sort.SliceStable(remotes, func(i, j int) bool {
		return strings.ToLower(remotes[i]) < strings.ToLower(remotes[j])
	})
	return remotes
}

func parseRemoteBranchChoices(remote, output string) []string {
	seen := map[string]struct{}{}
	branches := make([]string, 0)
	for _, line := range strings.Split(output, "\n") {
		index := strings.Index(line, "refs/heads/")
		if index < 0 {
			continue
		}
		branch := strings.TrimSpace(line[index+len("refs/heads/"):])
		if branch == "" {
			continue
		}
		value := remote + "/" + branch
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		branches = append(branches, value)
	}
	sort.Strings(branches)
	return branches
}

func containsExact(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
