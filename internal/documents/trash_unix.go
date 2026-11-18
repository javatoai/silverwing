//go:build aix || android || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package documents

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

func newPlatformTrashBackend() trashBackend { return unixTrashBackend{} }

type unixTrashBackend struct{}

func (unixTrashBackend) moveToTrash(ctx context.Context, path string) error {
	absolute, err := canonicalTrashPath(path)
	if err != nil {
		return err
	}
	var command *exec.Cmd
	if executable, lookupErr := exec.LookPath("gio"); lookupErr == nil {
		command = exec.CommandContext(ctx, executable, "trash", "--", absolute)
	} else if executable, lookupErr = exec.LookPath("trash-put"); lookupErr == nil {
		command = exec.CommandContext(ctx, executable, absolute)
	} else {
		return fmt.Errorf("系统没有可用的回收站命令（需要 gio 或 trash-put）")
	}
	output, err := command.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		message := strings.TrimSpace(string(output))
		if message == "" {
			return err
		}
		return fmt.Errorf("%w：%s", err, message)
	}
	return nil
}
