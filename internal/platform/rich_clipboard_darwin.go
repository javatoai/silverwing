//go:build darwin

package platform

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

const (
	richClipboardMaxBytes      = 64 << 20
	richClipboardPlainTextType = "public.utf8-plain-text"
	richClipboardHTMLType      = "public.html"
	nsUTF8StringEncoding       = 4
)

var (
	richClipboardAppKitOnce sync.Once
	richClipboardAppKitErr  error

	richClipboardSelAlloc                       = objc.RegisterName("alloc")
	richClipboardSelInit                        = objc.RegisterName("init")
	richClipboardSelInitWithBytesLengthEncoding = objc.RegisterName("initWithBytes:length:encoding:")
	richClipboardSelAutorelease                 = objc.RegisterName("autorelease")
	richClipboardSelDrain                       = objc.RegisterName("drain")
	richClipboardSelGeneralPasteboard           = objc.RegisterName("generalPasteboard")
	richClipboardSelChangeCount                 = objc.RegisterName("changeCount")
	richClipboardSelPasteboardItems             = objc.RegisterName("pasteboardItems")
	richClipboardSelCount                       = objc.RegisterName("count")
	richClipboardSelObjectAtIndex               = objc.RegisterName("objectAtIndex:")
	richClipboardSelStringForType               = objc.RegisterName("stringForType:")
	richClipboardSelSetStringForType            = objc.RegisterName("setString:forType:")
	richClipboardSelArrayWithObject             = objc.RegisterName("arrayWithObject:")
	richClipboardSelClearContents               = objc.RegisterName("clearContents")
	richClipboardSelWriteObjects                = objc.RegisterName("writeObjects:")
	richClipboardSelUTF8String                  = objc.RegisterName("UTF8String")
	richClipboardSelLength                      = objc.RegisterName("length")
	richClipboardSelLengthOfBytesUsingEncoding  = objc.RegisterName("lengthOfBytesUsingEncoding:")
)

// RichClipboardContent contains the plain-text and optional HTML-fragment
// representations of a clipboard selection.
type RichClipboardContent struct {
	Text string
	HTML string
}

// ReadRichClipboard reads both representations from the first pasteboard item.
// It checks changeCount around the read so text and HTML cannot come from
// different pasteboard owners.
// Callers must invoke it synchronously from the UI thread.
func ReadRichClipboard() (RichClipboardContent, error) {
	if err := richClipboardLoadAppKit(); err != nil {
		return RichClipboardContent{}, err
	}
	var content RichClipboardContent
	err := richClipboardWithPool(func() error {
		pasteboardClass := objc.GetClass("NSPasteboard")
		if pasteboardClass == 0 {
			return errors.New("AppKit NSPasteboard class is unavailable")
		}
		pasteboard := objc.Send[objc.ID](objc.ID(pasteboardClass), richClipboardSelGeneralPasteboard)
		if pasteboard == 0 {
			return errors.New("cannot access the system pasteboard")
		}
		changeCount := objc.Send[int](pasteboard, richClipboardSelChangeCount)
		stable := func(readErr error) error {
			if objc.Send[int](pasteboard, richClipboardSelChangeCount) != changeCount {
				return errors.Join(readErr, errors.New("pasteboard changed while reading"))
			}
			return readErr
		}

		items := objc.Send[objc.ID](pasteboard, richClipboardSelPasteboardItems)
		itemCount := 0
		if items != 0 {
			itemCount = objc.Send[int](items, richClipboardSelCount)
		}
		if itemCount <= 0 {
			return stable(nil)
		}
		item := objc.Send[objc.ID](items, richClipboardSelObjectAtIndex, uintptr(0))
		if item == 0 {
			return stable(errors.New("pasteboard returned an empty first item"))
		}

		textType, err := richClipboardNSString(richClipboardPlainTextType)
		if err != nil {
			return stable(err)
		}
		htmlType, err := richClipboardNSString(richClipboardHTMLType)
		if err != nil {
			return stable(err)
		}
		textObject := objc.Send[objc.ID](item, richClipboardSelStringForType, textType)
		htmlObject := objc.Send[objc.ID](item, richClipboardSelStringForType, htmlType)
		content.Text, err = richClipboardGoString(textObject, "plain-text")
		if err != nil {
			return stable(err)
		}
		content.HTML, err = richClipboardGoString(htmlObject, "HTML")
		if err != nil {
			return stable(err)
		}
		if err := validateRichClipboardText(content.Text); err != nil {
			return stable(err)
		}
		if content.HTML != "" {
			if err := validateRichClipboardHTML(content.HTML); err != nil {
				return stable(err)
			}
		}
		return stable(nil)
	})
	if err != nil {
		return RichClipboardContent{}, err
	}
	return content, nil
}

// WriteRichClipboard prepares a single pasteboard item containing plain text
// and optional HTML before replacing the current contents.
// Callers must invoke it synchronously from the UI thread.
func WriteRichClipboard(content RichClipboardContent) error {
	if err := validateRichClipboardText(content.Text); err != nil {
		return err
	}
	if content.HTML != "" {
		if err := validateRichClipboardHTML(content.HTML); err != nil {
			return err
		}
	}
	if err := richClipboardLoadAppKit(); err != nil {
		return err
	}
	return richClipboardWithPool(func() error {
		itemClass := objc.GetClass("NSPasteboardItem")
		arrayClass := objc.GetClass("NSArray")
		pasteboardClass := objc.GetClass("NSPasteboard")
		if itemClass == 0 || arrayClass == 0 || pasteboardClass == 0 {
			return errors.New("AppKit pasteboard classes are unavailable")
		}

		text, err := richClipboardNSString(content.Text)
		if err != nil {
			return err
		}
		textType, err := richClipboardNSString(richClipboardPlainTextType)
		if err != nil {
			return err
		}
		var html, htmlType objc.ID
		if content.HTML != "" {
			html, err = richClipboardNSString(content.HTML)
			if err != nil {
				return err
			}
			htmlType, err = richClipboardNSString(richClipboardHTMLType)
			if err != nil {
				return err
			}
		}

		item := objc.Send[objc.ID](objc.ID(itemClass), richClipboardSelAlloc)
		item = objc.Send[objc.ID](item, richClipboardSelInit)
		if item == 0 {
			return errors.New("cannot create a pasteboard item")
		}
		objc.Send[objc.ID](item, richClipboardSelAutorelease)
		if byte(objc.Send[uintptr](item, richClipboardSelSetStringForType, text, textType)) == 0 {
			return errors.New("cannot prepare the plain-text pasteboard representation")
		}
		if content.HTML != "" && byte(objc.Send[uintptr](item, richClipboardSelSetStringForType, html, htmlType)) == 0 {
			return errors.New("cannot prepare the HTML pasteboard representation")
		}
		items := objc.Send[objc.ID](objc.ID(arrayClass), richClipboardSelArrayWithObject, item)
		if items == 0 {
			return errors.New("cannot prepare the pasteboard item array")
		}
		pasteboard := objc.Send[objc.ID](objc.ID(pasteboardClass), richClipboardSelGeneralPasteboard)
		if pasteboard == 0 {
			return errors.New("cannot access the system pasteboard")
		}

		objc.Send[uintptr](pasteboard, richClipboardSelClearContents)
		if byte(objc.Send[uintptr](pasteboard, richClipboardSelWriteObjects, items)) == 0 {
			return errors.New("cannot write the prepared pasteboard item")
		}
		return nil
	})
}

func richClipboardLoadAppKit() error {
	richClipboardAppKitOnce.Do(func() {
		_, richClipboardAppKitErr = purego.Dlopen(
			"/System/Library/Frameworks/AppKit.framework/AppKit",
			purego.RTLD_GLOBAL|purego.RTLD_NOW,
		)
	})
	if richClipboardAppKitErr != nil {
		return fmt.Errorf("load AppKit: %w", richClipboardAppKitErr)
	}
	return nil
}

func richClipboardWithPool(fn func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	poolClass := objc.GetClass("NSAutoreleasePool")
	if poolClass == 0 {
		return errors.New("Foundation NSAutoreleasePool class is unavailable")
	}
	pool := objc.Send[objc.ID](objc.ID(poolClass), richClipboardSelAlloc)
	pool = objc.Send[objc.ID](pool, richClipboardSelInit)
	if pool == 0 {
		return errors.New("cannot create an autorelease pool")
	}
	defer objc.Send[objc.ID](pool, richClipboardSelDrain)
	return fn()
}

func richClipboardNSString(value string) (objc.ID, error) {
	stringClass := objc.GetClass("NSString")
	if stringClass == 0 {
		return 0, errors.New("Foundation NSString class is unavailable")
	}
	var empty byte
	data := unsafe.Pointer(&empty)
	if value != "" {
		data = unsafe.Pointer(unsafe.StringData(value))
	}
	str := objc.Send[objc.ID](objc.ID(stringClass), richClipboardSelAlloc)
	str = objc.Send[objc.ID](str, richClipboardSelInitWithBytesLengthEncoding, data, uintptr(len(value)), uintptr(nsUTF8StringEncoding))
	runtime.KeepAlive(value)
	if str == 0 {
		return 0, errors.New("cannot create an NSString from UTF-8 data")
	}
	return objc.Send[objc.ID](str, richClipboardSelAutorelease), nil
}

func richClipboardGoString(str objc.ID, representation string) (string, error) {
	if str == 0 {
		return "", nil
	}
	length := objc.Send[int](str, richClipboardSelLengthOfBytesUsingEncoding, uintptr(nsUTF8StringEncoding))
	if length < 0 || length > richClipboardMaxBytes {
		return "", fmt.Errorf("%s pasteboard representation exceeds %d MiB", representation, richClipboardMaxBytes>>20)
	}
	if length == 0 {
		if objc.Send[uintptr](str, richClipboardSelLength) != 0 {
			return "", fmt.Errorf("cannot encode the %s pasteboard representation as UTF-8", representation)
		}
		return "", nil
	}
	data := objc.Send[uintptr](str, richClipboardSelUTF8String)
	if data == 0 {
		return "", fmt.Errorf("cannot read the UTF-8 bytes of the %s pasteboard representation", representation)
	}
	value := string(unsafe.Slice((*byte)(unsafe.Pointer(data)), length))
	if !utf8.ValidString(value) {
		return "", fmt.Errorf("%s pasteboard representation is not valid UTF-8", representation)
	}
	if strings.IndexByte(value, 0) >= 0 {
		return "", fmt.Errorf("%s pasteboard representation contains NUL", representation)
	}
	return value, nil
}

func validateRichClipboardText(value string) error {
	if !utf8.ValidString(value) {
		return errors.New("plain-text clipboard content is not valid UTF-8")
	}
	if strings.IndexByte(value, 0) >= 0 {
		return errors.New("plain-text clipboard content cannot contain NUL")
	}
	if (len(utf16.Encode([]rune(value)))+1)*2 > richClipboardMaxBytes {
		return fmt.Errorf("plain-text clipboard content exceeds %d MiB", richClipboardMaxBytes>>20)
	}
	return nil
}

func validateRichClipboardHTML(fragment string) error {
	if !utf8.ValidString(fragment) {
		return errors.New("HTML clipboard content is not valid UTF-8")
	}
	if strings.IndexByte(fragment, 0) >= 0 {
		return errors.New("HTML clipboard content cannot contain NUL")
	}
	const prefix = "<html><body><!--StartFragment-->"
	const suffix = "<!--EndFragment--></body></html>"
	if len(fragment)+len(prefix)+len(suffix)+256 > richClipboardMaxBytes {
		return fmt.Errorf("HTML clipboard content exceeds %d MiB", richClipboardMaxBytes>>20)
	}
	return nil
}
