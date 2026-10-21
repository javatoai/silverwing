//go:build !windows

package process

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

type processCleanup struct{}

func platformCommand(ctx context.Context, executable string, arguments []string) *exec.Cmd {
	return exec.CommandContext(ctx, executable, arguments...)
}
func prepareProcess(cmd *exec.Cmd) (*processCleanup, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return &processCleanup{}, nil
}
func (p *processCleanup) Attach(cmd *exec.Cmd, ctx context.Context) error { return nil }
func (p *processCleanup) Kill(cmd *exec.Cmd) error {
	if cmd.Process != nil {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
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
	deadline := time.Now().Add(processCleanupTimeout)
	for {
		err := syscall.Kill(-cmd.Process.Pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		if err != nil {
			return err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("%s 内未能确认命令进程组退出", processCleanupTimeout)
		}
		time.Sleep(min(remaining, processCleanupPoll))
	}
}

func (p *processCleanup) Close() error { return nil }
