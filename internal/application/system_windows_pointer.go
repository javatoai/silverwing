//go:build windows

package application

import "unsafe"

func unsafePointer(value *uint16) unsafe.Pointer { return unsafe.Pointer(value) }
