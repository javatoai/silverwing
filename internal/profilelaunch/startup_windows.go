package profilelaunch

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"

	"silverwing-go/internal/productprofile"
)

// ShowStartupError provides feedback when the graphical executable has no
// terminal or redirected diagnostic output.
func ShowStartupError(err error) {
	handle := windows.Handle(os.Stderr.Fd())
	var consoleMode uint32
	if fileType, _ := windows.GetFileType(handle); fileType == windows.FILE_TYPE_DISK || fileType == windows.FILE_TYPE_PIPE ||
		windows.GetConsoleMode(handle, &consoleMode) == nil {
		return
	}
	title, titleErr := windows.UTF16PtrFromString(productprofile.Name + " 启动失败")
	message, messageErr := windows.UTF16PtrFromString(err.Error())
	if titleErr != nil || messageErr != nil {
		return
	}
	const iconError = 0x10
	const setForeground = 0x10000
	messageBox := windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW")
	messageBox.Call(0, uintptr(unsafe.Pointer(message)), uintptr(unsafe.Pointer(title)), iconError|setForeground)
}
