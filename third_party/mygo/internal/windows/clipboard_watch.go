//go:build windows && (amd64 || arm64)

package windows

import "fmt"

const wmClipboardUpdate = 0x031D

var (
	procAddClipboardFormatListener    = user32.NewProc("AddClipboardFormatListener")
	procRemoveClipboardFormatListener = user32.NewProc("RemoveClipboardFormatListener")
	procGetClipboardSequenceNumber    = user32.NewProc("GetClipboardSequenceNumber")
)

func (c clipboard) WatchChanges(changed func()) (func(), error) {
	sequence, _, _ := procGetClipboardSequenceNumber.Call()
	if ok, _, err := procAddClipboardFormatListener.Call(c.b.appHwnd); ok == 0 {
		return nil, fmt.Errorf("mygo: watch clipboard: %w", err)
	}
	c.b.clipboardChanged = func() {
		current, _, _ := procGetClipboardSequenceNumber.Call()
		if current != sequence {
			sequence = current
			changed()
		}
	}
	return func() {
		c.b.clipboardChanged = nil
		procRemoveClipboardFormatListener.Call(c.b.appHwnd)
	}, nil
}
