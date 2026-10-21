//go:build !windows

package integrations

import "os"

func requirementCacheReparsePoint(_ string, info os.FileInfo) bool {
	return info.Mode()&os.ModeSymlink != 0
}
