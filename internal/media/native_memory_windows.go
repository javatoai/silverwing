//go:build windows

package media

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	kernel32VirtualAlloc = syscall.NewLazyDLL("kernel32.dll").NewProc("VirtualAlloc")
	kernel32VirtualFree  = syscall.NewLazyDLL("kernel32.dll").NewProc("VirtualFree")
)

func makeNativeInput(data []byte) (nativeInput, error) {
	if len(data) == 0 {
		return nativeInput{}, fmt.Errorf("PDF 文件为空")
	}
	address, _, callErr := kernel32VirtualAlloc.Call(0, uintptr(len(data)), 0x3000, 0x04)
	if address == 0 {
		return nativeInput{}, fmt.Errorf("无法为 PDFium 分配稳定的输入缓冲区：%w", callErr)
	}
	buffer := unsafe.Slice((*byte)(unsafe.Pointer(address)), len(data))
	copy(buffer, data)
	return nativeInput{
		bytes: buffer,
		ptr:   unsafe.Pointer(address),
		free: func() error {
			result, _, err := kernel32VirtualFree.Call(address, 0, 0x8000)
			if result == 0 {
				return fmt.Errorf("释放 PDFium 输入缓冲区失败：%w", err)
			}
			return nil
		},
	}, nil
}
