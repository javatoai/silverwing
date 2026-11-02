package workspace

import (
	"context"
	"errors"
	"time"

	"silverwing-go/internal/process"
)

// runReadBudget preserves Git's normal executable/environment setup while
// bounding output before it enters memory. Truncated output is never success.
func (g *GitClient) runReadBudget(ctx context.Context, root string, timeout time.Duration, stdoutBytes int, arguments ...string) (process.Result, error) {
	if g == nil {
		g = NewGitClient(nil, "")
	}
	args := append([]string{"--no-optional-locks", "-c", "core.longpaths=true", "-C", root}, arguments...)
	spec := g.processSpec(args, root, timeout)
	spec.MaxStdoutBytes, spec.MaxStderrBytes, spec.KillOnOutputLimit = stdoutBytes, 64*1024, true
	run := g.runProcess
	if g.readCommand != nil {
		run = g.readCommand
	}
	result, err := run(ctx, spec)
	truncated := result.StdoutTruncated || result.StderrTruncated
	cause := err
	// Only the Runner's pure budget stop is expected. Joined cleanup or other
	// execution errors must remain failures, even when output was truncated.
	if err != nil && !(err == process.ErrOutputLimit && truncated) {
		cause = &gitReadExecutionFailure{cause: err}
	}
	if truncated {
		return result, &GitException{Message: "Git 读取超出输出预算，请缩小范围或继续分页读取", Result: result, Cause: errors.Join(errGitReadOutputLimit, cause, ctx.Err())}
	}
	if err != nil || result.ExitCode != 0 {
		return result, &GitException{Message: commandFailureMessage(arguments, true), Result: result, Cause: cause}
	}
	return result, nil
}

var errGitReadOutputLimit = errors.New("Git read output limit exceeded")

// An expected Git exit is a business result; a failed or uncertain process
// execution is not. Keep that distinction even when output was truncated too.
type gitReadExecutionFailure struct{ cause error }

func (e *gitReadExecutionFailure) Error() string { return e.cause.Error() }
func (e *gitReadExecutionFailure) Unwrap() error { return e.cause }

func gitReadExecutionFailed(err error) bool {
	var failure *gitReadExecutionFailure
	return errors.As(err, &failure)
}
