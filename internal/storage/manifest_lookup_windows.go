//go:build windows

package storage

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func manifestPathReparse(info os.FileInfo) bool {
	if info.Mode()&os.ModeSymlink != 0 {
		return true
	}
	attributes, ok := info.Sys().(*syscall.Win32FileAttributeData)
	return ok && attributes.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0
}

// Query only the leaf's real name. EvalSymlinks treats Windows junctions as
// ModeIrregular and rejects them while walking otherwise valid root ancestors.
// Root/task/file handle identity and reparse checks remain owned by LoadTask.
func manifestTaskDirectoryName(path string) (string, error) {
	query, err := manifestLookupWindowsPath(path)
	if err != nil {
		return "", err
	}
	name, err := syscall.UTF16PtrFromString(query)
	if err != nil {
		return "", err
	}
	var data syscall.Win32finddata
	handle, err := syscall.FindFirstFile(name, &data)
	if err != nil {
		return "", &os.PathError{Op: "FindFirstFile", Path: path, Err: err}
	}
	defer syscall.FindClose(handle)
	return syscall.UTF16ToString(data.FileName[:]), nil
}

// FindFirstFile bypasses os.fixLongPath. Preserve short-path Win32 aliases and
// add the same extended drive/UNC form for long absolute or relative paths.
func manifestLookupWindowsPath(path string) (string, error) {
	path = filepath.FromSlash(path)
	if strings.HasPrefix(path, `\\?\`) || strings.HasPrefix(path, `\??\`) || strings.HasPrefix(path, `\\.\`) {
		return path, nil
	}
	full, err := syscall.FullPath(path)
	if err != nil {
		return "", err
	}
	if len(full) < 248 {
		return path, nil
	}
	if strings.HasPrefix(full, `\\`) {
		return `\\?\UNC\` + full[2:], nil
	}
	return `\\?\` + full, nil
}
