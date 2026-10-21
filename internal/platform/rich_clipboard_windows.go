//go:build windows

package platform

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"
)

const (
	richClipboardUnicodeText = 13
	richClipboardMoveable    = 0x0002
	richClipboardMaxBytes    = 64 << 20
	richClipboardHeaderLimit = 16 << 10
)

// RichClipboardContent contains the plain-text and HTML representations of a
// clipboard selection. HTML is a fragment; the Windows implementation wraps
// it in a CF_HTML document when writing and returns just the fragment when
// reading.
type RichClipboardContent struct {
	Text string
	HTML string
}

var (
	richClipboardUser32   = syscall.NewLazyDLL("user32.dll")
	richClipboardKernel32 = syscall.NewLazyDLL("kernel32.dll")

	richClipboardOpenProc      = richClipboardUser32.NewProc("OpenClipboard")
	richClipboardCloseProc     = richClipboardUser32.NewProc("CloseClipboard")
	richClipboardCreateWindow  = richClipboardUser32.NewProc("CreateWindowExW")
	richClipboardDestroyWindow = richClipboardUser32.NewProc("DestroyWindow")
	richClipboardEmptyProc     = richClipboardUser32.NewProc("EmptyClipboard")
	richClipboardGetProc       = richClipboardUser32.NewProc("GetClipboardData")
	richClipboardSetProc       = richClipboardUser32.NewProc("SetClipboardData")
	richClipboardAvailableProc = richClipboardUser32.NewProc("IsClipboardFormatAvailable")
	richClipboardRegisterProc  = richClipboardUser32.NewProc("RegisterClipboardFormatW")
	richClipboardGlobalAlloc   = richClipboardKernel32.NewProc("GlobalAlloc")
	richClipboardGlobalFree    = richClipboardKernel32.NewProc("GlobalFree")
	richClipboardGlobalLock    = richClipboardKernel32.NewProc("GlobalLock")
	richClipboardGlobalUnlock  = richClipboardKernel32.NewProc("GlobalUnlock")
	richClipboardGlobalSize    = richClipboardKernel32.NewProc("GlobalSize")
)

// ReadRichClipboard reads CF_UNICODETEXT and, when present, the registered
// "HTML Format" CF_HTML representation. It holds one clipboard lock while
// reading both representations so they cannot come from different owners.
func ReadRichClipboard() (content RichClipboardContent, returnErr error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	htmlFormat, err := richClipboardHTMLFormat()
	if err != nil {
		return content, err
	}
	owner, err := openRichClipboard()
	if err != nil {
		return content, err
	}
	defer func() { returnErr = errors.Join(returnErr, closeRichClipboard(owner)) }()

	if available, _, _ := richClipboardAvailableProc.Call(richClipboardUnicodeText); available != 0 {
		data, err := readRichClipboardGlobal(richClipboardUnicodeText)
		if err != nil {
			return content, err
		}
		content.Text, err = decodeRichClipboardText(data)
		if err != nil {
			return content, err
		}
	}
	if available, _, _ := richClipboardAvailableProc.Call(uintptr(htmlFormat)); available != 0 {
		data, err := readRichClipboardGlobal(uintptr(htmlFormat))
		if err != nil {
			return content, err
		}
		content.HTML, err = decodeRichClipboardHTML(data)
		if err != nil {
			return content, err
		}
	}
	return content, nil
}

// WriteRichClipboard writes CF_UNICODETEXT and, for a non-empty HTML fragment,
// the registered "HTML Format" CF_HTML representation. Both HGLOBAL blocks
// are fully prepared before EmptyClipboard can discard existing contents.
func WriteRichClipboard(content RichClipboardContent) (returnErr error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	textData, err := encodeRichClipboardText(content.Text)
	if err != nil {
		return err
	}
	var htmlData []byte
	var htmlFormat uint32
	if content.HTML != "" {
		htmlData, err = encodeRichClipboardHTML(content.HTML)
		if err != nil {
			return err
		}
		htmlFormat, err = richClipboardHTMLFormat()
		if err != nil {
			return err
		}
	}

	var textHandle, htmlHandle uintptr
	defer func() {
		var cleanupErrs []error
		if textHandle != 0 {
			if err := freeRichClipboardGlobal(textHandle); err != nil {
				cleanupErrs = append(cleanupErrs, err)
			}
		}
		if htmlHandle != 0 {
			if err := freeRichClipboardGlobal(htmlHandle); err != nil {
				cleanupErrs = append(cleanupErrs, err)
			}
		}
		returnErr = errors.Join(returnErr, errors.Join(cleanupErrs...))
	}()

	textHandle, err = allocateRichClipboardGlobal(textData)
	if err != nil {
		return err
	}
	if len(htmlData) > 0 {
		htmlHandle, err = allocateRichClipboardGlobal(htmlData)
		if err != nil {
			return err
		}
	}

	owner, err := openRichClipboard()
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, closeRichClipboard(owner)) }()

	if result, _, callErr := richClipboardEmptyProc.Call(); result == 0 {
		return richClipboardCallError("清空系统剪贴板", callErr)
	}
	if result, _, callErr := richClipboardSetProc.Call(richClipboardUnicodeText, textHandle); result == 0 {
		return richClipboardCallError("写入纯文本剪贴板格式", callErr)
	}
	textHandle = 0 // SetClipboardData transferred HGLOBAL ownership to Windows.

	if htmlHandle != 0 {
		if result, _, callErr := richClipboardSetProc.Call(uintptr(htmlFormat), htmlHandle); result == 0 {
			return fmt.Errorf("写入 HTML 剪贴板格式失败（纯文本已写入）：%w", richClipboardCallError("SetClipboardData(HTML Format)", callErr))
		}
		htmlHandle = 0 // SetClipboardData transferred HGLOBAL ownership to Windows.
	}
	return nil
}

func richClipboardHTMLFormat() (uint32, error) {
	name, err := syscall.UTF16PtrFromString("HTML Format")
	if err != nil {
		return 0, fmt.Errorf("编码 HTML 剪贴板格式名称：%w", err)
	}
	format, _, callErr := richClipboardRegisterProc.Call(uintptr(unsafe.Pointer(name)))
	if format == 0 {
		return 0, richClipboardCallError("注册 HTML Format 剪贴板格式", callErr)
	}
	return uint32(format), nil
}

func openRichClipboard() (uintptr, error) {
	className, _ := syscall.UTF16PtrFromString("STATIC")
	title, _ := syscall.UTF16PtrFromString("")
	owner, _, callErr := richClipboardCreateWindow.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(title)),
		0, 0, 0, 0, 0, 0, 0, 0, 0,
	)
	if owner == 0 {
		return 0, richClipboardCallError("创建剪贴板所有者窗口", callErr)
	}
	for attempt := 0; attempt < 25; attempt++ {
		if result, _, lastErr := richClipboardOpenProc.Call(owner); result != 0 {
			return owner, nil
		} else {
			callErr = lastErr
		}
		if attempt < 24 {
			time.Sleep(20 * time.Millisecond)
		}
	}
	_ = destroyRichClipboardWindow(owner)
	return 0, richClipboardCallError("打开系统剪贴板（可能正被其他程序占用）", callErr)
}

func closeRichClipboard(owner uintptr) error {
	var errs []error
	if result, _, callErr := richClipboardCloseProc.Call(); result == 0 {
		errs = append(errs, richClipboardCallError("关闭系统剪贴板", callErr))
	}
	if err := destroyRichClipboardWindow(owner); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func destroyRichClipboardWindow(owner uintptr) error {
	if result, _, callErr := richClipboardDestroyWindow.Call(owner); result == 0 {
		return richClipboardCallError("销毁剪贴板所有者窗口", callErr)
	}
	return nil
}

func richClipboardCallError(operation string, callErr error) error {
	if callErr == nil || callErr == syscall.Errno(0) {
		return fmt.Errorf("%s失败", operation)
	}
	return fmt.Errorf("%s失败：%w", operation, callErr)
}

func readRichClipboardGlobal(format uintptr) ([]byte, error) {
	handle, _, callErr := richClipboardGetProc.Call(format)
	if handle == 0 {
		return nil, richClipboardCallError("读取剪贴板格式", callErr)
	}
	size, _, _ := richClipboardGlobalSize.Call(handle)
	if size == 0 || size > richClipboardMaxBytes {
		return nil, fmt.Errorf("剪贴板数据为空或超过 %d MiB", richClipboardMaxBytes>>20)
	}
	locked, _, lockErr := richClipboardGlobalLock.Call(handle)
	if locked == 0 {
		return nil, richClipboardCallError("锁定剪贴板数据", lockErr)
	}
	defer richClipboardGlobalUnlock.Call(handle)
	return append([]byte(nil), unsafe.Slice((*byte)(unsafe.Pointer(locked)), int(size))...), nil
}

func allocateRichClipboardGlobal(data []byte) (uintptr, error) {
	if len(data) == 0 || len(data) > richClipboardMaxBytes {
		return 0, fmt.Errorf("剪贴板表示为空或超过 %d MiB", richClipboardMaxBytes>>20)
	}
	handle, _, allocErr := richClipboardGlobalAlloc.Call(richClipboardMoveable, uintptr(len(data)))
	if handle == 0 {
		return 0, richClipboardCallError("分配剪贴板内存", allocErr)
	}
	locked, _, lockErr := richClipboardGlobalLock.Call(handle)
	if locked == 0 {
		return 0, errors.Join(richClipboardCallError("锁定待写入的剪贴板内存", lockErr), freeRichClipboardGlobal(handle))
	}
	copy(unsafe.Slice((*byte)(unsafe.Pointer(locked)), len(data)), data)
	if result, _, unlockErr := richClipboardGlobalUnlock.Call(handle); result == 0 && !richClipboardNoSystemError(unlockErr) {
		return 0, errors.Join(richClipboardCallError("解锁待写入的剪贴板内存", unlockErr), freeRichClipboardGlobal(handle))
	}
	return handle, nil
}

func freeRichClipboardGlobal(handle uintptr) error {
	if handle == 0 {
		return nil
	}
	if result, _, callErr := richClipboardGlobalFree.Call(handle); result != 0 {
		return richClipboardCallError("释放未转交的剪贴板内存", callErr)
	}
	return nil
}

func richClipboardNoSystemError(err error) bool {
	return err == nil || err == syscall.Errno(0)
}

func encodeRichClipboardText(text string) ([]byte, error) {
	if !utf8.ValidString(text) {
		return nil, errors.New("纯文本剪贴板内容不是有效 UTF-8")
	}
	if strings.IndexByte(text, 0) >= 0 {
		return nil, errors.New("纯文本剪贴板内容不能包含 NUL")
	}
	units := utf16.Encode([]rune(text))
	if (len(units)+1)*2 > richClipboardMaxBytes {
		return nil, fmt.Errorf("纯文本剪贴板内容超过 %d MiB", richClipboardMaxBytes>>20)
	}
	units = append(units, 0)
	data := make([]byte, len(units)*2)
	for index, unit := range units {
		binary.LittleEndian.PutUint16(data[index*2:], unit)
	}
	return data, nil
}

func decodeRichClipboardText(data []byte) (string, error) {
	if len(data)%2 != 0 {
		return "", errors.New("CF_UNICODETEXT 数据长度不是偶数")
	}
	units := make([]uint16, 0, len(data)/2)
	for offset := 0; offset < len(data); offset += 2 {
		unit := binary.LittleEndian.Uint16(data[offset:])
		if unit == 0 {
			break
		}
		units = append(units, unit)
	}
	return string(utf16.Decode(units)), nil
}

func encodeRichClipboardHTML(fragment string) ([]byte, error) {
	if !utf8.ValidString(fragment) {
		return nil, errors.New("HTML 剪贴板内容不是有效 UTF-8")
	}
	if strings.IndexByte(fragment, 0) >= 0 {
		return nil, errors.New("HTML 剪贴板内容不能包含 NUL")
	}
	const headerTemplate = "Version:0.9\r\nStartHTML:%010d\r\nEndHTML:%010d\r\nStartFragment:%010d\r\nEndFragment:%010d\r\n"
	const prefix = "<html><body><!--StartFragment-->"
	const suffix = "<!--EndFragment--></body></html>"
	if len(fragment)+len(prefix)+len(suffix)+256 > richClipboardMaxBytes {
		return nil, fmt.Errorf("HTML 剪贴板内容超过 %d MiB", richClipboardMaxBytes>>20)
	}
	headerSize := len(fmt.Sprintf(headerTemplate, 0, 0, 0, 0))
	startHTML := headerSize
	startFragment := startHTML + len(prefix)
	endFragment := startFragment + len(fragment)
	endHTML := endFragment + len(suffix)
	if endHTML > 9_999_999_999 {
		return nil, errors.New("HTML 剪贴板内容超出 CF_HTML 偏移范围")
	}
	header := fmt.Sprintf(headerTemplate, startHTML, endHTML, startFragment, endFragment)
	if len(header) != headerSize {
		return nil, errors.New("生成 CF_HTML 头失败")
	}
	document := make([]byte, 0, endHTML+1)
	document = append(document, header...)
	document = append(document, prefix...)
	document = append(document, fragment...)
	document = append(document, suffix...)
	document = append(document, 0)
	return document, nil
}

func decodeRichClipboardHTML(data []byte) (string, error) {
	data = bytes.TrimRight(data, "\x00")
	if !utf8.Valid(data) {
		return "", errors.New("CF_HTML 数据不是有效 UTF-8")
	}
	startHTML, ok := richClipboardHeaderOffset(data, "StartHTML")
	if !ok || startHTML > len(data) || startHTML > richClipboardHeaderLimit {
		return "", errors.New("CF_HTML StartHTML 偏移缺失或无效")
	}
	fields := parseRichClipboardHTMLHeader(data[:startHTML])
	startFragment, okStart := parseRichClipboardHTMLField(fields, "StartFragment")
	endFragment, okEnd := parseRichClipboardHTMLField(fields, "EndFragment")
	endHTML, okHTML := parseRichClipboardHTMLField(fields, "EndHTML")
	if !okStart || !okEnd || !okHTML || startHTML < 0 || startHTML > startFragment || startFragment > endFragment || endFragment > endHTML || endHTML > len(data) {
		return "", errors.New("CF_HTML 片段偏移越界或顺序无效")
	}
	fragment := data[startFragment:endFragment]
	if !utf8.Valid(fragment) {
		return "", errors.New("CF_HTML 片段边界不是 UTF-8 字节边界")
	}
	return string(fragment), nil
}

func richClipboardHeaderOffset(data []byte, name string) (int, bool) {
	limit := len(data)
	if limit > richClipboardHeaderLimit {
		limit = richClipboardHeaderLimit
	}
	fields := parseRichClipboardHTMLHeader(data[:limit])
	return parseRichClipboardHTMLField(fields, name)
}

func parseRichClipboardHTMLHeader(header []byte) map[string]string {
	fields := make(map[string]string)
	limit := len(header)
	if limit > richClipboardHeaderLimit {
		limit = richClipboardHeaderLimit
	}
	for offset := 0; offset < limit; {
		lineEnd := bytes.IndexByte(header[offset:limit], '\n')
		if lineEnd < 0 {
			lineEnd = limit - offset
		}
		line := bytes.TrimSuffix(header[offset:offset+lineEnd], []byte{'\r'})
		offset += lineEnd
		if offset < limit && header[offset] == '\n' {
			offset++
		}
		if len(line) == 0 || line[0] == '<' {
			break
		}
		key, value, ok := bytes.Cut(line, []byte{':'})
		if !ok || len(key) == 0 {
			break
		}
		for _, ch := range key {
			if !(ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z') {
				return fields
			}
		}
		fields[string(key)] = strings.TrimSpace(string(value))
	}
	return fields
}

func parseRichClipboardHTMLField(fields map[string]string, name string) (int, bool) {
	value, ok := fields[name]
	if !ok || value == "" {
		return 0, false
	}
	parsed, err := strconv.ParseUint(value, 10, 32)
	if err != nil || uint64(int(parsed)) != parsed {
		return 0, false
	}
	return int(parsed), true
}
