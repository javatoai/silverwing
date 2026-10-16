//go:build darwin || linux

package media

import (
	"fmt"
	"syscall"
	"unsafe"
)

func makeNativeInput(data []byte) (nativeInput, error) {
	if len(data) == 0 {
		return nativeInput{}, fmt.Errorf("PDF 文件为空")
	}
	mapped, err := syscall.Mmap(-1, 0, len(data), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
	if err != nil {
		return nativeInput{}, fmt.Errorf("无法为 PDFium 分配稳定的输入缓冲区：%w", err)
	}
	copy(mapped, data)
	return nativeInput{
		bytes: mapped,
		ptr:   unsafe.Pointer(&mapped[0]),
		free:  func() error { return syscall.Munmap(mapped) },
	}, nil
}
