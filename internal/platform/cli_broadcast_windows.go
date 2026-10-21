//go:build windows

package platform

import (
	"syscall"
	"unsafe"
)

func broadcastEnvironmentChange() {
	message, err := syscall.UTF16PtrFromString("Environment")
	if err != nil {
		return
	}
	user32 := syscall.NewLazyDLL("user32.dll")
	sendMessageTimeout := user32.NewProc("SendMessageTimeoutW")
	var result uintptr
	_, _, _ = sendMessageTimeout.Call(
		uintptr(0xffff),
		uintptr(0x001a),
		0,
		uintptr(unsafe.Pointer(message)),
		uintptr(0x0002),
		uintptr(5000),
		uintptr(unsafe.Pointer(&result)),
	)
}
