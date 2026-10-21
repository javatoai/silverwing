//go:build windows

package windows

import (
	"log"
	"os"
	"time"
	"unsafe"
)

var resizeTraceEnabled = os.Getenv("MYGO_RESIZE_TRACE") == "1"

func traceSurfaceResize(s *surface) {
	if !resizeTraceEnabled {
		return
	}
	var parent, child rect
	procGetClientRect.Call(s.w.hwnd, uintptr(unsafe.Pointer(&parent)))
	procGetClientRect.Call(s.hwnd, uintptr(unsafe.Pointer(&child)))
	log.Printf("mygo-resize size at=%d hwnd=%x parent=%dx%d child=%dx%d dpi=%d", time.Now().UnixMicro(), s.hwnd, parent.Right-parent.Left, parent.Bottom-parent.Top, child.Right-child.Left, child.Bottom-child.Top, s.dpi())
}
