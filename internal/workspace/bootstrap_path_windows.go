//go:build windows

package workspace

import (
	"fmt"
	"os"
	"syscall"
)

func bootstrapPathIsReparsePoint(path string, info os.FileInfo) (bool, error) {
	if info.Mode()&os.ModeSymlink != 0 || (!info.Mode().IsDir() && !info.Mode().IsRegular()) {
		return true, nil
	}
	if attributes, ok := info.Sys().(*syscall.Win32FileAttributeData); ok && attributes.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return true, nil
	}
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return false, err
	}
	attributes, err := syscall.GetFileAttributes(name)
	if err != nil {
		return false, fmt.Errorf("读取 Windows 文件属性：%w", err)
	}
	return attributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0, nil
}
