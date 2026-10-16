//go:build windows

package d3d11

import "unsafe"

var (
	procGetWindowThreadProcessID = user32.NewProc("GetWindowThreadProcessId")
	procGetGUIThreadInfo         = user32.NewProc("GetGUIThreadInfo")
	procGetAncestor              = user32.NewProc("GetAncestor")
)

// GUITHREADINFO layout and GUI_INMOVESIZE are documented in winuser.h:
// https://learn.microsoft.com/windows/win32/api/winuser/ns-winuser-guithreadinfo
type resizeThreadInfo struct {
	size, flags                                        uint32
	active, focus, capture, menuOwner, moveSize, caret uintptr
	caretRect                                          [4]int32
}

func windowInResizeLoop(hwnd uintptr) bool {
	thread, _, _ := procGetWindowThreadProcessID.Call(hwnd, 0)
	if thread == 0 {
		return false
	}
	var info resizeThreadInfo
	info.size = uint32(unsafe.Sizeof(info))
	ok, _, _ := procGetGUIThreadInfo.Call(thread, uintptr(unsafe.Pointer(&info)))
	if ok == 0 || info.flags&0x00000002 == 0 || info.moveSize == 0 {
		return false
	}
	root, _, _ := procGetAncestor.Call(hwnd, 2) // GA_ROOT: the surface is a child.
	return info.moveSize == root
}
