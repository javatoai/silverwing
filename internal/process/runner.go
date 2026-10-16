// Package process executes local tools with explicit arguments, cancellation,
// concurrent pipe draining and descendant cleanup.
package process

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

type StreamEvent struct {
	Stream string
	Text   string
}
type Spec struct {
	Executable        string
	Arguments         []string
	Directory         string
	Env               map[string]string
	RemoveEnv         []string
	Stdin             []byte
	Timeout           time.Duration
	OnOutput          func(StreamEvent)
	MaxStdoutBytes    int
	MaxStderrBytes    int
	KillOnOutputLimit bool
}
type Result struct {
	ExitCode        int
	Stdout, Stderr  string
	Duration        time.Duration
	StdoutTruncated bool
	StderrTruncated bool
}

func (r Result) Successful() bool { return r.ExitCode == 0 }
func (r Result) RequireSuccess() error {
	if r.ExitCode == 0 {
		return nil
	}
	return fmt.Errorf("命令退出码 %d：%s", r.ExitCode, strings.TrimSpace(r.Stderr+"\n"+r.Stdout))
}

type Runner struct{}

func NewRunner() *Runner { return &Runner{} }

// NewDetachedCommand preserves platform command handling for user-opened tools
// whose lifetime is independent of one operation or the application window.
func NewDetachedCommand(executable string, arguments ...string) *exec.Cmd {
	cmd := platformCommand(context.Background(), executable, arguments)
	// Batch commands carry their arguments in private child environment variables.
	// Detached commands do not have a Spec.Env override, so start with the current
	// environment and overlay those variables without changing the parent process.
	if cmd.Env != nil {
		cmd.Env = mergeEnvironment(os.Environ(), cmd.Env)
	}
	return cmd
}

func (r *Runner) Run(ctx context.Context, spec Spec) (Result, error) {
	return r.run(ctx, spec, nil)
}

// RunInteractive lets a command exchange line-oriented output with a caller
// while retaining the same timeout, output capture, cancellation, and child
// cleanup path as Run. Returning from the exchange ends the child process tree;
// the callback must return when ctx is canceled or output is closed. Static
// Spec.Stdin and interactive input are exclusive.
func (r *Runner) RunInteractive(
	ctx context.Context,
	spec Spec,
	exchange func(context.Context, io.WriteCloser, <-chan StreamEvent) error,
) (Result, error) {
	if exchange == nil {
		return Result{ExitCode: -1}, errors.New("交互命令回调不能为空")
	}
	if spec.Stdin != nil {
		return Result{ExitCode: -1}, errors.New("交互命令不能同时使用固定标准输入")
	}
	return r.run(ctx, spec, exchange)
}

func (r *Runner) run(ctx context.Context, spec Spec, exchange func(context.Context, io.WriteCloser, <-chan StreamEvent) error) (result Result, runErr error) {
	start := time.Now()
	result = Result{ExitCode: -1}
	if strings.TrimSpace(spec.Executable) == "" {
		return result, errors.New("可执行程序不能为空")
	}
	if spec.Timeout <= 0 {
		spec.Timeout = 10 * time.Minute
	}
	parentContext := ctx
	ctx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()
	cmd := platformCommand(ctx, spec.Executable, spec.Arguments)
	cmd.Dir = spec.Directory
	cmd.Env = mergeEnvironment(environment(spec.Env, spec.RemoveEnv), cmd.Env)
	var stdin io.WriteCloser
	var outputEvents chan StreamEvent
	var interactionDone chan struct{}
	if exchange != nil {
		var err error
		stdin, err = cmd.StdinPipe()
		if err != nil {
			return result, fmt.Errorf("打开命令标准输入：%w", err)
		}
		outputEvents = make(chan StreamEvent, 128)
		interactionDone = make(chan struct{})
		previousOutput := spec.OnOutput
		spec.OnOutput = func(event StreamEvent) {
			if previousOutput != nil {
				previousOutput(event)
			}
			select {
			case outputEvents <- event:
			case <-ctx.Done():
			case <-interactionDone:
			}
		}
	} else if spec.Stdin != nil {
		cmd.Stdin = bytes.NewReader(spec.Stdin)
	}
	var outputMutex sync.Mutex
	var onLimit func()
	if spec.KillOnOutputLimit {
		onLimit = cancel
	}
	stdout := &streamWriter{mutex: &outputMutex, stream: "STDOUT", callback: spec.OnOutput, limit: spec.MaxStdoutBytes, onLimit: onLimit}
	stderr := &streamWriter{mutex: &outputMutex, stream: "STDERR", callback: spec.OnOutput, limit: spec.MaxStderrBytes, onLimit: onLimit}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.WaitDelay = 2 * time.Second
	cleanup, err := prepareProcess(cmd)
	if err != nil {
		if stdin != nil {
			_ = stdin.Close()
		}
		return result, err
	}
	var lifecycleMutex sync.Mutex
	var cancelCleanupErr error
	lifecycleClosed := false
	waitObserved := false
	var waitDone chan error
	cmd.Cancel = func() error {
		lifecycleMutex.Lock()
		defer lifecycleMutex.Unlock()
		if lifecycleClosed {
			return os.ErrProcessDone
		}
		err := cleanup.Kill(cmd)
		cancelCleanupErr = cleanupPhaseError("取消时终止进程树", err)
		return err
	}
	defer func() {
		lifecycleMutex.Lock()
		defer lifecycleMutex.Unlock()
		// Start/Attach/Resume and Cancel/Close share this command's lock. A
		// retired exec.Wait may finish later, but cannot operate closed handles.
		if !waitObserved && waitDone != nil {
			select {
			case lateErr := <-waitDone:
				waitObserved = true
				runErr = errors.Join(runErr, lateErr)
			default:
			}
		}
		rootExited := waitObserved && cmd.ProcessState != nil && cmd.ProcessState.Exited()
		killErr := cleanupPhaseError("终止进程树", cleanup.Kill(cmd))
		waitErr := cleanupPhaseError("确认进程树退出", cleanup.Wait(cmd, rootExited))
		closeErr := cleanupPhaseError("关闭进程生命周期", cleanup.Close())
		lifecycleClosed = true
		if cancelCleanupErr != nil || killErr != nil || waitErr != nil || closeErr != nil {
			runErr = errors.Join(runErr, ctx.Err())
		}
		runErr = cleanupResultError(runErr, cancelCleanupErr, killErr, waitErr, closeErr)
		result.Duration = time.Since(start)
	}()
	lifecycleMutex.Lock()
	if err = cmd.Start(); err != nil {
		lifecycleMutex.Unlock()
		if stdin != nil {
			_ = stdin.Close()
		}
		return result, fmt.Errorf("启动 %s：%w", spec.Executable, err)
	}
	attachErr := cleanup.Attach(cmd, ctx)
	lifecycleMutex.Unlock()
	waitDone = make(chan error, 1)
	go func() {
		waitErr := cmd.Wait()
		stdout.Flush()
		stderr.Flush()
		if outputEvents != nil {
			close(outputEvents)
		}
		waitDone <- waitErr
	}()
	if attachErr != nil {
		if stdin != nil {
			_ = stdin.Close()
		}
		cancel()
		waitErr, observed := awaitCommandWait(ctx, waitDone)
		waitObserved = observed
		if !observed {
			stdout.stopEvents()
			stderr.stopEvents()
		}
		return result, fmt.Errorf("建立进程生命周期：%w", errors.Join(attachErr, waitErr))
	}
	var interactionErr error
	interactionCanceled := false
	if exchange == nil {
		err, waitObserved = awaitCommandWait(ctx, waitDone)
	} else {
		type waitResultValue struct {
			err      error
			observed bool
		}
		waitResult := make(chan waitResultValue, 1)
		go func() {
			waitErr, observed := awaitCommandWait(ctx, waitDone)
			waitResult <- waitResultValue{waitErr, observed}
		}()
		interactionResult := make(chan error, 1)
		go func() {
			interactionErr := exchange(ctx, stdin, outputEvents)
			close(interactionDone)
			_ = stdin.Close()
			interactionResult <- interactionErr
		}()
		waitFinished := false
		interactionFinished := false
		for !waitFinished {
			select {
			case interactionErr = <-interactionResult:
				interactionFinished = true
				_ = stdin.Close()
				interactionCanceled = true
				cancel()
			case waited := <-waitResult:
				err, waitObserved = waited.err, waited.observed
				waitFinished = true
				_ = stdin.Close()
				if !waitObserved {
					stdout.stopEvents()
					stderr.stopEvents()
				}
				if !interactionFinished {
					interactionErr = <-interactionResult
					interactionFinished = true
				}
			}
		}
	}
	if !waitObserved {
		stdout.stopEvents()
		stderr.stopEvents()
	}
	result.Stdout, result.StdoutTruncated = stdout.snapshot()
	result.Stderr, result.StderrTruncated = stderr.snapshot()
	result.Duration = time.Since(start)
	if waitObserved && cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	if IsCleanupFailure(err) {
		return result, cleanupResultError(errors.Join(ctx.Err(), interactionErr), err)
	}
	if spec.KillOnOutputLimit && (result.StdoutTruncated || result.StderrTruncated) {
		return result, errors.New("命令输出超过允许的大小，已停止进程")
	}
	if ctx.Err() != nil {
		deadlineExpired := errors.Is(ctx.Err(), context.DeadlineExceeded)
		if parentContext.Err() != nil || deadlineExpired || !interactionCanceled {
			return result, fmt.Errorf("命令已取消或超时：%w", ctx.Err())
		}
	}
	if interactionErr != nil {
		return result, fmt.Errorf("交互命令：%w", interactionErr)
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("读取命令结果：%w", err)
	}
	return result, nil
}

func environment(overrides map[string]string, removals []string) []string {
	key := func(s string) string {
		if runtime.GOOS == "windows" {
			return strings.ToUpper(s)
		}
		return s
	}
	values := map[string]string{}
	for _, entry := range os.Environ() {
		if at := strings.IndexByte(entry, '='); at > 0 {
			values[key(entry[:at])] = entry
		}
	}
	for _, name := range removals {
		delete(values, key(name))
	}
	for name, value := range overrides {
		values[key(name)] = name + "=" + value
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, values[k])
	}
	return out
}

// mergeEnvironment overlays additions onto a fully materialized environment.
// Windows environment variable names are case-insensitive. The result is sorted
// so that process launches remain deterministic, and empty values are retained
// as NAME= entries rather than being dropped.
func mergeEnvironment(base, additions []string) []string {
	key := func(s string) string {
		if runtime.GOOS == "windows" {
			return strings.ToUpper(s)
		}
		return s
	}
	values := make(map[string]string, len(base)+len(additions))
	for _, entry := range append(append([]string(nil), base...), additions...) {
		if at := strings.IndexByte(entry, '='); at > 0 {
			values[key(entry[:at])] = entry
		}
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, values[k])
	}
	return out
}

type streamWriter struct {
	mutex     *sync.Mutex
	stream    string
	callback  func(StreamEvent)
	buffer    bytes.Buffer
	pending   []byte
	skipLF    bool
	limit     int
	truncated bool
	onLimit   func()
	retired   bool
}

func (w *streamWriter) Write(p []byte) (int, error) {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	originalLength := len(p)
	if w.retired {
		return originalLength, nil
	}
	if w.limit > 0 && len(p) > w.limit-w.buffer.Len() {
		p = p[:max(0, w.limit-w.buffer.Len())]
		if !w.truncated {
			w.truncated = true
			if w.onLimit != nil {
				w.onLimit()
			}
		}
	}
	_, _ = w.buffer.Write(p)
	if w.callback != nil {
		w.pending = append(w.pending, p...)
		for len(w.pending) > 0 {
			if w.skipLF {
				w.skipLF = false
				if w.pending[0] == '\n' {
					w.pending = w.pending[1:]
					continue
				}
			}
			at := bytes.IndexAny(w.pending, "\r\n")
			if at < 0 {
				break
			}
			// Emit CR immediately; a following LF belongs to the same line,
			// even when it arrives in the next pipe read.
			w.skipLF = w.pending[at] == '\r'
			w.callback(StreamEvent{w.stream, strings.ToValidUTF8(string(w.pending[:at]), "�")})
			w.pending = w.pending[at+1:]
		}
	}
	return originalLength, nil
}
func (w *streamWriter) Flush() {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	if len(w.pending) > 0 && w.callback != nil {
		w.callback(StreamEvent{w.stream, strings.ToValidUTF8(string(w.pending), "�")})
		w.pending = nil
	}
}
func (w *streamWriter) stopEvents() {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	w.retired = true
	w.callback, w.pending = nil, nil
}

func (w *streamWriter) snapshot() (string, bool) {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	return strings.ToValidUTF8(w.buffer.String(), "�"), w.truncated
}

func (w *streamWriter) String() string {
	value, _ := w.snapshot()
	return value
}

var _ io.Writer = (*streamWriter)(nil)
