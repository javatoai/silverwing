//go:build !windows

package documents

import (
	"fmt"
	"os"
	"syscall"
)

func taskFilesFileIdentity(file *os.File) (string, error) {
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return fmt.Sprintf("%x:%x", stat.Dev, stat.Ino), nil
	}
	return "", nil
}
