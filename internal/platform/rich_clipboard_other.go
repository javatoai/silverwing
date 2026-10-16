//go:build !windows && !darwin

package platform

import "fmt"

// RichClipboardContent contains the plain-text and HTML representations of a
// clipboard selection. Unsupported platforms return an explicit error.
type RichClipboardContent struct {
	Text string
	HTML string
}

func ReadRichClipboard() (RichClipboardContent, error) {
	return RichClipboardContent{}, fmt.Errorf("%w on this platform", ErrRichClipboardUnsupported)
}

func WriteRichClipboard(RichClipboardContent) error {
	return fmt.Errorf("%w on this platform", ErrRichClipboardUnsupported)
}
