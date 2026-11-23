//go:build windows

package profilelaunch

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	profileLaunchOle32          = syscall.NewLazyDLL("ole32.dll")
	profileLaunchCoInitializeEx = profileLaunchOle32.NewProc("CoInitializeEx")
	profileLaunchCoUninitialize = profileLaunchOle32.NewProc("CoUninitialize")
	profileLaunchCoCreate       = profileLaunchOle32.NewProc("CoCreateInstance")
)

var (
	profileLaunchShellLinkCLSID = windows.GUID{
		Data1: 0x00021401, Data2: 0x0000, Data3: 0x0000,
		Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46},
	}
	profileLaunchShellLinkIID = windows.GUID{
		Data1: 0x000214F9, Data2: 0x0000, Data3: 0x0000,
		Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46},
	}
	profileLaunchPersistFileIID = windows.GUID{
		Data1: 0x0000010B, Data2: 0x0000, Data3: 0x0000,
		Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46},
	}
)

const (
	comQueryInterface    = 0
	comRelease           = 2
	persistFileSave      = 6
	shellLinkDescription = 7
	shellLinkWorkingDir  = 9
	shellLinkArguments   = 11
	shellLinkTargetPath  = 20
)

type shortcutCOMObject struct {
	vtable *[21]uintptr
}

func shortcutExtension() string { return ".lnk" }

func writeNativeShortcut(path string, spec Shortcut) error {
	if !filepath.IsAbs(spec.Executable) {
		return errors.New("shortcut target executable must be an absolute path")
	}
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("shortcut staging path already exists: %s", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect shortcut staging path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create shortcut directory: %w", err)
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	initialized, err := initializeShortcutCOM()
	if err != nil {
		return fmt.Errorf("initialize Windows shortcut support: %w", err)
	}
	if initialized {
		defer profileLaunchCoUninitialize.Call()
	}

	var shellLink *shortcutCOMObject
	if err := createShortcutCOMObject(&profileLaunchShellLinkCLSID, &profileLaunchShellLinkIID, &shellLink); err != nil {
		return fmt.Errorf("create Windows shortcut: %w", err)
	}
	defer releaseShortcutCOMObject(shellLink)

	if err := setShortcutString(shellLink, shellLinkTargetPath, spec.Executable); err != nil {
		return fmt.Errorf("set shortcut target: %w", err)
	}
	if err := setShortcutString(shellLink, shellLinkArguments, quoteWindowsShortcutArguments(spec.args())); err != nil {
		return fmt.Errorf("set shortcut arguments: %w", err)
	}
	if err := setShortcutString(shellLink, shellLinkWorkingDir, filepath.Dir(spec.Executable)); err != nil {
		return fmt.Errorf("set shortcut working directory: %w", err)
	}
	if spec.Name != "" {
		if err := setShortcutString(shellLink, shellLinkDescription, spec.Name); err != nil {
			return fmt.Errorf("set shortcut description: %w", err)
		}
	}

	var persistFile *shortcutCOMObject
	if err := queryShortcutInterface(shellLink, &profileLaunchPersistFileIID, &persistFile); err != nil {
		return fmt.Errorf("open Windows shortcut persistence interface: %w", err)
	}
	defer releaseShortcutCOMObject(persistFile)

	shortcutPath, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return fmt.Errorf("encode shortcut path: %w", err)
	}
	result, _, _ := syscall.SyscallN(persistFile.vtable[persistFileSave], uintptr(unsafe.Pointer(persistFile)), uintptr(unsafe.Pointer(shortcutPath)), 1)
	runtime.KeepAlive(shortcutPath)
	if err := shortcutHRESULT(result); err != nil {
		return fmt.Errorf("save Windows shortcut: %w", err)
	}
	return nil
}

func initializeShortcutCOM() (bool, error) {
	result, _, _ := profileLaunchCoInitializeEx.Call(0, 2)
	if int32(result) >= 0 {
		return true, nil
	}
	// RPC_E_CHANGED_MODE means this thread already initialized COM in the
	// other apartment model. COM calls remain valid in that apartment.
	if uint32(result) == 0x80010106 {
		return false, nil
	}
	return false, shortcutHRESULT(result)
}

func createShortcutCOMObject(classID, interfaceID *windows.GUID, object **shortcutCOMObject) error {
	result, _, _ := profileLaunchCoCreate.Call(
		uintptr(unsafe.Pointer(classID)), 0, 1,
		uintptr(unsafe.Pointer(interfaceID)), uintptr(unsafe.Pointer(object)),
	)
	runtime.KeepAlive(classID)
	runtime.KeepAlive(interfaceID)
	runtime.KeepAlive(object)
	return shortcutHRESULT(result)
}

func queryShortcutInterface(object *shortcutCOMObject, interfaceID *windows.GUID, resultObject **shortcutCOMObject) error {
	result, _, _ := syscall.SyscallN(object.vtable[comQueryInterface], uintptr(unsafe.Pointer(object)),
		uintptr(unsafe.Pointer(interfaceID)), uintptr(unsafe.Pointer(resultObject)))
	runtime.KeepAlive(interfaceID)
	runtime.KeepAlive(resultObject)
	return shortcutHRESULT(result)
}

func setShortcutString(object *shortcutCOMObject, method int, value string) error {
	wide, err := syscall.UTF16PtrFromString(value)
	if err != nil {
		return err
	}
	result, _, _ := syscall.SyscallN(object.vtable[method], uintptr(unsafe.Pointer(object)), uintptr(unsafe.Pointer(wide)))
	runtime.KeepAlive(wide)
	return shortcutHRESULT(result)
}

func releaseShortcutCOMObject(object *shortcutCOMObject) {
	syscall.SyscallN(object.vtable[comRelease], uintptr(unsafe.Pointer(object)))
}

func shortcutHRESULT(result uintptr) error {
	if int32(result) >= 0 {
		return nil
	}
	return syscall.Errno(uint32(result))
}

func quoteWindowsShortcutArguments(arguments []string) string {
	quoted := make([]string, len(arguments))
	for index, argument := range arguments {
		quoted[index] = windows.EscapeArg(argument)
	}
	return strings.Join(quoted, " ")
}
