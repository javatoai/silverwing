package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
)

type BootstrapStepResult struct {
	Name      string `json:"name"`
	Succeeded bool   `json:"succeeded"`
	Message   string `json:"message"`
}
type BootstrapResult struct {
	Steps    []BootstrapStepResult `json:"steps"`
	Warnings []string              `json:"warnings"`
}

func (r BootstrapResult) Succeeded() bool {
	for _, step := range r.Steps {
		if !step.Succeeded {
			return false
		}
	}
	return true
}

type BootstrapService struct {
	Runner interface {
		Run(context.Context, process.Spec) (process.Result, error)
	}
	Git *GitClient
}

func NewBootstrapService(runner *process.Runner, git *GitClient) *BootstrapService {
	if runner == nil {
		runner = process.NewRunner()
	}
	if git == nil {
		git = NewGitClient(runner, "")
	}
	return &BootstrapService{Runner: runner, Git: git}
}

func (s *BootstrapService) Initialize(ctx context.Context, sourceRepository, worktree string, config domain.BootstrapConfig, onOutput func(process.StreamEvent)) (BootstrapResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return BootstrapResult{}, err
	}
	var steps []BootstrapStepResult
	var warnings []string
	if err := validateBootstrapConfig(config); err != nil {
		return BootstrapResult{}, err
	}
	for _, rule := range config.CopyRules {
		if err := ctx.Err(); err != nil {
			return BootstrapResult{Steps: steps, Warnings: warnings}, err
		}
		name := "复制 " + rule.Source + " → " + rule.Target
		warning, err := s.copyRule(sourceRepository, worktree, rule)
		if err := ctx.Err(); err != nil {
			return BootstrapResult{Steps: steps, Warnings: warnings}, err
		}
		if err != nil {
			steps = append(steps, BootstrapStepResult{Name: name, Message: err.Error()})
			warnings = append(warnings, name+"："+err.Error())
		} else {
			steps = append(steps, BootstrapStepResult{Name: name, Succeeded: true, Message: "复制完成"})
			if warning != "" {
				warnings = append(warnings, warning)
			}
		}
	}
	for _, command := range config.Commands {
		if !command.Enabled {
			continue
		}
		if err := ctx.Err(); err != nil {
			return BootstrapResult{Steps: steps, Warnings: warnings}, err
		}
		name := "执行 " + command.Name
		workdir, err := safeWorkspacePath(worktree, command.WorkingDirectory, "命令工作目录")
		if err == nil {
			if info, statErr := os.Stat(workdir); statErr != nil || !info.IsDir() {
				err = fmt.Errorf("命令工作目录不存在：%s", workdir)
			}
		}
		if err != nil {
			steps = append(steps, BootstrapStepResult{Name: name, Message: err.Error()})
			warnings = append(warnings, name+"："+err.Error())
			continue
		}
		result, runErr := s.Runner.Run(ctx, process.Spec{
			Executable: resolveBootstrapExecutable(command.Executable),
			Arguments:  command.Arguments,
			Directory:  workdir,
			Timeout:    time.Duration(command.TimeoutSeconds) * time.Second,
			OnOutput:   onOutput,
		})
		if err := ctx.Err(); err != nil {
			return BootstrapResult{Steps: steps, Warnings: warnings}, err
		}
		if runErr != nil {
			steps = append(steps, BootstrapStepResult{Name: name, Message: runErr.Error()})
			warnings = append(warnings, name+"："+runErr.Error())
			continue
		}
		if result.ExitCode == 0 {
			steps = append(steps, BootstrapStepResult{Name: name, Succeeded: true, Message: lastOutput(result.Stdout, 2000)})
		} else {
			detail := strings.TrimSpace(result.Stderr)
			if detail == "" {
				detail = strings.TrimSpace(result.Stdout)
			}
			steps = append(steps, BootstrapStepResult{Name: name, Message: lastOutput(detail, 2000)})
			warnings = append(warnings, fmt.Sprintf("%s 失败（退出码 %d）", name, result.ExitCode))
		}
	}
	return BootstrapResult{Steps: steps, Warnings: warnings}, ctx.Err()
}

func validateBootstrapConfig(config domain.BootstrapConfig) error {
	for _, rule := range config.CopyRules {
		if strings.TrimSpace(rule.Source) == "" || strings.TrimSpace(rule.Target) == "" {
			return fmt.Errorf("Bootstrap 复制源和目标不能为空")
		}
		if _, err := validatedRelativePath(rule.Source, "复制源"); err != nil {
			return err
		}
		if _, err := validatedRelativePath(rule.Target, "复制目标"); err != nil {
			return err
		}
	}
	for _, command := range config.Commands {
		if strings.TrimSpace(command.Name) == "" {
			return fmt.Errorf("Bootstrap 命令名称不能为空")
		}
		if strings.TrimSpace(command.Executable) == "" {
			return fmt.Errorf("Bootstrap 可执行程序不能为空")
		}
		if command.TimeoutSeconds <= 0 {
			return fmt.Errorf("Bootstrap 命令超时必须大于 0")
		}
		if strings.TrimSpace(command.WorkingDirectory) == "" {
			return fmt.Errorf("Bootstrap 命令工作目录不能为空")
		}
	}
	return nil
}

func validatedRelativePath(value, label string) (string, error) {
	path := filepath.Clean(value)
	if filepath.IsAbs(value) || filepath.VolumeName(value) != "" {
		return "", fmt.Errorf("%s 必须使用仓库内相对路径：%s", label, value)
	}
	for _, part := range strings.FieldsFunc(strings.ReplaceAll(value, "\\", "/"), func(r rune) bool { return r == '/' }) {
		if part == ".." {
			return "", fmt.Errorf("%s 不能包含 ..：%s", label, value)
		}
		if strings.EqualFold(part, ".git") {
			return "", fmt.Errorf("%s 不能访问 .git：%s", label, value)
		}
	}
	if path == "." && strings.TrimSpace(value) != "." {
		return "", fmt.Errorf("%s 路径不合法：%s", label, value)
	}
	return path, nil
}

type bootstrapSafeRoot struct {
	path     string
	realPath string
}

func safeWorkspacePath(root, relative, label string) (string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	rootAbs = filepath.Clean(rootAbs)
	rel, err := validatedRelativePath(relative, label)
	if err != nil {
		return "", err
	}
	target := filepath.Clean(filepath.Join(rootAbs, rel))
	if !insidePath(rootAbs, target) {
		return "", fmt.Errorf("路径超出工作区：%s", relative)
	}
	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", fmt.Errorf("工作区目录不存在：%s", root)
	}
	rootInfo, err := os.Stat(rootReal)
	if err != nil || !rootInfo.IsDir() {
		return "", fmt.Errorf("工作区根目录必须是目录：%s", root)
	}
	if err := validateBootstrapPath(bootstrapSafeRoot{path: rootAbs, realPath: rootReal}, target, label); err != nil {
		return "", err
	}
	return target, nil
}

func (s *BootstrapService) copyRule(sourceRepository, worktree string, rule domain.BootstrapCopyRule) (string, error) {
	source, err := safeWorkspacePath(sourceRepository, rule.Source, "复制源")
	if err != nil {
		return "", fmt.Errorf("复制源路径不安全：%w", err)
	}
	target, err := safeWorkspacePath(worktree, rule.Target, "复制目标")
	if err != nil {
		return "", fmt.Errorf("复制目标路径不安全：%w", err)
	}
	if _, err := os.Stat(source); err != nil {
		return "", fmt.Errorf("复制源不存在：%s", source)
	}
	if _, err := os.Lstat(target); err == nil {
		if !rule.Overwrite {
			return "", fmt.Errorf("目标已存在且规则禁止覆盖：%s", target)
		}
	}
	warning := ""
	if _, err := os.Lstat(target); err == nil {
		if tracked, _ := s.isTracked(worktree, rule.Target); tracked {
			warning = "初始化覆盖了 Git 已跟踪路径：" + filepath.ToSlash(rule.Target)
		}
	}
	return warning, copyTreeAtomically(source, target, sourceRepository, worktree, rule.Overwrite)
}

func (s *BootstrapService) isTracked(worktree, relative string) (bool, error) {
	result, err := s.Git.RunContext(context.Background(), worktree, 2*time.Minute, false, true, "ls-files", "--error-unmatch", "--", relative)
	if err != nil {
		return false, err
	}
	return result.ExitCode == 0, nil
}

func copyTreeAtomically(source, target, sourceRoot, targetRoot string, overwrite bool) error {
	if err := validateCopyContainment(sourceRoot, source); err != nil {
		return err
	}
	if err := validateCopyContainment(targetRoot, target); err != nil {
		return err
	}
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return copyFileAtomically(source, target, sourceRoot, targetRoot, overwrite)
	}
	if stat, statErr := os.Lstat(target); statErr == nil && !stat.IsDir() {
		if !overwrite {
			return fmt.Errorf("目标已存在且规则禁止覆盖：%s", target)
		}
		if err := validateCopyContainment(targetRoot, target); err != nil {
			return err
		}
		if err := os.Remove(target); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return err
	}
	if err := validateCopyContainment(targetRoot, target); err != nil {
		return err
	}
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if err := validateCopyContainment(sourceRoot, path); err != nil {
			return err
		}
		destination := target
		if rel != "." {
			destination = filepath.Join(target, rel)
		}
		if err := validateCopyContainment(targetRoot, destination); err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if entry.IsDir() {
			if err := os.MkdirAll(destination, 0o755); err != nil {
				return err
			}
			return validateCopyContainment(targetRoot, destination)
		}
		return copyFileAtomically(path, destination, sourceRoot, targetRoot, overwrite)
	})
}

func copyFileAtomically(source, target, sourceRoot, targetRoot string, overwrite bool) error {
	if err := validateCopyContainment(sourceRoot, source); err != nil {
		return err
	}
	if err := validateCopyContainment(targetRoot, filepath.Dir(target)); err != nil {
		return err
	}
	if err := validateCopyContainment(targetRoot, target); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	if err := validateCopyContainment(targetRoot, filepath.Dir(target)); err != nil {
		return err
	}
	if err := validateCopyContainment(targetRoot, target); err != nil {
		return err
	}
	if err := validateCopyContainment(sourceRoot, source); err != nil {
		return err
	}
	if info, err := os.Lstat(target); err == nil {
		if !overwrite {
			return fmt.Errorf("目标已存在且规则禁止覆盖：%s", target)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("复制目标是符号链接：%s", target)
		}
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".silverwing-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if validateCopyContainment(targetRoot, tmpName) == nil {
			_ = os.Remove(tmpName)
		}
	}()
	if err = validateCopyContainment(targetRoot, tmpName); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err = io.Copy(tmp, in); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Chmod(info.Mode().Perm()); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = validateCopyContainment(targetRoot, target); err != nil {
		return err
	}
	if err = validateCopyContainment(targetRoot, tmpName); err != nil {
		return err
	}
	if !overwrite {
		if _, err = os.Lstat(target); err == nil {
			return fmt.Errorf("目标已存在且规则禁止覆盖：%s", target)
		}
	}
	if err = validateCopyContainment(targetRoot, target); err != nil {
		return err
	}
	if err = validateCopyContainment(targetRoot, tmpName); err != nil {
		return err
	}
	if overwrite {
		return replaceBootstrapFile(tmpName, target, targetRoot)
	}
	return os.Rename(tmpName, target)
}

func replaceBootstrapFile(source, target, targetRoot string) error {
	if err := validateCopyContainment(targetRoot, source); err != nil {
		return err
	}
	if err := validateCopyContainment(targetRoot, target); err != nil {
		return err
	}
	if err := os.Rename(source, target); err == nil {
		return nil
	}
	backup := target + ".silverwing-backup"
	if err := validateCopyContainment(targetRoot, backup); err != nil {
		return err
	}
	if _, err := os.Lstat(backup); err == nil {
		if err := validateCopyContainment(targetRoot, backup); err != nil {
			return err
		}
		if err := os.Remove(backup); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := validateCopyContainment(targetRoot, target); err != nil {
		return err
	}
	if err := os.Rename(target, backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := validateCopyContainment(targetRoot, source); err != nil {
		return err
	}
	if err := validateCopyContainment(targetRoot, target); err != nil {
		return err
	}
	if err := os.Rename(source, target); err != nil {
		if validateCopyContainment(targetRoot, backup) == nil && validateCopyContainment(targetRoot, target) == nil {
			_ = os.Rename(backup, target)
		}
		return err
	}
	if validateCopyContainment(targetRoot, backup) == nil {
		_ = os.Remove(backup)
	}
	return nil
}

func validateCopyContainment(root, path string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	pathAbs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if !insidePath(filepath.Clean(rootAbs), filepath.Clean(pathAbs)) {
		return fmt.Errorf("复制路径超出仓库范围：%s", path)
	}
	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return err
	}
	rootInfo, err := os.Stat(rootReal)
	if err != nil || !rootInfo.IsDir() {
		return fmt.Errorf("复制根目录必须是目录：%s", root)
	}
	return validateBootstrapPath(bootstrapSafeRoot{path: filepath.Clean(rootAbs), realPath: rootReal}, filepath.Clean(pathAbs), "复制路径")
}

func validateBootstrapPath(root bootstrapSafeRoot, target, label string) error {
	if !insidePath(root.path, target) {
		return fmt.Errorf("%s 超出工作区：%s", label, target)
	}
	relative, err := filepath.Rel(root.path, target)
	if err != nil {
		return err
	}
	if relative == "." {
		return nil
	}
	current := root.path
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		if strings.EqualFold(component, ".git") {
			return fmt.Errorf("%s 禁止访问 .git：%s", label, target)
		}
		current = filepath.Join(current, component)
		info, statErr := os.Lstat(current)
		if errors.Is(statErr, os.ErrNotExist) {
			break
		}
		if statErr != nil {
			return fmt.Errorf("检查%s路径失败：%w", label, statErr)
		}
		reparse, reparseErr := bootstrapPathIsReparsePoint(current, info)
		if reparseErr != nil {
			return fmt.Errorf("检查%s路径失败：%w", label, reparseErr)
		}
		if reparse {
			return fmt.Errorf("%s 不支持符号链接或 Windows 重解析点：%s", label, current)
		}
		real, resolveErr := filepath.EvalSymlinks(current)
		if resolveErr != nil {
			return fmt.Errorf("检查%s路径失败：%w", label, resolveErr)
		}
		if !insidePath(root.realPath, real) {
			return fmt.Errorf("%s 超出工作区：%s", label, current)
		}
	}
	return nil
}

func resolveBootstrapExecutable(executable string) string {
	command := strings.TrimSpace(executable)
	if runtime.GOOS != "windows" || strings.ContainsAny(command, `/\\`) {
		return command
	}
	if ext := filepath.Ext(command); ext != "" {
		if path, err := exec.LookPath(command); err == nil {
			return path
		}
		return command
	}
	for _, ext := range strings.Split(os.Getenv("PATHEXT"), ";") {
		ext = strings.TrimSpace(ext)
		if ext == "" {
			continue
		}
		candidate := command + ext
		if path, err := exec.LookPath(candidate); err == nil {
			return path
		}
	}
	return command
}

func lastOutput(value string, max int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) > max {
		return string(runes[len(runes)-max:])
	}
	return value
}
func insidePath(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
