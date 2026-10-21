//go:build !windows

package platform

import "os"

func applicationLogReparse(info os.FileInfo) bool { return info.Mode()&os.ModeSymlink != 0 }
