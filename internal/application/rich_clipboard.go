package application

import (
	"errors"
	"fmt"

	"silverwing-go/internal/platform"
)

// RichClipboardContent contains the plain-text and HTML representations of a
// selection. HTML is a fragment, not a complete CF_HTML document.
type RichClipboardContent struct {
	Text string
	HTML string
}

// RichClipboardBackend is the synchronous in-process boundary used by the
// editor for rich clipboard reads and writes.
type RichClipboardBackend interface {
	ReadRichClipboard() (RichClipboardContent, error)
	WriteRichClipboard(RichClipboardContent) error
}

// ErrRichClipboardUnsupported allows callers to fall back to plain clipboard
// behavior when the current platform has no native rich clipboard backend.
var ErrRichClipboardUnsupported = errors.New("rich clipboard is unsupported")

var _ RichClipboardBackend = (*App)(nil)

// ReadRichClipboard synchronously reads the native clipboard on supported
// platforms. Callers must invoke it from the UI thread.
func (a *App) ReadRichClipboard() (RichClipboardContent, error) {
	content, err := platform.ReadRichClipboard()
	if err != nil {
		return RichClipboardContent{}, normalizeRichClipboardError(err)
	}
	return RichClipboardContent{Text: content.Text, HTML: content.HTML}, nil
}

// WriteRichClipboard synchronously writes the native clipboard on supported
// platforms. Callers must invoke it from the UI thread.
func (a *App) WriteRichClipboard(content RichClipboardContent) error {
	err := platform.WriteRichClipboard(platform.RichClipboardContent{Text: content.Text, HTML: content.HTML})
	if err != nil {
		return normalizeRichClipboardError(err)
	}
	return nil
}

func normalizeRichClipboardError(err error) error {
	if errors.Is(err, platform.ErrRichClipboardUnsupported) {
		return fmt.Errorf("%w: %w", ErrRichClipboardUnsupported, err)
	}
	return err
}
