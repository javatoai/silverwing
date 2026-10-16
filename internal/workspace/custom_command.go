package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
)

var errWorkspaceCommandTimeout = errors.New("workspace command timeout")

type WorkspaceCommandService struct {
	Runner *process.Runner
	OS     string
}

func NewWorkspaceCommandService(runner *process.Runner) *WorkspaceCommandService {
	if runner == nil {
		runner = process.NewRunner()
	}
	return &WorkspaceCommandService{Runner: runner, OS: runtime.GOOS}
}

func (s *WorkspaceCommandService) Execute(ctx context.Context, config domain.AppConfig, workspace string, command domain.WorkspaceCommandConfig, onOutput func(process.StreamEvent)) (domain.CommandResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(command.ID) == "" || strings.TrimSpace(command.Name) == "" || strings.TrimSpace(command.Executable) == "" {
		return domain.CommandResult{}, fmt.Errorf("快捷命令 ID、名称和可执行程序不能为空")
	}
	if !command.Enabled {
		return domain.CommandResult{}, fmt.Errorf("快捷命令已禁用")
	}
	if command.TimeoutSeconds < 1 || command.TimeoutSeconds > int64((time.Duration(1<<63-1))/time.Second) {
		return domain.CommandResult{}, fmt.Errorf("快捷命令超时时间超出支持范围")
	}
	root, err := filepath.Abs(workspace)
	if err != nil {
		return domain.CommandResult{}, err
	}
	root = filepath.Clean(root)
	if info, e := os.Stat(root); e != nil || !info.IsDir() {
		return domain.CommandResult{}, fmt.Errorf("工作区目录不存在：%s", root)
	}
	working, err := commandWorkingDirectory(root, command.WorkingDirectory)
	if err != nil {
		return domain.CommandResult{}, err
	}
	executable, err := s.resolveWorkspaceExecutable(root, command.Executable)
	if err != nil {
		return domain.CommandResult{}, err
	}
	env, remove := workspaceCommandProxyEnvironment(config)
	timeout := time.Duration(command.TimeoutSeconds) * time.Second
	commandCtx, cancel := context.WithTimeoutCause(ctx, timeout, errWorkspaceCommandTimeout)
	defer cancel()
	result, runErr := s.Runner.Run(commandCtx, process.Spec{Executable: executable, Arguments: command.Arguments, Directory: working, Env: env, RemoveEnv: remove, Timeout: timeout, OnOutput: onOutput})
	return workspaceCommandResult(commandCtx, command.TimeoutSeconds, result, runErr)
}

func workspaceCommandResult(commandCtx context.Context, timeoutSeconds int64, result process.Result, runErr error) (domain.CommandResult, error) {
	partial := domain.CommandResult{ExitCode: result.ExitCode, Stdout: result.Stdout, Stderr: result.Stderr}
	if process.IsCleanupFailure(runErr) {
		return partial, runErr
	}
	if errors.Is(runErr, context.DeadlineExceeded) && context.Cause(commandCtx) == errWorkspaceCommandTimeout {
		// The source runner prefixes stderr with its timeout line, then appends its
		// timeout diagnostics and captured stderr. Keep that same prefix-first order
		// while retaining the partial stream buffers already returned by Runner.
		return domain.CommandResult{
			ExitCode: 124,
			Stdout:   result.Stdout,
			Stderr:   fmt.Sprintf("命令执行超时（%d 秒）\n%s", timeoutSeconds, result.Stderr),
		}, nil
	}
	return partial, runErr
}

func commandWorkingDirectory(root, raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", fmt.Errorf("快捷命令工作目录不能为空")
	}
	rel := filepath.FromSlash(value)
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("快捷命令工作目录必须是工作区内的相对路径：%s", raw)
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	for _, part := range parts {
		if part == ".." {
			return "", fmt.Errorf("快捷命令工作目录不能包含 ..：%s", raw)
		}
	}
	for _, part := range parts {
		if strings.EqualFold(part, ".git") {
			return "", fmt.Errorf("快捷命令工作目录不能访问 .git：%s", raw)
		}
	}
	rel = filepath.Clean(rel)
	resolved := filepath.Join(root, rel)
	if !pathWithin(root, resolved) {
		return "", fmt.Errorf("快捷命令工作目录超出工作区：%s", raw)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("快捷命令工作目录不存在：%s", resolved)
	}
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	resolvedReal, err := filepath.EvalSymlinks(resolved)
	if err != nil {
		return "", err
	}
	if !pathWithin(rootReal, resolvedReal) {
		return "", fmt.Errorf("快捷命令工作目录超出工作区：%s", raw)
	}
	return resolved, nil
}

func (s *WorkspaceCommandService) resolveWorkspaceExecutable(root, raw string) (string, error) {
	exe := strings.TrimSpace(raw)
	if exe == "" {
		return "", fmt.Errorf("快捷命令可执行程序不能为空")
	}
	path := filepath.FromSlash(exe)
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	if strings.ContainsAny(exe, "/\\") {
		resolved := filepath.Clean(filepath.Join(root, path))
		if !pathWithin(root, resolved) {
			return "", fmt.Errorf("快捷命令可执行程序超出工作区：%s", raw)
		}
		for _, part := range strings.Split(filepath.ToSlash(resolved), "/") {
			if strings.EqualFold(part, ".git") {
				return "", fmt.Errorf("快捷命令可执行程序不能访问 .git：%s", raw)
			}
		}
		if info, err := os.Stat(resolved); err != nil || info.IsDir() {
			return "", fmt.Errorf("快捷命令可执行程序不存在：%s", resolved)
		}
		return resolved, nil
	}
	if resolved, err := exec.LookPath(exe); err == nil {
		return resolved, nil
	}
	candidates := []string{exe}
	if strings.EqualFold(s.OS, "windows") || strings.HasPrefix(strings.ToLower(s.OS), "windows") {
		if filepath.Ext(exe) == "" {
			candidates = append(candidates, exe+".cmd", exe+".bat", exe+".exe")
		}
	}
	for _, candidate := range candidates {
		workspaceCandidate := filepath.Join(root, candidate)
		if !pathWithin(root, workspaceCandidate) {
			continue
		}
		info, err := os.Stat(workspaceCandidate)
		if err == nil && info.Mode().IsRegular() {
			return workspaceCandidate, nil
		}
	}
	return exe, nil
}

func workspaceCommandProxyEnvironment(config domain.AppConfig) (map[string]string, []string) {
	return proxyProcessEnvironment(config, domain.ProxyTargetWorkspaceCommand)
}
