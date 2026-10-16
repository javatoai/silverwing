package application

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"silverwing-go/internal/process"
	"silverwing-go/internal/workspace"
)

const repositoryAddressGitTimeout = 15 * time.Second

type RepositoryAddressesRequest struct {
	RepositoryID string `json:"repositoryId"`
	Force        bool   `json:"force"`
}

type RepositoryAddress struct {
	Remote string `json:"remote"`
	GitURL string `json:"gitUrl"`
	Fetch  bool   `json:"fetch"`
	Push   bool   `json:"push"`
}

type RepositoryAddresses struct {
	RepositoryID string              `json:"repositoryId"`
	RootPath     string              `json:"rootPath"`
	Remotes      []RepositoryAddress `json:"remotes"`
}

// repositoryAddresses reads only the selected repository's local Git config.
// The UI owns the same per-repository loading cache as the source desktop; this
// stateless action runs again only when the UI loads or explicitly refreshes.
func (a *App) repositoryAddresses(ctx context.Context, action Action) (ActionResult, error) {
	if err := ctx.Err(); err != nil {
		return ActionResult{}, err
	}
	var request RepositoryAddressesRequest
	if err := decodePayload(action.Payload, &request); err != nil {
		return ActionResult{}, err
	}
	request.RepositoryID = strings.TrimSpace(request.RepositoryID)
	if request.RepositoryID == "" {
		return ActionResult{}, errors.New("repositoryId 不能为空")
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
		return ActionResult{}, repositoryAddressIdentityFailure(ctx, err, false)
	}
	result := RepositoryAddresses{
		RepositoryID: repository.ID,
		RootPath:     repository.RootPath,
		Remotes:      []RepositoryAddress{},
	}

	namesOutput, err := readRepositoryAddressGit(ctx, git, repository.RootPath, workspace.RemoteAddressListStage, "remote")
	if err != nil {
		return ActionResult{}, a.repositoryAddressReadFailure(ctx, identity, err)
	}
	names := repositoryAddressRemoteNames(namesOutput)
	for _, remote := range names {
		fetchOutput, err := readRepositoryAddressGit(ctx, git, repository.RootPath, workspace.RemoteAddressFetchStage, "remote", "get-url", "--all", remote)
		if err != nil {
			return ActionResult{}, a.repositoryAddressReadFailure(ctx, identity, err)
		}
		pushOutput, err := readRepositoryAddressGit(ctx, git, repository.RootPath, workspace.RemoteAddressPushStage, "remote", "get-url", "--push", "--all", remote)
		if err != nil {
			return ActionResult{}, a.repositoryAddressReadFailure(ctx, identity, err)
		}
		mergeRepositoryAddressOutputs(&result.Remotes, remote, repositoryAddressLines(fetchOutput), repositoryAddressLines(pushOutput))
	}
	if err := a.requireCurrentRepositoryAddressIdentity(ctx, identity); err != nil {
		return ActionResult{}, err
	}
	return ActionResult{Message: "仓库远程地址已读取", Data: result}, nil
}

func readRepositoryAddressGit(ctx context.Context, git *workspace.GitClient, root string, stage workspace.RepositoryRemoteAddressStage, args ...string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	commandCtx, cancel := context.WithTimeout(ctx, repositoryAddressGitTimeout)
	defer cancel()
	command, err := git.RunContext(commandCtx, root, repositoryAddressGitTimeout, true, true, args...)
	return repositoryAddressGitResult(ctx, commandCtx, stage, command, err)
}

func repositoryAddressGitResult(ctx, commandCtx context.Context, stage workspace.RepositoryRemoteAddressStage, command process.Result, err error) (string, error) {
	if err == nil {
		return command.Stdout, nil
	}
	if process.IsCleanupFailure(err) {
		return "", workspace.NewRepositoryRemoteAddressFailure(stage, workspace.RemoteAddressOther, err)
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	kind := workspace.RemoteAddressOther
	var gitErr *workspace.GitException
	if errors.Is(commandCtx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &gitErr) && gitErr.Result.ExitCode == 124) {
		kind = workspace.RemoteAddressTimeout
	}
	return "", workspace.NewRepositoryRemoteAddressFailure(stage, kind, err)
}

func (a *App) repositoryAddressReadFailure(ctx context.Context, identity repositoryChoiceIdentity, readErr error) error {
	if process.IsCleanupFailure(readErr) {
		return readErr
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if identityErr := a.requireCurrentRepositoryAddressIdentity(ctx, identity); identityErr != nil {
		return identityErr
	}
	return readErr
}

func (a *App) requireCurrentRepositoryAddressIdentity(ctx context.Context, expected repositoryChoiceIdentity) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := a.requireCurrentRepositoryChoiceIdentity(ctx, expected); err != nil {
		return repositoryAddressIdentityFailure(ctx, err, true)
	}
	return nil
}

type repositoryAddressIdentityCleanupError struct{ cause error }

func (repositoryAddressIdentityCleanupError) Error() string {
	return "仓库身份校验失败：命令进程树清理未确认，已停止后续 Git 操作"
}

func (e repositoryAddressIdentityCleanupError) Unwrap() error { return e.cause }

// A failed identity command has already stopped the Git sequence. Preserve a
// cleanup failure before checking cancellation, without exposing Git stderr.
func repositoryAddressIdentityFailure(ctx context.Context, err error, recheck bool) error {
	if process.IsCleanupFailure(err) {
		return repositoryAddressIdentityCleanupError{cause: err}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if recheck {
		message := err.Error()
		for _, prefix := range []string{
			"找不到仓库：",
			"仓库身份配置不完整：",
			"仓库身份已变化，请刷新配置后重试：",
			"仓库配置已变化，请重新加载选择项：",
		} {
			if strings.HasPrefix(message, prefix) {
				return err
			}
		}
		return errors.New("仓库身份校验失败，请刷新配置后重试")
	}
	return errors.New("仓库身份校验失败，请检查本地路径和 Git 配置")
}

func repositoryAddressLines(output string) []string {
	lines := make([]string, 0)
	for _, line := range strings.Split(output, "\n") {
		if value := strings.TrimSpace(line); value != "" {
			lines = append(lines, value)
		}
	}
	return lines
}

func repositoryAddressRemoteNames(output string) []string {
	seen := make(map[string]struct{})
	names := make([]string, 0)
	for _, remote := range repositoryAddressLines(output) {
		if _, ok := seen[remote]; ok {
			continue
		}
		seen[remote] = struct{}{}
		names = append(names, remote)
	}
	sort.SliceStable(names, func(i, j int) bool {
		if names[i] == "origin" {
			return names[j] != "origin"
		}
		if names[j] == "origin" {
			return false
		}
		return names[i] < names[j]
	})
	return names
}

func mergeRepositoryAddressOutputs(result *[]RepositoryAddress, remote string, fetch, push []string) {
	indices := make(map[string]int, len(fetch)+len(push))
	for i, address := range *result {
		if address.Remote == remote {
			indices[address.GitURL] = i
		}
	}
	appendURL := func(raw string, isFetch, isPush bool) {
		safe := workspace.SanitizeGitRemoteURL(raw)
		index, exists := indices[safe]
		if !exists {
			index = len(*result)
			indices[safe] = index
			*result = append(*result, RepositoryAddress{Remote: remote, GitURL: safe})
		}
		(*result)[index].Fetch = (*result)[index].Fetch || isFetch
		(*result)[index].Push = (*result)[index].Push || isPush
	}
	for _, raw := range fetch {
		appendURL(raw, true, false)
	}
	for _, raw := range push {
		appendURL(raw, false, true)
	}
}
