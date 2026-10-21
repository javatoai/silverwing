//go:build windows

package documents

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	identityKernel             = syscall.NewLazyDLL("kernel32.dll")
	createFileForIdentity      = identityKernel.NewProc("CreateFileW")
	getFileInformationByHandle = identityKernel.NewProc("GetFileInformationByHandle")
	closeIdentityHandle        = identityKernel.NewProc("CloseHandle")
)

type byHandleFileInformation struct {
	FileAttributes     uint32
	CreationTimeLow    uint32
	CreationTimeHigh   uint32
	LastAccessTimeLow  uint32
	LastAccessTimeHigh uint32
	LastWriteTimeLow   uint32
	LastWriteTimeHigh  uint32
	VolumeSerialNumber uint32
	FileSizeHigh       uint32
	FileSizeLow        uint32
	NumberOfLinks      uint32
	FileIndexHigh      uint32
	FileIndexLow       uint32
}

func fileIdentityKey(path string) string {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return ""
	}
	handle, _, _ := createFileForIdentity.Call(uintptr(unsafe.Pointer(name)), 0x80, 0x1|0x2|0x4, 0, 3, 0x02000000, 0)
	if handle == ^uintptr(0) {
		return ""
	}
	defer closeIdentityHandle.Call(handle)
	var info byHandleFileInformation
	result, _, _ := getFileInformationByHandle.Call(handle, uintptr(unsafe.Pointer(&info)))
	if result == 0 {
		return ""
	}
	return fmt.Sprintf("%08x:%08x%08x", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow)
}
