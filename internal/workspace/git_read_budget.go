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
	result, err := g.runProcess(ctx, spec)
	if result.StdoutTruncated || result.StderrTruncated {
		return result, &GitException{Message: "Git 读取超出输出预算，请缩小范围或继续分页读取", Result: result, Cause: errors.Join(errGitReadOutputLimit, err, ctx.Err())}
	}
	if err != nil || result.ExitCode != 0 {
		return result, &GitException{Message: commandFailureMessage(arguments, true), Result: result, Cause: err}
	}
	return result, nil
}

var errGitReadOutputLimit = errors.New("Git read output limit exceeded")
