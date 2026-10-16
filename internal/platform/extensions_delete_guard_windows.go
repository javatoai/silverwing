//go:build windows

package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func ensureSafeDeleteAncestors(root, target string) error {
	if err := ensureNoSymlinksBeforeLeaf(root, target); err != nil {
		return err
	}
	// Protect ancestors above the controlled root too: the root itself can
	// otherwise be reached through a junction. The leaf keeps its safe unlink
	// contract and is deliberately not rejected here.
	for ancestor := filepath.Dir(target); ; {
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return nil // FindFirstFile does not inspect drive/share roots.
		}
		redirected, err := deleteAncestorRedirected(ancestor)
		if err != nil {
			return fmt.Errorf("检查删除路径祖先失败：%s: %w", ancestor, err)
		}
		if redirected {
			return fmt.Errorf("删除路径祖先不能是重定向链接：%s", ancestor)
		}
		ancestor = parent
	}
}

func deleteAncestorRedirected(path string) (bool, error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return false, err
	}
	var data syscall.Win32finddata
	handle, err := syscall.FindFirstFile(name, &data)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := syscall.FindClose(handle); err != nil {
		return false, err
	}
	return deleteRedirectTag(data.FileAttributes, data.Reserved0), nil
}

func deleteRedirectTag(attributes, tag uint32) bool {
	const nameSurrogate = 0x20000000
	return attributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 && tag&nameSurrogate != 0
}
