//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package documents

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"silverwing-go/internal/executable"
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
	switch runtime.GOOS {
	case "darwin":
		command = exec.CommandContext(ctx, executable.SystemCommand("osascript"), "-e", `on run argv
tell application "Finder" to delete POSIX file (item 1 of argv)
end run`, absolute)
	case "linux", "freebsd", "openbsd", "netbsd", "dragonfly", "illumos", "solaris", "aix", "android", "ios":
		if executable, lookupErr := exec.LookPath("gio"); lookupErr == nil {
			command = exec.CommandContext(ctx, executable, "trash", "--", absolute)
		} else if executable, lookupErr = exec.LookPath("trash-put"); lookupErr == nil {
			command = exec.CommandContext(ctx, executable, absolute)
		} else {
			return fmt.Errorf("系统没有可用的回收站命令（需要 gio 或 trash-put）")
		}
	default:
		return fmt.Errorf("当前系统不支持回收站操作")
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
