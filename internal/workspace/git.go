package workspace

import (
	"context"

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
