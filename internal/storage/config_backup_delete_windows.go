//go:build windows

package storage

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

func configBackupReparse(info os.FileInfo) bool {
	if info.Mode()&os.ModeSymlink != 0 {
		return true
	}
	attributes, ok := info.Sys().(*syscall.Win32FileAttributeData)
	return ok && attributes.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0
}

var configBackupNTCreate = syscall.NewLazyDLL("ntdll.dll").NewProc("NtCreateFile")
var configBackupNTError = syscall.NewLazyDLL("ntdll.dll").NewProc("RtlNtStatusToDosError")
var configBackupDisposition = syscall.NewLazyDLL("kernel32.dll").NewProc("SetFileInformationByHandle")

// Open relative to the pinned directory, without following reparse points, and
// delete the verified handle. Renaming/replacing a leaf cannot change the target.
func removeListedConfigBackup(root *os.Root, name string, expected os.FileInfo) error {
	parent, err := root.Open(".")
	if err != nil {
		return err
	}
	defer parent.Close()
	characters, err := syscall.UTF16FromString(name)
	if err != nil {
		return err
	}
	unicodeName := struct {
		Length, MaximumLength uint16
		Buffer                *uint16
	}{uint16((len(characters) - 1) * 2), uint16(len(characters) * 2), &characters[0]}
	attributes := struct {
		Length                                       uint32
		RootDirectory                                syscall.Handle
		ObjectName                                   unsafe.Pointer
		Attributes                                   uint32
		SecurityDescriptor, SecurityQualityOfService unsafe.Pointer
	}{}
	attributes.Length, attributes.RootDirectory, attributes.ObjectName = uint32(unsafe.Sizeof(attributes)), syscall.Handle(parent.Fd()), unsafe.Pointer(&unicodeName)
	statusBlock := struct{ Status, Information uintptr }{}
	var handle syscall.Handle
	status, _, _ := configBackupNTCreate.Call(uintptr(unsafe.Pointer(&handle)), 0x100000|0x10000|0x80,
		uintptr(unsafe.Pointer(&attributes)), uintptr(unsafe.Pointer(&statusBlock)), 0, 0,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, 1, 0x20|0x40|0x200000, 0, 0)
	if int32(status) < 0 {
		code, _, _ := configBackupNTError.Call(status)
		return syscall.Errno(code)
	}
	file := os.NewFile(uintptr(handle), name)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !sameConfigBackup(expected, info) {
		return errors.New("备份身份已变化")
	}
	// FILE_DISPOSITION_INFO contains one BOOLEAN; class 4 marks this handle only.
	remove := byte(1)
	ok, _, callErr := configBackupDisposition.Call(uintptr(handle), 4, uintptr(unsafe.Pointer(&remove)), 1)
	if ok == 0 {
		return callErr
	}
	return nil
}
