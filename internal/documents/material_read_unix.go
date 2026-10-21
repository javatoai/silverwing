//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package documents

import (
	"os"
	"syscall"
)

func openMaterialFileNoFollow(path string, expected os.FileInfo) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if err := validateOpenedMaterialFile(file, expected); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}
