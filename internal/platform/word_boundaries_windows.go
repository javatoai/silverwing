//go:build windows && (amd64 || arm64)

package platform

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"syscall"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"

	"github.com/ebitengine/purego"
)

const (
	icuBreakIteratorWord = int32(1)
	icuBreakIteratorDone = int32(-1)
	icuMaxTextLength     = int64(1<<31 - 1)
)

var ErrWordBoundariesUnavailable = errors.New("system ICU word boundaries are unavailable")

var (
	icuKernel32                    = syscall.NewLazyDLL("kernel32.dll")
	icuGetSystemDirectoryProcedure = icuKernel32.NewProc("GetSystemDirectoryW")
)

type icuWordBoundariesAPI struct {
	library *syscall.DLL
	open    func(int32, *byte, *uint16, int32, *int32) uintptr
	first   func(uintptr) int32
	next    func(uintptr) int32
	close   func(uintptr)
}

// WordBoundaries returns every UBRK_WORD boundary as a UTF-16 code-unit
// offset. It uses the Windows system ICU with the default locale; results can
// vary between Windows/ICU versions and are not pinned to Compose's ICU data.
func WordBoundaries(text string) (boundaries []int, returnErr error) {
	if !utf8.ValidString(text) {
		return nil, errors.New("word boundary text is not valid UTF-8")
	}
	if int64(len(text)) > icuMaxTextLength {
		return nil, fmt.Errorf("word boundary text exceeds ICU's int32 length limit")
	}

	textUnits := utf16.Encode([]rune(text))
	textLength := len(textUnits)
	if int64(textLength) > icuMaxTextLength {
		return nil, fmt.Errorf("UTF-16 word boundary text exceeds ICU's int32 length limit")
	}
	// ICU accepts a null text pointer for an empty input, but using a stable
	// non-null buffer makes the lifetime and pointer rules uniform. The explicit
	// zero length preserves the empty-input contract.
	if len(textUnits) == 0 {
		textUnits = []uint16{0}
	}

	api, err := loadICUWordBoundariesAPI()
	if err != nil {
		return nil, err
	}
	defer func() {
		returnErr = errors.Join(returnErr, api.library.Release())
	}()

	textPointer := &textUnits[0]
	var status int32
	iterator := api.open(icuBreakIteratorWord, nil, textPointer, int32(textLength), &status)
	runtime.KeepAlive(textUnits)
	if status > 0 {
		if iterator != 0 {
			api.close(iterator)
			runtime.KeepAlive(textUnits)
		}
		return nil, fmt.Errorf("%w: ubrk_open returned ICU error status %d", ErrWordBoundariesUnavailable, status)
	}
	if iterator == 0 {
		return nil, fmt.Errorf("%w: ubrk_open returned a null iterator with status %d", ErrWordBoundariesUnavailable, status)
	}
	defer func() {
		api.close(iterator)
		runtime.KeepAlive(textUnits)
	}()

	first := api.first(iterator)
	if first != 0 {
		return nil, fmt.Errorf("ICU word iterator began at %d; expected boundary 0", first)
	}
	boundaries = []int{int(first)}
	last := first
	for count := 0; ; count++ {
		if count > textLength {
			return nil, errors.New("ICU returned more word boundaries than UTF-16 positions")
		}
		boundary := api.next(iterator)
		if boundary == icuBreakIteratorDone {
			break
		}
		if boundary < 0 || boundary <= last || boundary > int32(textLength) {
			return nil, fmt.Errorf("ICU returned invalid word boundary %d after %d for UTF-16 length %d", boundary, last, textLength)
		}
		boundaries = append(boundaries, int(boundary))
		last = boundary
	}
	if last != int32(textLength) {
		return nil, fmt.Errorf("ICU word boundaries ended at %d; expected UTF-16 length %d", last, textLength)
	}
	runtime.KeepAlive(textUnits)
	return boundaries, nil
}

func loadICUWordBoundariesAPI() (*icuWordBoundariesAPI, error) {
	path, err := systemICUDLLPath()
	if err != nil {
		return nil, err
	}
	library, err := syscall.LoadDLL(path)
	if err != nil {
		return nil, fmt.Errorf("%w: load system ICU %q: %v", ErrWordBoundariesUnavailable, path, err)
	}
	api := &icuWordBoundariesAPI{library: library}
	bindings := []struct {
		name string
		fn   any
	}{
		{name: "ubrk_open", fn: &api.open},
		{name: "ubrk_first", fn: &api.first},
		{name: "ubrk_next", fn: &api.next},
		{name: "ubrk_close", fn: &api.close},
	}
	for _, binding := range bindings {
		procedure, findErr := library.FindProc(binding.name)
		if findErr != nil {
			return nil, errors.Join(
				fmt.Errorf("%w: system ICU %q has no %s export: %v", ErrWordBoundariesUnavailable, path, binding.name, findErr),
				library.Release(),
			)
		}
		purego.RegisterFunc(binding.fn, procedure.Addr())
	}
	return api, nil
}

func systemICUDLLPath() (string, error) {
	const initialBufferUnits = 260
	const maxSystemPathUnits = 32768
	size := uint32(initialBufferUnits)
	for size <= maxSystemPathUnits {
		buffer := make([]uint16, size)
		length, _, callErr := icuGetSystemDirectoryProcedure.Call(
			uintptr(unsafe.Pointer(&buffer[0])), uintptr(size),
		)
		runtime.KeepAlive(buffer)
		if length == 0 {
			return "", fmt.Errorf("%w: GetSystemDirectoryW failed: %v", ErrWordBoundariesUnavailable, callErr)
		}
		if length < uintptr(size) {
			directory := syscall.UTF16ToString(buffer[:length])
			if !filepath.IsAbs(directory) {
				return "", fmt.Errorf("%w: Windows returned a non-absolute system directory %q", ErrWordBoundariesUnavailable, directory)
			}
			return filepath.Join(directory, "icu.dll"), nil
		}
		if length >= maxSystemPathUnits {
			break
		}
		size = uint32(length + 1)
	}
	return "", fmt.Errorf("%w: Windows system directory path exceeded %d UTF-16 units", ErrWordBoundariesUnavailable, maxSystemPathUnits)
}
