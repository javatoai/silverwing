package auth

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

func createPrivateTemp(directory string) (*os.File, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	// P protects the DACL from parent inheritance. Each allow entry applies
	// only to this file. CreateFile assigns it before the file can be opened.
	descriptor, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + user.User.Sid.String() + ")(A;;FA;;;SY)")
	if err != nil {
		return nil, err
	}
	attributes := windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: descriptor,
	}
	path, err := filepath.Abs(filepath.Join(directory, ".snowball-auth-"+rand.Text()+".tmp"))
	if err != nil {
		return nil, err
	}
	name, err := windows.UTF16PtrFromString(authWindowsPath(path))
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_WRITE, 0, &attributes, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), path)
	if err := checkPrivateDACL(handle); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return nil, err
	}
	return file, nil
}

func checkPrivateDACL(handle windows.Handle) error {
	var flags uint32
	err := windows.GetVolumeInformationByHandle(handle, nil, 0, nil, nil, &flags, nil, 0)
	if err == nil && flags&windows.FILE_PERSISTENT_ACLS == 0 {
		return windows.ERROR_NOT_SUPPORTED
	}
	return err
}

// The direct Windows call bypasses the standard library's long-path handling.
// Preserve the ordinary path for Go filesystem operations.
func authWindowsPath(path string) string {
	if len(path) < 248 || strings.HasPrefix(path, `\\?\`) || strings.HasPrefix(path, `\??\`) || strings.HasPrefix(path, `\\.\`) {
		return path
	}
	if strings.HasPrefix(path, `\\`) {
		return `\\?\UNC\` + path[2:]
	}
	return `\\?\` + path
}
