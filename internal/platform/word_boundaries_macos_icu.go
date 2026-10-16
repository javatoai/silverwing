package platform

import (
	"errors"
	"fmt"
	"runtime"
	"unicode/utf16"
	"unicode/utf8"
)

const (
	macICUBreakWord     = int32(1)
	macICUBreakDone     = int32(-1)
	macICUMaxTextLength = int64(1<<31 - 1)
)

// The macOS binding owns its dylib reference; this small boundary lets the
// in-memory manual proof exercise the same validation and lifetime logic.
type macICUWordBoundariesAPI struct {
	open    func(int32, *byte, *uint16, int32, *int32) uintptr
	first   func(uintptr) int32
	next    func(uintptr) int32
	close   func(uintptr)
	release func() error
}

func macICUWordBoundaries(text string, load func() (*macICUWordBoundariesAPI, error)) (boundaries []int, returnErr error) {
	if !utf8.ValidString(text) {
		return nil, errors.New("word boundary text is not valid UTF-8")
	}
	if int64(len(text)) > macICUMaxTextLength {
		return nil, errors.New("word boundary text exceeds ICU's int32 length limit")
	}
	units := utf16.Encode([]rune(text))
	length := len(units)
	if int64(length) > macICUMaxTextLength {
		return nil, errors.New("UTF-16 word boundary text exceeds ICU's int32 length limit")
	}
	// ICU consumes the explicit UTF-16 length, including embedded NULs. Empty
	// text also gets a stable non-null buffer; its passed length remains zero.
	if length == 0 {
		units = []uint16{0}
	}
	api, err := load()
	if err != nil {
		return nil, err
	}
	defer func() { returnErr = errors.Join(returnErr, api.release()) }()
	var status int32
	iterator := api.open(macICUBreakWord, nil, &units[0], int32(length), &status)
	runtime.KeepAlive(units)
	if iterator != 0 {
		// The iterator borrows the UTF-16 buffer until close. Close must run
		// before releasing the library and before allowing the buffer to die.
		defer func() {
			api.close(iterator)
			runtime.KeepAlive(units)
		}()
	}
	if status > 0 {
		return nil, fmt.Errorf("%w: ubrk_open returned ICU error status %d", ErrWordBoundariesUnavailable, status)
	}
	if iterator == 0 {
		return nil, fmt.Errorf("%w: ubrk_open returned a null iterator with status %d", ErrWordBoundariesUnavailable, status)
	}
	first := api.first(iterator)
	if first != 0 {
		return nil, fmt.Errorf("ICU word iterator began at %d; expected boundary 0", first)
	}
	boundaries = []int{0}
	last := int32(0)
	for count := 0; ; count++ {
		if count > length {
			return nil, errors.New("ICU returned more word boundaries than UTF-16 positions")
		}
		boundary := api.next(iterator)
		if boundary == macICUBreakDone {
			break
		}
		if boundary < 0 || boundary <= last || boundary > int32(length) {
			return nil, fmt.Errorf("ICU returned invalid word boundary %d after %d for UTF-16 length %d", boundary, last, length)
		}
		if boundary < int32(length) && units[boundary-1] >= 0xd800 && units[boundary-1] <= 0xdbff && units[boundary] >= 0xdc00 && units[boundary] <= 0xdfff {
			return nil, fmt.Errorf("ICU word boundary %d splits a UTF-16 surrogate pair", boundary)
		}
		boundaries = append(boundaries, int(boundary))
		last = boundary
	}
	if last != int32(length) {
		return nil, fmt.Errorf("ICU word boundaries ended at %d; expected UTF-16 length %d", last, length)
	}
	return boundaries, nil
}
