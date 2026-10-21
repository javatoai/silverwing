//go:build !windows

package workspace

import "os"

func bootstrapPathIsReparsePoint(_ string, info os.FileInfo) (bool, error) {
	return info.Mode()&os.ModeSymlink != 0 || (!info.Mode().IsDir() && !info.Mode().IsRegular()), nil
}
