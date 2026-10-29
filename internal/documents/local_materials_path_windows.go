//go:build windows

package documents

import (
	"errors"
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

func openLocalMaterialsDirectory(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(materialReadWindowsPath(path))
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		_ = windows.CloseHandle(handle)
		return nil, err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		_ = windows.CloseHandle(handle)
		return nil, errors.New("本地资料目录句柄包含链接或不是目录")
	}
	return os.NewFile(uintptr(handle), path), nil
}

// EvalSymlinks does not traverse junctions with current Go's ModeIrregular
// classification. Resolve the real task by its directory handle instead. The
// final task itself is opened without following reparse points; only its
// configured ancestors may redirect to a different real directory.
func canonicalLocalTaskPath(path string) (string, error) {
	name, err := windows.UTF16PtrFromString(materialReadWindowsPath(path))
	if err != nil {
		return "", err
	}
	handle, err := windows.CreateFile(name, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return "", err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return "", errors.New("任务目录句柄不是安全的本地目录")
	}
	buffer := make([]uint16, 512)
	for {
		count, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), 0)
		if err != nil {
			return "", err
		}
		if count >= uint32(len(buffer)) {
			buffer = make([]uint16, count+1)
			continue
		}
		resolved := windows.UTF16ToString(buffer[:count])
		if strings.HasPrefix(resolved, `\\?\UNC\`) {
			return `\\` + resolved[len(`\\?\UNC\`):], nil
		}
		if len(resolved) >= 6 && strings.HasPrefix(resolved, `\\?\`) && resolved[5] == ':' {
			return resolved[4:], nil
		}
		return resolved, nil
	}
}
