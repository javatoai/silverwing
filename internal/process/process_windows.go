//go:build windows

package process

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

var kernel32 = syscall.NewLazyDLL("kernel32.dll")
var createJob = kernel32.NewProc("CreateJobObjectW")
var setJob = kernel32.NewProc("SetInformationJobObject")
var assignJob = kernel32.NewProc("AssignProcessToJobObject")
var terminateJob = kernel32.NewProc("TerminateJobObject")
var queryJob = kernel32.NewProc("QueryInformationJobObject")
var firstThread = kernel32.NewProc("Thread32First")
var nextThread = kernel32.NewProc("Thread32Next")
var openThread = kernel32.NewProc("OpenThread")
var threadProcessID = kernel32.NewProc("GetProcessIdOfThread")
var resumeThread = kernel32.NewProc("ResumeThread")

type jobLimits struct {
	PerProcessUserTimeLimit, PerJobUserTimeLimit int64
	LimitFlags                                   uint32
	MinimumWorkingSetSize, MaximumWorkingSetSize uintptr
	ActiveProcessLimit                           uint32
	Affinity                                     uintptr
	PriorityClass, SchedulingClass               uint32
}
type jobExtendedLimits struct {
	BasicLimitInformation                                                        jobLimits
	IOCounters                                                                   [6]uint64
	ProcessMemoryLimit, JobMemoryLimit, PeakProcessMemoryUsed, PeakJobMemoryUsed uintptr
}
type jobBasicAccounting struct {
	TotalUserTime, TotalKernelTime, ThisPeriodTotalUserTime, ThisPeriodTotalKernelTime int64
	TotalPageFaultCount, TotalProcesses, ActiveProcesses, TotalTerminatedProcesses     uint32
}
type processCleanup struct {
	handle, rootHandle   syscall.Handle
	attached             atomic.Bool
	members              []jobMemberHandle
	cleanupDeadline      time.Time
	terminationRequested bool
	killResult           error
	baselineAttempted    bool
	jobTotalBaseline     *uint32
}

type startupThreadEntry struct {
	Size, Usage, ThreadID, OwnerPID uint32
	BasePriority, DeltaPriority     int32
	Flags                           uint32
}

func platformCommand(ctx context.Context, executable string, arguments []string) *exec.Cmd {
	if strings.EqualFold(filepath.Ext(executable), ".ps1") {
		// Keep the script path and caller arguments as separate CreateProcess
		// arguments. PowerShell receives only fixed invocation flags as command
		// syntax, so paths and arguments never become shell source.
		powershellArguments := make([]string, 0, 4+len(arguments))
		powershellArguments = append(powershellArguments, "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", executable)
		powershellArguments = append(powershellArguments, arguments...)
		return exec.CommandContext(ctx, "powershell.exe", powershellArguments...)
	}
	if strings.EqualFold(filepath.Ext(executable), ".cmd") || strings.EqualFold(filepath.Ext(executable), ".bat") {
		cmd := exec.CommandContext(ctx, "cmd.exe")
		environment, parts, expandedParts, err := batchCommand(executable, arguments)
		if err != nil {
			cmd.Err = err
			return cmd
		}
		prefix := syscall.EscapeArg(cmd.Path) + ` /d /v:off /s /c "`
		cmdLine := prefix + strings.Join(parts, " ") + `"`
		expandedCmdLine := prefix + strings.Join(expandedParts, " ") + `"`
		if len(utf16.Encode([]rune(cmdLine))) > 8191 || len(utf16.Encode([]rune(expandedCmdLine))) > 8191 {
			cmd.Err = fmt.Errorf("Windows batch command exceeds cmd.exe's 8191 UTF-16-unit limit")
			return cmd
		}
		cmd.SysProcAttr = &syscall.SysProcAttr{
			HideWindow: true,
			CmdLine:    cmdLine,
		}
		// These are private to this child process. Runner overlays them on Spec.Env
		// after removals/overrides; detached commands use the current environment.
		cmd.Env = environment
		return cmd
	}
	return exec.CommandContext(ctx, executable, arguments...)
}

const (
	batchExecutableEnv = "SILVERWING_BATCH_EXECUTABLE"
	batchArgumentEnv   = "SILVERWING_BATCH_ARGUMENT_"
)

// batchCommand keeps argument data out of cmd.exe's source text. cmd expands only
// the fixed environment-variable names, with values protected by the quotes in
// parts. Embedded quotes and line breaks cannot be represented safely by cmd's
// batch argument grammar, so they fail before a process is started.
func batchCommand(executable string, arguments []string) ([]string, []string, []string, error) {
	if strings.ContainsAny(executable, "\x00\r\n\"") {
		return nil, nil, nil, fmt.Errorf("Windows batch executable path contains an unsupported control character or quote")
	}
	environment := []string{batchExecutableEnv + "=" + executable}
	parts := []string{`"%` + batchExecutableEnv + `%"`}
	expandedParts := []string{`"` + executable + `"`}
	for i, arg := range arguments {
		if strings.ContainsAny(arg, "\x00\r\n\"") {
			return nil, nil, nil, fmt.Errorf("Windows batch argument %d contains an unsupported control character or embedded quote; use a native executable", i)
		}
		if arg == "" {
			// cmd.exe does not reliably preserve an empty environment variable as
			// an argument (it can leave the %NAME% token unexpanded). An empty
			// quoted argument contains no caller-controlled syntax and survives
			// the batch %* forwarding path.
			parts = append(parts, `""`)
			expandedParts = append(expandedParts, `""`)
			continue
		}
		// cmd.exe preserves backslashes inside quotes. The batch wrapper commonly
		// forwards %* to a native program, whose CRT treats backslashes before the
		// closing quote as escapes. Double only the trailing run so the CRT decodes
		// it back to the exact argument.
		value := arg
		trailing := 0
		for trailing < len(value) && value[len(value)-1-trailing] == '\\' {
			trailing++
		}
		if trailing > 0 {
			value += strings.Repeat(`\`, trailing)
		}
		name := batchArgumentEnv + strconv.Itoa(i)
		environment = append(environment, name+"="+value)
		parts = append(parts, `"%`+name+`%"`)
		expandedParts = append(expandedParts, `"`+value+`"`)
	}
	return environment, parts, expandedParts, nil
}

func prepareProcess(cmd *exec.Cmd) (*processCleanup, error) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= 0x08000000 | 0x00000200 | 0x00000004 // CREATE_SUSPENDED
	h, _, e := createJob.Call(0, 0)
	if h == 0 {
		return nil, fmt.Errorf("CreateJobObject: %w", e)
	}
	job := &processCleanup{handle: syscall.Handle(h)}
	limits := jobExtendedLimits{}
	limits.BasicLimitInformation.LimitFlags = 0x00002000
	ok, _, e := setJob.Call(h, 9, uintptr(unsafe.Pointer(&limits)), unsafe.Sizeof(limits))
	if ok == 0 {
		return nil, cleanupResultError(fmt.Errorf("SetInformationJobObject: %w", e),
			cleanupPhaseError("关闭未启动的进程生命周期", job.Close()))
	}
	return job, nil
}
func (p *processCleanup) Attach(cmd *exec.Cmd, ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.handle == 0 {
		return &CleanupError{Phase: "建立生命周期", Cause: errors.New("Job 句柄已关闭")}
	}
	h, e := syscall.OpenProcess(0x0001|0x0100|syscall.SYNCHRONIZE, false, uint32(cmd.Process.Pid))
	if e != nil {
		return e
	}
	p.rootHandle = h
	ok, _, e := assignJob.Call(uintptr(p.handle), uintptr(h))
	if ok == 0 {
		return e
	}
	p.attached.Store(true)
	return resumeStartupThread(ctx, uint32(cmd.Process.Pid))
}

func resumeStartupThread(ctx context.Context, pid uint32) (err error) {
	snapshot, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer func() {
		err = cleanupResultError(err, cleanupPhaseError("关闭启动线程快照", syscall.CloseHandle(snapshot)))
	}()
	entry := startupThreadEntry{Size: uint32(unsafe.Sizeof(startupThreadEntry{}))}
	ok, _, callErr := firstThread.Call(uintptr(snapshot), uintptr(unsafe.Pointer(&entry)))
	var candidate, count uint32
	for ok != 0 {
		if entry.Size < 16 {
			return errors.New("启动线程快照缺少身份字段")
		}
		if entry.OwnerPID == pid {
			candidate, count = entry.ThreadID, count+1
		}
		entry.Size = uint32(unsafe.Sizeof(entry))
		ok, _, callErr = nextThread.Call(uintptr(snapshot), uintptr(unsafe.Pointer(&entry)))
	}
	if !errors.Is(callErr, syscall.ERROR_NO_MORE_FILES) {
		return fmt.Errorf("枚举启动线程：%w", callErr)
	}
	if count != 1 {
		return fmt.Errorf("无法唯一确认挂起的启动线程（%d 个候选）", count)
	}
	thread, _, callErr := openThread.Call(0x0002|0x0800, 0, uintptr(candidate))
	if thread == 0 {
		return fmt.Errorf("OpenThread: %w", callErr)
	}
	defer func() {
		err = cleanupResultError(err, cleanupPhaseError("关闭启动线程句柄", syscall.CloseHandle(syscall.Handle(thread))))
	}()
	owner, _, callErr := threadProcessID.Call(thread)
	if uint32(owner) != pid {
		return fmt.Errorf("启动线程归属不匹配（PID %d）：%w", owner, callErr)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	previous, _, callErr := resumeThread.Call(thread)
	if uint32(previous) == 0xffffffff {
		return fmt.Errorf("ResumeThread: %w", callErr)
	}
	if previous != 1 {
		return fmt.Errorf("启动线程挂起计数不符合预期：%d", previous)
	}
	return nil
}
func (p *processCleanup) Kill(cmd *exec.Cmd) error {
	if p.cleanupDeadline.IsZero() {
		p.cleanupDeadline = time.Now().Add(processCleanupTimeout)
	}
	if p.attached.Load() {
		if p.handle == 0 {
			return os.ErrProcessDone
		}
		if p.terminationRequested {
			return p.killResult
		}
		// ActiveProcesses may become zero before process handles are signaled.
		// Retain verified members before requesting private-Job termination.
		baselineErr := p.captureJobBaseline()
		captureErr := p.captureJobMembers(p.cleanupDeadline)
		ok, _, e := terminateJob.Call(uintptr(p.handle), 1)
		var terminateErr error
		if ok == 0 {
			terminateErr = e
		} else {
			p.terminationRequested = true
		}
		p.killResult = cleanupResultError(p.killResult,
			cleanupPhaseError("读取终止前 Job 累计进程数", baselineErr),
			cleanupPhaseError("捕获进程树同步句柄", captureErr),
			cleanupPhaseError("终止私有 Job", terminateErr))
		return p.killResult
	}
	if cmd.Process != nil {
		err := cmd.Process.Kill()
		if errors.Is(err, os.ErrProcessDone) {
			return nil
		}
		return err
	}
	return nil
}
func (p *processCleanup) Wait(cmd *exec.Cmd, rootExited bool) error {
	if cmd.Process == nil {
		return nil
	}
	deadline := p.cleanupDeadline
	if deadline.IsZero() {
		deadline = time.Now().Add(processCleanupTimeout)
	}
	if !p.attached.Load() {
		// The primary thread has not been resumed before assignment; therefore
		// this failed startup could not have created command descendants.
		if rootExited {
			return nil
		}
		if p.rootHandle == 0 {
			return errors.New("无法确认挂起的主进程退出")
		}
		return waitProcessHandle(p.rootHandle, deadline, "挂起的主进程")
	}
	if p.handle == 0 {
		return errors.New("Job 句柄已关闭，无法确认进程树退出")
	}
	if err := p.waitJobMembers(deadline); err != nil {
		return err
	}
	for {
		var accounting jobBasicAccounting
		ok, _, e := queryJob.Call(uintptr(p.handle), 1, uintptr(unsafe.Pointer(&accounting)), unsafe.Sizeof(accounting), 0)
		if ok == 0 {
			return fmt.Errorf("QueryInformationJobObject: %w", e)
		}
		if accounting.ActiveProcesses == 0 {
			return confirmJobCohort(p.jobTotalBaseline, accounting.TotalProcesses)
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("%s 内未能确认 %d 个 Job 进程退出", processCleanupTimeout, accounting.ActiveProcesses)
		}
		time.Sleep(min(remaining, processCleanupPoll))
	}
}

func (p *processCleanup) Close() error {
	var closeErrors []error
	if err := p.closeJobMembers(); err != nil {
		closeErrors = append(closeErrors, err)
	}
	if p.rootHandle != 0 {
		if err := syscall.CloseHandle(p.rootHandle); err != nil {
			closeErrors = append(closeErrors, cleanupPhaseError("关闭主进程句柄", err))
		} else {
			p.rootHandle = 0
		}
	}
	if p.handle != 0 {
		if err := syscall.CloseHandle(p.handle); err != nil {
			closeErrors = append(closeErrors, cleanupPhaseError("关闭 Job 句柄", err))
		} else {
			p.handle = 0
		}
	}
	return errors.Join(closeErrors...)
}
