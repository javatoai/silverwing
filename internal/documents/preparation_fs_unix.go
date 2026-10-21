//go:build !windows

package documents

import "os"

func isUnsafeDirectory(info os.FileInfo, _ string) bool {
	return info.Mode()&os.ModeSymlink != 0
}
