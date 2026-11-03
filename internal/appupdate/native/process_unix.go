//go:build !windows

package native

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

func hide(cmd *exec.Cmd) {}

func WaitParent(ctx context.Context, pid int) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func Lock(root string) (func(), error) {
	digest := sha256.Sum256([]byte(root))
	path := filepath.Join(filepath.Dir(root), fmt.Sprintf(".silverwing-update-%x.lock", digest[:16]))
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, errors.New("另一个更新正在执行")
	}
	return func() { syscall.Flock(int(file.Fd()), syscall.LOCK_UN); file.Close() }, nil
}

func OtherInstances(executable string) error {
	// pgrep's exact match avoids treating updater helpers and shell commands as
	// another copy. macOS is the only supported non-Windows installation.
	output, err := exec.Command("/usr/bin/pgrep", "-x", filepath.Base(executable)).Output()
	if err == nil && len(output) > 0 {
		return errors.New("请先关闭同一程序的其他实例")
	}
	if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 {
		return nil
	}
	return err
}
