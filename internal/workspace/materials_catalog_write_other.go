//go:build !windows

package workspace

import (
	"os"
	"syscall"
)

func openMaterialsCatalogForUpdate(path string) (*os.File, func(), error) {
	file, err := os.OpenFile(path, os.O_RDWR, 0644)
	if err != nil {
		return nil, nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, nil, err
	}
	return file, func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}, nil
}

func materialsCatalogLinkCount(file *os.File) (uint64, error) {
	var info syscall.Stat_t
	if err := syscall.Fstat(int(file.Fd()), &info); err != nil {
		return 0, err
	}
	return uint64(info.Nlink), nil
}
