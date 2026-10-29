//go:build !windows

package storage

import (
	"os"
	"path/filepath"
)

func manifestPathReparse(info os.FileInfo) bool { return info.Mode()&os.ModeSymlink != 0 }

func manifestTaskDirectoryName(path string) (string, error) {
	physical, err := filepath.EvalSymlinks(path)
	return filepath.Base(physical), err
}
