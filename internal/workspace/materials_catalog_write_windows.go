//go:build windows

package workspace

import (
	"os"
	"syscall"
)

func openMaterialsCatalogForUpdate(path string) (*os.File, func(), error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, nil, err
	}
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ|syscall.GENERIC_WRITE, syscall.FILE_SHARE_READ,
		nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL|syscall.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, nil, err
	}
	file := os.NewFile(uintptr(handle), path)
	return file, func() { _ = file.Close() }, nil
}

func materialsCatalogLinkCount(file *os.File) (uint64, error) {
	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(file.Fd()), &info); err != nil {
		return 0, err
	}
	return uint64(info.NumberOfLinks), nil
}
