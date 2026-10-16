//go:build windows

package documents

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math/bits"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"
)

const (
	cfDIB       = 8
	cfHDROP     = 15
	cfDIBV5     = 17
	memMoveable = 0x0002
)

var (
	clipboardUser32 = syscall.NewLazyDLL("user32.dll")
	clipboardKernel = syscall.NewLazyDLL("kernel32.dll")
	clipboardShell  = syscall.NewLazyDLL("shell32.dll")

	openClipboardProc      = clipboardUser32.NewProc("OpenClipboard")
	closeClipboardProc     = clipboardUser32.NewProc("CloseClipboard")
	createClipboardWindow  = clipboardUser32.NewProc("CreateWindowExW")
	destroyClipboardWindow = clipboardUser32.NewProc("DestroyWindow")
	emptyClipboardProc     = clipboardUser32.NewProc("EmptyClipboard")
	getClipboardProc       = clipboardUser32.NewProc("GetClipboardData")
	setClipboardProc       = clipboardUser32.NewProc("SetClipboardData")
	registerFormatProc     = clipboardUser32.NewProc("RegisterClipboardFormatW")

	globalAllocProc   = clipboardKernel.NewProc("GlobalAlloc")
	globalFreeProc    = clipboardKernel.NewProc("GlobalFree")
	globalLockProc    = clipboardKernel.NewProc("GlobalLock")
	globalUnlockProc  = clipboardKernel.NewProc("GlobalUnlock")
	globalSizeProc    = clipboardKernel.NewProc("GlobalSize")
	dragQueryFileProc = clipboardShell.NewProc("DragQueryFileW")
)

func newPlatformClipboardBackend() clipboardBackend { return windowsClipboardBackend{} }

type windowsClipboardBackend struct{}

func (windowsClipboardBackend) read(ctx context.Context) (ClipboardPayload, error) {
	owner, err := openSystemClipboard(ctx)
	if err != nil {
		return ClipboardPayload{}, err
	}
	defer closeSystemClipboard(owner)
	if handle, _, _ := getClipboardProc.Call(cfHDROP); handle != 0 {
		files, err := readClipboardFileList(handle)
		if err != nil {
			return ClipboardPayload{}, err
		}
		if len(files) > 0 {
			return ClipboardPayload{Files: files}, nil
		}
	}
	if format, err := syscall.UTF16PtrFromString("PNG"); err == nil {
		id, _, _ := registerFormatProc.Call(uintptr(unsafe.Pointer(format)))
		if id != 0 {
			if handle, _, _ := getClipboardProc.Call(id); handle != 0 {
				data, readErr := readGlobalMemory(handle)
				if readErr == nil && len(data) > 0 {
					return ClipboardPayload{ImagePNG: data}, nil
				}
			}
		}
	}
	for _, format := range []uintptr{cfDIBV5, cfDIB} {
		if handle, _, _ := getClipboardProc.Call(format); handle != 0 {
			data, err := readGlobalMemory(handle)
			if err != nil {
				continue
			}
			pngData, err := dibToPNG(data)
			if err == nil {
				return ClipboardPayload{ImagePNG: pngData}, nil
			}
		}
	}
	return ClipboardPayload{}, nil
}

func (windowsClipboardBackend) copyFiles(ctx context.Context, paths []string) error {
	var payload []uint16
	payload = make([]uint16, 10) // 20-byte DROPFILES header
	payload[0] = 20              // DROPFILES.pFiles offset
	payload[8] = 1               // DROPFILES.fWide
	for _, path := range paths {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		value, err := syscall.UTF16FromString(absolute)
		if err != nil {
			return err
		}
		payload = append(payload, value...)
	}
	payload = append(payload, 0) // list terminator after each path's NUL
	size := uintptr(len(payload) * 2)
	hGlobal, _, allocErr := globalAllocProc.Call(memMoveable, size)
	if hGlobal == 0 {
		return fmt.Errorf("无法分配剪贴板内存：%w", allocErr)
	}
	owned := true
	defer func() {
		if owned {
			globalFreeProc.Call(hGlobal)
		}
	}()
	locked, _, lockErr := globalLockProc.Call(hGlobal)
	if locked == 0 {
		return fmt.Errorf("无法锁定剪贴板内存：%w", lockErr)
	}
	copy(unsafe.Slice((*uint16)(unsafe.Pointer(locked)), len(payload)), payload)
	globalUnlockProc.Call(hGlobal)
	owner, err := openSystemClipboard(ctx)
	if err != nil {
		return err
	}
	defer closeSystemClipboard(owner)
	if result, _, callErr := emptyClipboardProc.Call(); result == 0 {
		return fmt.Errorf("无法清空系统剪贴板：%w", callErr)
	}
	if result, _, callErr := setClipboardProc.Call(cfHDROP, hGlobal); result == 0 {
		return fmt.Errorf("无法写入文件列表剪贴板：%w", callErr)
	}
	owned = false // The system owns this HGLOBAL after SetClipboardData.
	return nil
}

func openSystemClipboard(ctx context.Context) (uintptr, error) {
	static, _ := syscall.UTF16PtrFromString("STATIC")
	caption, _ := syscall.UTF16PtrFromString("")
	owner, _, createErr := createClipboardWindow.Call(0, uintptr(unsafe.Pointer(static)), uintptr(unsafe.Pointer(caption)),
		0, 0, 0, 0, 0, 0, 0, 0, 0)
	if owner == 0 {
		return 0, fmt.Errorf("无法建立剪贴板窗口：%w", createErr)
	}
	for attempt := 0; attempt < 25; attempt++ {
		if err := ctx.Err(); err != nil {
			destroyClipboardWindow.Call(owner)
			return 0, err
		}
		if result, _, _ := openClipboardProc.Call(owner); result != 0 {
			return owner, nil
		}
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			destroyClipboardWindow.Call(owner)
			return 0, ctx.Err()
		case <-timer.C:
		}
	}
	destroyClipboardWindow.Call(owner)
	return 0, errors.New("系统剪贴板正被其他程序占用")
}

func closeSystemClipboard(owner uintptr) {
	closeClipboardProc.Call()
	destroyClipboardWindow.Call(owner)
}

func readClipboardFileList(handle uintptr) ([]string, error) {
	count, _, _ := dragQueryFileProc.Call(handle, ^uintptr(0), 0, 0)
	if count == 0 || count > 10_000 {
		return nil, nil
	}
	paths := make([]string, 0, count)
	for index := uintptr(0); index < count; index++ {
		length, _, _ := dragQueryFileProc.Call(handle, index, 0, 0)
		if length == 0 || length > 32767 {
			continue
		}
		buffer := make([]uint16, length+1)
		written, _, _ := dragQueryFileProc.Call(handle, index, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
		if written > 0 {
			paths = append(paths, syscall.UTF16ToString(buffer[:written]))
		}
	}
	return paths, nil
}

func readGlobalMemory(handle uintptr) ([]byte, error) {
	size, _, _ := globalSizeProc.Call(handle)
	if size == 0 || size > 128*1024*1024 {
		return nil, errors.New("剪贴板数据为空或超过 128 MiB")
	}
	locked, _, callErr := globalLockProc.Call(handle)
	if locked == 0 {
		return nil, fmt.Errorf("无法读取剪贴板数据：%w", callErr)
	}
	defer globalUnlockProc.Call(handle)
	return append([]byte(nil), unsafe.Slice((*byte)(unsafe.Pointer(locked)), int(size))...), nil
}

func dibToPNG(data []byte) ([]byte, error) {
	if len(data) < 40 {
		return nil, errors.New("剪贴板 DIB 头无效")
	}
	headerSize := int(binary.LittleEndian.Uint32(data[0:4]))
	if headerSize < 40 || headerSize > len(data) {
		return nil, errors.New("剪贴板 DIB 头长度无效")
	}
	width := int(int32(binary.LittleEndian.Uint32(data[4:8])))
	height := int(int32(binary.LittleEndian.Uint32(data[8:12])))
	planes := binary.LittleEndian.Uint16(data[12:14])
	bitsPerPixel := int(binary.LittleEndian.Uint16(data[14:16]))
	compression := binary.LittleEndian.Uint32(data[16:20])
	if width <= 0 || height == 0 || planes != 1 || (bitsPerPixel != 24 && bitsPerPixel != 32) ||
		(compression != 0 && compression != 3 && compression != 6) {
		return nil, errors.New("暂不支持此剪贴板 DIB 格式")
	}
	topDown := height < 0
	if height < 0 {
		height = -height
	}
	if int64(width)*int64(height) > 32_000_000 {
		return nil, errors.New("剪贴板图片超过 3200 万像素")
	}
	redMask, greenMask, blueMask, alphaMask := uint32(0x00ff0000), uint32(0x0000ff00), uint32(0x000000ff), uint32(0)
	pixelOffset := headerSize
	if compression == 3 || compression == 6 {
		maskOffset := 40
		if headerSize >= 52 {
			pixelOffset = headerSize
		} else {
			pixelOffset += 12
			if compression == 6 {
				pixelOffset += 4
			}
			if pixelOffset > len(data) {
				return nil, errors.New("剪贴板 DIB 色彩掩码不完整")
			}
		}
		redMask = binary.LittleEndian.Uint32(data[maskOffset : maskOffset+4])
		greenMask = binary.LittleEndian.Uint32(data[maskOffset+4 : maskOffset+8])
		blueMask = binary.LittleEndian.Uint32(data[maskOffset+8 : maskOffset+12])
		if headerSize >= 56 {
			alphaMask = binary.LittleEndian.Uint32(data[maskOffset+12 : maskOffset+16])
		} else if compression == 6 {
			alphaOffset := headerSize
			if headerSize < 52 {
				alphaOffset += 12
			}
			if alphaOffset+4 > len(data) {
				return nil, errors.New("剪贴板 DIB 透明度掩码不完整")
			}
			alphaMask = binary.LittleEndian.Uint32(data[alphaOffset : alphaOffset+4])
			if headerSize >= 52 {
				pixelOffset += 4
			}
		}
	} else if headerSize >= 56 && bitsPerPixel == 32 {
		alphaMask = binary.LittleEndian.Uint32(data[52:56])
	}
	if redMask == 0 || greenMask == 0 || blueMask == 0 {
		return nil, errors.New("剪贴板 DIB 色彩掩码无效")
	}
	stride := ((width*bitsPerPixel + 31) / 32) * 4
	if stride <= 0 || int64(pixelOffset)+int64(stride)*int64(height) > int64(len(data)) {
		return nil, errors.New("剪贴板 DIB 像素数据不完整")
	}
	imageData := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		sourceY := y
		if !topDown {
			sourceY = height - 1 - y
		}
		row := data[pixelOffset+sourceY*stride : pixelOffset+(sourceY+1)*stride]
		for x := 0; x < width; x++ {
			offset := x * (bitsPerPixel / 8)
			pixel := uint32(row[offset]) | uint32(row[offset+1])<<8 | uint32(row[offset+2])<<16
			if bitsPerPixel == 32 {
				pixel |= uint32(row[offset+3]) << 24
			}
			alpha := uint8(255)
			if alphaMask != 0 {
				alpha = dibChannel(pixel, alphaMask)
			}
			imageData.SetNRGBA(x, y, color.NRGBA{R: dibChannel(pixel, redMask), G: dibChannel(pixel, greenMask), B: dibChannel(pixel, blueMask), A: alpha})
		}
	}
	var output bytes.Buffer
	if err := png.Encode(&output, imageData); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func dibChannel(pixel, mask uint32) uint8 {
	shift := bits.TrailingZeros32(mask)
	maximum := mask >> shift
	value := (pixel & mask) >> shift
	return uint8((uint64(value)*255 + uint64(maximum)/2) / uint64(maximum))
}
