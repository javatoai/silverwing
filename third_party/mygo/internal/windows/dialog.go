//go:build windows && (amd64 || arm64)

package windows

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"github.com/egoist/mygo/internal/platform"
)

type dialogs struct{ b *Backend }

var (
	clsidFileOpenDialog = guid("dc1c5a9c-e88a-4dde-a5a1-60f82a20aef7")
	iidIFileOpenDialog  = guid("d57c7288-d4ad-4768-be02-9d969532d960")
	clsidFileSaveDialog = guid("c0b4e2f3-ba21-4773-8dba-335ec946eb8b")
	iidIFileSaveDialog  = guid("84bccd23-5fde-4cdb-aea4-af64b83d78ab")
	iidIShellItem       = guid("43826d1e-e718-42ee-bc55-a1e261c37bfe")
)

// IFileDialog (IModalWindow) and IFileOpenDialog vtable indices.
const (
	fdShow             = 3
	fdSetFileTypes     = 4
	fdSetOptions       = 9
	fdGetOptions       = 10
	fdSetFolder        = 12
	fdSetFileName      = 15
	fdSetTitle         = 17
	fdSetOkButtonLabel = 18
	fdSetFileNameLabel = 19
	fdGetResult        = 20
	fodGetResults      = 27

	siGetDisplayName = 5
	siaGetCount      = 7
	siaGetItemAt     = 8

	fosOverwritePrompt  = 0x2
	fosPickFolders      = 0x20
	fosForceFileSystem  = 0x40
	fosAllowMultiselect = 0x200
	fosPathMustExist    = 0x800
	fosFileMustExist    = 0x1000
	fosForceShowHidden  = 0x10000000

	sigdnFileSysPath    = 0x80058000
	fileDialogCancelled = 0x800704c7
	fileDialogNoPointer = 0x80004003
)

func ownerOf(pw platform.Window) uintptr {
	if w, ok := pw.(*window); ok && w != nil && !w.closed {
		return w.hwnd
	}
	return 0
}

func (d dialogs) ShowOpenDialog(parent platform.Window, o *platform.OpenDialogOptions, cb func([]string, error)) {
	dlg, err := createFileDialog(&clsidFileOpenDialog, &iidIFileOpenDialog)
	if err != nil {
		cb(nil, err)
		return
	}
	defer release(dlg)
	opts := uint32(fosForceFileSystem | fosPathMustExist)
	if o.OpenDirectories {
		opts |= fosPickFolders
	} else {
		opts |= fosFileMustExist
	}
	if o.Multiple {
		opts |= fosAllowMultiselect
	}
	if o.ShowHiddenFiles {
		opts |= fosForceShowHidden
	}
	if err := setupFileDialog(dlg, opts, o.Title, o.ButtonLabel, "", o.DefaultPath, o.Filters); err != nil {
		cb(nil, err)
		return
	}
	paths, err := openDialogPaths(dlg, ownerOf(parent))
	cb(paths, err)
}

func openDialogPaths(dlg, owner uintptr) ([]string, error) {
	hr := comCall(dlg, fdShow, owner)
	if uint32(hr) == fileDialogCancelled {
		return nil, nil
	}
	if failed(hr) {
		return nil, hresultError("IFileDialog.Show", hr)
	}
	var items uintptr
	if hr := comCall(dlg, fodGetResults, uintptr(unsafe.Pointer(&items))); failed(hr) {
		return nil, hresultError("IFileOpenDialog.GetResults", hr)
	}
	if items == 0 {
		return nil, hresultError("IFileOpenDialog.GetResults returned no items", fileDialogNoPointer)
	}
	defer release(items)
	var n uint32
	if hr := comCall(items, siaGetCount, uintptr(unsafe.Pointer(&n))); failed(hr) {
		return nil, hresultError("IShellItemArray.GetCount", hr)
	}
	var paths []string
	for i := uint32(0); i < n; i++ {
		var item uintptr
		if hr := comCall(items, siaGetItemAt, uintptr(i), uintptr(unsafe.Pointer(&item))); failed(hr) {
			return nil, hresultError("IShellItemArray.GetItemAt", hr)
		}
		if item == 0 {
			return nil, hresultError("IShellItemArray.GetItemAt returned no item", fileDialogNoPointer)
		}
		path, err := itemPath(item)
		release(item)
		if err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	return paths, nil
}

func (d dialogs) ShowSaveDialog(parent platform.Window, o *platform.SaveDialogOptions, cb func(string, error)) {
	dlg, err := createFileDialog(&clsidFileSaveDialog, &iidIFileSaveDialog)
	if err != nil {
		cb("", err)
		return
	}
	defer release(dlg)
	opts := uint32(fosForceFileSystem | fosOverwritePrompt | fosPathMustExist)
	if o.ShowHiddenFiles {
		opts |= fosForceShowHidden
	}
	if err := setupFileDialog(dlg, opts, o.Title, o.ButtonLabel, o.NameFieldLabel, o.DefaultPath, o.Filters); err != nil {
		cb("", err)
		return
	}
	path, err := saveDialogPath(dlg, ownerOf(parent))
	cb(path, err)
}

func saveDialogPath(dlg, owner uintptr) (string, error) {
	hr := comCall(dlg, fdShow, owner)
	if uint32(hr) == fileDialogCancelled {
		return "", nil
	}
	if failed(hr) {
		return "", hresultError("IFileDialog.Show", hr)
	}
	var item uintptr
	if hr := comCall(dlg, fdGetResult, uintptr(unsafe.Pointer(&item))); failed(hr) {
		return "", hresultError("IFileDialog.GetResult", hr)
	}
	if item == 0 {
		return "", hresultError("IFileDialog.GetResult returned no item", fileDialogNoPointer)
	}
	defer release(item)
	return itemPath(item)
}

func createFileDialog(clsid, iid *GUID) (uintptr, error) {
	var dlg uintptr
	hr, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(clsid)), 0, clsctxInprocServer, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&dlg)))
	if failed(hr) {
		return 0, hresultError("CoCreateInstance(IFileDialog)", hr)
	}
	if dlg == 0 {
		return 0, hresultError("CoCreateInstance(IFileDialog) returned no dialog", fileDialogNoPointer)
	}
	return dlg, nil
}

func setupFileDialog(dlg uintptr, opts uint32, title, button, nameLabel, defaultPath string, filters []platform.FileFilter) error {
	var current uint32
	if hr := comCall(dlg, fdGetOptions, uintptr(unsafe.Pointer(&current))); failed(hr) {
		return hresultError("IFileDialog.GetOptions", hr)
	}
	if hr := comCall(dlg, fdSetOptions, uintptr(current|opts)); failed(hr) {
		return hresultError("IFileDialog.SetOptions", hr)
	}
	if title != "" {
		if hr := comCall(dlg, fdSetTitle, uintptr(unsafe.Pointer(u16(title)))); failed(hr) {
			return hresultError("IFileDialog.SetTitle", hr)
		}
	}
	if button != "" {
		if hr := comCall(dlg, fdSetOkButtonLabel, uintptr(unsafe.Pointer(u16(strings.ReplaceAll(button, "_", ""))))); failed(hr) {
			return hresultError("IFileDialog.SetOkButtonLabel", hr)
		}
	}
	if nameLabel != "" {
		if hr := comCall(dlg, fdSetFileNameLabel, uintptr(unsafe.Pointer(u16(nameLabel)))); failed(hr) {
			return hresultError("IFileDialog.SetFileNameLabel", hr)
		}
	}
	if defaultPath != "" {
		dir, name := defaultPath, ""
		if st, err := os.Stat(defaultPath); err != nil || !st.IsDir() {
			dir, name = filepath.Dir(defaultPath), filepath.Base(defaultPath)
		}
		var folder uintptr
		hr, _, _ := procSHCreateItemFromParsingName.Call(uintptr(unsafe.Pointer(u16(dir))), 0, uintptr(unsafe.Pointer(&iidIShellItem)), uintptr(unsafe.Pointer(&folder)))
		// An unavailable initial folder is optional; the user can choose another.
		if !failed(hr) && folder != 0 {
			hr = comCall(dlg, fdSetFolder, folder)
			release(folder)
			if failed(hr) {
				return hresultError("IFileDialog.SetFolder", hr)
			}
		}
		if name != "" {
			if hr := comCall(dlg, fdSetFileName, uintptr(unsafe.Pointer(u16(name)))); failed(hr) {
				return hresultError("IFileDialog.SetFileName", hr)
			}
		}
	}
	// COMDLG_FILTERSPEC {pszName, pszSpec} pairs; the strings stay alive
	// in specs until the call returns.
	type filterSpec struct{ Name, Spec *uint16 }
	var specs []filterSpec
	for _, f := range filters {
		var patterns []string
		for _, e := range f.Extensions {
			if e == "*" {
				patterns = append(patterns, "*.*")
			} else {
				patterns = append(patterns, "*."+strings.TrimPrefix(e, "."))
			}
		}
		if len(patterns) > 0 {
			specs = append(specs, filterSpec{u16(f.Name), u16(strings.Join(patterns, ";"))})
		}
	}
	if len(specs) > 0 {
		if hr := comCall(dlg, fdSetFileTypes, uintptr(len(specs)), uintptr(unsafe.Pointer(&specs[0]))); failed(hr) {
			return hresultError("IFileDialog.SetFileTypes", hr)
		}
	}
	return nil
}

func itemPath(item uintptr) (string, error) {
	var p uintptr
	if hr := comCall(item, siGetDisplayName, sigdnFileSysPath, uintptr(unsafe.Pointer(&p))); failed(hr) {
		return "", hresultError("IShellItem.GetDisplayName", hr)
	}
	if p == 0 {
		return "", hresultError("IShellItem.GetDisplayName returned no path", fileDialogNoPointer)
	}
	path := takeWstr(p)
	if path == "" {
		return "", hresultError("IShellItem.GetDisplayName returned an empty path", 0x80004005)
	}
	return path, nil
}

func (d dialogs) ShowMessageBox(parent platform.Window, o *platform.MessageBoxOptions, cb func(platform.MessageBoxResult, error)) {
	cb(d.b.messageBox(ownerOf(parent), o), nil)
}

var (
	taskDialogOnce sync.Once
	taskDialogProc uintptr
)

// Task dialog constants (commctrl.h).
const (
	tdfAllowDialogCancellation  = 0x0008
	tdfVerificationFlagChecked  = 0x0100
	tdfPositionRelativeToWindow = 0x1000
	tdfSizeToContent            = 0x01000000
	tdWarningIcon               = 0xFFFF
	tdErrorIcon                 = 0xFFFE
	tdInformationIcon           = 0xFFFD
	idCancel                    = 2
	firstButtonID               = 100
)

// messageBox shows a task dialog, or a plain message box where task
// dialogs are unavailable, and blocks until it is answered.
func (b *Backend) messageBox(owner uintptr, o *platform.MessageBoxOptions) platform.MessageBoxResult {
	taskDialogOnce.Do(func() { taskDialogProc = commonControl("TaskDialogIndirect") })
	buttons := o.Buttons
	if len(buttons) == 0 {
		buttons = []string{"OK"}
	}
	if taskDialogProc == 0 {
		return legacyMessageBox(owner, o, buttons)
	}
	title := o.Title
	if title == "" {
		title = b.name
	}
	message, detail := o.Message, o.Detail
	// Keep every string alive until TaskDialogIndirect returns.
	strs := []*uint16{u16(title), u16(message), u16(detail)}
	var icon uintptr
	switch o.Type {
	case "warning":
		icon = tdWarningIcon
	case "error":
		icon = tdErrorIcon
	case "info", "question":
		icon = tdInformationIcon
	}

	// TASKDIALOG_BUTTON and TASKDIALOGCONFIG are packed (pshpack1.h), so
	// they are laid out by hand: pointers are not 8-byte aligned.
	btns := make([]byte, 12*len(buttons))
	for i, label := range buttons {
		s := u16(label)
		strs = append(strs, s)
		binary.LittleEndian.PutUint32(btns[12*i:], uint32(firstButtonID+i))
		binary.LittleEndian.PutUint64(btns[12*i+4:], uint64(uintptr(unsafe.Pointer(s))))
	}
	var verification *uint16
	if o.CheckboxLabel != "" {
		verification = u16(o.CheckboxLabel)
	}
	flags := uint32(tdfAllowDialogCancellation | tdfSizeToContent)
	if owner != 0 {
		flags |= tdfPositionRelativeToWindow
	}
	if o.CheckboxChecked {
		flags |= tdfVerificationFlagChecked
	}
	cfg := make([]byte, 160)
	le := binary.LittleEndian
	le.PutUint32(cfg[0:], 160)                                       // cbSize
	le.PutUint64(cfg[4:], uint64(owner))                             // hwndParent
	le.PutUint64(cfg[12:], uint64(instance()))                       // hInstance
	le.PutUint32(cfg[20:], flags)                                    // dwFlags
	le.PutUint32(cfg[24:], 0)                                        // dwCommonButtons
	le.PutUint64(cfg[28:], uint64(uintptr(unsafe.Pointer(strs[0])))) // pszWindowTitle
	le.PutUint64(cfg[36:], uint64(icon))                             // pszMainIcon
	le.PutUint64(cfg[44:], uint64(uintptr(unsafe.Pointer(strs[1])))) // pszMainInstruction
	if detail != "" {
		le.PutUint64(cfg[52:], uint64(uintptr(unsafe.Pointer(strs[2])))) // pszContent
	}
	le.PutUint32(cfg[60:], uint32(len(buttons)))                      // cButtons
	le.PutUint64(cfg[64:], uint64(uintptr(unsafe.Pointer(&btns[0])))) // pButtons
	le.PutUint32(cfg[72:], uint32(firstButtonID+o.DefaultID))         // nDefaultButton
	if verification != nil {
		le.PutUint64(cfg[92:], uint64(uintptr(unsafe.Pointer(verification)))) // pszVerificationText
	}

	var pressed, radio int32
	var checked int32
	r, _, _ := syscall.SyscallN(taskDialogProc, uintptr(unsafe.Pointer(&cfg[0])), uintptr(unsafe.Pointer(&pressed)), uintptr(unsafe.Pointer(&radio)), uintptr(unsafe.Pointer(&checked)))
	runtime.KeepAlive(strs)
	runtime.KeepAlive(btns)
	runtime.KeepAlive(verification)
	res := platform.MessageBoxResult{Response: o.CancelID, CheckboxChecked: checked != 0}
	if !failed(r) && pressed >= firstButtonID && int(pressed-firstButtonID) < len(buttons) {
		res.Response = int(pressed - firstButtonID)
	}
	return res
}

func legacyMessageBox(owner uintptr, o *platform.MessageBoxOptions, buttons []string) platform.MessageBoxResult {
	const (
		mbOK, mbOKCancel, mbYesNoCancel = 0x0, 0x1, 0x3
		mbIconError, mbIconWarning      = 0x10, 0x30
		mbIconInformation               = 0x40
		idOK, idYes, idNo               = 1, 6, 7
	)
	style := uintptr(mbOK)
	switch len(buttons) {
	case 2:
		style = mbOKCancel
	case 3:
		style = mbYesNoCancel
	}
	switch o.Type {
	case "error":
		style |= mbIconError
	case "warning":
		style |= mbIconWarning
	case "info", "question":
		style |= mbIconInformation
	}
	text := o.Message
	if o.Detail != "" {
		text += "\n\n" + o.Detail
	}
	r, _, _ := procMessageBoxW.Call(owner, uintptr(unsafe.Pointer(u16(text))), uintptr(unsafe.Pointer(u16(o.Title))), style)
	res := platform.MessageBoxResult{Response: o.CancelID}
	switch int(r) {
	case idOK, idYes:
		res.Response = 0
	case idNo:
		res.Response = 1
	case idCancel:
		if len(buttons) >= 2 {
			res.Response = len(buttons) - 1
		}
	}
	return res
}
