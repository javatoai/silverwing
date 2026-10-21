package platform

import "errors"

// ErrRichClipboardUnsupported reports that this operating system has no
// implementation for the synchronous rich clipboard API.
var ErrRichClipboardUnsupported = errors.New("rich clipboard is unsupported")
