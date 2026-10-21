//go:build windows

package documents

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func openMaterialFileNoFollow(path string, expected os.FileInfo) (*os.File, error) {
	name, err := syscall.UTF16PtrFromString(materialReadWindowsPath(path))
	if err != nil {
		return nil, err
	}
	handle, err := syscall.CreateFile(
		name,
		syscall.GENERIC_READ,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil,
		syscall.OPEN_EXISTING,
		syscall.FILE_FLAG_OPEN_REPARSE_POINT|syscall.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), path)
	var opened syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(handle, &opened); err != nil {
		_ = file.Close()
		return nil, err
	}
	if opened.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		_ = file.Close()
		return nil, errors.New("不能读取符号链接或 reparse point 资料文件")
	}
	if opened.FileAttributes&syscall.FILE_ATTRIBUTE_DIRECTORY != 0 {
		_ = file.Close()
		return nil, errors.New("只能读取普通文件")
	}
	if err := validateOpenedMaterialFile(file, expected); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

// os.OpenFile adds the extended-length prefix on Windows when needed. This
// direct CreateFileW path must do the same because syscall bypasses os.fixLongPath.
func materialReadWindowsPath(path string) string {
	if len(path) < 248 || !filepath.IsAbs(path) {
		return path
	}
	if strings.HasPrefix(path, `\\?\`) || strings.HasPrefix(path, `\??\`) || strings.HasPrefix(path, `\\.\`) {
		return path
	}
	if strings.HasPrefix(path, `\\`) {
		return `\\?\UNC\` + path[2:]
	}
	return `\\?\` + path
}
